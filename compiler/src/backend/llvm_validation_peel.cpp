//go:build cgo
#include <llvm/Config/llvm-config.h>
#include <llvm-c/Core.h>
#if LLVM_VERSION_MAJOR == 23
#include <llvm/IR/Dominators.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Analysis/AssumptionCache.h>
#include <llvm/Analysis/CaptureTracking.h>
#include <llvm/Analysis/CFG.h>
#include <llvm/IR/ConstantRange.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/OptimizationRemarkEmitter.h>
#include <llvm/Analysis/TargetTransformInfo.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Transforms/Utils/Cloning.h>
#include <llvm/Transforms/Utils/LoopPeel.h>
#include <llvm/Transforms/Utils/LoopSimplify.h>
#include <llvm/Transforms/Utils/LoopUtils.h>
#include <llvm/Transforms/Utils/UnrollLoop.h>
using namespace llvm;
extern "C" unsigned elisacorePeelSmallValidationLoops(LLVMModuleRef moduleRef) {
  if (!moduleRef) return 0;
  Module &module = *unwrap(moduleRef);
  PassBuilder PB;
  LoopAnalysisManager LAM; FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM; ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM); PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM); PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
  unsigned count = 0;
  for (Function &F : module) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasMinSize() || F.hasOptSize()) continue;
    auto &LI = FAM.getResult<LoopAnalysis>(F);
    auto &DT = FAM.getResult<DominatorTreeAnalysis>(F);
    auto &SE = FAM.getResult<ScalarEvolutionAnalysis>(F);
    auto &AC = FAM.getResult<AssumptionAnalysis>(F);
    for (Loop *L : LI) {
      if (!L->getSubLoops().empty() || L->getNumBlocks() != 2 || L->getLoopID()) continue;
      unsigned loads = 0, instructions = 0, exits = 0, cmps = 0;
      bool safe = true;
      for (BasicBlock *BB : L->blocks()) for (Instruction &I : *BB) {
        ++instructions;
        if (auto *load = dyn_cast<LoadInst>(&I)) { if (!load->isSimple()) safe = false; ++loads; }
        else if (I.mayHaveSideEffects() && !isa<BranchInst>(I)) safe = false;
        if (isa<ICmpInst>(I)) ++cmps;
        if (auto *branch = dyn_cast<CondBrInst>(&I))
          for (auto *successor : branch->successors()) if (!L->contains(successor)) ++exits;
      }
      if (!safe || loads != 1 || instructions > 16 || exits != 2 || cmps != 2) continue;
      simplifyLoop(L, &DT, &LI, &SE, &AC, nullptr, false);
      if (!L->isLoopSimplifyForm() || !canPeel(L)) continue;
      formLCSSA(*L, DT, &LI, &SE);
      ValueToValueMapTy values;
      peelLoop(L, 1, false, &LI, &SE, DT, &AC, true, values);
      // Preserve every load and early exit in order. LLVM's unroller handles
      // the runtime remainder without speculating accesses past an invalid item.
      auto &TTI = FAM.getResult<TargetIRAnalysis>(F);
      auto &ORE = FAM.getResult<OptimizationRemarkEmitterAnalysis>(F);
      UnrollLoopOptions options{4, true, true, false, true, false, nullptr, 1000, true, false};
      UnrollLoop(L, options, &LI, &SE, &DT, &AC, &TTI, &ORE, true);
      ++count;
      break; // Invalidate function analyses before inspecting another loop.
    }
  }
  return count;
}


