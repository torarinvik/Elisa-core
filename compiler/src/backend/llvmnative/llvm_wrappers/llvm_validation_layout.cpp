//go:build cgo

// Layout hint for short, leaf validation chains with a shared negative return.
// We change only expected branch weights, never checks, loads or value facts.
#include <llvm/Config/llvm-config.h>
#include <llvm-c/Core.h>
#if LLVM_VERSION_MAJOR >= 23
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/Constants.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/MDBuilder.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>
using namespace llvm;
namespace ValidationReturnLayout {
static bool negativeSentinel(Value *V) {
  if (auto *C = dyn_cast<ConstantInt>(V))
    return C->getBitWidth() >= 8 && C->isNegative();
  if (auto *C = dyn_cast<ConstantFP>(V)) {
    const APFloat &N = C->getValueAPF();
    return N.isNegative() && !N.isZero() && !N.isNaN() && !N.isInfinity();
  }
  return false;
}
static bool plainGuard(BasicBlock *B, unsigned &Loads) {
  unsigned Size = 0;
  for (Instruction &I : *B) {
    if (++Size > 12) return false;
    if (I.isTerminator() || isa<DbgInfoIntrinsic>(I)) continue;
    if (auto *L = dyn_cast<LoadInst>(&I)) {
      if (L->isVolatile() || L->isAtomic() || !L->getType()->isIntegerTy())
        return false;
      ++Loads;
      continue;
    }
    if (I.mayHaveSideEffects() || isa<CallBase>(I) || isa<PHINode>(I))
      return false;
  }
  return true;
}
static unsigned annotate(Function &F, BasicBlock *Exit, PHINode *Result) {
  Value *Failure = nullptr;
  BasicBlock *Success = nullptr;
  unsigned Failures = 0;
  for (unsigned I = 0; I < Result->getNumIncomingValues(); ++I) {
    Value *V = Result->getIncomingValue(I);
    if (negativeSentinel(V)) {
      if (Failure && Failure != V) return 0;
      Failure = V;
      ++Failures;
    } else {
      if (Success || isa<Constant>(V)) return 0;
      Success = Result->getIncomingBlock(I);
    }
  }
  if (Failures < 4 || Failures > 8 || !Success) return 0;
  auto *Finish = dyn_cast<UncondBrInst>(Success->getTerminator());
  if (!Finish || Finish->getSuccessor(0) != Exit) return 0;
  for (Instruction &I : *Success) {
    if (auto *C = dyn_cast<CallBase>(&I))
      if (!isa<IntrinsicInst>(C) || !C->doesNotAccessMemory()) return 0;
    if (!I.isTerminator() && I.mayHaveSideEffects()) return 0;
  }
  SmallVector<std::pair<CondBrInst *, unsigned>, 8> Guards;
  SmallPtrSet<BasicBlock *, 8> Seen;
  BasicBlock *B = &F.getEntryBlock();
  unsigned Loads = 0;
  while (B != Success) {
    if (Guards.size() >= 8 || !Seen.insert(B).second ||
        !plainGuard(B, Loads)) return 0;
    auto *Br = dyn_cast<CondBrInst>(B->getTerminator());
    if (!Br || Br->hasMetadataOtherThanDebugLoc()) return 0;
    bool A = Br->getSuccessor(0) == Exit, C = Br->getSuccessor(1) == Exit;
    if (A == C || Result->getIncomingValueForBlock(B) != Failure) return 0;
    unsigned ErrorEdge = A ? 0 : 1;
    BasicBlock *Next = Br->getSuccessor(1 - ErrorEdge);
    if (Next->getSinglePredecessor() != B) return 0;
    Guards.push_back({Br, ErrorEdge});
    B = Next;
  }
  if (Guards.size() != Failures || Loads < 3) return 0;
  MDBuilder Builder(F.getContext());
  for (auto [Br, ErrorEdge] : Guards)
    Br->setMetadata(LLVMContext::MD_prof, ErrorEdge == 0
        ? Builder.createBranchWeights(1, 31, true)
        : Builder.createBranchWeights(31, 1, true));
  return Guards.size();
}
}
extern "C" unsigned elisacoreBiasValidationReturns(LLVMModuleRef Ref) {
  if (!Ref) return 0;
  Module &M = *unwrap(Ref);
  if (M.getTargetTriple().getArch() != Triple::aarch64 ||
      M.getNamedMetadata("llvm.dbg.cu") || M.getModuleFlag("ProfileSummary"))
    return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() ||
        F.hasMinSize() || F.getEntryCount() || F.getInstructionCount() > 256 ||
        F.hasFnAttribute(Attribute::SanitizeAddress) ||
        F.hasFnAttribute(Attribute::SanitizeMemory) ||
        F.hasFnAttribute(Attribute::SanitizeThread) ||
        F.hasFnAttribute(Attribute::SanitizeHWAddress)) continue;
    for (BasicBlock &BB : F) {
      auto *R = dyn_cast<ReturnInst>(BB.getTerminator());
      if (!R || !R->getReturnValue()) continue;
      auto *P = dyn_cast<PHINode>(R->getReturnValue());
      if (!P || P->getParent() != &BB) continue;
      bool Simple = true;
      for (Instruction &I : BB)
        Simple &= &I == P || &I == R || isa<DbgInfoIntrinsic>(I);
      if (Simple) {
        Changed += ValidationReturnLayout::annotate(F, &BB, P);
        break;
      }
    }
  }
  return Changed;
}
#else
extern "C" unsigned elisacoreBiasValidationReturns(LLVMModuleRef) { return 0; }
#endif
