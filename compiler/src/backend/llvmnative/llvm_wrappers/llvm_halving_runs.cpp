// Replace consecutive even steps of a counted parity recurrence with one
// trailing-zero count and a variable shift. Zero remains a nonterminating
// state, including when the wrapping odd update produces zero.
namespace {
static bool halvingConstant(Value *V, uint64_t Expected) {
  auto *C = dyn_cast<ConstantInt>(V);
  return C && C->equalsInt(Expected);
}

static Value *halvingOtherOperand(Value *V, unsigned Opcode, uint64_t Constant) {
  auto *Op = dyn_cast_or_null<BinaryOperator>(V);
  if (!Op || Op->getOpcode() != Opcode) return nullptr;
  if (halvingConstant(Op->getOperand(1), Constant)) return Op->getOperand(0);
  if (Op->isCommutative() && halvingConstant(Op->getOperand(0), Constant))
    return Op->getOperand(1);
  return nullptr;
}

static Value *halvingEqualityOperand(ICmpInst *Cmp, uint64_t Constant) {
  if (!Cmp || !Cmp->isEquality()) return nullptr;
  if (halvingConstant(Cmp->getOperand(1), Constant)) return Cmp->getOperand(0);
  if (halvingConstant(Cmp->getOperand(0), Constant)) return Cmp->getOperand(1);
  return nullptr;
}

static bool foldHalvingRun(BasicBlock &Body) {
  if (Body.size() != 11 || Body.hasAddressTaken()) return false;
  auto *Branch = dyn_cast<CondBrInst>(Body.getTerminator());
  auto *Done = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  auto *Next = dyn_cast_or_null<SelectInst>(halvingEqualityOperand(Done, 1));
  if (!Next || Next->getParent() != &Body || !Next->getType()->isIntegerTy(64) ||
      Branch->hasMetadataOtherThanDebugLoc())
    return false;
  unsigned ExitIndex = Done->getPredicate() == ICmpInst::ICMP_EQ ? 0 : 1;
  if (Branch->getSuccessor(1 - ExitIndex) != &Body ||
      Branch->getSuccessor(ExitIndex) == &Body)
    return false;

  auto *Parity = dyn_cast<ICmpInst>(Next->getCondition());
  Value *Bit = halvingEqualityOperand(Parity, 0);
  auto *Word = dyn_cast_or_null<PHINode>(halvingOtherOperand(Bit, Instruction::And, 1));
  if (!Word || Word->getParent() != &Body || Word->getNumIncomingValues() != 2 ||
      Word->getBasicBlockIndex(&Body) < 0 || Word->getIncomingValueForBlock(&Body) != Next)
    return false;
  bool EvenTrue = Parity->getPredicate() == ICmpInst::ICMP_EQ;
  Value *Half = EvenTrue ? Next->getTrueValue() : Next->getFalseValue();
  Value *Odd = EvenTrue ? Next->getFalseValue() : Next->getTrueValue();
  Value *Triple = halvingOtherOperand(Odd, Instruction::Add, 1);
  if (!Triple || halvingOtherOperand(Triple, Instruction::Mul, 3) != Word ||
      halvingOtherOperand(Half, Instruction::LShr, 1) != Word)
    return false;

  auto *Count = dyn_cast<PHINode>(Word->getNextNode());
  if (!Count) Count = dyn_cast<PHINode>(Body.begin());
  if (!Count || Count == Word || Count->getType() != Word->getType() ||
      Count->getNumIncomingValues() != 2 || Count->getBasicBlockIndex(&Body) < 0)
    return false;
  Value *Step = Count->getIncomingValueForBlock(&Body);
  if (halvingOtherOperand(Step, Instruction::Add, 1) != Count) return false;

  // All state changes are represented by these two PHIs. The counter
  // after the final transition is the only value observable outside.
  for (Instruction &I : Body) {
    if (&I != Word && &I != Count && &I != Half && &I != Bit && &I != Parity &&
        &I != Triple && &I != Odd && &I != Next && &I != Step &&
        &I != Done && &I != Branch)
      return false;
    if (&I == Step) continue;
    for (User *U : I.users())
      if (auto *Use = dyn_cast<Instruction>(U); !Use || Use->getParent() != &Body)
        return false;
  }

  IRBuilder<> B(Next);
  B.SetCurrentDebugLocation(Next->getDebugLoc());
  Value *Even = B.CreateSelect(Parity, EvenTrue ? Word : Odd,
                               EvenTrue ? Odd : Word, "halving.even");
  Value *RawZeros = B.CreateIntrinsic(Intrinsic::cttz, {B.getInt64Ty()}, {Even, B.getFalse()});
  Value *Zeros = B.CreateAnd(RawZeros, 63, "halving.count");
  Value *Shifted = B.CreateLShr(Even, Zeros, "halving.next");
  Value *Delta = B.CreateAdd(Bit, Zeros);
  Value *Steps = B.CreateAdd(Count, Delta, "halving.steps");
  Next->replaceAllUsesWith(Shifted);
  Step->replaceAllUsesWith(Steps);
  return true;
}
} // namespace

static unsigned elisacoreFoldHalvingRuns(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (BDU_SkipFunction(F, M) || F.size() > 128) continue;
    for (BasicBlock &BB : F) Changed += foldHalvingRun(BB);
  }
  return Changed;
}
