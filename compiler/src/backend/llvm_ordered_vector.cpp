// go:build cgo
#include <llvm-c/Core.h>
#include <llvm-c/Error.h>
#include <llvm-c/TargetMachine.h>
#include <llvm-c/Transforms/PassBuilder.h>
#include <llvm/Config/llvm-config.h>
#if LLVM_VERSION_MAJOR == 23
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/IR/Dominators.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Target/TargetMachine.h>
#endif
// Ordered integer-to-float reductions and paired loop counters.
#if LLVM_VERSION_MAJOR == 23
#include <limits>
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Intrinsics.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/PassManager.h>
#include <llvm/Passes/PassBuilder.h>

using namespace llvm;
namespace {
// Preserve the scalar floating-point order: the inputs are signed integers, so
// their conversions are finite and the ordered reduction's initial -0 is exact.
// Pair only consecutive unconditional loads in the same side-effect-free block.
struct ElisaOrderedCasts : PassInfoMixin<ElisaOrderedCasts> {
  PreservedAnalyses run(Function &F, FunctionAnalysisManager &) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() ||
        F.hasMinSize() || F.hasFnAttribute(Attribute::StrictFP) ||
        F.hasFnAttribute(Attribute::SanitizeAddress) ||
        F.hasFnAttribute(Attribute::SanitizeMemory) ||
        F.hasFnAttribute(Attribute::SanitizeThread) ||
        F.hasFnAttribute(Attribute::SanitizeHWAddress))
      return PreservedAnalyses::all();
    bool changed = false;
    for (BasicBlock &B : F) {
      SmallVector<SIToFPInst *, 32> cs;
      for (Instruction &I : B)
        if (auto *C = dyn_cast<SIToFPInst>(&I))
          if (C->getSrcTy()->isIntegerTy(64) && C->getDestTy()->isDoubleTy() &&
              isa<LoadInst>(C->getOperand(0)))
            cs.push_back(C);
      for (unsigned i = 0; i + 7 < cs.size(); i += 8) {
        auto *first = cs[i], *last = cs[i + 7];
        if (!first->comesBefore(last))
          continue;
        SmallVector<Instruction *, 16> moved;
        SmallPtrSet<Instruction *, 16> moving;
        bool safe = true;
        for (unsigned j = 0; j < 8; j++) {
          auto *C = cs[i + j];
          auto *L = cast<LoadInst>(C->getOperand(0));
          if (!L->isSimple() || L->getParent() != &B || !L->comesBefore(C)) {
            safe = false;
            break;
          }
        }
        if (!safe)
          continue;
        for (Instruction *I = first; I != last; I = I->getNextNode()) {
          if (I->mayHaveSideEffects() ||
              (isa<LoadInst>(I) && !cast<LoadInst>(I)->isSimple())) {
            safe = false;
            break;
          }
          if (I->getOpcode() == Instruction::FAdd) {
            moved.push_back(I);
            moving.insert(I);
          }
        }
        if (!safe)
          continue;
        for (unsigned j = 0; j < 8 && safe; j++)
          for (User *U : cs[i + j]->users()) {
            auto *I = dyn_cast<Instruction>(U);
            if (!I || I->getParent() != &B) {
              safe = false;
              break;
            }
            if (I != last && I->comesBefore(last) && !moving.count(I)) {
              safe = false;
              break;
            }
          }
        for (Instruction *A : moved)
          for (User *U : A->users()) {
            auto *I = dyn_cast<Instruction>(U);
            if (!I || I->getParent() != &B) {
              safe = false;
              break;
            }
            if (I != last && I->comesBefore(last) && !moving.count(I)) {
              safe = false;
              break;
            }
          }
        if (!safe)
          continue;
        SmallVector<BinaryOperator *, 5> reduce;
        Value *prev = cs[i];
        for (unsigned j = 1; j < 6; j++) {
          if (!prev->hasOneUse()) {
            safe = false;
            break;
          }
          auto *Add = dyn_cast<BinaryOperator>(*prev->user_begin());
          if (!Add || Add->getOpcode() != Instruction::FAdd ||
              Add->getOperand(0) != prev || Add->getOperand(1) != cs[i + j] ||
              Add->getParent() != &B) {
            safe = false;
            break;
          }
          if (j > 1 &&
              Add->getFastMathFlags() != reduce[0]->getFastMathFlags()) {
            safe = false;
            break;
          }
          reduce.push_back(Add);
          prev = Add;
        }
        if (!safe)
          continue;
        for (unsigned j = 0; j < 8; j += 2) {
          auto *L = cast<LoadInst>(cs[i + j]->getOperand(0));
          auto *R = cast<LoadInst>(cs[i + j + 1]->getOperand(0));
          if (!L->comesBefore(R)) {
            safe = false;
            break;
          }
          for (Instruction *I = L->getNextNode(); I != R; I = I->getNextNode())
            if (I->mayHaveSideEffects()) {
              safe = false;
              break;
            }
          int64_t x = 0, y = 0;
          const DataLayout &DL = F.getParent()->getDataLayout();
          Value *P = GetPointerBaseWithConstantOffset(L->getPointerOperand(), x,
                                                      DL),
                *Q = GetPointerBaseWithConstantOffset(R->getPointerOperand(), y,
                                                      DL);
          if (P != Q || x > std::numeric_limits<int64_t>::max() - 8 ||
              y != x + 8) {
            safe = false;
            break;
          }
        }
        if (!safe)
          continue;
        for (unsigned j = 0; j < 8; j += 2) {
          auto *L = cast<LoadInst>(cs[i + j]->getOperand(0));
          auto *R = cast<LoadInst>(cs[i + j + 1]->getOperand(0));
          IRBuilder<> pb(L);
          auto *T = FixedVectorType::get(pb.getInt64Ty(), 2);
          Align alignment = std::min(L->getAlign(), R->getAlign());
          auto *wide =
              pb.CreateAlignedLoad(T, L->getPointerOperand(), alignment);
          Value *v0 = pb.CreateExtractElement(wide, pb.getInt32(0));
          Value *v1 = pb.CreateExtractElement(wide, pb.getInt32(1));
          L->replaceAllUsesWith(v0);
          R->replaceAllUsesWith(v1);
          L->eraseFromParent();
          R->eraseFromParent();
        }
        IRBuilder<> b(last);
        auto *IV = FixedVectorType::get(b.getInt64Ty(), 8);
        Value *V = PoisonValue::get(IV);
        for (unsigned j = 0; j < 8; j++)
          V = b.CreateInsertElement(V, cs[i + j]->getOperand(0), b.getInt32(j));
        V = b.CreateSIToFP(V, FixedVectorType::get(b.getDoubleTy(), 8));
        SmallVector<Value *, 8> result;
        for (unsigned j = 0; j < 8; j++)
          result.push_back(b.CreateExtractElement(V, b.getInt32(j)));
        SmallVector<int, 6> idx = {0, 1, 2, 3, 4, 5};
        Value *six =
            b.CreateShuffleVector(V, PoisonValue::get(V->getType()), idx);
        Function *reduceFn = Intrinsic::getOrInsertDeclaration(
            F.getParent(), Intrinsic::vector_reduce_fadd, {six->getType()});
        auto *sum = b.CreateCall(
            reduceFn, {ConstantFP::getNegativeZero(b.getDoubleTy()), six});
        sum->setFastMathFlags(reduce[0]->getFastMathFlags());
        for (Instruction *A : moved)
          A->moveBefore(last->getIterator());
        reduce.back()->replaceAllUsesWith(sum);
        for (auto it = reduce.rbegin(); it != reduce.rend(); ++it)
          (*it)->eraseFromParent();
        for (unsigned j = 0; j < 8; j++)
          cs[i + j]->replaceAllUsesWith(result[j]);
        for (unsigned j = 0; j < 8; j++)
          cs[i + j]->eraseFromParent();
        changed = true;
      }
    }
    if (changed)
      for (BasicBlock &B : F) {
        auto *branch = dyn_cast<BranchInst>(B.getTerminator());
        if (!branch || !branch->isConditional())
          continue;
        SmallVector<LoadInst *, 4> loads;
        bool blocked = false;
        for (Instruction &I : B) {
          if (auto *L = dyn_cast<LoadInst>(&I)) {
            if (!L->isSimple() || !L->getType()->isIntegerTy(64)) {
              blocked = true;
              break;
            }
            loads.push_back(L);
          } else if (I.mayHaveSideEffects() && !isa<BranchInst>(I)) {
            blocked = true;
            break;
          }
        }
        if (blocked || loads.size() != 3)
          continue;
        auto *L = loads[0], *R = loads[1];
        int64_t x = 0, y = 0;
        const DataLayout &DL = F.getParent()->getDataLayout();
        Value *P = GetPointerBaseWithConstantOffset(L->getPointerOperand(), x,
                                                    DL),
              *Q = GetPointerBaseWithConstantOffset(R->getPointerOperand(), y,
                                                    DL);
        if (P != Q || x > std::numeric_limits<int64_t>::max() - 8 || y != x + 8)
          continue;
        IRBuilder<> b(L);
        auto *T = FixedVectorType::get(b.getInt64Ty(), 2);
        auto *wide = b.CreateAlignedLoad(
            T, L->getPointerOperand(), std::min(L->getAlign(), R->getAlign()));
        Value *v0 = b.CreateExtractElement(wide, b.getInt32(0)),
              *v1 = b.CreateExtractElement(wide, b.getInt32(1));
        L->replaceAllUsesWith(v0);
        R->replaceAllUsesWith(v1);
        L->eraseFromParent();
        R->eraseFromParent();
        break;
      }
    return changed ? PreservedAnalyses::none() : PreservedAnalyses::all();
  }
};
} // namespace

