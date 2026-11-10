//go:build cgo

// Bounded unrolling for simple monotone unsigned-division loops.
// This pass never weakens the loop's tests or arithmetic checks. The LVI range
// is used only to derive an unroll factor/cap; LLVM's unroller retains runtime
// exit checks and a remainder loop.
#include <llvm/Config/llvm-config.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/ConstantRange.h>
#include <llvm/Analysis/LazyValueInfo.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Analysis/AssumptionCache.h>
#include <llvm/Analysis/OptimizationRemarkEmitter.h>
#include <llvm/Analysis/TargetTransformInfo.h>
#include <llvm/IR/Dominators.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Transforms/Utils/LoopUtils.h>
#include <llvm/Transforms/Utils/UnrollLoop.h>
#include <llvm/Support/CBindingWrapping.h>

#if LLVM_VERSION_MAJOR >= 23
using namespace llvm;

namespace {
constexpr unsigned BDU_MaxTrips = 11;
constexpr unsigned BDU_MaxFunctionInstructions = 800;
constexpr unsigned BDU_MaxLoopInstructions = 64;
constexpr unsigned BDU_MaxGrowthInstructions = 256;

static bool BDU_NoUnrollPolicy(const Loop &L) {
  MDNode *ID = L.getLoopID();
  if (!ID) return true;
  if (ID->getNumOperands() > 64) return false;
  for (unsigned I = 1; I < ID->getNumOperands(); ++I) {
    auto *Property = dyn_cast_or_null<MDNode>(ID->getOperand(I).get());
    if (!Property || !Property->getNumOperands()) return false;
    auto *Tag = dyn_cast_or_null<MDString>(Property->getOperand(0).get());
    if (!Tag || Tag->getString().starts_with("llvm.loop.unroll")) return false;
  }
  return true;
}

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

// Cheap gate before building LoopInfo/SE/DT/LVI for a function. The current
// transform requires a constant unsigned division recurrence; functions
// without one cannot qualify.
static bool BDU_MayContainCandidate(const Function &F, unsigned FunctionInsts) {
  if (FunctionInsts > BDU_MaxFunctionInstructions) return false;
  bool HasDiv = false;
  for (const BasicBlock &BB : F) for (const Instruction &I : BB) {
    if (auto *BO = dyn_cast<BinaryOperator>(&I)) {
      if (BO->getOpcode() == Instruction::UDiv) {
        auto *C = dyn_cast<ConstantInt>(BO->getOperand(1));
        if (C && C->getValue().getActiveBits() <= 32 && C->getZExtValue() >= 2)
          HasDiv = true;
      }
    }
    if (HasDiv) return true;
  }
  return false;
}

static bool BDU_IsTrap(const CallBase &CB) {
  auto *II = dyn_cast<IntrinsicInst>(&CB);
  return II && II->getIntrinsicID() == Intrinsic::trap &&
         !CB.hasOperandBundles() && !CB.isConvergent();
}

static bool BDU_IsCheckedArithmetic(const CallBase &CB) {
  auto *II = dyn_cast<IntrinsicInst>(&CB);
  if (!II || CB.hasOperandBundles() || CB.isConvergent()) return false;
  switch (II->getIntrinsicID()) {
  case Intrinsic::sadd_with_overflow:
  case Intrinsic::ssub_with_overflow:
  case Intrinsic::smul_with_overflow:
  case Intrinsic::uadd_with_overflow:
  case Intrinsic::usub_with_overflow:
  case Intrinsic::umul_with_overflow:
    return true;
  default:
    return false;
  }
}

static bool BDU_IsTrapBlock(const BasicBlock &BB) {
  bool SawTrap = false;
  for (const Instruction &I : BB) {
    if (isa<DbgInfoIntrinsic>(I)) continue;
    if (auto *CB = dyn_cast<CallBase>(&I)) {
      if (!BDU_IsTrap(*CB) || SawTrap) return false;
      SawTrap = true;
    } else if (!isa<UnreachableInst>(I)) {
      return false;
    }
  }
  return SawTrap && isa<UnreachableInst>(BB.getTerminator());
}

static bool BDU_SafeLoop(const Loop &L, unsigned &Insts) {
  if ((L.getNumBlocks() != 1 && L.getNumBlocks() != 2) || !L.getSubLoops().empty()) return false;
  unsigned NormalExitEdges = 0;
  for (BasicBlock *BB : L.blocks()) {
    if (!isa<BranchInst>(BB->getTerminator())) return false;
    for (Instruction &I : *BB) {
      ++Insts;
      if (isa<LoadInst>(I) || isa<StoreInst>(I) || isa<AtomicRMWInst>(I) ||
          isa<AtomicCmpXchgInst>(I) || isa<AllocaInst>(I) || isa<InvokeInst>(I) ||
          isa<CallBrInst>(I) || isa<VAArgInst>(I) || isa<FenceInst>(I) ||
          isa<IndirectBrInst>(I)) return false;
      if (auto *CB = dyn_cast<CallBase>(&I))
        if (!BDU_IsTrap(*CB) && !BDU_IsCheckedArithmetic(*CB)) return false;
      if (auto *BI = dyn_cast<BranchInst>(&I)) {
        for (BasicBlock *Succ : BI->successors()) {
          if (!L.contains(Succ) && !BDU_IsTrapBlock(*Succ)) ++NormalExitEdges;
        }
      }
    }
  }
  return Insts <= BDU_MaxLoopInstructions && NormalExitEdges == 1;
}

struct BDU_Candidate {
  Loop *L = nullptr;
  PHINode *P = nullptr;
  BinaryOperator *Div = nullptr;
  Value *Initial = nullptr;
  BasicBlock *Pred = nullptr;
  unsigned Divisor = 0;
  unsigned Bound = 0;
};

static bool BDU_FindCandidate(Loop &L, LazyValueInfo &LVI,
                              BDU_Candidate &Out) {
  BasicBlock *Header = L.getHeader();
  BasicBlock *Pred = L.getLoopPredecessor();
  if (!Header || !Pred || !L.contains(Header) || !BDU_NoUnrollPolicy(L)) return false;
  unsigned LoopInsts = 0;
  if (!BDU_SafeLoop(L, LoopInsts)) return false;
  auto *BI = dyn_cast<BranchInst>(Header->getTerminator());
  auto *Cmp = BI && BI->isConditional()
                  ? dyn_cast<ICmpInst>(BI->getCondition()) : nullptr;
  if (!Cmp) return false;
  BasicBlock *T = BI->getSuccessor(0), *F = BI->getSuccessor(1);
  if (L.contains(T) == L.contains(F)) return false;

  auto IsZero = [](Value *V) {
    auto *C = dyn_cast<ConstantInt>(V);
    return C && C->isZero();
  };
  for (PHINode &P : Header->phis()) {
    if (P.getNumIncomingValues() != 2) continue;
    int PreIdx = P.getBasicBlockIndex(Pred);
    if (PreIdx < 0) continue;
    int BackIdx = 1 - PreIdx;
    BasicBlock *BackPred = P.getIncomingBlock(BackIdx);
    if (!L.contains(BackPred)) continue;
    auto *Div = dyn_cast<BinaryOperator>(P.getIncomingValue(BackIdx));
    if (!Div || Div->getOpcode() != Instruction::UDiv || Div->getOperand(0) != &P ||
        Div->getParent() != BackPred) continue;
    auto *DivC = dyn_cast<ConstantInt>(Div->getOperand(1));
    if (!DivC || DivC->getValue().getActiveBits() > 32) continue;
    uint64_t Divisor64 = DivC->getZExtValue();
    if (Divisor64 < 2 || Divisor64 > UINT32_MAX) continue;
    unsigned Divisor = static_cast<unsigned>(Divisor64);

    Value *Other = nullptr;
    if (Cmp->getOperand(0) == &P) Other = Cmp->getOperand(1);
    if (Cmp->getOperand(1) == &P) Other = Cmp->getOperand(0);
    if (!Other) continue;
    bool ThresholdExit = false;
    bool TestMatches = false;
    if (Cmp->getPredicate() == ICmpInst::ICMP_ULT) {
      auto *K = dyn_cast<ConstantInt>(Other);
      TestMatches = K && K->getValue().getActiveBits() <= 32 &&
                    K->getZExtValue() == Divisor && L.contains(F) && !L.contains(T);
      ThresholdExit = true;
    } else if (Cmp->getPredicate() == ICmpInst::ICMP_NE) {
      TestMatches = IsZero(Other) && L.contains(T) && !L.contains(F);
    }
    if (!TestMatches) continue;

    Instruction *Context = Pred->getTerminator();
    ConstantRange R = LVI.getConstantRangeOnEdge(P.getIncomingValue(PreIdx),
                                                  Pred, Header, Context);
    if (R.isEmptySet() || R.isFullSet() || R.getBitWidth() == 0) continue;
    APInt Max = R.getUnsignedMax();
    APInt C(Max.getBitWidth(), Divisor);
    unsigned Trips = 0;
    while (ThresholdExit ? Max.uge(C) : !Max.isZero()) {
      if (++Trips > BDU_MaxTrips) break;
      Max = Max.udiv(C);
    }
    if (!Trips || Trips > BDU_MaxTrips || LoopInsts * (Trips - 1) > BDU_MaxGrowthInstructions)
      continue;

    Out = {&L, &P, Div, P.getIncomingValue(PreIdx), Pred, Divisor, Trips};
    return true;
  }
  return false;
}

static PHINode *BDU_FindRecurrence(Loop &L, unsigned Divisor) {
  for (PHINode &P : L.getHeader()->phis()) {
    if (P.getNumIncomingValues() != 2) continue;
    Value *InsideValue = nullptr;
    for (unsigned I = 0; I != 2; ++I)
      if (L.contains(P.getIncomingBlock(I))) InsideValue = P.getIncomingValue(I);
    auto *D = dyn_cast_or_null<BinaryOperator>(InsideValue);
    auto *C = D ? dyn_cast<ConstantInt>(D->getOperand(1)) : nullptr;
    if (D && D->getOpcode() == Instruction::UDiv && D->getOperand(0) == &P &&
        C && C->getValue() == APInt(C->getBitWidth(), Divisor)) return &P;
  }
  return nullptr;
}
} // namespace