// Expand one level of a small, directly recursive local traversal. LLVM's
// inliner preserves checks and memory effects; the budget bounds code growth.
extern "C" unsigned elisacoreInlineSmallRecursion(LLVMModuleRef ref) {
  if (!ref) return 0;
  Module &M = *unwrap(ref);
  if ((!M.getTargetTriple().isMacOSX() && !M.getTargetTriple().isOSLinux()) ||
      M.getTargetTriple().getArch() != Triple::aarch64 ||
      M.getNamedMetadata("llvm.dbg.cu")) return 0;
  unsigned changed = 0;
  for (Function &F : M) {
    if (!F.hasLocalLinkage() || F.isDeclaration() || F.isVarArg() || F.size() > 20 ||
        F.hasOptNone() || F.hasOptSize() || F.hasMinSize() || F.hasPersonalityFn() ||
        F.hasGC() || F.getSubprogram() || F.hasFnAttribute(Attribute::NoInline) ||
        F.hasFnAttribute(Attribute::Naked) || F.hasFnAttribute(Attribute::Convergent) ||
        F.hasFnAttribute(Attribute::NoDuplicate) || F.hasFnAttribute("elisa.recursion.expanded") ||
        !F.getReturnType()->isVoidTy()) continue;
    bool safe = true, hasCaller = false;
    for (User *U : F.users()) {
      auto *CB = dyn_cast<CallBase>(U);
      if (!CB || CB->getCalledOperand() != &F) { safe = false; break; }
      if (CB->getFunction() != &F) hasCaller = true;
    }
    unsigned instructions = 0, loads = 0, stores = 0, recursive = 0;
    CallBase *site = nullptr;
    for (BasicBlock &B : F) for (Instruction &I : B) {
      ++instructions;
      if (isa<AllocaInst>(I) || isa<InvokeInst>(I) || isa<CallBrInst>(I)) safe = false;
      if (auto *L = dyn_cast<LoadInst>(&I)) { ++loads; if (!L->isSimple()) safe = false; }
      if (auto *S = dyn_cast<StoreInst>(&I)) { ++stores; if (!S->isSimple()) safe = false; }
      if (auto *CB = dyn_cast<CallBase>(&I)) {
        if (CB->hasOperandBundles() || CB->isConvergent() || CB->cannotDuplicate() ||
            CB->isNoInline()) safe = false;
        if (CB->getCalledFunction() == &F) { ++recursive; site = CB; }
        else if (CB->getIntrinsicID() != Intrinsic::trap) safe = false;
      }
    }
    if (!safe || !hasCaller || instructions > 80 || recursive != 1 || !loads || !stores) continue;
    InlineFunctionInfo IFI;
    if (InlineFunction(*site, IFI).isSuccess()) {
      F.addFnAttr("elisa.recursion.expanded");
      ++changed;
    }
  }
  return changed;
}

