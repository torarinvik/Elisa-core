// For a narrow dividend, divide by (1 or 3) * (1 or 2) using a shift
// and a constant-divisor quotient. Unsigned division rounds down, so
// floor(floor(n / 2^b) / k) equals floor(n / (k * 2^b)).
namespace {
static bool factoredDivisorConstant(Value *V, unsigned Expected) {
  auto *C = dyn_cast<ConstantInt>(V);
  return C && C->equalsInt(Expected);
}

static Value *factoredDivisorBit(Value *Factor, bool &Invert) {
  auto *Op = dyn_cast<BinaryOperator>(Factor);
  if (!Op) return nullptr;
  Value *Bit;
  if (Op->getOpcode() == Instruction::Add && factoredDivisorConstant(Op->getOperand(1), 1)) {
    Bit = Op->getOperand(0);
    Invert = false;
  } else if (Op->getOpcode() == Instruction::Sub && factoredDivisorConstant(Op->getOperand(0), 2)) {
    Bit = Op->getOperand(1);
    Invert = true;
  } else {
    return nullptr;
  }
  auto *Mask = dyn_cast<BinaryOperator>(Bit);
  return Mask && Mask->getOpcode() == Instruction::And &&
                 factoredDivisorConstant(Mask->getOperand(1), 1) ? Bit : nullptr;
}

static bool splitFactoredDivisor(BinaryOperator &Mul, Function &F) {
  if (Mul.getOpcode() != Instruction::Mul || !Mul.getType()->isIntegerTy(64) ||
      !Mul.hasNUses(2) || Mul.hasMetadataOtherThanDebugLoc())
    return false;
  SmallVector<BinaryOperator *, 2> Divisions;
  for (User *U : Mul.users()) {
    auto *Div = dyn_cast<BinaryOperator>(U);
    if (!Div || Div->getOpcode() != Instruction::UDiv || Div->getOperand(1) != &Mul ||
        Div->getOperand(0) == &Mul || Div->getParent() != Mul.getParent() || Div->isExact() ||
        Div->hasMetadataOtherThanDebugLoc())
      return false;
    Divisions.push_back(Div);
  }
  auto *Odd = dyn_cast<SelectInst>(Mul.getOperand(0));
  Value *Even = Mul.getOperand(1);
  if (!Odd) {
    Odd = dyn_cast<SelectInst>(Even);
    Even = Mul.getOperand(0);
  }
  if (!Odd || !factoredDivisorConstant(Odd->getTrueValue(), 3) ||
      !factoredDivisorConstant(Odd->getFalseValue(), 1))
    return false;
  bool Invert;
  Value *Bit = factoredDivisorBit(Even, Invert);
  if (!Bit) return false;

  DominatorTree DT(F);
  LoopInfo LI(DT);
  if (!LI.getLoopFor(Mul.getParent())) return false;
  TargetLibraryInfoImpl TLII(F.getParent()->getTargetTriple());
  TargetLibraryInfo TLI(TLII);
  AssumptionCache AC(F);
  ScalarEvolution SE(F, TLI, AC, DT, LI);
  for (auto *Div : Divisions)
    if (SE.getUnsignedRangeMax(SE.getSCEV(Div->getOperand(0))).getActiveBits() > 32)
      return false;

  IRBuilder<> Builder(&Mul);
  Builder.SetCurrentDebugLocation(Mul.getDebugLoc());
  Value *Shift = Builder.CreateTrunc(Bit, Builder.getInt32Ty());
  if (Invert) Shift = Builder.CreateXor(Shift, Builder.getInt32(1));
  for (auto *Div : Divisions) {
    Builder.SetInsertPoint(Div);
    Builder.SetCurrentDebugLocation(Div->getDebugLoc());
    Value *Narrow = Builder.CreateTrunc(Div->getOperand(0), Builder.getInt32Ty());
    Value *Shifted = Builder.CreateLShr(Narrow, Shift, "factored.div.half");
    Value *Third = Builder.CreateUDiv(Shifted, Builder.getInt32(3), "factored.div.third");
    Value *Quotient = Builder.CreateSelect(Odd->getCondition(), Third, Shifted);
    Div->replaceAllUsesWith(Builder.CreateZExt(Quotient, Div->getType()));
    Div->eraseFromParent();
  }
  Mul.eraseFromParent();
  return true;
}
} // namespace

static unsigned elisacoreSplitFactoredDivisions(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (BDU_SkipFunction(F, M) || F.size() > 24 || F.getInstructionCount() > 256) continue;
    for (Instruction &I : instructions(F)) {
      auto *Mul = dyn_cast<BinaryOperator>(&I);
      if (!Mul || !splitFactoredDivisor(*Mul, F)) continue;
      ++Changed;
      break;
    }
  }
  return Changed;
}
