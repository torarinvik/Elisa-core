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

static unsigned elisacoreFoldDecimalDigitCounts(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  GlobalVariable *Powers = nullptr;
  for (Function &F : M) {
    if (BDU_SkipFunction(F, M) || F.size() > 128) continue;
    for (BasicBlock &BB : F) Changed += foldDecimalDigitCount(BB, Powers);
  }
  return Changed;
}
