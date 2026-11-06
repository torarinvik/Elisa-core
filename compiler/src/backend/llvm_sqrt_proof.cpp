//go:build cgo

// Prove only the nnan result fact of an existing recognized sqrt libcall.
// Calls, rounding, errno effects, negative zero and infinity stay represented.
#include <llvm/Config/llvm-config.h>
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
#include <llvm-c/Error.h>
#include <llvm-c/Transforms/PassBuilder.h>
#if LLVM_VERSION_MAJOR >= 23
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/Analysis/LazyValueInfo.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/TargetLibraryInfo.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/ConstantRange.h>
#include <llvm/IR/Constants.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/InstIterator.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Support/KnownFPClass.h>
using namespace llvm;
namespace {
enum SqrtFact : unsigned { SQ_NN = 1, SQ_FINITE = 2, SQ_NONNEG = 4 };
struct SqrtProof {
  DominatorTree &DT;
  LoopInfo &LI;
  LazyValueInfo &LVI;
  unsigned Budget = 4096;
  SmallPtrSet<const PHINode *, 8> Active;
};
static bool SQ_IntConversion(Value *V) {
  auto *Cast = dyn_cast<CastInst>(V);
  if (!Cast || (Cast->getOpcode() != Instruction::SIToFP &&
                Cast->getOpcode() != Instruction::UIToFP)) return false;
  auto *Src = Cast->getSrcTy()->getScalarType();
  auto *Dst = Cast->getDestTy()->getScalarType();
  return Src->isIntegerTy() && Src->getIntegerBitWidth() <= 64 &&
         (Dst->isFloatTy() || Dst->isDoubleTy());
}
static bool SQ_Prove(Value *V, unsigned Want, Instruction *At, SqrtProof &C,
                     const PHINode *Hyp = nullptr, unsigned HypFacts = 0,
                     unsigned Depth = 0) {
  if (!V || !At || Depth > 32 || C.Budget == 0) return false;
  --C.Budget;
  if (V == Hyp) return (Want & HypFacts) == Want;
  auto K = computeKnownFPClass(V, At->getModule()->getDataLayout(),
                              fcAllFlags, nullptr, nullptr, At, &C.DT);
  if ((!(Want & SQ_NN) || K.isKnownNeverNaN()) &&
      (!(Want & SQ_FINITE) || K.isKnownNeverInfOrNaN()) &&
      (!(Want & SQ_NONNEG) || K.isKnownNever(fcNan | fcNegative))) return true;
  if (SQ_IntConversion(V)) {
    return !(Want & SQ_NONNEG) || isa<UIToFPInst>(V);
  }
  // Recognize only a uniform broadcast whose every lane selects the inserted
  // scalar. Never infer facts from the poison/undef lanes it discards.
  if (auto *Shuffle = dyn_cast<ShuffleVectorInst>(V)) {
    auto *Insert = dyn_cast<InsertElementInst>(Shuffle->getOperand(0));
    auto *Index = Insert ? dyn_cast<ConstantInt>(Insert->getOperand(2)) : nullptr;
    if (Index && Index->getValue().getActiveBits() <= 32) {
      unsigned Lane = Index->getZExtValue();
      auto *VT = dyn_cast<FixedVectorType>(Insert->getType());
      bool Same = VT && Lane < VT->getNumElements();
      for (int M : Shuffle->getShuffleMask()) Same &= M >= 0 && unsigned(M) == Lane;
      if (Same) return SQ_Prove(Insert->getOperand(1), Want, At, C, Hyp, HypFacts, Depth+1);
    }
    return false;
  }
  if (auto *P = dyn_cast<PHINode>(V)) {
    // Never assume finiteness of a recurrence: finite summands may overflow.
    if ((Want & SQ_FINITE) || C.Active.contains(P)) return false;
    C.Active.insert(P);
    bool OK = true;
    Loop *L = C.LI.getLoopFor(P->getParent());
    if (L && L->getHeader() == P->getParent() && P->getNumIncomingValues() == 2) {
      int Outside = -1, Inside = -1;
      for (unsigned I=0; I<2; ++I) {
        if (L->contains(P->getIncomingBlock(I))) { if (Inside != -1) OK=false; Inside=I; }
        else { if (Outside != -1) OK=false; Outside=I; }
      }
      OK &= Outside >= 0 && Inside >= 0;
      if (OK) OK = SQ_Prove(P->getIncomingValue(Outside), Want,
                           P->getIncomingBlock(Outside)->getTerminator(), C, nullptr, 0, Depth+1) &&
                   SQ_Prove(P->getIncomingValue(Inside), Want,
                           P->getIncomingBlock(Inside)->getTerminator(), C, P, Want, Depth+1);
    } else {
      for (unsigned I=0; I<P->getNumIncomingValues() && OK; ++I)
        OK = SQ_Prove(P->getIncomingValue(I), Want,
                      P->getIncomingBlock(I)->getTerminator(), C, nullptr, 0, Depth+1);
    }
    C.Active.erase(P);
    return OK;
  }
  if (auto *BO = dyn_cast<BinaryOperator>(V)) {
    if (Want & SQ_FINITE) return false;
    Value *A=BO->getOperand(0), *B=BO->getOperand(1);
    auto Prove = [&](Value *X, unsigned W) {
      return SQ_Prove(X, W, BO, C, Hyp, HypFacts, Depth+1);
    };
    switch (BO->getOpcode()) {
    case Instruction::FAdd:
      if (Prove(A, SQ_NN|SQ_NONNEG) && Prove(B, SQ_NN|SQ_NONNEG)) return true;
      if (Want & SQ_NONNEG) return false;
      return (Prove(A,SQ_FINITE) && Prove(B,SQ_NN)) ||
             (Prove(B,SQ_FINITE) && Prove(A,SQ_NN));
    case Instruction::FSub:
      if (Want & SQ_NONNEG) return false;
      return (Prove(A,SQ_FINITE) && Prove(B,SQ_NN)) ||
             (Prove(B,SQ_FINITE) && Prove(A,SQ_NN));
    case Instruction::FMul:
      return A == B && Prove(A,SQ_NN);
    case Instruction::FDiv: {
      auto *Den = dyn_cast<UIToFPInst>(B);
      if (!Den || !Den->getSrcTy()->isIntegerTy() || !SQ_IntConversion(B) ||
          !Prove(A,SQ_NN|(Want & SQ_NONNEG))) return false;
      auto R = C.LVI.getConstantRange(Den->getOperand(0), BO, false);
      return !R.isEmptySet() && !R.isWrappedSet() && !R.getUnsignedMin().isZero();
    }
    default: return false;
    }
  }
  if (auto *CB = dyn_cast<IntrinsicInst>(V)) {
    if (CB->getIntrinsicID() != Intrinsic::vector_reduce_fadd ||
        CB->arg_size() != 2 || (Want & SQ_FINITE)) return false;
    Value *Seed=CB->getArgOperand(0), *Vec=CB->getArgOperand(1);
    auto *VT=dyn_cast<FixedVectorType>(Vec->getType());
    if (!VT || VT->getNumElements()>256 || !VT->getElementType()->isDoubleTy()) return false;
    if (SQ_Prove(Seed,SQ_NN|SQ_NONNEG,CB,C,Hyp,HypFacts,Depth+1) &&
        SQ_Prove(Vec,SQ_NN|SQ_NONNEG,CB,C,Hyp,HypFacts,Depth+1)) return true;
    if (Want & SQ_NONNEG) return false;
    // At most 256 integer-to-double lanes of width <=64: every partial
    // lane sum is finite, even when the incoming accumulator is infinite.
    // Reassociated reductions may combine lanes in any order under that bound.
    return SQ_IntConversion(Vec) &&
           SQ_Prove(Seed,SQ_NN,CB,C,Hyp,HypFacts,Depth+1);
  }
  return false;
}
}
extern "C" unsigned elisacoreProveSqrtNonNaN(LLVMModuleRef Ref) {
  if (!Ref) return 0;
  Module &M=*unwrap(Ref);
  if (M.getNamedMetadata("llvm.dbg.cu")) return 0;
  PassBuilder PB;
  LoopAnalysisManager LAM; FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM; ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM); PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM); PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM,FAM,CGAM,MAM);
  unsigned Changed=0;
  for (Function &F:M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.hasFnAttribute(Attribute::StrictFP) || F.getSubprogram() ||
        F.hasFnAttribute(Attribute::SanitizeAddress) ||
        F.hasFnAttribute(Attribute::SanitizeHWAddress) ||
        F.hasFnAttribute(Attribute::SanitizeMemory) ||
        F.hasFnAttribute(Attribute::SanitizeThread)) continue;
    SmallVector<CallInst *,4> Calls;
    unsigned Size=0;
    for (Instruction &I:instructions(F)) {
      ++Size;
      if (auto *CI=dyn_cast<CallInst>(&I))
        if (auto *Callee=CI->getCalledFunction())
          if (Callee->getName()=="sqrt" && Callee->isDeclaration() &&
              !CI->isNoBuiltin() && !CI->isConvergent() &&
              !CI->hasFnAttr(Attribute::StrictFP) && !CI->hasOperandBundles() &&
              !CI->hasMetadataOtherThanDebugLoc() && !CI->hasNoNaNs() && CI->arg_size()==1 && CI->getType()->isDoubleTy()) Calls.push_back(CI);
    }
    if (Calls.empty() || Calls.size()>8 || Size>2500) continue;
    auto &TLI=FAM.getResult<TargetLibraryAnalysis>(F);
    auto &DT=FAM.getResult<DominatorTreeAnalysis>(F);
    auto &LI=FAM.getResult<LoopAnalysis>(F);
    auto &LVI=FAM.getResult<LazyValueAnalysis>(F);
    for (auto *CI:Calls) {
      LibFunc LF;
      if (!TLI.getLibFunc(*CI,LF) || LF!=LibFunc_sqrt || !TLI.has(LF)) continue;
      SqrtProof C{DT,LI,LVI};
      if (!SQ_Prove(CI->getArgOperand(0),SQ_NN|SQ_NONNEG,CI,C)) continue;
      CI->setHasNoNaNs(true);
      ++Changed;
    }
  }
  return Changed;
}
#else
extern "C" unsigned elisacoreProveSqrtNonNaN(LLVMModuleRef) { return 0; }
#endif

// Let LLVM use the proven call fact before instruction selection. In LLVM 23
// AggressiveInstCombine performs the libcall-to-intrinsic simplification.
extern "C" unsigned elisacoreOptimizeSqrtNonNaN(LLVMModuleRef M, LLVMTargetMachineRef T) {
  if (!elisacoreProveSqrtNonNaN(M)) return 0;
  auto Options = LLVMCreatePassBuilderOptions();
  auto Error = LLVMRunPasses(M, "function(aggressive-instcombine,instcombine<max-iterations=8;no-verify-fixpoint>,simplifycfg)", T, Options);
  LLVMDisposePassBuilderOptions(Options);
  if (Error) { LLVMConsumeError(Error); return 2; }
  return 1;
}
