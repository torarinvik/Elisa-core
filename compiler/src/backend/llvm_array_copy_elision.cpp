//go:build cgo

// Remove an array's round trip through a local copy: memcpy(U, T); ...; memcpy(T, U).
// U can use T's storage when both are private, full-size allocas, T is untouched
// between the copies, and every path from the first copy reaches the second or
// ends in unreachable (a trap, after which the private array is unobservable).
#include <llvm/Config/llvm-config.h>
#include <llvm-c/Core.h>
#if LLVM_VERSION_MAJOR >= 23
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>
using namespace llvm;
namespace {
// Collects non-pointer-escaping uses of A; false if the address can escape.
static bool ACE_Users(AllocaInst *A, SmallVectorImpl<Instruction *> &Uses) {
  SmallVector<Value *, 16> Work{A};
  while (!Work.empty()) {
    Value *V = Work.pop_back_val();
    for (Use &U : V->uses()) {
      auto *I = dyn_cast<Instruction>(U.getUser());
      if (!I) return false;
      if (auto *G = dyn_cast<GetElementPtrInst>(I)) {
        if (G->getPointerOperand() != V) return false;
        Work.push_back(G);
      } else if (auto *L = dyn_cast<LoadInst>(I)) {
        if (L->isVolatile() || L->isAtomic()) return false;
        Uses.push_back(I);
      } else if (auto *S = dyn_cast<StoreInst>(I)) {
        if (S->getPointerOperand() != V || S->isVolatile() || S->isAtomic()) return false;
        Uses.push_back(I);
      } else if (auto *M = dyn_cast<MemIntrinsic>(I)) {
        if (M->isVolatile()) return false;
        Uses.push_back(I);
      } else if (auto *C = dyn_cast<IntrinsicInst>(I); C && C->isLifetimeStartOrEnd()) {
        if (V != A) return false;
        Uses.push_back(I);
      } else {
        return false;
      }
    }
  }
  return true;
}
// Instructions after First up to Second on every path. False if a path can
// avoid Second other than by trapping, or can re-run First.
static bool ACE_Region(MemCpyInst *First, MemCpyInst *Second, SmallPtrSetImpl<Instruction *> &Region) {
  SmallVector<Instruction *, 16> Work{First->getNextNode()};
  SmallPtrSet<BasicBlock *, 16> SeenBlocks;
  while (!Work.empty()) {
    Instruction *I = Work.pop_back_val();
    for (; I; I = I->getNextNode()) {
      if (I == Second) break;
      if (I == First) return false;
      Region.insert(I);
      if (!I->isTerminator()) continue;
      if (isa<UnreachableInst>(I)) break;
      if (!isa<CondBrInst>(I) && !isa<UncondBrInst>(I) && !isa<SwitchInst>(I)) return false;
      for (BasicBlock *S : successors(I->getParent()))
        if (SeenBlocks.insert(S).second) Work.push_back(&S->front());
    }
  }
  return true;
}
static bool ACE_Try(AllocaInst *U, const DataLayout &DL, DominatorTree &DT) {
  auto Size = U->getAllocationSize(DL);
  if (!Size || Size->isScalable() || !U->isStaticAlloca()) return false;
  SmallVector<Instruction *, 16> UUses, TUses;
  if (!ACE_Users(U, UUses)) return false;
  MemCpyInst *First = nullptr, *Second = nullptr;
  for (Instruction *I : UUses)
    if (auto *M = dyn_cast<MemCpyInst>(I)) {
      if (M->getRawDest() == U && !First) First = M;
      else if (M->getRawSource() == U && !Second) Second = M;
      else return false;
    }
  if (!First || !Second) return false;
  auto *T = dyn_cast<AllocaInst>(First->getRawSource());
  auto *N1 = dyn_cast<ConstantInt>(First->getLength()), *N2 = dyn_cast<ConstantInt>(Second->getLength());
  if (!T || T == U || Second->getRawDest() != T || !T->isStaticAlloca() || !N1 || !N2 ||
      N1->getZExtValue() != Size->getFixedValue() || N2->getZExtValue() != Size->getFixedValue() ||
      T->getAlign() < U->getAlign() || T->getParent() != &T->getFunction()->getEntryBlock()) return false;
  auto TSize = T->getAllocationSize(DL);
  if (!TSize || *TSize != *Size || !DT.dominates(First, Second)) return false;
  if (!ACE_Users(T, TUses)) return false;
  SmallPtrSet<Instruction *, 32> Region;
  if (!ACE_Region(First, Second, Region)) return false;
  for (Instruction *I : TUses)
    if (Region.contains(I)) return false;
  SmallVector<Instruction *, 4> Markers;
  for (Instruction *I : UUses) {
    if (I == First || I == Second) continue;
    if (auto *C = dyn_cast<IntrinsicInst>(I); C && C->isLifetimeStartOrEnd()) {
      Markers.push_back(I);
      continue;
    }
    if (!Region.contains(I)) return false;
  }
  for (Instruction *I : Markers) I->eraseFromParent();
  First->eraseFromParent();
  Second->eraseFromParent();
  U->replaceAllUsesWith(T);
  U->eraseFromParent();
  return true;
}
}
extern "C" unsigned elisacoreElideArrayRoundTrips(LLVMModuleRef Ref) {
  if (!Ref) return 0;
  Module &M = *unwrap(Ref);
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone()) continue;
    SmallVector<AllocaInst *, 8> Allocas;
    for (Instruction &I : F.getEntryBlock())
      if (auto *A = dyn_cast<AllocaInst>(&I)) Allocas.push_back(A);
    if (Allocas.size() < 2) continue;
    bool Any = false;
    {
      DominatorTree DT(F);
      for (AllocaInst *A : Allocas)
        if (ACE_Try(A, M.getDataLayout(), DT)) { ++Changed; Any = true; }
    }
    (void)Any;
  }
  return Changed;
}
#else
extern "C" unsigned elisacoreElideArrayRoundTrips(LLVMModuleRef) { return 0; }
#endif
