// Fold a right-shift loop whose only observable result is the low bit of
// its accumulator. Adding each shifted word or its low bit has the same
// parity as the population count of the initial word.
namespace {
static bool parityConstant(Value *V, uint64_t Expected) {
  auto *C = dyn_cast<ConstantInt>(V);
  return C && C->equalsInt(Expected);
}

static bool foldShiftParity(BasicBlock &Body) {
  auto *Branch = dyn_cast<CondBrInst>(Body.getTerminator());
  auto *Done = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  if (!Done || !Done->isEquality() || Branch->getMetadata(LLVMContext::MD_loop) ||
      Body.size() < 6 || Body.size() > 7)
    return false;
  unsigned ExitIndex = Done->getPredicate() == ICmpInst::ICMP_EQ ? 0 : 1;
  BasicBlock *Exit = Branch->getSuccessor(ExitIndex);
  if (Exit == &Body || Branch->getSuccessor(1 - ExitIndex) != &Body ||
      Exit->getSinglePredecessor() != &Body || !Exit->phis().empty())
    return false;

  auto *Shift = dyn_cast<BinaryOperator>(Done->getOperand(0));
  if (!Shift || Shift->getParent() != &Body ||
      Shift->getOpcode() != Instruction::LShr ||
      !parityConstant(Shift->getOperand(1), 1) ||
      !parityConstant(Done->getOperand(1), 0))
    return false;
  auto *Word = dyn_cast<PHINode>(Shift->getOperand(0));
  if (!Word || Word->getParent() != &Body || !Word->getType()->isIntegerTy(64) ||
      Word->getNumIncomingValues() != 2 || Word->getIncomingValueForBlock(&Body) != Shift)
    return false;
  unsigned EntryIndex = Word->getIncomingBlock(0) == &Body ? 1 : 0;
  BasicBlock *Entry = Word->getIncomingBlock(EntryIndex);
  if (Entry == Exit) return false;
  auto *Count = dyn_cast<PHINode>(Word->getNextNode());
  if (!Count) Count = dyn_cast<PHINode>(Body.begin());
  if (!Count || Count == Word || Count->getNumIncomingValues() != 2 ||
      !parityConstant(Count->getIncomingValueForBlock(Entry), 0))
    return false;
  auto *Sum = dyn_cast<BinaryOperator>(Count->getIncomingValueForBlock(&Body));
  if (!Sum || Sum->getParent() != &Body || Sum->getOpcode() != Instruction::Add)
    return false;
  Value *Term = Sum->getOperand(0);
  if (Term == Count) Term = Sum->getOperand(1);
  else if (Sum->getOperand(1) != Count) return false;
  auto *LowBit = dyn_cast<BinaryOperator>(Term);
  if (Term != Word &&
      (!LowBit || LowBit->getParent() != &Body ||
       LowBit->getOpcode() != Instruction::And ||
       LowBit->getOperand(0) != Word || !parityConstant(LowBit->getOperand(1), 1)))
    return false;

  BinaryOperator *Parity = nullptr;
  for (Instruction &I : Body) {
    if (&I != Word && &I != Count && &I != Sum && &I != Shift &&
        &I != Done && &I != Branch && &I != LowBit)
      return false;
    for (User *U : I.users()) {
      auto *Use = dyn_cast<Instruction>(U);
      if (Use && Use->getParent() == &Body) continue;
      auto *And = dyn_cast<BinaryOperator>(U);
      if (&I != Sum || !And || And->getParent() != Exit ||
          And->getOpcode() != Instruction::And ||
          And->getOperand(0) != Sum || !parityConstant(And->getOperand(1), 1) ||
          (Parity && Parity != And))
        return false;
      Parity = And;
    }
  }
  if (!Parity) return false;

  Value *Input = Word->getIncomingValue(EntryIndex);
  if (auto *Definition = dyn_cast<Instruction>(Input))
    if (Definition->getParent() == &Body || Definition->getParent() == Exit)
      return false;
  IRBuilder<> Builder(Parity);
  auto *Popcount = Intrinsic::getOrInsertDeclaration(Body.getModule(), Intrinsic::ctpop,
                                                    {Input->getType()});
  auto *Bits = Builder.CreateCall(Popcount, {Input});
  Parity->replaceAllUsesWith(Builder.CreateAnd(Bits, 1, "bit.parity"));
  Parity->eraseFromParent();
  Entry->getTerminator()->replaceSuccessorWith(&Body, Exit);
  Body.dropAllReferences();
  Body.eraseFromParent();
  return true;
}
} // namespace

static unsigned elisacoreFoldShiftParityLoops(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (BDU_SkipFunction(F, M) || F.size() > 24) continue;
    for (BasicBlock &Body : F) {
      if (!foldShiftParity(Body)) continue;
      ++Changed;
      break;
    }
  }
  return Changed;
}
