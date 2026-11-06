//go:build cgo

// Remove an array's round trip through a local copy: memcpy(U, T); ...; memcpy(T, U).
// U can use T's storage when both are private, full-size allocas, T is untouched
// between the copies, and every path from the first copy reaches the second or
// ends in unreachable (a trap, after which the private array is unobservable).
#include <llvm/Config/llvm-config.h>
#include <llvm-c/Core.h>
#if LLVM_VERSION_MAJOR >= 23
#include <optional>
#include <llvm/ADT/DenseSet.h>
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>
using namespace llvm;
namespace {
// A private block: a static alloca, or malloc of a constant size in this function.
static std::optional<uint64_t> ACE_Size(Value *V, const DataLayout &DL) {
  if (auto *A = dyn_cast<AllocaInst>(V)) {
    auto Size = A->getAllocationSize(DL);
    if (!A->isStaticAlloca() || !Size || Size->isScalable()) return std::nullopt;
    return Size->getFixedValue();
  }
  if (auto *C = dyn_cast<CallInst>(V)) {
    Function *Callee = C->getCalledFunction();
    if (!Callee || Callee->getName() != "malloc" || C->arg_size() != 1 || C->hasOperandBundles())
      return std::nullopt;
    if (auto *N = dyn_cast<ConstantInt>(C->getArgOperand(0))) return N->getZExtValue();
  }
  return std::nullopt;
}
static bool ACE_IsFree(const Instruction *I) {
  auto *C = dyn_cast<CallInst>(I);
  Function *Callee = C ? C->getCalledFunction() : nullptr;
  return Callee && Callee->getName() == "free" && C->arg_size() == 1;
}
// Collects the uses of a block; false if its address can escape.
static bool ACE_Users(Value *Base, SmallVectorImpl<Instruction *> &Uses) {
  SmallVector<Value *, 16> Work{Base};
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
        if (V != Base) return false;
        Uses.push_back(I);
      } else if (ACE_IsFree(I)) {
        if (V != Base) return false;
        Uses.push_back(I);
      } else {
        return false;
      }
    }
  }
  return true;
}
static bool ACE_Marker(const Instruction *I) {
  auto *C = dyn_cast<IntrinsicInst>(I);
  return (C && C->isLifetimeStartOrEnd()) || ACE_IsFree(I);
}
static bool ACE_Writes(Instruction *I, const SmallPtrSetImpl<Instruction *> &Same, Value *Base) {
  if (isa<StoreInst>(I) || isa<MemSetInst>(I)) return true;
  if (auto *M = dyn_cast<MemTransferInst>(I)) {
    // A transfer writes this block unless it is only the source.
    Value *D = M->getRawDest();
    while (auto *G = dyn_cast<GetElementPtrInst>(D)) D = G->getPointerOperand();
    return D == Base;
  }
  return ACE_Marker(I);
}
// Walks every path from First. Clean: U and T are equal. Dirty: U was written, so
// only Second may restore equality and T must not be observed. Tainted: T was
// written while U is stale, so U must not be used again.
enum ACE_State : unsigned { ACE_Clean, ACE_Dirty, ACE_Tainted };
static bool ACE_Walk(MemCpyInst *First, MemCpyInst *Second, Value *U, Value *T,
                     const SmallPtrSetImpl<Instruction *> &USet, const SmallPtrSetImpl<Instruction *> &TSet,
                     SmallPtrSetImpl<Instruction *> &Visited) {
  SmallVector<std::pair<Instruction *, ACE_State>, 16> Work{{First->getNextNode(), ACE_Clean}};
  DenseSet<std::pair<BasicBlock *, unsigned>> Seen;
  while (!Work.empty()) {
    auto [Start, State] = Work.pop_back_val();
    bool Done = false;
    for (Instruction *I = Start; I && !Done; I = I->getNextNode()) {
      if (I == Second) {
        if (State == ACE_Tainted) return false;
        Done = true;
        break;
      }
      if (I == First) {
        if (State == ACE_Dirty) return false;
        Done = true;
        break;
      }
      if (USet.contains(I)) {
        // Ending U's life is fine once nothing is pending: the markers go with U.
        if (ACE_Marker(I)) {
          if (State == ACE_Dirty) return false;
        } else {
          if (State == ACE_Tainted) return false;
          Visited.insert(I);
          if (ACE_Writes(I, USet, U)) State = ACE_Dirty;
        }
      }
      if (TSet.contains(I)) {
        if (State == ACE_Dirty) return false;
        if (ACE_Writes(I, TSet, T)) State = ACE_Tainted;
      }
      if (!I->isTerminator()) continue;
      if (isa<UnreachableInst>(I)) break;
      if (isa<ReturnInst>(I)) {
        if (State == ACE_Dirty) return false;
        break;
      }
      if (!isa<CondBrInst>(I) && !isa<UncondBrInst>(I) && !isa<SwitchInst>(I)) return false;
      for (BasicBlock *S : successors(I->getParent()))
        if (Seen.insert({S, (unsigned)State}).second) Work.push_back({&S->front(), State});
    }
  }
  return true;
}
static bool ACE_Try(Instruction *U, const DataLayout &DL, DominatorTree &DT) {
  auto Size = ACE_Size(U, DL);
  if (!Size) return false;
  SmallVector<Instruction *, 16> UUses, TUses;
  if (!ACE_Users(U, UUses)) return false;
  MemCpyInst *First = nullptr, *Second = nullptr;
  for (Instruction *I : UUses)
    if (auto *M = dyn_cast<MemCpyInst>(I)) {
      if (M->getRawDest() == U && !First) First = M;
      else if (M->getRawSource() == U && !Second) Second = M;
      else return false;
    }
  if (!First || !Second || First->isVolatile() || Second->isVolatile()) return false;
  auto *T = dyn_cast<Instruction>(First->getRawSource());
  auto *N1 = dyn_cast<ConstantInt>(First->getLength()), *N2 = dyn_cast<ConstantInt>(Second->getLength());
  if (!T || T == U || Second->getRawDest() != T || !N1 || !N2 || N1->getZExtValue() != *Size ||
      N2->getZExtValue() != *Size || isa<AllocaInst>(T) != isa<AllocaInst>(U)) return false;
  if (auto *TA = dyn_cast<AllocaInst>(T))
    if (TA->getAlign() < cast<AllocaInst>(U)->getAlign()) return false;
  auto TSize = ACE_Size(T, DL);
  if (!TSize || *TSize != *Size || !DT.dominates(First, Second) || !ACE_Users(T, TUses)) return false;
  SmallPtrSet<Instruction *, 32> USet, TSet, Visited;
  for (Instruction *I : UUses)
    if (I != First && I != Second) USet.insert(I);
  for (Instruction *I : TUses)
    if (I != First && I != Second) TSet.insert(I);
  if (!ACE_Walk(First, Second, U, T, USet, TSet, Visited)) return false;
  SmallVector<Instruction *, 4> Markers;
  for (Instruction *I : USet) {
    if (ACE_Marker(I)) Markers.push_back(I);
    else if (!Visited.contains(I) || !DT.dominates(First, I)) return false;
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
    SmallVector<Instruction *, 8> Blocks;
    for (Instruction &I : F.getEntryBlock())
      if (ACE_Size(&I, M.getDataLayout())) Blocks.push_back(&I);
    if (Blocks.size() < 2) continue;
    DominatorTree DT(F);
    for (Instruction *B : Blocks)
      if (ACE_Try(B, M.getDataLayout(), DT)) ++Changed;
  }
  return Changed;
}
#else
extern "C" unsigned elisacoreElideArrayRoundTrips(LLVMModuleRef) { return 0; }
#endif
