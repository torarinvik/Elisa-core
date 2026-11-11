#include <llvm-c/Core.h>
#include <llvm/Config/llvm-config.h>

#if LLVM_VERSION_MAJOR >= 23
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>
#include <llvm/Analysis/AssumptionCache.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/OptimizationRemarkEmitter.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Analysis/TargetTransformInfo.h>
#include <llvm/IR/Dominators.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Transforms/Utils/LoopPeel.h>
#include <llvm/Transforms/Utils/LoopUtils.h>
#include <llvm/Transforms/Utils/UnrollLoop.h>
#include <llvm/Transforms/Utils/ValueMapper.h>

using namespace llvm;

static bool BDU_SkipFunction(const Function &F, const Module &M) {
  if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
      F.getSubprogram() || M.getNamedMetadata("llvm.dbg.cu")) return true;
  for (StringRef Flag : {"SanitizeAddress", "SanitizeHWAddress", "SanitizeMemory",
                         "SanitizeThread", "SanitizeRealtime"})
    if (M.getModuleFlag(Flag)) return true;
  for (Attribute::AttrKind Attr : {Attribute::SanitizeAddress, Attribute::SanitizeHWAddress,
        Attribute::SanitizeMemory, Attribute::SanitizeThread, Attribute::SanitizeMemTag,
        Attribute::SanitizeRealtime, Attribute::SanitizeRealtimeBlocking})
    if (F.hasFnAttribute(Attr)) return true;
  return false;
}

// Unroll a three-state addition by its rotation length or a scaled two-state
// recurrence by two rotations. Peel the first scaled iteration to keep a
// one-trip input out of the runtime remainder loop.
static unsigned laggedRecurrenceUnrollFactor(BasicBlock &B) {
  auto *Branch = dyn_cast<CondBrInst>(B.getTerminator());
  auto *Cmp = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  if (!Cmp || !Cmp->isEquality() || B.size() != 8 ||
      Branch->getSuccessor(Cmp->getPredicate() == ICmpInst::ICMP_EQ ? 1 : 0) != &B ||
      Branch->getSuccessor(Cmp->getPredicate() == ICmpInst::ICMP_EQ ? 0 : 1) == &B)
    return 0;
  auto *Counter = dyn_cast<PHINode>(Cmp->getOperand(0));
  if (!Counter || Counter->getParent() != &B ||
      !Counter->getType()->isIntegerTy(64) || Counter->getNumIncomingValues() != 2)
    return 0;
  auto *Step = dyn_cast<BinaryOperator>(Counter->getIncomingValueForBlock(&B));
  auto *One = Step ? dyn_cast<ConstantInt>(Step->getOperand(1)) : nullptr;
  if (!Step || Step->getParent() != &B || Step->getOpcode() != Instruction::Add ||
      Step->getOperand(0) != Counter || !One || !One->isOne()) return 0;
  unsigned PhiCount = 0;
  BinaryOperator *Sum = nullptr, *Scale = nullptr;
  for (Instruction &I : B) {
    if (isa<PHINode>(I)) { ++PhiCount; continue; }
    if (&I == Step || &I == Cmp || &I == Branch) continue;
    if (!Sum && I.getOpcode() == Instruction::Add) {
      Sum = cast<BinaryOperator>(&I);
    } else if (!Scale && I.getOpcode() == Instruction::Shl) {
      Scale = cast<BinaryOperator>(&I);
    } else {
      return 0;
    }
  }
  if (!Sum) return 0;
  auto Shift = [&](Value *V) -> PHINode * {
    auto *P = dyn_cast<PHINode>(V);
    return P && P != Counter && P->getParent() == &B &&
           P->getNumIncomingValues() == 2 ? P : nullptr;
  };
  if (Scale) {
    if (PhiCount != 3 || !Scale->hasOneUse()) return 0;
    auto *OneBit = dyn_cast<ConstantInt>(Scale->getOperand(1));
    auto *Current = Shift(Scale->getOperand(0));
    Value *Other = Sum->getOperand(0);
    if (Sum->getOperand(1) != Scale) {
      if (Other != Scale) return 0;
      Other = Sum->getOperand(1);
    }
    auto *Previous = Shift(Other);
    return OneBit && OneBit->isOne() && Current && Previous && Current != Previous &&
                   Previous->getIncomingValueForBlock(&B) == Current &&
                   Current->getIncomingValueForBlock(&B) == Sum ? 4 : 0;
  }
  if (PhiCount != 4) return 0;
  PHINode *A = Shift(Sum->getOperand(0)), *Next = Shift(Sum->getOperand(1));
  if (!A || !Next || A == Next) return 0;
  if (A->getIncomingValueForBlock(&B) != Next) std::swap(A, Next);
  if (A->getIncomingValueForBlock(&B) != Next) return 0;
  auto *Newest = Shift(Next->getIncomingValueForBlock(&B));
  return Newest && Newest != A && Newest != Next &&
         Newest->getIncomingValueForBlock(&B) == Sum ? 3 : 0;
}

extern "C" unsigned elisacoreUnrollLaggedRecurrences(LLVMModuleRef Ref) {
  if (!Ref) return 0;
  Module &M = *unwrap(Ref);
  if (!M.getTargetTriple().isAArch64()) return 0;
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
  unsigned Changed = 0, Analyzed = 0;
  for (Function &F : M) {
    if (BDU_SkipFunction(F, M) || F.size() > 24) continue;
    for (BasicBlock &B : F) {
      unsigned Factor = laggedRecurrenceUnrollFactor(B);
      if (!Factor) continue;
      if (++Analyzed > 32) return Changed;
      auto &LI = FAM.getResult<LoopAnalysis>(F);
      Loop *L = LI.getLoopFor(&B);
      if (!L || L->getHeader() != &B || L->getParentLoop() || L->getLoopID() ||
          L->getNumBlocks() != 1) break;
      auto &SE = FAM.getResult<ScalarEvolutionAnalysis>(F);
      unsigned MaxTrips = SE.getSmallConstantMaxTripCount(L);
      if (MaxTrips < 16 || MaxTrips > 256) break;
      auto &DT = FAM.getResult<DominatorTreeAnalysis>(F);
      auto &AC = FAM.getResult<AssumptionAnalysis>(F);
      auto &TTI = FAM.getResult<TargetIRAnalysis>(F);
      auto &ORE = FAM.getResult<OptimizationRemarkEmitterAnalysis>(F);
      if (simplifyLoop(L, &DT, &LI, &SE, &AC, nullptr, false)) ++Changed;
      if (!L->isLoopSimplifyForm()) break;
      if (formLCSSA(*L, DT, &LI, &SE)) ++Changed;
      if (Factor == 4) {
        if (!canPeel(L)) break;
        ValueToValueMapTy FirstIteration;
        peelLoop(L, 1, false, &LI, &SE, DT, &AC, true, FirstIteration);
        ++Changed;
      }
      UnrollLoopOptions Options{Factor, true, true, true, false, false, nullptr, 1000};
      if (UnrollLoop(L, Options, &LI, &SE, &DT, &AC, &TTI, &ORE, true) !=
          LoopUnrollResult::Unmodified) ++Changed;
      break;
    }
  }
  return Changed;
}

#else
extern "C" unsigned elisacoreUnrollLaggedRecurrences(LLVMModuleRef) { return 0; }
#endif
