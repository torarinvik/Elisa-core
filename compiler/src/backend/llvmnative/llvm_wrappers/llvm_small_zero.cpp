//go:build cgo
// Expand small dynamic constant-byte fills without writing outside their range.
#include <llvm-c/Core.h>
#include <llvm/Config/llvm-config.h>
#include <llvm/Analysis/CFG.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Analysis/AssumptionCache.h>
#include <llvm/Analysis/TargetTransformInfo.h>
#include <llvm/Analysis/OptimizationRemarkEmitter.h>
#include <llvm/IR/Dominators.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Transforms/Utils/LoopUtils.h>
#include <llvm/Transforms/Utils/UnrollLoop.h>

#include <llvm/AsmParser/Parser.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Linker/Linker.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/Transforms/Utils/Cloning.h>
using namespace llvm;
// Preserve the original evaluation order while unrolling small, guarded
// floating-point reductions. LLVM's unroller handles all remainder iterations.
// Only loops on a path proving a trip bound of 1..16 are considered.
#if LLVM_VERSION_MAJOR >= 23
// Accept a value only when its enclosing path proves 1 <= N <= 16.
static bool smallReductionGuardedBound(Value *N, Loop *L, DominatorTree &DT) {
  Function &F = *L->getHeader()->getParent();
  for (BasicBlock &B : F) {
    auto *Br = dyn_cast<CondBrInst>(B.getTerminator());
    if (!Br || Br->getSuccessor(0) == Br->getSuccessor(1))
      continue;
    auto *C = dyn_cast<ICmpInst>(Br->getCondition());
    if (!C || C->getPredicate() != ICmpInst::ICMP_ULT)
      continue;
    auto *K = dyn_cast<ConstantInt>(C->getOperand(1));
    auto *Add = dyn_cast<BinaryOperator>(C->getOperand(0));
    if (!K || !K->getType()->isIntegerTy(64) ||
        K->getValue() != APInt(64, -16, true) || !Add ||
        Add->getOpcode() != Instruction::Add)
      continue;
    Value *X = Add->getOperand(0);
    auto *Offset = dyn_cast<ConstantInt>(Add->getOperand(1));
    if (X != N || !Offset || Offset->getValue() != APInt(64, -17, true))
      continue;
    if (DT.dominates(BasicBlockEdge(&B, Br->getSuccessor(1)),
                     L->getLoopPreheader()))
      return true;
  }
  return false;
}

