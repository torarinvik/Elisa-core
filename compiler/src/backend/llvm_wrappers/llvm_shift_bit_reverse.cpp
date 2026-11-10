// A shift-and-append loop reverses the significant bits of its input.
// A full-width reverse moves the leading zeroes to the low end, where a
// right shift by the original leading-zero count removes them.
namespace {
static bool reverseConstant(Value *V, uint64_t Expected) {
  auto *C = dyn_cast<ConstantInt>(V);
  return C && C->equalsInt(Expected);
}

static bool reverseHasNonnegativeInput(BasicBlock &Entry, Value *Input) {
  BasicBlock *Pred = Entry.getSinglePredecessor();
  auto *Branch = Pred ? dyn_cast<CondBrInst>(Pred->getTerminator()) : nullptr;
  auto *Cmp = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  if (!Cmp || Cmp->getOperand(0) != Input || !reverseConstant(Cmp->getOperand(1), 0))
    return false;
  unsigned Nonnegative;
  if (Cmp->getPredicate() == ICmpInst::ICMP_SLT)
    Nonnegative = 1;
  else if (Cmp->getPredicate() == ICmpInst::ICMP_SGE)
    Nonnegative = 0;
  else
    return false;
  return Branch->getSuccessor(Nonnegative) == &Entry &&
         Branch->getSuccessor(1 - Nonnegative) != &Entry;
}

static bool foldShiftBitReverse(BasicBlock &Latch) {
  auto *Back = dyn_cast<CondBrInst>(Latch.getTerminator());
  auto *Done = Back ? dyn_cast<ICmpInst>(Back->getCondition()) : nullptr;
  if (!Done || !Done->isEquality() || Latch.hasAddressTaken() ||
      Back->getMetadata(LLVMContext::MD_loop))
    return false;
  unsigned ExitIndex = Done->getPredicate() == ICmpInst::ICMP_EQ ? 0 : 1;
  BasicBlock *Exit = Back->getSuccessor(ExitIndex);
  BasicBlock *Head = Back->getSuccessor(1 - ExitIndex);
  if (Head == Exit || Head->hasAddressTaken() || Exit->getSinglePredecessor() != &Latch || !Exit->phis().empty())
    return false;

  auto *Shift = dyn_cast<BinaryOperator>(Done->getOperand(0));
  if (!Shift || Shift->getParent() != &Latch || Shift->getOpcode() != Instruction::LShr ||
      !reverseConstant(Shift->getOperand(1), 1) || !reverseConstant(Done->getOperand(1), 0))
    return false;
  auto *Word = dyn_cast<PHINode>(Shift->getOperand(0));
  if (!Word || Word->getParent() != Head || !Word->getType()->isIntegerTy(64) ||
      Word->getNumIncomingValues() != 2 || Word->getIncomingValueForBlock(&Latch) != Shift)
    return false;
  unsigned EntryIndex = Word->getIncomingBlock(0) == &Latch ? 1 : 0;
  BasicBlock *Entry = Word->getIncomingBlock(EntryIndex);
  if (Entry == Exit || Entry == Head || Entry == &Latch) return false;
  Value *Input = Word->getIncomingValue(EntryIndex);
  // Both entry guards and the replacement must see the same word.
  auto *Parameter = dyn_cast<Argument>(Input);
  if (!isa<FreezeInst, ConstantInt>(Input) &&
      !(Parameter && Parameter->hasAttribute(Attribute::NoUndef)))
    return false;
  auto *Enter = dyn_cast<CondBrInst>(Entry->getTerminator());
  auto *Zero = Enter ? dyn_cast<ICmpInst>(Enter->getCondition()) : nullptr;
  if (!Zero || !Zero->isEquality() || Zero->getOperand(0) != Input ||
      !reverseConstant(Zero->getOperand(1), 0))
    return false;
  unsigned Nonzero = Zero->getPredicate() == ICmpInst::ICMP_EQ ? 1 : 0;
  if (Enter->getSuccessor(Nonzero) != Head || Enter->getSuccessor(1 - Nonzero) == Head ||
      Enter->getSuccessor(1 - Nonzero) == Exit)
    return false;

  auto *Reverse = dyn_cast<PHINode>(Word->getNextNode());
  if (!Reverse) Reverse = dyn_cast<PHINode>(Head->begin());
  if (!Reverse || Reverse == Word || Reverse->getNumIncomingValues() != 2 ||
      !reverseConstant(Reverse->getIncomingValueForBlock(Entry), 0))
    return false;
  auto *Append = dyn_cast<BinaryOperator>(Reverse->getIncomingValueForBlock(&Latch));
  if (!Append || Append->getParent() != &Latch || Append->getOpcode() != Instruction::Or)
    return false;
  auto *Grow = dyn_cast<BinaryOperator>(Append->getOperand(0));
  auto *Bit = dyn_cast<BinaryOperator>(Append->getOperand(1));
  if (Grow && Grow->getOpcode() == Instruction::And) std::swap(Grow, Bit);
  if (!Grow || Grow->getParent() != &Latch || Grow->getOpcode() != Instruction::Shl ||
      Grow->getOperand(0) != Reverse || !reverseConstant(Grow->getOperand(1), 1) ||
      !Bit || Bit->getParent() != &Latch || Bit->getOpcode() != Instruction::And ||
      Bit->getOperand(0) != Word || !reverseConstant(Bit->getOperand(1), 1))
    return false;

  BasicBlock *Trap = nullptr;
  CondBrInst *Guard = nullptr;
  ICmpInst *Overflow = nullptr;
  BinaryOperator *Offset = nullptr;
  if (Head != &Latch) {
    // The checked signed form guards a doubling with reversed >= 2^62.
    // A nonnegative i64 has at most 63 bits, so its partial reverse has at
    // most 62 bits at every doubling, including the final one.
    Guard = dyn_cast<CondBrInst>(Head->getTerminator());
    Overflow = Guard ? dyn_cast<ICmpInst>(Guard->getCondition()) : nullptr;
    Offset = Overflow ? dyn_cast<BinaryOperator>(Overflow->getOperand(0)) : nullptr;
    if (!Offset || Offset->getParent() != Head || Offset->getOpcode() != Instruction::Add ||
        Offset->getOperand(0) != Reverse ||
        !reverseConstant(Offset->getOperand(1), uint64_t{1} << 62) ||
        Overflow->getPredicate() != ICmpInst::ICMP_SLT ||
        !reverseConstant(Overflow->getOperand(1), 0) ||
        Guard->getSuccessor(1) != &Latch || Latch.getSinglePredecessor() != Head ||
        !reverseHasNonnegativeInput(*Entry, Input))
      return false;
    Trap = Guard->getSuccessor(0);
    if (Trap == &Latch || Trap->hasAddressTaken() || Trap->getSinglePredecessor() != Head || !BDU_IsTrapBlock(*Trap))
      return false;
  }
  auto CheckBody = [&](BasicBlock &Block) {
    for (Instruction &I : Block) {
      if (&I != Word && &I != Reverse && &I != Append && &I != Grow &&
          &I != Bit && &I != Shift && &I != Done && &I != Back &&
          &I != Guard && &I != Overflow && &I != Offset)
        return false;
      for (User *U : I.users()) {
        auto *Use = dyn_cast<Instruction>(U);
        if (!Use) return false;
        if (Use->getParent() == Head || Use->getParent() == &Latch) continue;
        if (&I != Append || Use->getParent() != Exit) return false;
      }
    }
    return true;
  };
  if (!CheckBody(*Head) || (Head != &Latch && !CheckBody(Latch))) return false;
  if (auto *Definition = dyn_cast<Instruction>(Input))
    if (Definition->getParent() == Head || Definition->getParent() == &Latch ||
        Definition->getParent() == Exit)
      return false;

  IRBuilder<> Builder(&*Exit->getFirstInsertionPt());
  auto *M = Latch.getModule();
  auto *ReverseBits = Intrinsic::getOrInsertDeclaration(M, Intrinsic::bitreverse,
                                                        {Input->getType()});
  auto *LeadingZeroes = Intrinsic::getOrInsertDeclaration(M, Intrinsic::ctlz,
                                                          {Input->getType()});
  auto *Bits = Builder.CreateCall(ReverseBits, {Input});
  auto *Count = Builder.CreateCall(LeadingZeroes, {Input, Builder.getTrue()});
  auto *Result = Builder.CreateLShr(Bits, Count, "significant.reverse");
  for (Use &U : make_early_inc_range(Append->uses()))
    if (cast<Instruction>(U.getUser())->getParent() == Exit) U.set(Result);
  Enter->setSuccessor(Nonzero, Exit);
  Head->dropAllReferences();
  if (Head != &Latch) Latch.dropAllReferences();
  Head->eraseFromParent();
  if (Head != &Latch) Latch.eraseFromParent();
  if (Trap) Trap->eraseFromParent();
  return true;
}
} // namespace

static unsigned elisacoreFoldShiftBitReverseLoops(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (BDU_SkipFunction(F, M) || F.size() > 24) continue;
    for (BasicBlock &Block : F) {
      if (!foldShiftBitReverse(Block)) continue;
      ++Changed;
      break;
    }
  }
  return Changed;
}
