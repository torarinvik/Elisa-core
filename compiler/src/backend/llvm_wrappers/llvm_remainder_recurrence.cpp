// Two unsigned remainder steps establish x > y > 0 on entry to the loop.
// Thereafter x - y is the remainder whenever it is smaller than y.
namespace {
static bool remainderZeroTest(Value *V, Value *Remainder) {
  auto *Compare = dyn_cast<ICmpInst>(V);
  auto *Zero = Compare ? dyn_cast<ConstantInt>(Compare->getOperand(1)) : nullptr;
  return Zero && Zero->isZero() && Compare->getPredicate() == ICmpInst::ICMP_EQ &&
         Compare->getOperand(0) == Remainder && Compare->hasOneUse();
}

static bool splitRemainderRecurrence(Function &F) {
  BasicBlock &Entry = F.getEntryBlock();
  auto *Guard = dyn_cast<CondBrInst>(Entry.getTerminator());
  if (!Guard || Entry.size() != 2 || Guard->hasMetadataOtherThanDebugLoc())
    return false;
  BasicBlock *Exit = Guard->getSuccessor(0), *Loop = Guard->getSuccessor(1);
  auto *Back = dyn_cast<CondBrInst>(Loop->getTerminator());
  if (Exit == Loop || Exit == &Entry || Loop == &Entry || Loop->size() != 5 ||
      Exit->size() != 2 || !Back || Back->getSuccessor(0) != Exit ||
      Back->getSuccessor(1) != Loop || Back->hasMetadataOtherThanDebugLoc() ||
      Loop->hasAddressTaken() || Exit->hasAddressTaken())
    return false;

  auto *OldRem = dyn_cast<BinaryOperator>(&*Loop->getFirstNonPHIIt());
  if (!OldRem || OldRem->getOpcode() != Instruction::URem ||
      !OldRem->getType()->isIntegerTy(64) || OldRem->hasMetadataOtherThanDebugLoc() ||
      !remainderZeroTest(Back->getCondition(), OldRem) || !OldRem->hasNUses(2))
    return false;
  auto *X = dyn_cast<PHINode>(OldRem->getOperand(0));
  auto *Y = dyn_cast<PHINode>(OldRem->getOperand(1));
  auto *Result = dyn_cast<PHINode>(&Exit->front());
  auto *Return = dyn_cast<ReturnInst>(Exit->getTerminator());
  if (!X || !Y || X == Y || X->getParent() != Loop || Y->getParent() != Loop ||
      !X->hasOneUse() || !Y->hasNUses(3) || X->getNumIncomingValues() != 2 ||
      Y->getNumIncomingValues() != 2 || X->getIncomingValueForBlock(Loop) != Y ||
      Y->getIncomingValueForBlock(Loop) != OldRem || !Result || !Return ||
      Return->getReturnValue() != Result || Result->getNumIncomingValues() != 2 ||
      Result->getIncomingValueForBlock(Loop) != Y)
    return false;
  Value *InitialX = X->getIncomingValueForBlock(&Entry);
  Value *InitialY = Y->getIncomingValueForBlock(&Entry);
  if (!InitialX || !InitialY || !remainderZeroTest(Guard->getCondition(), InitialY) ||
      Result->getIncomingValueForBlock(&Entry) != InitialX)
    return false;

  LLVMContext &Context = F.getContext();
  BasicBlock *First = BasicBlock::Create(Context, "remainder.first", &F, Loop);
  BasicBlock *Second = BasicBlock::Create(Context, "remainder.second", &F, Loop);
  BasicBlock *Slow = BasicBlock::Create(Context, "remainder.divide", &F, Exit);
  BasicBlock *Latch = BasicBlock::Create(Context, "remainder.next", &F, Exit);
  auto *EntryTest = cast<ICmpInst>(Guard->getCondition());
  IRBuilder<> Builder(EntryTest);
  // Use the same divisor in the zero check and in the initial division.
  Value *Divisor = Builder.CreateFreeze(InitialY);
  EntryTest->setOperand(0, Divisor);
  Guard->setSuccessor(1, First);

  Builder.SetInsertPoint(First);
  Value *FirstRem = Builder.CreateURem(InitialX, Divisor);
  Value *Zero = Builder.getInt64(0);
  Builder.CreateCondBr(Builder.CreateICmpEQ(FirstRem, Zero), Exit, Second);
  Builder.SetInsertPoint(Second);
  Value *SecondRem = Builder.CreateURem(Divisor, FirstRem);
  Builder.CreateCondBr(Builder.CreateICmpEQ(SecondRem, Zero), Exit, Loop);

  auto *OldTest = cast<Instruction>(Back->getCondition());
  Back->eraseFromParent();
  OldTest->eraseFromParent();
  Builder.SetInsertPoint(Loop);
  Value *Difference = Builder.CreateSub(X, Y);
  Builder.CreateCondBr(Builder.CreateICmpULT(Difference, Y), Latch, Slow);
  Builder.SetInsertPoint(Slow);
  Value *FullRem = Builder.CreateURem(X, Y);
  Builder.CreateBr(Latch);
  Builder.SetInsertPoint(Latch);
  auto *Remainder = Builder.CreatePHI(OldRem->getType(), 2);
  Remainder->addIncoming(Difference, Loop);
  Remainder->addIncoming(FullRem, Slow);
  Builder.CreateCondBr(Builder.CreateICmpEQ(Remainder, Zero), Exit, Loop);

  X->setIncomingValueForBlock(&Entry, FirstRem);
  Y->setIncomingValueForBlock(&Entry, SecondRem);
  Y->setIncomingValueForBlock(Loop, Remainder);
  for (auto *P : {X, Y}) {
    P->setIncomingBlock(P->getBasicBlockIndex(&Entry), Second);
    P->setIncomingBlock(P->getBasicBlockIndex(Loop), Latch);
  }
  Result->setIncomingBlock(Result->getBasicBlockIndex(Loop), Latch);
  Result->addIncoming(Divisor, First);
  Result->addIncoming(FirstRem, Second);
  OldRem->eraseFromParent();
  return true;
}
} // namespace

static unsigned elisacoreSplitRemainderRecurrences(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M)
    if (F.size() == 3 && !BDU_SkipFunction(F, M) && splitRemainderRecurrence(F)) ++Changed;
  return Changed;
}
