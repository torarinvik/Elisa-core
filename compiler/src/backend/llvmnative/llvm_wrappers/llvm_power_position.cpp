// Recover the exponent of the greatest power of two not exceeding a positive
// input. Recognize both the doubling loop and the fully unrolled search.
namespace {
static bool powerPositionConstant(Value *V, uint64_t Expected) {
  auto *C = dyn_cast<ConstantInt>(V);
  return C && C->equalsInt(Expected);
}

static Value *powerPosition(IRBuilder<> &B, Value *Input, Type *ResultType) {
  auto *Decl = Intrinsic::getOrInsertDeclaration(B.GetInsertBlock()->getModule(),
                                                  Intrinsic::ctlz, {Input->getType()});
  Value *Zeros = B.CreateCall(Decl, {Input, B.getTrue()});
  return B.CreateTrunc(B.CreateSub(B.getInt64(63), Zeros), ResultType, "power.position");
}

static bool foldPowerPositionLoop(BasicBlock &Body) {
  auto *Back = dyn_cast<CondBrInst>(Body.getTerminator());
  auto *Done = Back ? dyn_cast<ICmpInst>(Back->getCondition()) : nullptr;
  if (Body.size() != 6 || Body.hasAddressTaken() || !Done ||
      Done->getPredicate() != ICmpInst::ICMP_SGT || Back->getSuccessor(1) != &Body ||
      Back->getMetadata(LLVMContext::MD_loop))
    return false;
  BasicBlock *Exit = Back->getSuccessor(0);
  if (Exit == &Body || Exit->getSinglePredecessor() != &Body || !Exit->phis().empty())
    return false;

  auto *Doubled = dyn_cast<BinaryOperator>(Done->getOperand(0));
  auto *Limit = dyn_cast<BinaryOperator>(Done->getOperand(1));
  if (!Doubled || Doubled->getParent() != &Body || Doubled->getOpcode() != Instruction::Shl ||
      !powerPositionConstant(Doubled->getOperand(1), 1) || !Doubled->getType()->isIntegerTy(64) ||
      !Limit || Limit->getOpcode() != Instruction::SDiv || Limit->isExact() ||
      !powerPositionConstant(Limit->getOperand(1), 2))
    return false;
  auto *Power = dyn_cast<PHINode>(Doubled->getOperand(0));
  if (!Power || Power->getParent() != &Body || Power->getNumIncomingValues() != 2 ||
      Power->getIncomingValueForBlock(&Body) != Doubled)
    return false;
  BasicBlock *Entry = Power->getIncomingBlock(Power->getIncomingBlock(0) == &Body ? 1 : 0);
  if (Entry == Exit || !powerPositionConstant(Power->getIncomingValueForBlock(Entry), 1))
    return false;
  auto *Enter = dyn_cast<CondBrInst>(Entry->getTerminator());
  auto *Small = Enter ? dyn_cast<ICmpInst>(Enter->getCondition()) : nullptr;
  if (!Small || Small->getPredicate() != ICmpInst::ICMP_SLT ||
      Small->getOperand(0) != Limit->getOperand(0) || !powerPositionConstant(Small->getOperand(1), 2) ||
      Enter->getSuccessor(1) != &Body || Enter->getSuccessor(0) == &Body ||
      Enter->getSuccessor(0) == Exit)
    return false;

  auto *Position = dyn_cast<PHINode>(Power->getNextNode());
  if (!Position) Position = dyn_cast<PHINode>(Body.begin());
  if (!Position || Position == Power || !Position->getType()->isIntegerTy(32) ||
      Position->getNumIncomingValues() != 2 || !powerPositionConstant(Position->getIncomingValueForBlock(Entry), 0))
    return false;
  auto *Next = dyn_cast<BinaryOperator>(Position->getIncomingValueForBlock(&Body));
  if (!Next || Next->getParent() != &Body || Next->getOpcode() != Instruction::Add ||
      Next->getOperand(0) != Position || !powerPositionConstant(Next->getOperand(1), 1))
    return false;

  for (Instruction &I : Body) {
    if (&I != Power && &I != Position && &I != Doubled && &I != Next && &I != Done && &I != Back)
      return false;
    for (User *U : I.users()) {
      auto *Use = dyn_cast<Instruction>(U);
      if (Use && Use->getParent() == &Body) continue;
      if (!Use || Use->getParent() != Exit || (&I != Position && &I != Next))
        return false;
    }
  }

  // The signed half-limit is less than 2^62. Starting at one, the first
  // doubling that exceeds it is representable, and the loop executes at
  // most 62 times. The counter after that doubling is floor(log2(input)).
  IRBuilder<> B(Enter);
  Value *Input = B.CreateFreeze(Small->getOperand(0));
  Value *InRange = B.CreateICmpSLT(Input, B.getInt64(2));
  Enter->setCondition(InRange);
  Value *Index = powerPosition(B, Input, Position->getType());
  Value *Previous = B.CreateSub(Index, B.getInt32(1));
  auto ReplaceExitUses = [Exit](Value *Old, Value *New) {
    for (Use &U : make_early_inc_range(Old->uses()))
      if (cast<Instruction>(U.getUser())->getParent() == Exit) U.set(New);
  };
  ReplaceExitUses(Next, Index);
  ReplaceExitUses(Position, Previous);
  Enter->setSuccessor(1, Exit);
  Body.dropAllReferences();
  Body.eraseFromParent();
  return true;
}

// The header tests the current power before doubling. The half-limit ensures
// every executed doubling fits, including for the largest unsigned input.
static bool foldHighestPowerOfTwoLoop(BasicBlock &Body) {
  auto *Back = dyn_cast<CondBrInst>(Body.getTerminator());
  auto *Done = Back ? dyn_cast<ICmpInst>(Back->getCondition()) : nullptr;
  if (Body.size() != 4 || Body.hasAddressTaken() || !Done ||
      Done->getPredicate() != ICmpInst::ICMP_UGT || Back->getSuccessor(1) != &Body ||
      Back->getMetadata(LLVMContext::MD_loop))
    return false;
  BasicBlock *Exit = Back->getSuccessor(0);
  auto *Power = dyn_cast<PHINode>(Done->getOperand(0));
  auto *Limit = dyn_cast<BinaryOperator>(Done->getOperand(1));
  if (Exit == &Body || !Power || Power->getParent() != &Body ||
      !Power->getType()->isIntegerTy(64) || Power->getNumIncomingValues() != 2 ||
      !Limit || Limit->getOpcode() != Instruction::LShr || Limit->isExact() ||
      !powerPositionConstant(Limit->getOperand(1), 1))
    return false;
  auto *Doubled = dyn_cast_or_null<BinaryOperator>(Power->getIncomingValueForBlock(&Body));
  if (!Doubled || Doubled->getParent() != &Body || Doubled->getOpcode() != Instruction::Shl ||
      Doubled->getOperand(0) != Power || !powerPositionConstant(Doubled->getOperand(1), 1))
    return false;
  BasicBlock *Entry = Power->getIncomingBlock(Power->getIncomingBlock(0) == &Body ? 1 : 0);
  auto *Enter = dyn_cast<UncondBrInst>(Entry->getTerminator());
  if (Entry == Exit || !Enter || Enter->getSuccessor() != &Body ||
      !powerPositionConstant(Power->getIncomingValueForBlock(Entry), 1))
    return false;
  for (Instruction &I : Body) {
    for (Use &U : I.uses()) {
      auto *UseInst = dyn_cast<Instruction>(U.getUser());
      if (!UseInst) return false;
      if (UseInst->getParent() == &Body) continue;
      auto *Phi = dyn_cast<PHINode>(UseInst);
      if (&I != Power || !Phi || Phi->getParent() != Exit || Phi->getIncomingBlock(U) != &Body)
        return false;
    }
  }

  IRBuilder<> B(Enter);
  Value *Input = B.CreateFreeze(Limit->getOperand(0));
  // The loop returns one for zero as well as one. Setting the low bit
  // preserves every other highest set bit and makes ctlz defined.
  Value *Nonzero = B.CreateOr(Input, B.getInt64(1));
  Value *Zeros = B.CreateIntrinsic(Intrinsic::ctlz, {Input->getType()}, {Nonzero, B.getTrue()});
  Value *Highest = B.CreateLShr(B.getInt64(uint64_t{1} << 63), Zeros, "highest.power.of.two");
  for (PHINode &Phi : Exit->phis()) {
    int Index = Phi.getBasicBlockIndex(&Body);
    if (Phi.getIncomingValue(Index) == Power) Phi.setIncomingValue(Index, Highest);
    Phi.setIncomingBlock(Index, Entry);
  }
  Enter->setSuccessor(0, Exit);
  Body.dropAllReferences();
  Body.eraseFromParent();
  return true;
}

static bool foldPowerPositionChain(BasicBlock &Entry) {
  auto *Enter = dyn_cast<CondBrInst>(Entry.getTerminator());
  auto *Small = Enter ? dyn_cast<ICmpInst>(Enter->getCondition()) : nullptr;
  if (!Small || Small->getPredicate() != ICmpInst::ICMP_SLT ||
      !Small->getOperand(0)->getType()->isIntegerTy(64) ||
      !powerPositionConstant(Small->getOperand(1), 2))
    return false;
  Value *Input = Small->getOperand(0);
  BasicBlock *Current = Enter->getSuccessor(1);
  auto *First = dyn_cast<CondBrInst>(Current->getTerminator());
  if (!First) return false;
  BasicBlock *Join = First->getSuccessor(0);
  if (Join == Enter->getSuccessor(0) || Join->phis().empty() ||
      !isa<PHINode>(Join->front()) || isa<PHINode>(Join->front().getNextNode()))
    return false;
  auto *Position = cast<PHINode>(&Join->front());
  if (!Position->getType()->isIntegerTy(32) || Position->getNumIncomingValues() != 61)
    return false;

  SmallVector<BasicBlock *, 64> Chain;
  BasicBlock *Previous = &Entry;
  for (unsigned Exponent = 1; Exponent <= 61; ++Exponent) {
    if (Current == Join || Current->getSinglePredecessor() != Previous ||
        Current->hasAddressTaken())
      return false;
    auto *Test = dyn_cast<ICmpInst>(&Current->front());
    if (!Test || Test->getPredicate() != ICmpInst::ICMP_ULT ||
        Test->getOperand(0) != Input ||
        !powerPositionConstant(Test->getOperand(1), uint64_t{1} << (Exponent + 1)) ||
        !Test->hasOneUse())
      return false;
    int Incoming = Position->getBasicBlockIndex(Current);
    if (Incoming < 0) return false;
    Value *Selected = Position->getIncomingValue(Incoming);
    Chain.push_back(Current);
    if (Exponent == 61) {
      auto *Last = dyn_cast<SelectInst>(Selected);
      auto *Jump = dyn_cast<UncondBrInst>(Current->getTerminator());
      if (Current->size() != 3 || !Last || Last->getParent() != Current ||
          !Last->hasOneUse() || Last->getCondition() != Test || !Jump || Jump->getSuccessor() != Join ||
          !powerPositionConstant(Last->getTrueValue(), 61) ||
          !powerPositionConstant(Last->getFalseValue(), 62))
        return false;
    } else {
      auto *Branch = dyn_cast<CondBrInst>(Current->getTerminator());
      if (Current->size() != 2 || !Branch || Branch->getCondition() != Test ||
          Branch->getSuccessor(0) != Join || Branch->getMetadata(LLVMContext::MD_loop) ||
          !powerPositionConstant(Selected, Exponent))
        return false;
      Previous = Current;
      Current = Branch->getSuccessor(1);
    }
  }

  IRBuilder<> B(Enter);
  Value *Stable = B.CreateFreeze(Input);
  Enter->setCondition(B.CreateICmpSLT(Stable, B.getInt64(2)));
  // Preserve the first comparison so inputs 2 and 3 keep the constant path.
  cast<ICmpInst>(First->getCondition())->setOperand(0, Stable);
  auto *Compute = BasicBlock::Create(Entry.getContext(), "power.position.compute",
                                     Entry.getParent(), Join);
  B.SetInsertPoint(Compute);
  Position->addIncoming(powerPosition(B, Stable, Position->getType()), Compute);
  B.CreateBr(Join);
  First->setSuccessor(1, Compute);
  DeleteDeadBlocks(ArrayRef(Chain).drop_front());
  return true;
}
} // namespace

static unsigned elisacoreFoldPowerPosition(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (BDU_SkipFunction(F, M) || F.size() > 128) continue;
    for (BasicBlock &B : F) {
      if (!foldHighestPowerOfTwoLoop(B) && !foldPowerPositionLoop(B) &&
          !foldPowerPositionChain(B)) continue;
      ++Changed;
      break;
    }
  }
  return Changed;
}