#include <llvm/ADT/SmallVector.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/PassManager.h>
#include <llvm/Passes/PassBuilder.h>

using namespace llvm;
namespace {
struct ElisaCounter {
  PHINode *phi;
  BinaryOperator *update;
  Value *step;
};
// Pair two independent zero-based loop counters without changing control flow.
// Dropping scalar no-wrap flags is conservative: vector lanes cannot introduce
// poison where the original unflagged additions were defined.
struct ElisaPairCounters : PassInfoMixin<ElisaPairCounters> {
  PreservedAnalyses run(Function &F, FunctionAnalysisManager &AM) {
    auto &LI = AM.getResult<LoopAnalysis>(F);
    auto &DT = AM.getResult<DominatorTreeAnalysis>(F);
    bool changed = false;
    for (Loop *L : LI.getLoopsInPreorder()) {
      BasicBlock *H = L->getHeader(), *P = L->getLoopPreheader(),
                 *B = L->getLoopLatch();
      if (!P || !B || H == B)
        continue;
      SmallVector<ElisaCounter, 4> cs;
      for (PHINode &phi : H->phis()) {
        if (!phi.getType()->isIntegerTy(64) || phi.getNumIncomingValues() != 2)
          continue;
        Value *initial = phi.getIncomingValueForBlock(P),
              *back = phi.getIncomingValueForBlock(B);
        if (!initial || !back)
          continue;
        auto *zero = dyn_cast<ConstantInt>(initial);
        auto *add = dyn_cast<BinaryOperator>(back);
        if (!zero || !zero->isZero() || !add || add->getParent() != B ||
            add->getOpcode() != Instruction::Add)
          continue;
        Value *step = nullptr;
        if (add->getOperand(0) == &phi)
          step = add->getOperand(1);
        else if (add->getOperand(1) == &phi)
          step = add->getOperand(0);
        else
          continue;
        if (!L->isLoopInvariant(step))
          continue;
        cs.push_back({&phi, add, step});
      }
      if (cs.size() != 2)
        continue;
      bool phiUse = false;
      for (auto c : cs)
        for (User *U : c.phi->users())
          if (isa<PHINode>(U))
            phiUse = true;
      if (phiUse)
        continue;
      ElisaCounter a = cs[0], b = cs[1];
      if (a.update == b.update)
        continue;
      auto *oneA = dyn_cast<ConstantInt>(a.step),
           *oneB = dyn_cast<ConstantInt>(b.step);
      if (!(oneA && oneA->isOne()) && !(oneB && oneB->isOne()))
        continue;
      Instruction *earlier = a.update->comesBefore(b.update)
                                 ? static_cast<Instruction *>(a.update)
                                 : static_cast<Instruction *>(b.update);
      if (auto *I = dyn_cast<Instruction>(a.step))
        if (!DT.dominates(I, earlier))
          continue;
      if (auto *I = dyn_cast<Instruction>(b.step))
        if (!DT.dominates(I, earlier))
          continue;
      IRBuilder<> hb(&*H->getFirstInsertionPt());
      auto *VT = FixedVectorType::get(hb.getInt64Ty(), 2);
      auto *vec = PHINode::Create(VT, 2, "paired.counters", H->begin());
      Value *aValue = hb.CreateExtractElement(vec, hb.getInt32(0)),
            *bValue = hb.CreateExtractElement(vec, hb.getInt32(1));
      IRBuilder<> lb(earlier);
      Value *step = PoisonValue::get(VT);
      step = lb.CreateInsertElement(step, a.step, lb.getInt32(0));
      step = lb.CreateInsertElement(step, b.step, lb.getInt32(1));
      Value *next = lb.CreateAdd(vec, step, "paired.next");
      Value *an = lb.CreateExtractElement(next, lb.getInt32(0)),
            *bn = lb.CreateExtractElement(next, lb.getInt32(1));
      vec->addIncoming(Constant::getNullValue(VT), P);
      vec->addIncoming(next, B);
      a.phi->replaceAllUsesWith(aValue);
      b.phi->replaceAllUsesWith(bValue);
      a.update->replaceAllUsesWith(an);
      b.update->replaceAllUsesWith(bn);
      a.update->eraseFromParent();
      b.update->eraseFromParent();
      a.phi->eraseFromParent();
      b.phi->eraseFromParent();
      changed = true;
    }
    return changed ? PreservedAnalyses::none() : PreservedAnalyses::all();
  }
};
} // namespace

