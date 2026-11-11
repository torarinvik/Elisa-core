//go:build cgo

#include <cstdint>
#include <llvm-c/Core.h>
#include <llvm/Config/llvm-config.h>

#if LLVM_VERSION_MAJOR >= 23
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/GlobalVariable.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Module.h>

using namespace llvm;

// Count the visits to an unsigned divide-by-ten loop using the input's
// highest set bit and one comparison against the next decimal power.
namespace {
constexpr auto decimalDigitPowers() {
  struct { uint64_t values[19]; } Powers{};
  uint64_t Power = 10;
  for (unsigned I = 0; I < 19; ++I) {
    Powers.values[I] = Power;
    if (I != 18) Power *= 10;
  }
  return Powers;
}

constexpr auto DecimalDigitPowers = decimalDigitPowers();

constexpr bool decimalDigitEstimateIsExact() {
  for (unsigned Bit = 0; Bit < 64; ++Bit) {
    unsigned Estimate = (Bit * 1233) >> 12;
    uint64_t Low = uint64_t(1) << Bit;
    uint64_t High = Low | (Low - 1);
    if (Estimate >= 19 ||
        (Estimate && Low < DecimalDigitPowers.values[Estimate - 1]) ||
        (Estimate < 18 && High >= DecimalDigitPowers.values[Estimate + 1]))
      return false;
  }
  return true;
}
static_assert(decimalDigitEstimateIsExact());

static bool decimalDigitConstant(Value *V, uint64_t Expected) {
  auto *C = dyn_cast<ConstantInt>(V);
  return C && C->equalsInt(Expected);
}

// Match the width loop left by division by a power of two. The entry edge
// handles the one-digit case and the loop carries only the word and count.
static bool foldPowerOfTwoDigitCount(BasicBlock &Body) {
  if (Body.size() != 6 || Body.hasAddressTaken()) return false;
  auto *Branch = dyn_cast<CondBrInst>(Body.getTerminator());
  auto *Cmp = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  if (!Cmp || Cmp->getPredicate() != ICmpInst::ICMP_UGT ||
      Branch->getSuccessor(0) != &Body || Branch->hasMetadataOtherThanDebugLoc())
    return false;
  BasicBlock *Exit = Branch->getSuccessor(1);
  if (Exit == &Body) return false;

  auto *Word = dyn_cast<PHINode>(Cmp->getOperand(0));
  if (!Word || Word->getParent() != &Body || !Word->getType()->isIntegerTy(64) ||
      Word->getNumIncomingValues() != 2)
    return false;
  int Back = Word->getBasicBlockIndex(&Body);
  if (Back < 0) return false;
  auto *Quotient = dyn_cast<BinaryOperator>(Word->getIncomingValue(Back));
  if (!Quotient || Quotient->getParent() != &Body ||
      Quotient->getOpcode() != Instruction::LShr || Quotient->isExact() ||
      Quotient->getOperand(0) != Word)
    return false;
  auto *Shift = dyn_cast<ConstantInt>(Quotient->getOperand(1));
  if (!Shift || Shift->isZero() || Shift->getValue().uge(32)) return false;
  uint64_t Bits = Shift->getZExtValue();
  if (!decimalDigitConstant(Cmp->getOperand(1), (uint64_t(1) << (2 * Bits)) - 1))
    return false;

  BasicBlock *Entry = Word->getIncomingBlock(1 - Back);
  Value *Input = Word->getIncomingValue(1 - Back);
  auto *EntryBranch = dyn_cast<CondBrInst>(Entry->getTerminator());
  auto *EntryCmp = EntryBranch ? dyn_cast<ICmpInst>(EntryBranch->getCondition()) : nullptr;
  if (!EntryCmp || EntryCmp->getPredicate() != ICmpInst::ICMP_UGT ||
      EntryBranch->getSuccessor(0) != &Body || EntryBranch->getSuccessor(1) != Exit ||
      EntryCmp->getOperand(0) != Input ||
      !decimalDigitConstant(EntryCmp->getOperand(1), (uint64_t(1) << Bits) - 1))
    return false;

  auto *Count = dyn_cast<PHINode>(Word->getNextNode());
  if (!Count) Count = dyn_cast<PHINode>(Body.begin());
  if (!Count || Count == Word || !Count->getType()->isIntegerTy(64) ||
      Count->getNumIncomingValues() != 2 ||
      !decimalDigitConstant(Count->getIncomingValueForBlock(Entry), 1))
    return false;
  auto *Next = dyn_cast<BinaryOperator>(Count->getIncomingValueForBlock(&Body));
  if (!Next || Next->getParent() != &Body || Next->getOpcode() != Instruction::Add ||
      Next->getOperand(0) != Count || !decimalDigitConstant(Next->getOperand(1), 1))
    return false;

  // All exit PHIs must agree when bypassing the loop, except the width PHI.
  PHINode *Width = nullptr;
  for (PHINode &Phi : Exit->phis()) {
    Value *FromBody = Phi.getIncomingValueForBlock(&Body);
    Value *FromEntry = Phi.getIncomingValueForBlock(Entry);
    if (FromBody == Next && decimalDigitConstant(FromEntry, 1) && !Width)
      Width = &Phi;
    else if (FromBody != FromEntry)
      return false;
  }
  if (!Width) return false;
  for (Instruction &I : Body) {
    if (&I != Word && &I != Quotient && &I != Count && &I != Next && &I != Cmp && &I != Branch)
      return false;
    for (User *U : I.users()) {
      auto *Use = dyn_cast<Instruction>(U);
      if (!Use || (Use->getParent() != &Body && !(&I == Next && Use == Width)))
        return false;
    }
  }

  IRBuilder<> B(EntryBranch);
  B.SetCurrentDebugLocation(EntryBranch->getDebugLoc());
  Value *Nonzero = B.CreateOr(B.CreateFreeze(Input), 1);
  Value *Zeros = B.CreateIntrinsic(Intrinsic::ctlz, {B.getInt64Ty()}, {Nonzero, B.getTrue()});
  Value *Log2 = B.CreateSub(B.getInt64(63), Zeros);
  Value *Digits = B.CreateAdd(B.CreateUDiv(Log2, B.getInt64(Bits)), B.getInt64(1));
  Width->setIncomingValueForBlock(Entry, Digits);
  EntryBranch->setCondition(B.getFalse());
  return true;
}

static bool foldDecimalDigitCount(BasicBlock &Body, GlobalVariable *&Powers) {
  if (Body.size() != 6 || Body.hasAddressTaken()) return false;
  auto *Branch = dyn_cast<CondBrInst>(Body.getTerminator());
  auto *Cmp = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  if (!Cmp || Cmp->getPredicate() != ICmpInst::ICMP_ULT ||
      Branch->getSuccessor(0) == &Body || Branch->getSuccessor(1) != &Body ||
      !decimalDigitConstant(Cmp->getOperand(1), 10) ||
      Branch->hasMetadataOtherThanDebugLoc())
    return false;
  auto *Word = dyn_cast<PHINode>(Cmp->getOperand(0));
  if (!Word || Word->getParent() != &Body || !Word->getType()->isIntegerTy(64) ||
      Word->getNumIncomingValues() != 2)
    return false;
  int Back = Word->getBasicBlockIndex(&Body);
  if (Back < 0) return false;
  auto *Quotient = dyn_cast<BinaryOperator>(Word->getIncomingValue(Back));
  if (!Quotient || Quotient->getParent() != &Body ||
      Quotient->getOpcode() != Instruction::UDiv || Quotient->isExact() ||
      Quotient->getOperand(0) != Word || !decimalDigitConstant(Quotient->getOperand(1), 10))
    return false;
  BasicBlock *Entry = Word->getIncomingBlock(1 - Back);
  auto *Count = dyn_cast<PHINode>(Word->getNextNode());
  if (!Count) Count = dyn_cast<PHINode>(Body.begin());
  if (!Count || Count == Word || !Count->getType()->isIntegerTy(64) ||
      Count->getNumIncomingValues() != 2 ||
      !decimalDigitConstant(Count->getIncomingValueForBlock(Entry), 0))
    return false;
  auto *Next = dyn_cast<BinaryOperator>(Count->getIncomingValueForBlock(&Body));
  if (!Next || Next->getParent() != &Body || Next->getOpcode() != Instruction::Add ||
      Next->getOperand(0) != Count || !decimalDigitConstant(Next->getOperand(1), 1))
    return false;
  for (Instruction &I : Body) {
    if (&I != Word && &I != Quotient && &I != Count && &I != Next && &I != Cmp && &I != Branch)
      return false;
    if (&I == Count || &I == Next) continue;
    for (User *U : I.users())
      if (auto *Use = dyn_cast<Instruction>(U); !Use || Use->getParent() != &Body)
        return false;
  }

  IRBuilder<> B(&*Body.getFirstNonPHIIt());
  B.SetCurrentDebugLocation(Next->getDebugLoc());
  if (!Powers) {
    SmallVector<Constant *, 19> Values;
    for (uint64_t Power : DecimalDigitPowers.values) Values.push_back(B.getInt64(Power));
    auto *Type = ArrayType::get(B.getInt64Ty(), Values.size());
    Powers = new GlobalVariable(*Body.getModule(), Type, true, GlobalValue::PrivateLinkage,
                                ConstantArray::get(Type, Values), "decimal.digit.powers");
    Powers->setUnnamedAddr(GlobalValue::UnnamedAddr::Global);
  }
  Value *Input = B.CreateFreeze(Word->getIncomingValue(1 - Back));
  Value *Nonzero = B.CreateOr(Input, 1);
  Value *Zeros = B.CreateIntrinsic(Intrinsic::ctlz, {B.getInt64Ty()}, {Nonzero, B.getTrue()});
  Value *Log2 = B.CreateSub(B.getInt64(63), Zeros);
  Value *Estimate = B.CreateLShr(B.CreateMul(Log2, B.getInt64(1233)), 12);
  Value *Address = B.CreateInBoundsGEP(Powers->getValueType(), Powers, {B.getInt64(0), Estimate});
  Value *Threshold = B.CreateLoad(B.getInt64Ty(), Address);
  Value *Adjustment = B.CreateZExt(B.CreateICmpUGE(Input, Threshold), B.getInt64Ty());
  Value *LastCount = B.CreateAdd(Estimate, Adjustment, "decimal.last.count");
  Value *Digits = B.CreateAdd(LastCount, B.getInt64(1), "decimal.digits");
  auto OutsideBody = [&Body](Use &U) {
    auto *I = dyn_cast<Instruction>(U.getUser());
    return I && I->getParent() != &Body;
  };
  Count->replaceUsesWithIf(LastCount, OutsideBody);
  Next->replaceUsesWithIf(Digits, OutsideBody);
  Branch->setCondition(B.getTrue());
  return true;
}
} // namespace

extern "C" unsigned elisacoreFoldDigitCountsInFunction(LLVMValueRef FunctionRef,
                                                       LLVMValueRef *PowersRef) {
  auto &F = *cast<Function>(unwrap(FunctionRef));
  auto *Powers = cast_or_null<GlobalVariable>(unwrap(*PowersRef));
  unsigned Changed = 0;
  for (BasicBlock &BB : F)
    Changed += foldPowerOfTwoDigitCount(BB) || foldDecimalDigitCount(BB, Powers);
  *PowersRef = Powers ? wrap(Powers) : nullptr;
  return Changed;
}
#endif
