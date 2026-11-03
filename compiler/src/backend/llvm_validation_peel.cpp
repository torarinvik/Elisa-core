//go:build cgo
#include <llvm/Config/llvm-config.h>
#include <llvm-c/Core.h>
#if LLVM_VERSION_MAJOR == 23
#include <llvm/IR/Dominators.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Analysis/AssumptionCache.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/OptimizationRemarkEmitter.h>
#include <llvm/Analysis/TargetTransformInfo.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Support/CBindingWrapping.h>
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
extern "C" unsigned elisacoreUnrollNestedScans(LLVMModuleRef) { return 0; }
extern "C" unsigned elisacorePeelSmallValidationLoops(LLVMModuleRef) { return 0; }
#endif
