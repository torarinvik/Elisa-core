//go:build cgo
// Unroll guarded reductions and revisit covered arrays.
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
