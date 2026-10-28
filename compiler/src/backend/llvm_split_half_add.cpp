//go:build cgo

// Canonicalize a precisely matched u64 addition that was expanded into two
// 32-bit halves. The rule is intentionally fail-closed: it accepts only the
// low-sum/carry/high-sum/concatenate shape, with private single-store allocas
// allowed as transparent lowering temporaries.
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/BasicBlock.h>
#include <llvm/IR/Constants.h>
#include <llvm/IR/DebugProgramInstruction.h>
#include <llvm/IR/Function.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>

using namespace llvm;

namespace {

constexpr uint64_t LowHalfMask = 0x00000000ffffffffULL;
constexpr unsigned HalfWidth = 32;
constexpr unsigned IntegerWidth = 64;
constexpr unsigned MaxTransparentAllocaUses = 256;

bool isI64(Value *V) {
  return V && V->getType()->isIntegerTy(IntegerWidth);
}

bool isI64Constant(Value *V, uint64_t Expected) {
  auto *C = dyn_cast_or_null<ConstantInt>(V);
  return C && C->getBitWidth() == IntegerWidth &&
         C->getValue().getZExtValue() == Expected;
}

// A load is transparent only when it reads one non-volatile, non-atomic store
// to a private i64 alloca in the same block, and that store precedes every load.
// The strict use-list check proves that the stack address never escapes or
// participates in another memory operation.
Value *resolveSingleStoreLoad(Value *V, unsigned Depth = 0) {
  if (Depth > 8)
    return nullptr;
  auto *Load = dyn_cast_or_null<LoadInst>(V);
  if (!Load)
    return V;
  if (Load->isVolatile() || Load->isAtomic() || !isI64(Load))
    return nullptr;
  auto *Alloca = dyn_cast<AllocaInst>(Load->getPointerOperand());
  if (!Alloca || !Alloca->getAllocatedType()->isIntegerTy(IntegerWidth) ||
      Alloca->isUsedWithInAlloca() || Alloca->isSwiftError() ||
      Alloca->hasMetadataOtherThanDebugLoc() ||
      Load->getAlign() > Alloca->getAlign() ||
      Load->hasMetadataOtherThanDebugLoc())
    return nullptr;

  StoreInst *OnlyStore = nullptr;
  unsigned UseCount = 0;
  for (User *U : Alloca->users()) {
    // The matcher is invoked for every candidate OR. Bound this scan so a
    // malicious/high-fanout alloca cannot turn candidate matching quadratic.
    if (++UseCount > MaxTransparentAllocaUses)
      return nullptr;
    if (auto *Store = dyn_cast<StoreInst>(U)) {
      if (Store->getPointerOperand() != Alloca || Store->isVolatile() ||
          Store->isAtomic() || Store->getValueOperand()->getType() != Load->getType() ||
          Store->getAlign() > Alloca->getAlign() ||
          Store->hasMetadataOtherThanDebugLoc() ||
          Store->getParent() != Load->getParent() || OnlyStore)
        return nullptr;
      OnlyStore = Store;
      continue;
    }
    if (auto *OtherLoad = dyn_cast<LoadInst>(U)) {
      if (OtherLoad->getPointerOperand() != Alloca || OtherLoad->isVolatile() ||
          OtherLoad->isAtomic() || OtherLoad->getType() != Load->getType() ||
          OtherLoad->getParent() != Load->getParent())
        return nullptr;
      continue;
    }
    return nullptr;
  }
  if (!OnlyStore || !OnlyStore->comesBefore(Load))
    return nullptr;
  return resolveSingleStoreLoad(OnlyStore->getValueOperand(), Depth + 1);
}

bool matchAndMask(Value *V, uint64_t Mask, Value *&Input) {
  V = resolveSingleStoreLoad(V);
  auto *And = dyn_cast_or_null<BinaryOperator>(V);
  if (!And || And->getOpcode() != Instruction::And || !isI64(And))
    return false;
  Value *LHS = resolveSingleStoreLoad(And->getOperand(0));
  Value *RHS = resolveSingleStoreLoad(And->getOperand(1));
  if (isI64Constant(LHS, Mask)) {
    Input = RHS;
    return true;
  }
  if (isI64Constant(RHS, Mask)) {
    Input = LHS;
    return true;
  }
  return false;
}

bool matchShift(Value *V, Instruction::BinaryOps Opcode, Value *Input,
                unsigned Amount, bool AllowUnsignedNoWrap = false) {
  V = resolveSingleStoreLoad(V);
  auto *Shift = dyn_cast_or_null<BinaryOperator>(V);
  if (!Shift || Shift->getOpcode() != Opcode || !isI64(Shift) ||
      resolveSingleStoreLoad(Shift->getOperand(0)) != Input ||
      !isI64Constant(resolveSingleStoreLoad(Shift->getOperand(1)), Amount))
    return false;
  if (Opcode == Instruction::LShr && Shift->isExact())
    return false;
  if (Opcode == Instruction::Shl && Shift->hasNoSignedWrap())
    return false;
  if (!AllowUnsignedNoWrap && Shift->hasNoUnsignedWrap())
    return false;
  return true;
}

bool matchLowSum(Value *V, Value *&A, Value *&B) {
  V = resolveSingleStoreLoad(V);
  auto *Sum = dyn_cast_or_null<BinaryOperator>(V);
  if (!Sum || Sum->getOpcode() != Instruction::Add || !isI64(Sum))
    return false;
  Value *L = nullptr;
  Value *R = nullptr;
  Value *L0 = resolveSingleStoreLoad(Sum->getOperand(0));
  Value *R0 = resolveSingleStoreLoad(Sum->getOperand(1));
  if (!matchAndMask(L0, LowHalfMask, L) ||
      !matchAndMask(R0, LowHalfMask, R)) {
    if (!matchAndMask(L0, LowHalfMask, R) ||
        !matchAndMask(R0, LowHalfMask, L))
      return false;
  }
  // The operands are each in [0, 2^32-1], so even nuw/nsw on this low sum
  // cannot introduce poison in i64.
  A = L;
  B = R;
  return isI64(A) && isI64(B);
}

bool collectHighAddTerms(Value *Root, SmallVectorImpl<Value *> &Terms) {
  SmallVector<Value *, 8> Worklist;
  Worklist.push_back(resolveSingleStoreLoad(Root));
  while (!Worklist.empty()) {
    Value *V = resolveSingleStoreLoad(Worklist.pop_back_val());
    auto *Add = dyn_cast_or_null<BinaryOperator>(V);
    if (Add && Add->getOpcode() == Instruction::Add) {
      if (!isI64(Add))
        return false;
      if (Terms.size() + Worklist.size() + 2 > 3)
        return false;
      Worklist.push_back(resolveSingleStoreLoad(Add->getOperand(0)));
      Worklist.push_back(resolveSingleStoreLoad(Add->getOperand(1)));
      continue;
    }
    Terms.push_back(V);
    if (Terms.size() > 3)
      return false;
  }
  // After matchHighSum, each term is one of lshr(A, 32), lshr(B, 32), or
  // lshr(LowSum, 32). Each is at most 2^32-1 and the carry is at most 1, so
  // every partial addition is below 2^33 and any nuw/nsw flags are safe.
  return Terms.size() == 3;
}

bool matchHighSum(Value *V, Value *A, Value *B, Value *LowSum) {
  SmallVector<Value *, 4> Terms;
  if (!collectHighAddTerms(V, Terms))
    return false;
  Value *Expected[] = {A, B, LowSum};
  bool Used[] = {false, false, false};
  for (Value *Term : Terms) {
    bool Matched = false;
    for (unsigned I = 0; I < 3; ++I) {
      if (!Used[I] && matchShift(Term, Instruction::LShr, Expected[I],
                                 HalfWidth)) {
        Used[I] = true;
        Matched = true;
        break;
      }
    }
    if (!Matched)
      return false;
  }
  return Used[0] && Used[1] && Used[2];
}

bool matchSplitHalfAdd(BinaryOperator *Combined, Value *&A, Value *&B) {
  if (!Combined || Combined->getOpcode() != Instruction::Or || !isI64(Combined))
    return false;

  for (unsigned Order = 0; Order != 2; ++Order) {
    Value *ShiftedHigh = resolveSingleStoreLoad(Combined->getOperand(Order));
    Value *MaskedLow = resolveSingleStoreLoad(Combined->getOperand(1 - Order));
    Value *MaskedHighSum = nullptr;
    Value *LowSum = nullptr;
    auto *Shift = dyn_cast_or_null<BinaryOperator>(ShiftedHigh);
    if (!Shift || Shift->getOpcode() != Instruction::Shl || !isI64(Shift) ||
        Shift->hasNoSignedWrap() ||
        !isI64Constant(resolveSingleStoreLoad(Shift->getOperand(1)), HalfWidth) ||
        !matchAndMask(Shift->getOperand(0), LowHalfMask, MaskedHighSum) ||
        !matchAndMask(MaskedLow, LowHalfMask, LowSum) ||
        !matchLowSum(LowSum, A, B) ||
        !matchHighSum(MaskedHighSum, A, B, LowSum))
      continue;
    return true;
  }
  return false;
}

} // namespace

extern "C" unsigned
elisacoreCanonicalizeSplitHalfUnsignedAdds(LLVMModuleRef ModuleRef) {
  if (!ModuleRef)
    return 0;
  Module *M = unwrap(ModuleRef);
  unsigned Rewritten = 0;
  for (Function &F : *M) {
    if (F.isDeclaration())
      continue;
    for (BasicBlock &BB : F) {
      for (auto It = BB.begin(); It != BB.end();) {
        Instruction *I = &*It++;
        auto *Combined = dyn_cast<BinaryOperator>(I);
        Value *A = nullptr;
        Value *B = nullptr;
        if (!matchSplitHalfAdd(Combined, A, B))
          continue;

        auto *Add = BinaryOperator::CreateAdd(A, B);
        Add->insertBefore(Combined->getIterator());
        Add->setDebugLoc(Combined->getDebugLoc());
        Add->cloneDebugInfoFrom(Combined);
        Combined->replaceAllUsesWith(Add);
        Combined->dropDbgRecords();
        Combined->eraseFromParent();
        ++Rewritten;
      }
    }
  }
  return Rewritten;
}