// Predicate a checked 0/1 update of a private stack array. Extra accesses are
// permitted only when SCEV proves their full byte range and the array cannot
// escape. Selects and freezes preserve defined results without introducing
// poison through a value that the zero case originally did not evaluate.
extern "C" unsigned elisacorePredicatePrivateBitUpdates(LLVMModuleRef ref) {
  if (!ref) return 0;
  Module &M = *unwrap(ref);
  if (M.getTargetTriple().getArch() != Triple::aarch64 || !M.getDataLayout().isLittleEndian() ||
      (!M.getTargetTriple().isMacOSX() && !M.getTargetTriple().isOSLinux()) ||
      M.getNamedMetadata("llvm.dbg.cu")) return 0;
  PassBuilder PB;
  LoopAnalysisManager LAM; FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM; ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM); PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM); PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
  const DataLayout &DL = M.getDataLayout();
  unsigned changed = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.getSubprogram() || F.getInstructionCount() > 1000) continue;
    for (BasicBlock &B : F) {
      auto *SW = dyn_cast<SwitchInst>(B.getTerminator());
      if (!SW || SW->getNumCases() != 2 || !SW->getCondition()->getType()->isIntegerTy()) continue;
      BasicBlock *zero = nullptr, *one = nullptr;
      for (auto C : SW->cases()) {
        if (C.getCaseValue()->isZero()) zero = C.getCaseSuccessor();
        if (C.getCaseValue()->isOne()) one = C.getCaseSuccessor();
      }
      if (!zero || !one || zero == one || zero == SW->getDefaultDest() ||
          one == SW->getDefaultDest() || one->getSinglePredecessor() != &B ||
          !zero->phis().empty()) continue;
      auto *end = dyn_cast<UncondBrInst>(one->getTerminator());
      if (!end || end->getSuccessor(0) != zero) continue;
      StoreInst *store = nullptr;
      unsigned stores = 0;
      for (Instruction &I : *one) if (auto *S = dyn_cast<StoreInst>(&I)) { store = S; ++stores; }
      if (stores != 1 || !store->isSimple()) continue;
      auto *update = dyn_cast<BinaryOperator>(store->getValueOperand());
      auto *pointer = dyn_cast<GetElementPtrInst>(store->getPointerOperand());
      if (!update || update->getParent() != one || update->getOpcode() != Instruction::Or || !update->hasOneUse() ||
          !pointer || pointer->getParent() != one || pointer->getNumIndices() != 1) continue;
      auto *load = dyn_cast<LoadInst>(update->getOperand(0));
      Value *mask = update->getOperand(1);
      if (!load) { load = dyn_cast<LoadInst>(mask); mask = update->getOperand(0); }
      if (!load || !load->isSimple() || load->getParent() != one || !load->hasOneUse() ||
          load->getPointerOperand() != pointer || !load->getType()->isIntegerTy()) continue;
      unsigned bits = load->getType()->getIntegerBitWidth();
      if (bits != 8 && bits != 16 && bits != 32 && bits != 64) continue;
      bool simple = true;
      for (Instruction &I : *one)
        if (&I != pointer && &I != load && &I != update && &I != store && &I != end) simple = false;
      if (!simple) continue;
      auto &SE = FAM.getResult<ScalarEvolutionAnalysis>(F);
      auto &DT = FAM.getResult<DominatorTreeAnalysis>(F);
      auto dominates = [&](Value *V) {
        auto *I = dyn_cast<Instruction>(V); return !I || DT.dominates(I, SW);
      };
      Value *index = pointer->getOperand(1);
      if (!index->getType()->isIntegerTy() || index->getType()->getIntegerBitWidth() > 64 ||
          !dominates(index) || !dominates(mask)) continue;
      auto *array = dyn_cast<AllocaInst>(pointer->getPointerOperand());
      if (!array || !array->isStaticAlloca() || array->getAddressSpace() != 0 ||
          !cast<ConstantInt>(array->getArraySize())->isOne() || !DT.dominates(array, SW) ||
          PointerMayBeCaptured(array, true)) continue;
      TypeSize allocation = DL.getTypeAllocSize(array->getAllocatedType());
      TypeSize stride = DL.getTypeAllocSize(pointer->getSourceElementType());
      TypeSize access = DL.getTypeStoreSize(load->getType());
      if (allocation.isScalable() || stride.isScalable() || access.isScalable()) continue;
      uint64_t bytes = allocation.getFixedValue(), step = stride.getFixedValue(), width = access.getFixedValue();
      if (!width || !step || bytes > 256 || bytes < width ||
          array->getAlign() < load->getAlign() || array->getAlign() < store->getAlign() ||
          step % load->getAlign().value() || step % store->getAlign().value()) continue;
      const ConstantRange &range = SE.getUnsignedRange(SE.getSCEV(index));
      if (range.getUnsignedMax().ugt((bytes - width) / step)) continue;
      // Lifetime markers must not make the newly accessed zero path dead.
      for (BasicBlock &BB : F) for (Instruction &I : BB) if (auto *II = dyn_cast<IntrinsicInst>(&I)) {
        if (II->getIntrinsicID() == Intrinsic::lifetime_end && isPotentiallyReachable(II, SW)) simple = false;
        if (II->getIntrinsicID() == Intrinsic::lifetime_start && !DT.dominates(II, SW)) simple = false;
      }
      if (!simple) continue;
      IRBuilder<> head(SW);
      Value *valid = head.CreateICmpULE(SW->getCondition(), ConstantInt::get(SW->getCondition()->getType(), 1));
      Value *set = head.CreateICmpEQ(SW->getCondition(), ConstantInt::get(SW->getCondition()->getType(), 1));
      auto *branch = head.CreateCondBr(valid, one, SW->getDefaultDest());
      branch->setDebugLoc(SW->getDebugLoc());
      zero->removePredecessor(&B);
      SW->eraseFromParent();
      IRBuilder<> body(update);
      Value *selected = body.CreateSelect(set, mask, ConstantInt::get(load->getType(), 0));
      Value *result = body.CreateOr(body.CreateFreeze(load), selected);
      store->setOperand(0, result);
      update->eraseFromParent();
      // Metadata valid only on the original one path must not constrain zero.
      for (Instruction *I : {static_cast<Instruction *>(load), static_cast<Instruction *>(store)}) {
        SmallVector<std::pair<unsigned, MDNode *>, 8> metadata;
        I->getAllMetadataOtherThanDebugLoc(metadata);
        for (auto item : metadata) I->setMetadata(item.first, nullptr);
      }
      ++changed;
      break; // Analyses describe the original CFG; one update per function.
    }
  }
  return changed;
}

