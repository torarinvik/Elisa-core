//go:build cgo
//  Bounded late loop versioning: hoist invariant branches without global LLVM
//  tuning.
#include <llvm-c/Core.h>
#include <llvm/Config/llvm-config.h>
#if LLVM_VERSION_MAJOR >= 23
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Transforms/InstCombine/InstCombine.h>
#include <llvm/Transforms/Scalar/SimplifyCFG.h>
#include <llvm/Transforms/Utils/BasicBlockUtils.h>
#include <llvm/Transforms/Utils/Cloning.h>
#include <llvm/Transforms/Utils/LCSSA.h>
#include <llvm/Transforms/Utils/LoopSimplify.h>
using namespace llvm;
namespace {
static bool boundedUnswitchOnce(Function &F, unsigned Budget) {
  DominatorTree DT(F);
  LoopInfo LI(DT);
  for (Loop *Root : LI)
    for (Loop *L : Root->getLoopsInPreorder()) {
      if (!L->getSubLoops().empty() || !L->isLoopSimplifyForm() ||
          L->getNumBlocks() > 40)
        continue;
      BasicBlock *P = L->getLoopPreheader();
      unsigned N = 0;
      bool safe = true;
      for (BasicBlock *BB : L->blocks()) {
        if (BB->hasAddressTaken() || BB->isEHPad())
          safe = false;
        for (Instruction &I : *BB) {
          ++N;
          if (isa<AllocaInst>(I) || I.getType()->isTokenTy() ||
              isa<IndirectBrInst>(I))
            safe = false;
          if (auto *C = dyn_cast<CallBase>(&I)) {
            auto *II = dyn_cast<IntrinsicInst>(C);
            if (!II || C->isConvergent() || C->cannotDuplicate() ||
                C->hasOperandBundles())
              safe = false;
            else
              switch (II->getIntrinsicID()) {
              case Intrinsic::trap:
              case Intrinsic::sadd_with_overflow:
              case Intrinsic::ssub_with_overflow:
              case Intrinsic::smul_with_overflow:
                break;
              default:
                safe = false;
              }
          }
          if (auto *V = dyn_cast<LoadInst>(&I))
            if (!V->isSimple())
              safe = false;
          if (auto *V = dyn_cast<StoreInst>(&I))
            if (!V->isSimple())
              safe = false;
        }
      }
      if (!safe || N > 300 || N < 20 ||
          F.getInstructionCount() + N + 3 > Budget)
        continue;
      Value *Cond = nullptr;
      for (BasicBlock *BB : L->blocks()) {
        auto *B = dyn_cast<CondBrInst>(BB->getTerminator());
        if (!B || !L->isLoopInvariant(B->getCondition()) ||
            isa<Constant>(B->getCondition()))
          continue;
        if (auto *I = dyn_cast<Instruction>(B->getCondition()))
          if (!DT.dominates(I, P->getTerminator()))
            continue;
        if (!L->contains(B->getSuccessor(0)) ||
            !L->contains(B->getSuccessor(1)))
          continue;
        Cond = B->getCondition();
        break;
      }
      if (!Cond)
        continue;
      // Keep all preheader computations in a common dispatcher. Clone only the
      // newly split, empty preheader and loop; never duplicate setup side
      // effects.
      BasicBlock *PH = SplitBlock(P, P->getTerminator(), &DT, &LI, nullptr,
                                  "unswitch.preheader");
      ValueToValueMapTy Map;
      SmallVector<BasicBlock *, 40> Clones;
      cloneLoopWithPreheader(PH, P, L, Map, ".unswitch", &LI, &DT, Clones);
      remapInstructionsInBlocks(Clones, Map);
      // The clone has the same exits; extend their PHIs before any
      // simplification.
      SmallVector<BasicBlock *, 8> Exits;
      L->getUniqueExitBlocks(Exits);
      for (BasicBlock *Exit : Exits)
        for (PHINode &Phi : Exit->phis()) {
          unsigned Count = Phi.getNumIncomingValues();
          for (unsigned i = 0; i < Count; ++i) {
            auto It = Map.find(Phi.getIncomingBlock(i));
            if (It == Map.end())
              continue;
            Value *V = Phi.getIncomingValue(i);
            auto VI = Map.find(V);
            Phi.addIncoming(VI == Map.end() ? V
                                            : static_cast<Value *>(VI->second),
                            cast<BasicBlock>(It->second));
          }
        }
      IRBuilder<> B(P->getTerminator());
      Value *Frozen = B.CreateFreeze(Cond, "unswitch.condition");
      B.CreateCondBr(Frozen, PH, cast<BasicBlock>(Map[PH]));
      P->getTerminator()->eraseFromParent();
      for (BasicBlock *BB : L->blocks()) {
        auto *Br = dyn_cast<CondBrInst>(BB->getTerminator());
        if (Br && Br->getCondition() == Cond) {
          Br->setCondition(B.getTrue());
          cast<CondBrInst>(cast<BasicBlock>(Map[BB])->getTerminator())
              ->setCondition(B.getFalse());
        }
      }
      return true;
    }
  return false;
}
static bool hasBoundedUnswitchCandidate(Function &F) {
  DominatorTree DT(F);
  LoopInfo LI(DT);
  for (Loop *Root : LI)
    for (Loop *L : Root->getLoopsInPreorder()) {
      if (!L->getSubLoops().empty() || L->getNumBlocks() > 40)
        continue;
      for (BasicBlock *BB : L->blocks())
        if (auto *B = dyn_cast<CondBrInst>(BB->getTerminator()))
          if (!isa<Constant>(B->getCondition()) &&
              L->isLoopInvariant(B->getCondition()) &&
              L->contains(B->getSuccessor(0)) &&
              L->contains(B->getSuccessor(1)))
            return true;
    }
  return false;
}
struct BoundedUnswitch : PassInfoMixin<BoundedUnswitch> {
  PreservedAnalyses run(Function &F, FunctionAnalysisManager &AM) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() ||
        F.hasMinSize() || F.getSubprogram() || F.hasPersonalityFn() ||
        F.hasGC() || F.getInstructionCount() < 20 || F.getInstructionCount() > 1000 ||
        F.hasFnAttribute("elisa.unswitch.versioned") ||
        F.hasFnAttribute(Attribute::Convergent) ||
        F.hasFnAttribute(Attribute::NoDuplicate) ||
        F.getEntryCount().has_value())
      return PreservedAnalyses::all();
    if (!hasBoundedUnswitchCandidate(F))
      return PreservedAnalyses::all();
    unsigned Initial = F.getInstructionCount();
    bool Changed = false;
    FunctionPassManager Prepare;
    Prepare.addPass(LoopSimplifyPass());
    Prepare.addPass(LCSSAPass());
    Prepare.run(F, AM);
    for (unsigned i = 0; i < 8 && F.getInstructionCount() < Initial * 4 &&
                         F.getInstructionCount() < 1800;
         ++i) {
      if (!boundedUnswitchOnce(F, std::min(1800u, Initial * 4)))
        break;
      Changed = true;
      AM.invalidate(F, PreservedAnalyses::none());
      FunctionPassManager Cleanup;
      Cleanup.addPass(InstCombinePass());
      Cleanup.addPass(SimplifyCFGPass());
      Cleanup.addPass(LoopSimplifyPass());
      Cleanup.addPass(LCSSAPass());
      Cleanup.run(F, AM);
    }
    if (Changed)
      F.addFnAttr("elisa.unswitch.versioned");
    // Preparation may canonicalize loops even if a later safety check declines.
    return PreservedAnalyses::none();
  }
};
} // namespace
extern "C" unsigned elisacoreBoundedLoopUnswitch(LLVMModuleRef Ref) {
  if (!Ref)
    return 0;
  Module &M = *unwrap(Ref);
  if (M.getTargetTriple().getArch() != Triple::aarch64 ||
      M.getNamedMetadata("llvm.dbg.cu"))
    return 0;
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
  unsigned Changed = 0;
  for (Function &F : M) {
    bool Before = F.hasFnAttribute("elisa.unswitch.versioned");
    FunctionPassManager PM;
    PM.addPass(BoundedUnswitch());
    PM.run(F, FAM);
    if (!Before && F.hasFnAttribute("elisa.unswitch.versioned"))
      ++Changed;
  }
  return Changed;
}

#else
extern "C" unsigned elisacoreBoundedLoopUnswitch(LLVMModuleRef) { return 0; }
#endif