static unsigned unrollGuardedReductions(Module &M) {
  PassBuilder PB;
  LoopAnalysisManager LAM;
  FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM;
  ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM);
  PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM);
  PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
  unsigned changed = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() ||
        F.hasMinSize() || F.getSubprogram() ||
        F.hasFnAttribute(Attribute::SanitizeAddress) ||
        F.hasFnAttribute(Attribute::SanitizeMemory) ||
        F.hasFnAttribute(Attribute::SanitizeThread) ||
        F.hasFnAttribute(Attribute::SanitizeHWAddress))
      continue;
    // Avoid constructing loop and scalar-evolution analyses for the many
    // functions that cannot match, and cap the work on very large modules.
    bool hasAdd = false, hasMul = false, hasBound = false;
    unsigned instructions = 0;
    for (BasicBlock &B : F) {
      for (Instruction &I : B) {
        if (++instructions > 20000) break;
        hasAdd |= I.getOpcode() == Instruction::FAdd;
        hasMul |= I.getOpcode() == Instruction::FMul;
        if (auto *C = dyn_cast<ICmpInst>(&I)) {
          auto *K = dyn_cast<ConstantInt>(C->getOperand(1));
          hasBound |= C->getPredicate() == ICmpInst::ICMP_ULT && K &&
                      K->getType()->isIntegerTy(64) &&
                      K->getValue() == APInt(64, -16, true);
        }
      }
      if (instructions > 20000) break;
    }
    if (instructions > 20000 || !hasAdd || !hasMul || !hasBound)
      continue;
    for (unsigned iteration = 0; iteration < 12; iteration++) {
      auto &LI = FAM.getResult<LoopAnalysis>(F);
      auto &SE = FAM.getResult<ScalarEvolutionAnalysis>(F);
      auto &DT = FAM.getResult<DominatorTreeAnalysis>(F);
      auto &AC = FAM.getResult<AssumptionAnalysis>(F);
      auto &TTI = FAM.getResult<TargetIRAnalysis>(F);
      auto &ORE = FAM.getResult<OptimizationRemarkEmitterAnalysis>(F);
      SmallVector<Loop *, 16> todo(LI.begin(), LI.end());
      Loop *candidate = nullptr;
      while (!todo.empty()) {
        Loop *L = todo.pop_back_val();
        for (Loop *S : L->getSubLoops())
          todo.push_back(S);
        if (!L->getSubLoops().empty() || L->getNumBlocks() != 1 ||
            L->getLoopID())
          continue;
        unsigned loads = 0, adds = 0, muls = 0, count = 0;
        bool safe = true;
        for (Instruction &I : *L->getHeader()) {
          ++count;
          if (auto *V = dyn_cast<LoadInst>(&I)) {
            if (!V->isSimple() || !V->getType()->isDoubleTy())
              safe = false;
            loads++;
          } else if (I.mayHaveSideEffects() && !isa<BranchInst>(I))
            safe = false;
          if (I.getOpcode() == Instruction::FAdd)
            adds++;
          if (I.getOpcode() == Instruction::FMul)
            muls++;
        }
        if (!safe || loads != 2 || adds != 1 || muls != 1 || count > 20 ||
            !L->getLoopPreheader() || !L->getLoopLatch() ||
            !L->isLoopSimplifyForm())
          continue;
        auto *B = dyn_cast<CondBrInst>(L->getLoopLatch()->getTerminator());
        auto *Cmp = B ? dyn_cast<ICmpInst>(B->getCondition()) : nullptr;
        if (!Cmp || !Cmp->isEquality())
          continue;
        bool bounded = false;
        for (Value *N : Cmp->operands())
          if (L->isLoopInvariant(N) && smallReductionGuardedBound(N, L, DT))
            bounded = true;
        if (!bounded)
          continue;
        candidate = L;
        break;
      }
      if (!candidate)
        break;
      formLCSSA(*candidate, DT, &LI, &SE);
      UnrollLoopOptions opts{4,     true,    true, false, true,
                             false, nullptr, 1000, true,  false};
      auto R =
          UnrollLoop(candidate, opts, &LI, &SE, &DT, &AC, &TTI, &ORE, true);
      if (R == LoopUnrollResult::Unmodified)
        break;
      changed++;
      FAM.invalidate(F, PreservedAnalyses::none());
    }
  }
  return changed;
}
#else
static unsigned unrollGuardedReductions(Module &) { return 0; }
#endif

#if LLVM_VERSION_MAJOR >= 23
#include "llvm_guarded_divisor.h"
#endif

// Revisit only small modules in which both array-coverage and short-reduction
// rewrites fired. The bounded second O3 run exposes cleanup opportunities
// created after the original pipeline; large modules keep their compile budget.
static bool revisitCoveredArrays(Module &M, bool unrolled) {
  auto *marker = M.getNamedMetadata("elisa.covered-array-revisit");
  if (!marker) return false;
  marker->eraseFromParent();
  if (!unrolled) return false;
  unsigned count = 0, functions = 0;
  for (Function &F : M) {
    if (F.isDeclaration()) continue;
    if (++functions > 32 || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.getSubprogram() || F.hasFnAttribute(Attribute::SanitizeAddress) ||
        F.hasFnAttribute(Attribute::SanitizeMemory) ||
        F.hasFnAttribute(Attribute::SanitizeThread) ||
        F.hasFnAttribute(Attribute::SanitizeHWAddress)) return false;
    count += F.getInstructionCount();
    if (count > 5000) return false;
  }
  PassBuilder PB;
  LoopAnalysisManager LAM; FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM; ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM); PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM); PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
  auto MPM = PB.buildPerModuleDefaultPipeline(OptimizationLevel::O3);
  MPM.run(M, MAM);
#if LLVM_VERSION_MAJOR >= 23
  bool versioned = false;
  for (Function &F : M)
    if (divisorSpecialize(F)) { versioned = true; break; }
  if (versioned) {
    LoopAnalysisManager LAM2; FunctionAnalysisManager FAM2;
    CGSCCAnalysisManager CGAM2; ModuleAnalysisManager MAM2;
    PB.registerModuleAnalyses(MAM2); PB.registerCGSCCAnalyses(CGAM2);
    PB.registerFunctionAnalyses(FAM2); PB.registerLoopAnalyses(LAM2);
    PB.crossRegisterProxies(LAM2, FAM2, CGAM2, MAM2);
    auto cleanup = PB.buildPerModuleDefaultPipeline(OptimizationLevel::O3);
    cleanup.run(M, MAM2);
  }