// Unroll short nested scans with conditional stores. LLVM retains all exits and
// bounds checks and builds a runtime remainder where necessary.
extern "C" unsigned elisacoreUnrollNestedScans(LLVMModuleRef ref) {
  if (!ref) return 0;
  Module &M = *unwrap(ref);
  if (!M.getTargetTriple().isOSLinux() || M.getTargetTriple().getArch() != Triple::aarch64 ||
      M.getNamedMetadata("llvm.dbg.cu")) return 0;
  PassBuilder PB;
  LoopAnalysisManager LAM; FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM; ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM); PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM); PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
  unsigned changed = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.hasPersonalityFn() || F.hasGC() || F.getSubprogram() ||
        F.hasFnAttribute(Attribute::Naked) || F.hasFnAttribute(Attribute::Convergent) ||
        F.hasFnAttribute(Attribute::NoDuplicate) || F.getEntryCount().has_value()) continue;
    auto &LI = FAM.getResult<LoopAnalysis>(F);
    auto &SE = FAM.getResult<ScalarEvolutionAnalysis>(F);
    auto &DT = FAM.getResult<DominatorTreeAnalysis>(F);
    auto &AC = FAM.getResult<AssumptionAnalysis>(F);
    auto &TTI = FAM.getResult<TargetIRAnalysis>(F);
    auto &ORE = FAM.getResult<OptimizationRemarkEmitterAnalysis>(F);
    bool done = false;
    for (Loop *root : LI) {
      if (done) break;
      for (Loop *L : root->getLoopsInPreorder()) {
        if (!L->getParentLoop() || !L->getSubLoops().empty() || L->getLoopID() ||
            L->getNumBlocks() < 5 || L->getNumBlocks() > 8) continue;
        BasicBlock *latch = L->getLoopLatch();
        if (!latch) continue;
        auto *branch = dyn_cast<CondBrInst>(latch->getTerminator());
        auto *cmp = branch ? dyn_cast<ICmpInst>(branch->getCondition()) : nullptr;
        if (!cmp || !cmp->isEquality()) continue;
        bool induction = false;
        for (Value *v : cmp->operands())
          if (auto *add = dyn_cast<BinaryOperator>(v))
            if (add->getOpcode() == Instruction::Add)
              for (unsigned i = 0; i < 2; ++i)
                if (auto *phi = dyn_cast<PHINode>(add->getOperand(i)))
                  if (phi->getParent() == L->getHeader())
                    if (auto *one = dyn_cast<ConstantInt>(add->getOperand(1 - i)))
                      if (one->isOne()) induction = true;
        if (!induction) continue;
        unsigned loads = 0, stores = 0, instructions = 0, conditions = 0;
        bool safe = true;
        for (BasicBlock *BB : L->blocks()) {
          if (BB->hasAddressTaken() || BB->isEHPad()) safe = false;
          for (Instruction &I : *BB) {
            ++instructions;
            if (auto *load = dyn_cast<LoadInst>(&I)) { ++loads; safe &= load->isSimple(); }
            else if (auto *store = dyn_cast<StoreInst>(&I)) { ++stores; safe &= store->isSimple(); }
            else if (isa<CondBrInst>(I)) ++conditions;
            else if (isa<CallBase>(I) || isa<AllocaInst>(I) || isa<IndirectBrInst>(I) ||
                     I.getType()->isTokenTy()) safe = false;
          }
        }
        if (!safe || loads < 2 || stores < 1 || conditions < 3 || instructions > 65) continue;
        simplifyLoop(L, &DT, &LI, &SE, &AC, nullptr, false);
        if (!L->isLoopSimplifyForm()) continue;
        formLCSSA(*L, DT, &LI, &SE);
        UnrollLoopOptions options{2, true, true, false, true, false, nullptr, 1000, true, false};
        if (UnrollLoop(L, options, &LI, &SE, &DT, &AC, &TTI, &ORE, true) != LoopUnrollResult::Unmodified)
          ++changed;
        done = true; // Recompute analyses before considering another loop in this function.
        break;
      }
    }
  }
  return changed;
}

#else
extern "C" unsigned elisacorePredicatePrivateBitUpdates(LLVMModuleRef) { return 0; }
extern "C" unsigned elisacoreInlineSmallRecursion(LLVMModuleRef) { return 0; }
extern "C" unsigned elisacoreUnrollNestedScans(LLVMModuleRef) { return 0; }
extern "C" unsigned elisacorePeelSmallValidationLoops(LLVMModuleRef) { return 0; }
#endif