extern "C" unsigned elisacoreUnrollBoundedDivisionLoops(LLVMModuleRef Ref) {
  if (!Ref) return 0;
  Module &M = *unwrap(Ref);
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
  unsigned AnalyzedFunctions = 0;
  unsigned AnalyzedLoops = 0;
  for (Function &F : M) {
    if (AnalyzedLoops >= 64) break;
    if (BDU_SkipFunction(F, M)) continue;
    unsigned FunctionInsts = 0;
    for (BasicBlock &BB : F) FunctionInsts += BB.size();
    if (!BDU_MayContainCandidate(F, FunctionInsts)) continue;
    if (++AnalyzedFunctions > 32) break;

    auto &LI = FAM.getResult<LoopAnalysis>(F);
    auto &DT = FAM.getResult<DominatorTreeAnalysis>(F);
    auto &SE = FAM.getResult<ScalarEvolutionAnalysis>(F);
    auto &AC = FAM.getResult<AssumptionAnalysis>(F);
    auto &LVI = FAM.getResult<LazyValueAnalysis>(F);
    auto &TTI = FAM.getResult<TargetIRAnalysis>(F);
    auto &ORE = FAM.getResult<OptimizationRemarkEmitterAnalysis>(F);
    for (Loop *L : LI) {
      if (++AnalyzedLoops > 64) break;
      if (!L) continue;
      BDU_Candidate C;
      if (!BDU_FindCandidate(*L, LVI, C)) continue;

      // Range proof and shape recognition happen before CFG normalization, on
      // the actual outside predecessor edge. Only a proven candidate is
      // normalized; this avoids rewriting unrelated loops in the function.
      bool Normalized = simplifyLoop(L, &DT, &LI, &SE, &AC, nullptr, false);
      if (L->isLoopSimplifyForm()) Normalized |= formLCSSA(*L, DT, &LI, &SE);
      if (Normalized) ++Changed;
      if (!L->isLoopSimplifyForm()) break; // LVI may have been invalidated.
      PHINode *P = BDU_FindRecurrence(*L, C.Divisor);
      if (!P) break; // Never query the stale pre-normalization LVI again.

      // Full expansion hoists many division constants before the first exit.
      // Four iterations keep short inputs compact while unrolling long inputs.
      unsigned Factor = C.Bound > 4 ? 4 : C.Bound;
      UnrollLoopOptions Options{Factor, false, false, false, true, false,
                                nullptr, 1000, true, false};
      Loop *Remainder = nullptr;
      LoopUnrollResult Result = UnrollLoop(L, Options, &LI, &SE, &DT, &AC,
                                           &TTI, &ORE, true, &Remainder);
      if (Result != LoopUnrollResult::Unmodified) {
        ++Changed;
      }
      break; // Normalization/unrolling may invalidate LVI; never inspect next loop.
    }
  }
  return Changed;
}
// Unroll a three-state shift recurrence by its rotation length. This gives
// the register allocator a straight-line body with no register shifts between
// the three additions. LLVM retains the runtime remainder and exit checks.
static bool isLaggedAdditionBlock(BasicBlock &B) {
  auto *Branch = dyn_cast<CondBrInst>(B.getTerminator());
  auto *Cmp = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  if (!Cmp || !Cmp->isEquality() || B.size() != 8 ||
      Branch->getSuccessor(Cmp->getPredicate() == ICmpInst::ICMP_EQ ? 1 : 0) != &B ||
      Branch->getSuccessor(Cmp->getPredicate() == ICmpInst::ICMP_EQ ? 0 : 1) == &B)
    return false;
  auto *Counter = dyn_cast<PHINode>(Cmp->getOperand(0));
  if (!Counter || Counter->getParent() != &B ||
      !Counter->getType()->isIntegerTy(64) || Counter->getNumIncomingValues() != 2)
    return false;
  auto *Step = dyn_cast<BinaryOperator>(Counter->getIncomingValueForBlock(&B));
  auto *One = Step ? dyn_cast<ConstantInt>(Step->getOperand(1)) : nullptr;
  if (!Step || Step->getParent() != &B || Step->getOpcode() != Instruction::Add ||
      Step->getOperand(0) != Counter || !One || !One->isOne()) return false;
  unsigned PhiCount = 0;
  BinaryOperator *Sum = nullptr;
  for (Instruction &I : B) {
    if (isa<PHINode>(I)) { ++PhiCount; continue; }
    if (&I == Step || &I == Cmp || &I == Branch) continue;
    if (Sum || I.getOpcode() != Instruction::Add) return false;
    Sum = cast<BinaryOperator>(&I);
  }
  if (PhiCount != 4 || !Sum) return false;
  auto Shift = [&](Value *V) -> PHINode * {
    auto *P = dyn_cast<PHINode>(V);
    return P && P != Counter && P->getParent() == &B &&
           P->getNumIncomingValues() == 2 ? P : nullptr;
  };
  PHINode *A = Shift(Sum->getOperand(0)), *Next = Shift(Sum->getOperand(1));
  if (!A || !Next || A == Next) return false;
  if (A->getIncomingValueForBlock(&B) != Next) std::swap(A, Next);
  if (A->getIncomingValueForBlock(&B) != Next) return false;
  auto *Newest = Shift(Next->getIncomingValueForBlock(&B));
  return Newest && Newest != A && Newest != Next &&
         Newest->getIncomingValueForBlock(&B) == Sum;
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
      if (!isLaggedAdditionBlock(B)) continue;
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
      UnrollLoopOptions Options{3, true, true, true, false, false, nullptr, 1000};
      if (UnrollLoop(L, Options, &LI, &SE, &DT, &AC, &TTI, &ORE, true) !=
          LoopUnrollResult::Unmodified) ++Changed;
      break;
    }
  }
  return Changed;
}