#endif
  return true;
}

extern "C" unsigned elisacoreUnrollGuardedReductions(LLVMModuleRef ref) {
  if (!ref)
    return 0;
  Module &M = *unwrap(ref);
  if (M.getTargetTriple().getArch() != Triple::aarch64 ||
      M.getNamedMetadata("llvm.dbg.cu"))
    return 0;
  unsigned reductions = unrollGuardedReductions(M);
  revisitCoveredArrays(M, reductions != 0);
  return reductions;
}

extern "C" unsigned elisacoreExpandSmallZeroFills(LLVMModuleRef ref) {
  if (!ref) return 0;
  Module &M = *unwrap(ref);
  if (M.getTargetTriple().getArch() != Triple::aarch64 || !M.getDataLayout().isLittleEndian() || M.getNamedValue("__elisacore_smallzero_fill")) return 0;
  // Inline medium, aligned fixed-size fills on Apple AArch64. LLVM otherwise
  // lowers these to a library call. Keep the size bounded to control code growth.
  unsigned fixedChanged = 0;
  if (M.getTargetTriple().isMacOSX()) {
    SmallVector<MemSetInst *, 8> fixed;
    for (Function &F : M) {
      if (F.hasOptNone() || F.hasOptSize() || F.hasMinSize()) continue;
      SmallVector<MemSetInst *, 2> candidates;
      for (BasicBlock &B : F) for (Instruction &I : B) {
        auto *S = dyn_cast<MemSetInst>(&I);
        if (!S || S->getIntrinsicID() != Intrinsic::memset || S->isVolatile() ||
            S->hasOperandBundles() || S->getDestAddressSpace() != 0 ||
            S->getDestAlign().valueOrOne().value() < 8) continue;
        auto *size = dyn_cast<ConstantInt>(S->getLength());
        auto *value = dyn_cast<ConstantInt>(S->getValue());
        if (!size || !value || !value->isZero() || size->getBitWidth() != 64) continue;
        uint64_t bytes = size->getZExtValue();
        if (bytes < 1024 || bytes > 2048 || bytes % 128 != 0) continue;
        candidates.push_back(S);
      }
      if (candidates.size() == 1) fixed.push_back(candidates.front());
    }
    for (MemSetInst *S : fixed) {
      IRBuilder<> builder(S);
      auto *replacement = builder.CreateMemSetInline(S->getDest(), S->getDestAlign(),
                                                       S->getValue(), S->getLength());
      replacement->setDebugLoc(S->getDebugLoc());
      replacement->copyMetadata(*S);
      S->eraseFromParent();
      ++fixedChanged;
    }
  }
  SmallVector<MemSetInst *, 16> work;
  SmallVector<MemSetInst *, 17> nonzeroFills;
  for (Function &F : M) {
    if (F.hasOptNone() || F.hasOptSize() || F.hasMinSize()) continue;
    for (BasicBlock &B : F) for (Instruction &I : B) {
      auto *S = dyn_cast<MemSetInst>(&I);
      if (S && !S->hasOperandBundles() && !S->getMetadata("elisa.smallzero.fallback") && !S->isVolatile() && S->getDestAddressSpace() == 0 &&
          S->getLength()->getType()->isIntegerTy(64) && !isa<ConstantInt>(S->getLength())) {
        auto *V = dyn_cast<ConstantInt>(S->getValue());
        if (!V) continue;
        if (!V->isZero()) {
          // Each inline expansion adds several branches. Bound code growth
          // for the additional nonzero-fill sites.
          if (nonzeroFills.size() <= 16 &&
              !S->getMetadata(LLVMContext::MD_nontemporal) &&
              !F.hasFnAttribute(Attribute::SanitizeAddress) &&
              !F.hasFnAttribute(Attribute::SanitizeHWAddress) &&
              !F.hasFnAttribute(Attribute::SanitizeMemory) &&
              !F.hasFnAttribute(Attribute::SanitizeThread))
            nonzeroFills.push_back(S);
        } else {
          work.push_back(S);
        }
      }
    }
  }
  if (nonzeroFills.size() <= 16) work.append(nonzeroFills);
  if (work.empty()) return fixedChanged;
  SMDiagnostic error;
  // The assembly parser needs local names to resolve the helper's operands.
  // Native object contexts discard names; restore that policy immediately.
  bool discardNames = M.getContext().shouldDiscardValueNames();
  M.getContext().setDiscardValueNames(false);
  auto helper = parseAssemblyString(R"ELISA(define void @__elisacore_smallzero_fill(ptr %p, i64 %n, i64 %pattern) alwaysinline {
entry:
 %byte = trunc i64 %pattern to i8
 %word = trunc i64 %pattern to i16
 %dword = trunc i64 %pattern to i32
 %half = zext i64 %pattern to i128
 %high = shl i128 %half, 64
 %vector = or i128 %high, %half
 %empty = icmp eq i64 %n, 0
 br i1 %empty, label %exit, label %choose
choose:
 %small = icmp ule i64 %n, 64
 br i1 %small, label %size8, label %large
large:
 call void @llvm.memset.p0.i64(ptr %p, i8 %byte, i64 %n, i1 false), !elisa.smallzero.fallback !0
 br label %exit
size8:
 %eight = icmp uge i64 %n, 8
 br i1 %eight, label %size16, label %size4
size16:
 %sixteen = icmp uge i64 %n, 16
 br i1 %sixteen, label %size32, label %fill8
fill8:
 %off8 = sub i64 %n, 8
 %end8 = getelementptr i8, ptr %p, i64 %off8
 store i64 %pattern, ptr %p, align 1
 store i64 %pattern, ptr %end8, align 1
 br label %exit
size32:
 %big = icmp ugt i64 %n, 32
 br i1 %big, label %fill64, label %fill16
fill16:
 %off16 = sub i64 %n, 16
 %end16 = getelementptr i8, ptr %p, i64 %off16
 store i128 %vector, ptr %p, align 1
 store i128 %vector, ptr %end16, align 1
 br label %exit
fill64:
 %p16 = getelementptr i8, ptr %p, i64 16
 %off32 = sub i64 %n, 32
 %end32 = getelementptr i8, ptr %p, i64 %off32
 %offlast = sub i64 %n, 16
 %last = getelementptr i8, ptr %p, i64 %offlast
 store i128 %vector, ptr %p, align 1
 store i128 %vector, ptr %p16, align 1
 store i128 %vector, ptr %end32, align 1
 store i128 %vector, ptr %last, align 1
 br label %exit
size4:
 %four = icmp uge i64 %n, 4
 br i1 %four, label %fill4, label %size2
fill4:
 %off4 = sub i64 %n, 4
 %end4 = getelementptr i8, ptr %p, i64 %off4
 store i32 %dword, ptr %p, align 1
 store i32 %dword, ptr %end4, align 1
 br label %exit
size2:
 %two = icmp uge i64 %n, 2
 br i1 %two, label %fill2, label %fill1
fill2:
 %off2 = sub i64 %n, 2
 %end2 = getelementptr i8, ptr %p, i64 %off2
 store i16 %word, ptr %p, align 1
 store i16 %word, ptr %end2, align 1
 br label %exit
fill1:
 store i8 %byte, ptr %p, align 1
 br label %exit
exit:
 ret void
}

declare void @llvm.memset.p0.i64(ptr, i8, i64, i1 immarg)
!0 = !{}
)ELISA", error, M.getContext());
  M.getContext().setDiscardValueNames(discardNames);
  if (!helper) return fixedChanged;
  helper->setDataLayout(M.getDataLayout());
  helper->setTargetTriple(M.getTargetTriple());
  if (Linker::linkModules(M, std::move(helper))) return fixedChanged;
  Function *F = M.getFunction("__elisacore_smallzero_fill");
  if (!F) return fixedChanged;
  F->setLinkage(GlobalValue::InternalLinkage);
  unsigned changed = 0;
  for (MemSetInst *S : work) {
    IRBuilder<> builder(S);
    uint64_t byte = cast<ConstantInt>(S->getValue())->getZExtValue();
    Value *pattern = builder.getInt64(byte * 0x0101010101010101ULL);
    auto *call = builder.CreateCall(F, {S->getDest(), S->getLength(), pattern});
    call->setDebugLoc(S->getDebugLoc());
    InlineFunctionInfo info;
    if (InlineFunction(*call, info).isSuccess()) {
      S->eraseFromParent();
      ++changed;
    } else call->eraseFromParent();
  }
  if (F->use_empty()) F->eraseFromParent();
  return changed + fixedChanged;
}