extern "C" unsigned
elisacoreOptimizeOrderedConversions(LLVMModuleRef ref,
                                    LLVMTargetMachineRef target) {
  if (!ref || !target)
    return 0;
  Module &M = *unwrap(ref);
  if (!M.getTargetTriple().isAArch64() || M.getNamedMetadata("llvm.dbg.cu"))
    return 0;
  unsigned functions = 0, instructions = 0;
  for (Function &F : M)
    if (!F.isDeclaration()) {
      if (++functions > 1 || (instructions += F.getInstructionCount()) > 2000)
        return 0;
    }
  SmallPtrSet<Function *, 4> selected;
  FunctionAnalysisManager FAM;
  for (Function &F : M)
    if (!F.isDeclaration()) {
      auto result = ElisaOrderedCasts().run(F, FAM);
      if (!result.areAllPreserved()) {
        selected.insert(&F);
#if defined(__linux__) && defined(__aarch64__)
        if (M.getTargetTriple().isOSLinux())
          F.addFnAttr("elisa.ordered.ilp");
#endif
      }
    }
  if (selected.empty())
    return 0;
  // Only run the extra cleanup when the ordered conversion rewrite applied.
  // The single-function and instruction caps bound this work for compiler
  // builds.
  auto options = LLVMCreatePassBuilderOptions();
  auto error = LLVMRunPasses(ref, "function(instcombine)", target, options);
  LLVMDisposePassBuilderOptions(options);
  if (error) {
    LLVMConsumeError(error);
    return 2;
  }
  PassBuilder PB(reinterpret_cast<TargetMachine *>(target));
  LoopAnalysisManager LAM;
  FunctionAnalysisManager FAM2;
  CGSCCAnalysisManager CGAM;
  ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM);
  PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM2);
  PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM2, CGAM, MAM);
  for (Function *F : selected)
    ElisaPairCounters().run(*F, FAM2);
  return 1;
}

#else
extern "C" unsigned elisacoreOptimizeOrderedConversions(LLVMModuleRef,
                                                        LLVMTargetMachineRef) {
  return 0;
}
#endif