#include "llvm_shift_parity.cpp"
#include "llvm_shift_bit_reverse.cpp"
#include "llvm_power_position.cpp"
#include "llvm_edge_division_reuse.cpp"

#else
extern "C" unsigned elisacoreUnrollLaggedRecurrences(LLVMModuleRef) { return 0; }
extern "C" unsigned elisacoreUnrollBoundedDivisionLoops(LLVMModuleRef) { return 0; }
#endif

#include <llvm-c/Transforms/PassBuilder.h>
extern "C" unsigned elisacoreOptimizeBoundedDivisionLoops(LLVMModuleRef M, LLVMTargetMachineRef T) {
  unsigned Changed = elisacoreUnrollBoundedDivisionLoops(M);
  Changed += elisacoreUnrollLaggedRecurrences(M);
#if LLVM_VERSION_MAJOR >= 23
  if (M) {
    Changed += elisacoreFoldShiftParityLoops(*llvm::unwrap(M));
    Changed += elisacoreFoldShiftBitReverseLoops(*llvm::unwrap(M));
    Changed += elisacoreFoldPowerPosition(*llvm::unwrap(M));
  }
#endif
  if (Changed) {
    auto Options = LLVMCreatePassBuilderOptions();
    auto Error = LLVMRunPasses(M, "function(instcombine<no-verify-fixpoint>,simplifycfg)", T, Options);
    LLVMDisposePassBuilderOptions(Options);
    if (Error) { LLVMConsumeError(Error); return 2; }
  }
#if LLVM_VERSION_MAJOR >= 23
  if (M) Changed += elisacoreReuseEdgeDivisions(*llvm::unwrap(M));
#endif
  return Changed ? 1 : 0;
}

extern "C" int elisacoreBoundedDivisionUnrollSupported() { return LLVM_VERSION_MAJOR >= 23; }
