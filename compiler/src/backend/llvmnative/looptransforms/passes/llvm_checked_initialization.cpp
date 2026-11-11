// Unroll constant-trip private initializers so LLVM can propagate values
// across array stores and fold their checked arithmetic.
#include <llvm/Analysis/OptimizationRemarkEmitter.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Analysis/TargetTransformInfo.h>
#include <llvm/IR/CFG.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Transforms/Utils/Cloning.h>
#include <llvm/Transforms/Utils/LoopSimplify.h>
#include <llvm/Transforms/Utils/LoopUtils.h>
#include <llvm/Transforms/Utils/UnrollLoop.h>

#if LLVM_VERSION_MAJOR >= 23
namespace {
static bool isInitializerCheckedArithmetic(const Instruction &I) {
  auto *C = dyn_cast<IntrinsicInst>(&I);
  if (!C) return false;
  switch (C->getIntrinsicID()) {
  case Intrinsic::sadd_with_overflow:
  case Intrinsic::ssub_with_overflow:
  case Intrinsic::smul_with_overflow:
    return !C->hasOperandBundles() && !C->isConvergent() && !C->cannotDuplicate();
  default:
    return false;
  }
}

static bool isCheckedInitializer(Loop &L, BasicBlock &Latch) {
  bool HasStore = false, HasCheckedArithmetic = false;
  AllocaInst *Storage = nullptr;
  SmallVector<BasicBlock *, 4> Exiting;
  L.getExitingBlocks(Exiting);
  for (BasicBlock *Block : Exiting) {
    if (Block == &Latch) continue;
    auto *Br = dyn_cast<CondBrInst>(Block->getTerminator());
    if (!Br) return false;
    for (BasicBlock *Exit : successors(Block)) {
      if (L.contains(Exit)) continue;
      auto *Trap = dyn_cast<IntrinsicInst>(&Exit->front());
      if (Exit->size() != 2 || !Trap || Trap->getIntrinsicID() != Intrinsic::trap ||
          !isa<UnreachableInst>(Exit->getTerminator()))
        return false;
    }
  }
  for (BasicBlock *Block : L.blocks()) {
    for (Instruction &I : *Block) {
      Value *Address = nullptr;
      if (auto *Store = dyn_cast<StoreInst>(&I)) {
        if (!Store->isSimple()) return false;
        Address = Store->getPointerOperand();
        HasStore = true;
      } else if (auto *Load = dyn_cast<LoadInst>(&I)) {
        if (!Load->isSimple()) return false;
        Address = Load->getPointerOperand();
      } else if (isa<CallBase>(I)) {
        if (!isInitializerCheckedArithmetic(I)) return false;
        HasCheckedArithmetic = true;
      } else if (!I.isTerminator() && (I.mayReadOrWriteMemory() || I.mayHaveSideEffects())) {
        return false;
      }
      if (Address) {
        auto *Object = dyn_cast<AllocaInst>(getUnderlyingObject(Address));
        if (!Object || !Object->isStaticAlloca() ||
            !isa<ArrayType>(Object->getAllocatedType()) || (Storage && Storage != Object))
          return false;
        Storage = Object;
      }
      for (Value *V : I.operands()) {
        if (auto *Outside = dyn_cast<Instruction>(V)) {
          if (!L.contains(Outside) && !isa<AllocaInst>(Outside)) return false;
        } else if (!isa<Constant>(V) && !isa<BasicBlock>(V)) {
          return false;
        }
      }
    }
  }
  return HasStore && HasCheckedArithmetic;
}

static bool unrollCheckedInitializers(Function &F, unsigned &Budget) {
  PassBuilder PB;
  FunctionAnalysisManager FAM;
  PB.registerFunctionAnalyses(FAM);
  auto &LI = FAM.getResult<LoopAnalysis>(F);
  auto &DT = FAM.getResult<DominatorTreeAnalysis>(F);
  auto &SE = FAM.getResult<ScalarEvolutionAnalysis>(F);
  auto &AC = FAM.getResult<AssumptionAnalysis>(F);
  auto &TTI = FAM.getResult<TargetIRAnalysis>(F);
  auto &ORE = FAM.getResult<OptimizationRemarkEmitterAnalysis>(F);
  SmallVector<Loop *, 8> Loops(LI.begin(), LI.end());
  bool Changed = false;
  for (Loop *L : Loops) {
    auto *Latch = L->getLoopLatch();
    if (!Latch || !L->getSubLoops().empty() || L->getLoopID() ||
        !isCheckedInitializer(*L, *Latch))
      continue;
    unsigned Trips = SE.getSmallConstantTripCount(L, Latch);
    if (Trips < 2 || Trips > 64 || SE.getSmallConstantMaxTripCount(L) != Trips) continue;
    unsigned Instructions = 0;
    for (BasicBlock *Block : L->blocks()) Instructions += Block->size();
    unsigned Growth = Trips * Instructions;
    if (Growth > 1200 || Growth > Budget) continue;
    Budget -= Growth;
    simplifyLoop(L, &DT, &LI, &SE, &AC, nullptr, false);
    if (!L->isLoopSimplifyForm()) continue;
    formLCSSA(*L, DT, &LI, &SE);
    UnrollLoopOptions Options{Trips, true, false, false, false, false, nullptr, 1000};
    Changed |= UnrollLoop(L, Options, &LI, &SE, &DT, &AC, &TTI, &ORE, true) !=
               LoopUnrollResult::Unmodified;
  }
  return Changed;
}
static unsigned tryFoldCheckedInitializers(Function &F, LLVMTargetMachineRef Target,
                                            unsigned &Budget) {
  // The unrolled form is useful only if LLVM can resolve enough of the memory
  // values. Try it on a copy and keep the original when simplification stalls.
  ValueToValueMapTy Map;
  Function *Trial = CloneFunction(&F, Map);
  if (!unrollCheckedInitializers(*Trial, Budget)) {
    Trial->eraseFromParent();
    return 0;
  }

  auto Options = LLVMCreatePassBuilderOptions();
  const char *Cleanup =
      "sroa,early-cse<memssa>,gvn,sccp,instcombine,simplifycfg,dse,"
      "sroa,early-cse<memssa>,gvn,sccp,instcombine,simplifycfg,dse,slp-vectorizer";
  auto Error = LLVMRunPassesOnFunction(wrap(Trial), Cleanup, Target, Options);
  LLVMDisposePassBuilderOptions(Options);
  if (Error) {
    LLVMConsumeError(Error);
    Trial->eraseFromParent();
    return 2;
  }
  if (Trial->getInstructionCount() >= F.getInstructionCount() ||
      Trial->size() > F.size()) {
    Trial->eraseFromParent();
    return 0;
  }

  for (Argument &Arg : Trial->args())
    Arg.replaceAllUsesWith(F.getArg(Arg.getArgNo()));
  Trial->replaceAllUsesWith(&F);
  for (BasicBlock &B : F) B.dropAllReferences();
  F.erase(F.begin(), F.end());
  F.splice(F.end(), Trial);
  Trial->eraseFromParent();
  return 1;
}

static bool mayFoldCheckedInitializers(Function &F) {
  if (F.size() < 3 || F.size() > 96 || F.hasMetadata() ||
      F.hasFnAttribute(Attribute::SanitizeAddress) ||
      F.hasFnAttribute(Attribute::SanitizeHWAddress) ||
      F.hasFnAttribute(Attribute::SanitizeMemory) ||
      F.hasFnAttribute(Attribute::SanitizeThread)) return false;
  bool Array = false;
  for (Instruction &I : F.getEntryBlock())
    if (auto *A = dyn_cast<AllocaInst>(&I))
      Array |= A->isStaticAlloca() && isa<ArrayType>(A->getAllocatedType());
  if (!Array) return false;
  bool Checked = false;
  for (BasicBlock &B : F) {
    if (B.hasAddressTaken()) return false;
    for (Instruction &I : B)
      Checked |= isInitializerCheckedArithmetic(I);
  }
  return Checked;
}
} // namespace
#endif
