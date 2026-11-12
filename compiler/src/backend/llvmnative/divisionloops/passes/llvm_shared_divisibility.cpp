// Share the odd factor in a short-circuit chain of divisibility tests.
#include <llvm/IR/IRBuilder.h>
#include <llvm/Transforms/Utils/Local.h>

namespace {
static ICmpInst *divisibilityComparison(Value *V, ICmpInst::Predicate Predicate) {
  auto *Compare = dyn_cast<ICmpInst>(V);
  auto *Zero = Compare ? dyn_cast<ConstantInt>(Compare->getOperand(1)) : nullptr;
  return Zero && Zero->isZero() && Compare->getPredicate() == Predicate ? Compare : nullptr;
}

static ConstantInt *divisibilityOperand(ICmpInst *Compare, unsigned Opcode,
                                        Value *&Numerator) {
  auto *Operation = Compare ? dyn_cast<BinaryOperator>(Compare->getOperand(0)) : nullptr;
  if (!Operation || Operation->getOpcode() != Opcode || !Operation->hasOneUse())
    return nullptr;
  Numerator = Operation->getOperand(0);
  return dyn_cast<ConstantInt>(Operation->getOperand(1));
}

static bool foldSharedDivisibility(CondBrInst &First) {
  BasicBlock *Start = First.getParent();
  BasicBlock *Exit = First.getSuccessor(0);
  BasicBlock *Middle = First.getSuccessor(1);
  if (Middle->getSinglePredecessor() != Start || Middle->size() != 3 ||
      Exit->size() != 2)
    return false;
  auto *Phi = dyn_cast<PHINode>(&Exit->front());
  auto *Ret = dyn_cast<ReturnInst>(Exit->getTerminator());
  auto *Second = dyn_cast<CondBrInst>(Middle->getTerminator());
  if (!Phi || !Ret || Ret->getReturnValue() != Phi || !Second ||
      Second->getSuccessor(1) != Exit)
    return false;
  BasicBlock *Last = Second->getSuccessor(0);
  if (Last->getSinglePredecessor() != Middle || Last->size() != 4 ||
      Last->getSingleSuccessor() != Exit)
    return false;

  auto *One = dyn_cast<ConstantInt>(Phi->getIncomingValueForBlock(Start));
  auto *Zero = dyn_cast<ConstantInt>(Phi->getIncomingValueForBlock(Middle));
  auto *Result = dyn_cast<ZExtInst>(Phi->getIncomingValueForBlock(Last));
  if (!One || !One->isOne() || !Zero || !Zero->isZero() ||
      !Result || Result->getParent() != Last)
    return false;

  auto *LargeTest = divisibilityComparison(First.getCondition(), ICmpInst::ICMP_EQ);
  auto *MaskTest = divisibilityComparison(Second->getCondition(), ICmpInst::ICMP_EQ);
  auto *SmallTest = divisibilityComparison(Result->getOperand(0), ICmpInst::ICMP_NE);
  Value *N = nullptr, *Masked = nullptr, *Reduced = nullptr;
  auto *Large = divisibilityOperand(LargeTest, Instruction::URem, N);
  auto *Mask = divisibilityOperand(MaskTest, Instruction::And, Masked);
  auto *Small = divisibilityOperand(SmallTest, Instruction::URem, Reduced);
  if (!Large || !Mask || !Small || N != Masked || N != Reduced ||
      MaskTest->getParent() != Middle || SmallTest->getParent() != Last ||
      cast<Instruction>(MaskTest->getOperand(0))->getParent() != Middle ||
      cast<Instruction>(SmallTest->getOperand(0))->getParent() != Last)
    return false;

  const APInt &A = Large->getValue(), &B = Small->getValue();
  if (A.isZero() || B.isZero() || A.countTrailingZeros() <= B.countTrailingZeros())
    return false;
  const APInt Odd = B.lshr(B.countTrailingZeros());
  if (Odd.isOne() || A.lshr(A.countTrailingZeros()) != Odd ||
      Mask->getValue() != APInt::getLowBitsSet(B.getBitWidth(), B.countTrailingZeros()))
    return false;

  // A = odd * 2^a and B = odd * 2^b, with a > b. A multiple of A
  // already passes the low-bits guard. On that guard, testing B only
  // requires the shared odd factor.
  IRBuilder<> Builder(&First);
  Builder.SetCurrentDebugLocation(First.getDebugLoc());
  N = Builder.CreateFreeze(N);
  Value *Low = Builder.CreateICmpEQ(Builder.CreateAnd(N, Mask), ConstantInt::getNullValue(N->getType()));
  Value *High = Builder.CreateICmpEQ(
      Builder.CreateAnd(N, APInt::getLowBitsSet(A.getBitWidth(), A.countTrailingZeros())),
      ConstantInt::getNullValue(N->getType()));
  Value *OddRemainder = Builder.CreateURem(N, ConstantInt::get(N->getType(), Odd));
  Value *NotMultiple = Builder.CreateIsNotNull(OddRemainder);
  Value *Match = Builder.CreateAnd(Low, Builder.CreateOr(High, NotMultiple));
  Phi->setIncomingValueForBlock(Start, Builder.CreateZExt(Match, Phi->getType()));
  Builder.CreateBr(Exit);
  First.eraseFromParent();
  return true;
}

static unsigned foldSharedDivisibilityTests(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.size() < 4 || F.size() > 16 || BDU_SkipFunction(F, M)) continue;
    for (BasicBlock &Block : F) {
      auto *Branch = dyn_cast<CondBrInst>(Block.getTerminator());
      if (!Branch || !foldSharedDivisibility(*Branch)) continue;
      removeUnreachableBlocks(F);
      ++Changed;
      break;
    }
  }
  return Changed;
}
} // namespace
