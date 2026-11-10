// Clearing low set bits while upper > lower keeps precisely the common
// prefix of the unsigned endpoints. The first differing bit identifies
// the prefix directly, independent of how many bits the loop would clear.
namespace {
static ICmpInst *rangeAndContinueTest(CondBrInst &Branch, BasicBlock *Continue,
                                    Value *Upper, Value *&Lower) {
  auto *Cmp = dyn_cast<ICmpInst>(Branch.getCondition());
  if (!Cmp || !Cmp->hasOneUse() || Branch.hasMetadataOtherThanDebugLoc() ||
      Branch.getSuccessor(0) == Branch.getSuccessor(1)) return nullptr;
  bool TrueEdge = Branch.getSuccessor(0) == Continue;
  if (!TrueEdge && Branch.getSuccessor(1) != Continue) return nullptr;
  auto Pred = TrueEdge ? Cmp->getPredicate() : Cmp->getInversePredicate();
  if (Pred == ICmpInst::ICMP_ULT && Cmp->getOperand(1) == Upper) {
    Lower = Cmp->getOperand(0);
    return Cmp;
  }
  if (Pred == ICmpInst::ICMP_UGT && Cmp->getOperand(0) == Upper) {
    Lower = Cmp->getOperand(1);
    return Cmp;
  }
  return nullptr;
}

// When a dominating guard admits only ordered 32-bit endpoints, a 64-bit
// suffix mask also handles equality. A shift of 32 then clears the entire
// suffix mask, so the original upper endpoint survives unchanged.
static bool narrowRangeAndBounds(BasicBlock &Entry, BasicBlock &Loop, BasicBlock &Exit,
                                CondBrInst &Guard, Value *Lower, Value *Initial, Value *Next,
                                Value *&Low, Value *&High) {
  if (!Initial->getType()->isIntegerTy(64) ||
      (Guard.getSuccessor(0) != &Exit && Guard.getSuccessor(1) != &Exit))
    return false;
  BasicBlock *Pred = Entry.getSinglePredecessor();
  auto *Check = Pred ? dyn_cast<CondBrInst>(Pred->getTerminator()) : nullptr;
  auto *Either = Check ? dyn_cast<BinaryOperator>(Check->getCondition()) : nullptr;
  if (!Either || Either->getOpcode() != Instruction::Or || !Either->hasOneUse() ||
      Check->getSuccessor(1) != &Entry || Check->getSuccessor(0) == &Entry)
    return false;
  ICmpInst *Order = nullptr, *Bound = nullptr;
  for (Value *Term : Either->operands()) {
    auto *Cmp = dyn_cast<ICmpInst>(Term);
    if (!Cmp || Cmp->getParent() != Pred || !Cmp->hasOneUse() ||
        Cmp->getPredicate() != ICmpInst::ICMP_UGT) return false;
    if (Cmp->getOperand(0) == Lower && Cmp->getOperand(1) == Initial) Order = Cmp;
    auto *K = dyn_cast<ConstantInt>(Cmp->getOperand(1));
    if (Cmp->getOperand(0) == Initial && K && K->getValue().getActiveBits() <= 32)
      Bound = Cmp;
  }
  if (!Order || !Bound) return false;
  for (PHINode &P : Exit.phis()) {
    Value *Skipped = P.getIncomingValueForBlock(&Entry), *Ran = P.getIncomingValueForBlock(&Loop);
    if (Skipped != Ran && (Skipped != Initial || Ran != Next)) return false;
  }

  IRBuilder<> B(Order);
  Low = B.CreateFreeze(Lower);
  B.SetInsertPoint(Order->comesBefore(Bound) ? Order : Bound);
  High = B.CreateFreeze(Initial);
  Order->setOperand(0, Low);
  Order->setOperand(1, High);
  Bound->setOperand(0, High);
  Exit.removePredecessor(&Entry);
  B.SetInsertPoint(&Guard);
  B.CreateBr(&Loop);
  Guard.eraseFromParent();
  return true;
}

static bool foldRangeAndLoop(BasicBlock &Loop) {
  auto *Back = dyn_cast<CondBrInst>(Loop.getTerminator());
  auto *Upper = dyn_cast<PHINode>(&Loop.front());
  if (!Back || !Upper || Upper->getNumIncomingValues() != 2 ||
      !(Upper->getType()->isIntegerTy(32) || Upper->getType()->isIntegerTy(64)) ||
      Loop.size() != 5 || Loop.hasAddressTaken()) return false;
  if (Back->getSuccessor(0) != &Loop && Back->getSuccessor(1) != &Loop)
    return false;
  auto *Next = dyn_cast_or_null<BinaryOperator>(Upper->getIncomingValueForBlock(&Loop));
  if (!Next || Next->getOpcode() != Instruction::And || Next->getParent() != &Loop)
    return false;
  Value *Other = Next->getOperand(0) == Upper ? Next->getOperand(1) :
                 Next->getOperand(1) == Upper ? Next->getOperand(0) : nullptr;
  auto *Step = dyn_cast_or_null<BinaryOperator>(Other);
  if (!Step || Step->getOpcode() != Instruction::Add || Step->getParent() != &Loop ||
      !Step->hasOneUse() || Step->getOperand(0) != Upper || !Upper->hasNUses(2))
    return false;
  auto *MinusOne = dyn_cast<ConstantInt>(Step->getOperand(1));
  if (!MinusOne || !MinusOne->isMinusOne()) return false;

  Value *Lower = nullptr;
  auto *Test = rangeAndContinueTest(*Back, &Loop, Next, Lower);
  if (!Test) return false;
  if (auto *LowInst = dyn_cast<Instruction>(Lower); LowInst && LowInst->getParent() == &Loop)
    return false;
  auto *Entry = Upper->getIncomingBlock(Upper->getIncomingBlock(0) == &Loop ? 1 : 0);
  if (Entry == &Loop) return false;
  auto *Guard = dyn_cast<CondBrInst>(Entry->getTerminator());
  Value *Initial = Upper->getIncomingValueForBlock(Entry), *GuardLower = nullptr;
  auto *GuardTest = Guard ? rangeAndContinueTest(*Guard, &Loop, Initial, GuardLower) : nullptr;
  if (!GuardTest || GuardLower != Lower || Lower == Initial) return false;

  BasicBlock *Exit = Back->getSuccessor(Back->getSuccessor(0) == &Loop ? 1 : 0);
  // Use the same endpoint values for the entry test and for the prefix. In
  // particular, the exclusive loop edge makes ctlz's zero case unreachable.
  Value *Low = nullptr, *High = nullptr;
  bool Narrow = narrowRangeAndBounds(*Entry, Loop, *Exit, *Guard, Lower, Initial, Next, Low, High);
  IRBuilder<> B(GuardTest);
  if (Narrow) {
    GuardTest->eraseFromParent();
  } else {
    Low = B.CreateFreeze(Lower);
    High = B.CreateFreeze(Initial);
    GuardTest->replaceUsesOfWith(Lower, Low);
    GuardTest->replaceUsesOfWith(Initial, High);
  }
  B.SetInsertPoint(Test);
  Value *Difference = B.CreateXor(Low, High);
  if (Narrow) Difference = B.CreateTrunc(Difference, B.getInt32Ty());
  Value *PrefixLength = B.CreateIntrinsic(Intrinsic::ctlz, {Difference->getType()},
                                         {Difference, B.getInt1(!Narrow)});
  Value *SuffixBits = ConstantInt::getAllOnesValue(Upper->getType());
  if (Narrow) {
    PrefixLength = B.CreateZExt(PrefixLength, Upper->getType());
    SuffixBits = B.getInt64(UINT64_C(0xffffffff));
  }
  Value *Suffix = B.CreateLShr(SuffixBits, PrefixLength);
  Value *Result = B.CreateAnd(High, B.CreateNot(Suffix), "range.and");
  B.SetInsertPoint(Back);
  B.CreateBr(Exit);
  Back->eraseFromParent();
  Test->eraseFromParent();
  Upper->removeIncomingValue(&Loop, false);
  Next->replaceAllUsesWith(Result);
  Next->eraseFromParent();
  Step->eraseFromParent();
  Upper->eraseFromParent();
  return true;
}
} // namespace

static unsigned elisacoreFoldRangeAndLoops(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.size() > 16 || BDU_SkipFunction(F, M)) continue;
    for (BasicBlock &B : F)
      if (B.size() == 5 && foldRangeAndLoop(B)) ++Changed;
  }
  return Changed;
}
