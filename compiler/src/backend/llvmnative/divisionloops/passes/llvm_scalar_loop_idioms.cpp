// Recognize scalar loops whose result can be computed without iteration.
namespace {
static BinaryOperator *scalarLoopBinOp(Value *V, unsigned Opcode, BasicBlock &Loop) {
  auto *Bin = dyn_cast_or_null<BinaryOperator>(V);
  return Bin && Bin->getOpcode() == Opcode && Bin->getParent() == &Loop ? Bin : nullptr;
}

static bool scalarLoopPair(BinaryOperator &Bin, Value *X, Value *Y) {
  return (Bin.getOperand(0) == X && Bin.getOperand(1) == Y) ||
         (Bin.getOperand(0) == Y && Bin.getOperand(1) == X);
}

static bool scalarLoopHasConstant(Value *V, uint64_t Expected) {
  auto *C = dyn_cast<ConstantInt>(V);
  return C && C->equalsInt(Expected);
}

static BasicBlock *scalarLoopExit(CondBrInst &Branch, BasicBlock &Loop) {
  if (Branch.hasMetadataOtherThanDebugLoc()) return nullptr;
  if (Branch.getSuccessor(0) == &Loop && Branch.getSuccessor(1) != &Loop)
    return Branch.getSuccessor(1);
  if (Branch.getSuccessor(1) == &Loop && Branch.getSuccessor(0) != &Loop)
    return Branch.getSuccessor(0);
  return nullptr;
}

// (x xor y) + 2 * (x and y) == x + y modulo the type width. Each
// iteration shifts the remaining carry left, so the loop terminates.
static bool foldCarryAddition(BasicBlock &Loop, CondBrInst &Back, BasicBlock *Exit) {
  auto *Test = dyn_cast<ICmpInst>(Back.getCondition());
  if (!Test || !Test->isEquality() || !Test->hasOneUse() ||
      Back.getSuccessor(Test->getPredicate() == ICmpInst::ICMP_EQ ? 0 : 1) != Exit)
    return false;
  auto *Carry = scalarLoopBinOp(Test->getOperand(0), Instruction::Shl, Loop);
  if (!Carry || !scalarLoopHasConstant(Test->getOperand(1), 0) ||
      !scalarLoopHasConstant(Carry->getOperand(1), 1) || !Carry->hasNUses(2) ||
      !Carry->getType()->isIntegerTy() || Carry->getType()->isIntegerTy(1))
    return false;
  auto *Both = scalarLoopBinOp(Carry->getOperand(0), Instruction::And, Loop);
  if (!Both || !Both->hasOneUse()) return false;
  auto *X = dyn_cast<PHINode>(Both->getOperand(0));
  auto *Y = dyn_cast<PHINode>(Both->getOperand(1));
  if (!X || !Y || X == Y || X->getParent() != &Loop || Y->getParent() != &Loop ||
      X->getNumIncomingValues() != 2 || Y->getNumIncomingValues() != 2 ||
      !X->hasNUses(2) || !Y->hasNUses(2)) return false;
  if (X->getIncomingValueForBlock(&Loop) == Carry) std::swap(X, Y);
  if (Y->getIncomingValueForBlock(&Loop) != Carry) return false;
  auto *SumBits = scalarLoopBinOp(X->getIncomingValueForBlock(&Loop), Instruction::Xor, Loop);
  if (!SumBits || !scalarLoopPair(*SumBits, X, Y)) return false;
  auto *Entry = X->getIncomingBlock(X->getIncomingBlock(0) == &Loop ? 1 : 0);
  Value *InitialX = X->getIncomingValueForBlock(Entry), *InitialY = Y->getIncomingValueForBlock(Entry);
  if (Entry == &Loop || !InitialX || !InitialY) return false;

  IRBuilder<> B(&Back);
  Value *Left = B.CreateFreeze(InitialX);
  Value *Right = B.CreateFreeze(InitialY);
  Value *Sum = B.CreateAdd(Left, Right, "carry.sum");
  B.CreateBr(Exit);
  Back.eraseFromParent();
  Test->eraseFromParent();
  X->removeIncomingValue(&Loop, false);
  Y->removeIncomingValue(&Loop, false);
  SumBits->replaceAllUsesWith(Sum);
  SumBits->eraseFromParent();
  Carry->eraseFromParent();
  Both->eraseFromParent();
  X->eraseFromParent();
  Y->eraseFromParent();
  return true;
}

// An unsigned lower-bound search with a direct threshold comparison
// preserves the threshold clamped to its interval as the interval shrinks.
static bool foldThresholdSearch(BasicBlock &Loop, CondBrInst &Back, BasicBlock *Exit) {
  auto *Test = dyn_cast<ICmpInst>(Back.getCondition());
  if (!Test || !Test->hasOneUse()) return false;
  bool BackOnTrue = Back.getSuccessor(0) == &Loop;
  auto Pred = BackOnTrue ? Test->getPredicate() : Test->getInversePredicate();
  if (Pred != ICmpInst::ICMP_ULT && Pred != ICmpInst::ICMP_UGT) return false;
  auto *NextLow = dyn_cast<SelectInst>(Test->getOperand(Pred == ICmpInst::ICMP_ULT ? 0 : 1));
  auto *NextHigh = dyn_cast<SelectInst>(Test->getOperand(Pred == ICmpInst::ICMP_ULT ? 1 : 0));
  if (!NextLow || !NextHigh || NextLow->getParent() != &Loop || NextHigh->getParent() != &Loop ||
      !NextHigh->hasNUses(2) || NextLow->getCondition() != NextHigh->getCondition()) return false;

  auto *Split = dyn_cast<ICmpInst>(NextLow->getCondition());
  if (!Split || Split->getParent() != &Loop ||
      (Split->getPredicate() != ICmpInst::ICMP_ULT && Split->getPredicate() != ICmpInst::ICMP_UGE))
    return false;
  bool GoRight = Split->getPredicate() == ICmpInst::ICMP_ULT;
  auto *Low = dyn_cast<PHINode>(GoRight ? NextLow->getFalseValue() : NextLow->getTrueValue());
  auto *High = dyn_cast<PHINode>(GoRight ? NextHigh->getTrueValue() : NextHigh->getFalseValue());
  auto *Mid = scalarLoopBinOp(Split->getOperand(0), Instruction::Add, Loop);
  Value *Target = Split->getOperand(1);
  auto *TargetInst = dyn_cast<Instruction>(Target);
  if (!Low || !High || Low == High || !Mid || (TargetInst && TargetInst->getParent() == &Loop) ||
      (GoRight ? NextHigh->getFalseValue() : NextHigh->getTrueValue()) != Mid ||
      Low->getParent() != &Loop || High->getParent() != &Loop ||
      Low->getNumIncomingValues() != 2 || High->getNumIncomingValues() != 2 ||
      Low->getIncomingValueForBlock(&Loop) != NextLow || High->getIncomingValueForBlock(&Loop) != NextHigh)
    return false;
  auto *Step = scalarLoopBinOp(GoRight ? NextLow->getTrueValue() : NextLow->getFalseValue(),
                               Instruction::Add, Loop);
  if (!Step || Step->getOperand(0) != Mid || !scalarLoopHasConstant(Step->getOperand(1), 1))
    return false;
  Value *Half = Mid->getOperand(0) == Low ? Mid->getOperand(1) :
                Mid->getOperand(1) == Low ? Mid->getOperand(0) : nullptr;
  auto *Shift = scalarLoopBinOp(Half, Instruction::LShr, Loop);
  auto *Width = Shift ? scalarLoopBinOp(Shift->getOperand(0), Instruction::Sub, Loop) : nullptr;
  if (!Width || !scalarLoopHasConstant(Shift->getOperand(1), 1) ||
      Width->getOperand(0) != High || Width->getOperand(1) != Low ||
      !Low->hasNUses(3) || !High->hasNUses(2) || !Mid->hasNUses(3) ||
      !Shift->hasOneUse() || !Width->hasOneUse() || !Step->hasOneUse() ||
      !Split->hasNUses(2) || !Low->getType()->isIntegerTy())
    return false;
  auto *Entry = Low->getIncomingBlock(Low->getIncomingBlock(0) == &Loop ? 1 : 0);
  if (Entry == &Loop) return false;
  auto *Guard = dyn_cast<CondBrInst>(Entry->getTerminator());
  Value *InitialLow = Low->getIncomingValueForBlock(Entry);
  Value *InitialHigh = High->getIncomingValueForBlock(Entry), *GuardLow = nullptr;
  auto *GuardTest = Guard ? rangeAndContinueTest(*Guard, &Loop, InitialHigh, GuardLow) : nullptr;
  if (!GuardTest || !InitialHigh || InitialLow != GuardLow || InitialLow == InitialHigh)
    return false;

  IRBuilder<> B(GuardTest);
  Value *First = B.CreateFreeze(InitialLow);
  Value *Last = B.CreateFreeze(InitialHigh);
  GuardTest->replaceUsesOfWith(InitialLow, First);
  GuardTest->replaceUsesOfWith(InitialHigh, Last);
  B.SetInsertPoint(&Back);
  Value *Key = Target == InitialLow ? First : Target == InitialHigh ? Last : B.CreateFreeze(Target);
  Value *Capped = B.CreateIntrinsic(Intrinsic::umin, {Key->getType()}, {Key, Last});
  Value *Result = B.CreateIntrinsic(Intrinsic::umax, {Key->getType()}, {First, Capped});
  B.CreateBr(Exit);
  Back.eraseFromParent();
  Test->eraseFromParent();
  Low->removeIncomingValue(&Loop, false);
  High->removeIncomingValue(&Loop, false);
  NextLow->replaceAllUsesWith(Result);
  Instruction *Dead[] = {NextLow, NextHigh, Step, Split, Mid, Shift, Width, Low, High};
  for (Instruction *I : Dead) I->eraseFromParent();
  return true;
}
// A self-loop that shifts away low zero bits terminates after cttz(seed)
// shifts for a nonzero seed. Match both a pre-test of bit zero and a
// post-test of bit one paired with an exact shift.
static bool foldTrailingZeroLoop(BasicBlock &Loop, CondBrInst &Back, BasicBlock *Exit) {
  auto *Test = dyn_cast<ICmpInst>(Back.getCondition());
  if (!Test || !Test->isEquality() || !Test->hasOneUse() ||
      !scalarLoopHasConstant(Test->getOperand(1), 0) ||
      Back.getSuccessor(Test->getPredicate() == ICmpInst::ICMP_EQ ? 0 : 1) != &Loop)
    return false;
  auto *Bit = scalarLoopBinOp(Test->getOperand(0), Instruction::And, Loop);
  if (!Bit || !Bit->hasOneUse())
    return false;
  bool Post = scalarLoopHasConstant(Bit->getOperand(1), 2);
  if (!Post && !scalarLoopHasConstant(Bit->getOperand(1), 1))
    return false;
  auto *Word = dyn_cast<PHINode>(Bit->getOperand(0));
  if (!Word || Word->getParent() != &Loop || Word->getNumIncomingValues() != 2 ||
      (!Word->getType()->isIntegerTy(32) && !Word->getType()->isIntegerTy(64)))
    return false;
  auto *Entry = Word->getIncomingBlock(Word->getIncomingBlock(0) == &Loop ? 1 : 0);
  auto *Shift = scalarLoopBinOp(Word->getIncomingValueForBlock(&Loop), Instruction::LShr, Loop);
  if (Entry == &Loop || !Shift || Shift->getOperand(0) != Word ||
      !scalarLoopHasConstant(Shift->getOperand(1), 1) || Shift->isExact() != Post)
    return false;

  PHINode *Count = nullptr;
  BinaryOperator *Increment = nullptr;
  for (PHINode &Phi : Loop.phis()) {
    if (&Phi == Word)
      continue;
    if (Count || Phi.getType() != Word->getType() || Phi.getNumIncomingValues() != 2 ||
        !scalarLoopHasConstant(Phi.getIncomingValueForBlock(Entry), 0))
      return false;
    Count = &Phi;
    Increment = scalarLoopBinOp(Phi.getIncomingValueForBlock(&Loop), Instruction::Add, Loop);
    if (!Increment || Increment->getOperand(0) != Count ||
        !scalarLoopHasConstant(Increment->getOperand(1), 1))
      return false;
  }
  for (Instruction &I : Loop)
    if ((&I != Word && &I != Shift && &I != Bit && &I != Test &&
         &I != Count && &I != Increment && &I != &Back) ||
        I.hasMetadataOtherThanDebugLoc())
      return false;

  Value *Seed = Word->getIncomingValueForBlock(Entry);
  AssumptionCache AC(*Loop.getParent());
  LazyValueInfo LVI(Loop.getParent(), &AC);
  ConstantRange Range = LVI.getConstantRange(Seed, Entry->getTerminator(), false)
                            .intersectWith(LVI.getConstantRangeOnEdge(Seed, Entry, &Loop));
  if (Range.isEmptySet() || Range.contains(APInt::getZero(Word->getType()->getIntegerBitWidth())))
    return false;

  IRBuilder<> B(&Back);
  auto *Type = cast<IntegerType>(Word->getType());
  Value *Stable = B.CreateFreeze(Seed, "trailing.zero.seed");
  Value *Zeros = B.CreateIntrinsic(Intrinsic::cttz, {Type}, {Stable, B.getTrue()});
  Value *One = ConstantInt::get(Type, 1);
  Value *Before = Zeros;
  if (Post) {
    // An odd seed can exit after one iteration with a defined count, even
    // though its exact shift is poison. Keep that iteration in the count.
    Value *AtLeastOne = B.CreateIntrinsic(Intrinsic::umax, {Type}, {Zeros, One});
    Before = B.CreateSub(AtLeastOne, One);
  }
  Value *Current = B.CreateLShr(Stable, Before, "trailing.zero.word");
  Value *Next = B.CreateLShr(Current, One);
  Value *NextCount = Count ? B.CreateAdd(Before, One) : nullptr;
  B.CreateBr(Exit);
  Back.eraseFromParent();
  Test->eraseFromParent();
  Bit->eraseFromParent();
  Word->removeIncomingValue(&Loop, false);
  Word->replaceAllUsesWith(Current);
  Shift->replaceAllUsesWith(Next);
  Shift->eraseFromParent();
  Word->eraseFromParent();
  if (Count) {
    Count->removeIncomingValue(&Loop, false);
    Count->replaceAllUsesWith(Before);
    Increment->replaceAllUsesWith(NextCount);
    Increment->eraseFromParent();
    Count->eraseFromParent();
  }
  return true;
}

} // namespace

static unsigned elisacoreFoldScalarLoopIdioms(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.size() > 48 || BDU_SkipFunction(F, M)) continue;
    for (BasicBlock &Loop : F) {
      if ((Loop.size() != 5 && Loop.size() != 7 && Loop.size() != 11) || Loop.hasAddressTaken()) continue;
      auto *Back = dyn_cast<CondBrInst>(Loop.getTerminator());
      BasicBlock *Exit = Back ? scalarLoopExit(*Back, Loop) : nullptr;
      if (!Exit) continue;
      if (Loop.size() <= 7 && foldTrailingZeroLoop(Loop, *Back, Exit)) {
        ++Changed;
        continue;
      }
      if (F.size() > 16)
        continue;
      if (Loop.size() == 7)
        Changed += foldCarryAddition(Loop, *Back, Exit);
      else if (Loop.size() == 11)
        Changed += foldThresholdSearch(Loop, *Back, Exit);
    }
  }
  return Changed;
}
