// Narrow division in a carry recurrence (carry + small product) / divisor.
namespace {
static bool narrowBoundedCarry(BinaryOperator &Div, LoopInfo &LI, ScalarEvolution &SE) {
  auto *Divisor = dyn_cast<ConstantInt>(Div.getOperand(1));
  auto *Sum = dyn_cast<BinaryOperator>(Div.getOperand(0));
  if (!Divisor || Divisor->isNegative() || Divisor->getValue().ult(2) ||
      Divisor->getValue().ugt(UINT32_MAX) || Div.isExact() ||
      !Sum || Sum->getOpcode() != Instruction::Add)
    return false;
  Loop *L = LI.getLoopFor(Div.getParent());
  if (!L || !L->getSubLoops().empty()) return false;
  BasicBlock *Pre = L->getLoopPredecessor(), *Latch = L->getLoopLatch();
  if (!Pre || !Latch) return false;

  for (unsigned Side : {0u, 1u}) {
    auto *Carry = dyn_cast<PHINode>(Sum->getOperand(Side));
    auto *StableProduct = dyn_cast<FreezeInst>(Sum->getOperand(1 - Side));
    if (!Carry || Carry->getParent() != L->getHeader() || Carry->getNumIncomingValues() != 2 ||
        Carry->getBasicBlockIndex(Pre) < 0 || Carry->getBasicBlockIndex(Latch) < 0 ||
        Carry->getIncomingValueForBlock(Latch) != &Div || !StableProduct)
      continue;
    auto *Seed = dyn_cast<ConstantInt>(Carry->getIncomingValueForBlock(Pre));
    auto *Product = dyn_cast<BinaryOperator>(StableProduct->getOperand(0));
    if (!Seed || !Product || Product->getOpcode() != Instruction::Mul ||
        Seed->getValue().ugt(UINT32_MAX / 2))
      continue;
    unsigned Bits = SE.getUnsignedRangeMax(SE.getSCEV(Product)).getActiveBits();
    if (!Bits || Bits > 31) continue;

    // Freeze the product at its proven width. If the original product is
    // poison, a narrower choice is still a valid result of freeze. Replacing
    // every use preserves agreement between the quotient and remainder.
    IRBuilder<> B(StableProduct);
    B.SetCurrentDebugLocation(StableProduct->getDebugLoc());
    Value *Narrow = B.CreateTrunc(Product, B.getIntNTy(Bits));
    Value *Stable = B.CreateZExt(B.CreateFreeze(Narrow), Div.getType(), "carry.product");
    StableProduct->replaceAllUsesWith(Stable);
    StableProduct->eraseFromParent();

    // Let B be the larger of the seed and the largest narrow product.
    // Each sum is at most 2*B and dividing by at least 2 restores the bound.
    B.SetInsertPoint(&Div);
    B.SetCurrentDebugLocation(Div.getDebugLoc());
    Value *Dividend = B.CreateTrunc(Sum, B.getInt32Ty());
    Value *Quotient = B.CreateUDiv(Dividend, B.getInt32(Divisor->getZExtValue()), "carry.quotient");
    Div.replaceAllUsesWith(B.CreateZExt(Quotient, Div.getType()));
    Div.eraseFromParent();
    return true;
  }
  return false;
}
} // namespace

static unsigned elisacoreNarrowBoundedCarries(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.size() < 3 || F.size() > 64 || BDU_SkipFunction(F, M) ||
        F.getInstructionCount() > 512)
      continue;
    SmallVector<BinaryOperator *, 8> Candidates;
    for (Instruction &I : instructions(F)) {
      auto *Op = dyn_cast<BinaryOperator>(&I);
      if (!Op || (Op->getOpcode() != Instruction::SDiv && Op->getOpcode() != Instruction::UDiv) ||
          !Op->getType()->isIntegerTy(64) || !isa<ConstantInt>(Op->getOperand(1)) ||
          Op->hasMetadataOtherThanDebugLoc())
        continue;
      auto *Sum = dyn_cast<BinaryOperator>(Op->getOperand(0));
      if (Sum && Sum->getOpcode() == Instruction::Add &&
          (isa<PHINode>(Sum->getOperand(0)) || isa<PHINode>(Sum->getOperand(1))))
        Candidates.push_back(Op);
    }
    if (Candidates.empty() || Candidates.size() > 16) continue;
    DominatorTree DT(F);
    LoopInfo LI(DT);
    TargetLibraryInfoImpl TLII(M.getTargetTriple());
    TargetLibraryInfo TLI(TLII);
    AssumptionCache AC(F);
    ScalarEvolution SE(F, TLI, AC, DT, LI);
    for (auto *Div : Candidates)
      if (narrowBoundedCarry(*Div, LI, SE)) {
        ++Changed;
        break;
      }
  }
  return Changed;
}
