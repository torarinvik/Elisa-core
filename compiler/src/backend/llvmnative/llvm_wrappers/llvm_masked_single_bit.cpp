// An at-most-one-bit value selected by a mask that excludes the sign bit
// is already positive. Fuse the positivity, population and mask checks.
namespace {
static bool singleBitConstant(Value *V, uint64_t Expected) {
  auto *C = dyn_cast<ConstantInt>(V);
  return C && C->equalsInt(Expected);
}

static bool foldMaskedSingleBit(BasicBlock &Block) {
  auto *Branch = dyn_cast<CondBrInst>(Block.getTerminator());
  auto *Select = Branch ? dyn_cast<SelectInst>(Branch->getCondition()) : nullptr;
  if (!Select || !Select->hasOneUse() || !singleBitConstant(Select->getFalseValue(), 0))
    return false;
  auto *Positive = dyn_cast<ICmpInst>(Select->getCondition());
  auto *SmallCount = dyn_cast<ICmpInst>(Select->getTrueValue());
  auto *Count = SmallCount ? dyn_cast<IntrinsicInst>(SmallCount->getOperand(0)) : nullptr;
  if (!Positive || Positive->getPredicate() != ICmpInst::ICMP_SGT ||
      !singleBitConstant(Positive->getOperand(1), 0) ||
      !Count || Count->getIntrinsicID() != Intrinsic::ctpop || !Count->hasOneUse() ||
      !SmallCount->hasOneUse() || SmallCount->getPredicate() != ICmpInst::ICMP_ULT ||
      !singleBitConstant(SmallCount->getOperand(1), 2))
    return false;
  Value *Input = Positive->getOperand(0);
  if (Count->getArgOperand(0) != Input) return false;

  BasicBlock *MaskBlock = Branch->getSuccessor(0);
  BasicBlock *Exit = Branch->getSuccessor(1);
  auto *Jump = dyn_cast<UncondBrInst>(MaskBlock->getTerminator());
  if (Exit == &Block || MaskBlock == Exit || MaskBlock->hasAddressTaken() ||
      MaskBlock->getSinglePredecessor() != &Block || MaskBlock->size() != 4 ||
      !Jump || Jump->getSuccessor() != Exit || Exit->phis().empty())
    return false;
  auto *Result = cast<PHINode>(&Exit->front());
  if (isa<PHINode>(Result->getNextNode()) ||
      !singleBitConstant(Result->getIncomingValueForBlock(&Block), 0))
    return false;
  auto *Extend = dyn_cast<ZExtInst>(Result->getIncomingValueForBlock(MaskBlock));
  auto *Nonzero = Extend ? dyn_cast<ICmpInst>(Extend->getOperand(0)) : nullptr;
  auto *Masked = Nonzero ? dyn_cast<BinaryOperator>(Nonzero->getOperand(0)) : nullptr;
  auto *Mask = Masked ? dyn_cast<ConstantInt>(Masked->getOperand(1)) : nullptr;
  if (!Extend || Extend->getParent() != MaskBlock || !Nonzero ||
      Nonzero->getParent() != MaskBlock || Nonzero->getPredicate() != ICmpInst::ICMP_NE ||
      !singleBitConstant(Nonzero->getOperand(1), 0) ||
      !Masked || Masked->getParent() != MaskBlock || Masked->getOpcode() != Instruction::And ||
      Masked->getOperand(0) != Input || !Mask || Mask->isNegative())
    return false;

  IRBuilder<> B(Branch);
  // Use wrapping arithmetic rather than the original ctpop, which can
  // carry a nonzero result range justified only by the old positive arm.
  Value *Stable = B.CreateFreeze(Input);
  auto *Type = cast<IntegerType>(Stable->getType());
  Value *MinusOne = B.CreateSub(Stable, ConstantInt::get(Type, 1));
  Value *Single = B.CreateIsNull(B.CreateAnd(Stable, MinusOne));
  Value *MaskInput = Stable;
  if (Type->getBitWidth() > 32 && Mask->getValue().getActiveBits() <= 32) {
    MaskInput = B.CreateTrunc(Stable, B.getInt32Ty());
    Mask = B.getInt32(Mask->getZExtValue());
  }
  Value *HasBit = B.CreateIsNotNull(B.CreateAnd(MaskInput, Mask));
  Result->setIncomingValueForBlock(&Block, B.CreateZExt(B.CreateAnd(Single, HasBit), Result->getType()));
  B.CreateBr(Exit);
  Branch->eraseFromParent();
  DeleteDeadBlock(MaskBlock);
  RecursivelyDeleteTriviallyDeadInstructions(Select);
  return true;
}
} // namespace

static unsigned elisacoreFoldMaskedSingleBits(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.size() > 16 || BDU_SkipFunction(F, M)) continue;
    for (BasicBlock &B : F) {
      if (foldMaskedSingleBit(B)) {
        ++Changed;
        break;
      }
    }
  }
  return Changed;
}
