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
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Transforms/Utils/LoopPeel.h>
#include <llvm/Transforms/Utils/LoopSimplify.h>
#include <llvm/Transforms/Utils/LoopUtils.h>
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
      ++count;
      break; // Invalidate function analyses before inspecting another loop.
    }
  }
  return count;
}
#else
extern "C" unsigned elisacorePeelSmallValidationLoops(LLVMModuleRef) { return 0; }
#endif
