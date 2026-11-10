// A unit counter bounds the number of products in a checked accumulation.
#if LLVM_VERSION_MAJOR >= 23
namespace {
static ConstantRange checkedPowerGuardRange(Value *V, BasicBlock *Use,
                                            DominatorTree &DT) {
  unsigned Width = V->getType()->getIntegerBitWidth();
  auto Range = ConstantRange::getFull(Width);
  for (auto *Node = DT.getNode(Use)->getIDom(); Node; Node = Node->getIDom()) {
    BasicBlock *Block = Node->getBlock();
    auto *Br = dyn_cast<CondBrInst>(Block->getTerminator());
    if (!Br) continue;
    for (unsigned Edge : {0u, 1u}) {
      if (!DT.dominates(BasicBlockEdge(Block, Br->getSuccessor(Edge)), Use))
        continue;
      Value *Condition = Br->getCondition();
      SmallVector<Value *, 2> Terms{Condition};
      if (Edge == 1) {
        if (auto *Or = dyn_cast<BinaryOperator>(Condition);
            Or && Or->getOpcode() == Instruction::Or)
          Terms = {Or->getOperand(0), Or->getOperand(1)};
        if (auto *Select = dyn_cast<SelectInst>(Condition))
          if (auto *True = dyn_cast<ConstantInt>(Select->getTrueValue()); True && True->isOne())
            Terms = {Select->getCondition(), Select->getFalseValue()};
      }
      for (Value *Term : Terms) {
        auto *Compare = dyn_cast<ICmpInst>(Term);
        auto *Bound = Compare ? dyn_cast<ConstantInt>(Compare->getOperand(1)) : nullptr;
        if (!Bound || Compare->getOperand(0) != V) continue;
        auto Pred = Edge == 0 ? Compare->getPredicate() : Compare->getInversePredicate();
        Range = Range.intersectWith(ConstantRange::makeExactICmpRegion(Pred, Bound->getValue()));
      }
    }
  }
  return Range;
}

static Value *checkedPowerTripLimit(Loop &L, BasicBlock *Pre, BasicBlock *Latch) {
  auto *Branch = dyn_cast<CondBrInst>(Latch->getTerminator());
  auto *Compare = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  if (!Compare || !Compare->isEquality()) return nullptr;
  unsigned Exit = Compare->getPredicate() == ICmpInst::ICMP_EQ ? 0 : 1;
  if (L.contains(Branch->getSuccessor(Exit)) ||
      Branch->getSuccessor(1 - Exit) != L.getHeader())
    return nullptr;
  for (PHINode &P : L.getHeader()->phis()) {
    if (!P.getType()->isIntegerTy(64) || P.getNumIncomingValues() != 2) continue;
    auto *Start = dyn_cast<ConstantInt>(P.getIncomingValueForBlock(Pre));
    auto *Next = dyn_cast<BinaryOperator>(P.getIncomingValueForBlock(Latch));
    if (!Start || !Start->isZero() || !Next || Next->getOpcode() != Instruction::Add ||
        Next->getOperand(0) != &P) continue;
    auto *One = dyn_cast<ConstantInt>(Next->getOperand(1));
    if (!One || !One->isOne()) continue;
    Value *Limit = Compare->getOperand(0) == Next ? Compare->getOperand(1) :
                   Compare->getOperand(1) == Next ? Compare->getOperand(0) : nullptr;
    if (Limit && L.isLoopInvariant(Limit)) return Limit;
  }
  return nullptr;
}

static bool foldCheckedPowers(Function &F) {
  DominatorTree DT(F);
  LoopInfo LI(DT);
  AssumptionCache AC(F);
  bool Changed = false;
  for (Loop *L : LI.getLoopsInPreorder()) {
    BasicBlock *Pre = L->getLoopPredecessor(), *Latch = L->getLoopLatch();
    if (!Pre || !Latch || !L->getSubLoops().empty()) continue;
    Value *Limit = checkedPowerTripLimit(*L, Pre, Latch);
    if (!Limit) continue;
    auto *At = L->getHeader()->getTerminator();
    bool Defined = isGuaranteedNotToBeUndefOrPoison(Limit, &AC, At, &DT);
    if (!Defined && !isa<Argument>(Limit)) continue;
    auto Trips = checkedPowerGuardRange(Limit, L->getHeader(), DT);
    if (Trips.isEmptySet() || Trips.getUnsignedMin().isZero() ||
        Trips.getUnsignedMax().ugt(16)) continue;

    bool LoopChanged = false;
    for (PHINode &Product : L->getHeader()->phis()) {
      if (!Product.getType()->isIntegerTy(64) || Product.getNumIncomingValues() != 2) continue;
      auto *Start = dyn_cast<ConstantInt>(Product.getIncomingValueForBlock(Pre));
      auto *Mul = dyn_cast<BinaryOperator>(Product.getIncomingValueForBlock(Latch));
      if (!Start || Start->isNegative() || !Mul || Mul->getOpcode() != Instruction::Mul ||
          !L->contains(Mul) || Mul->hasNoSignedWrap()) continue;
      Value *Base = Mul->getOperand(0) == &Product ? Mul->getOperand(1) :
                    Mul->getOperand(1) == &Product ? Mul->getOperand(0) : nullptr;
      if (!Base || !L->isLoopInvariant(Base) ||
          !isGuaranteedNotToBeUndefOrPoison(Base, &AC, Mul, &DT)) continue;
      auto Bases = checkedPowerGuardRange(Base, L->getHeader(), DT);
      if (Bases.isEmptySet() || !Bases.isAllNonNegative()) continue;
      APInt Max = Start->getValue().zext(128);
      APInt Factor = Bases.getUnsignedMax().zext(128);
      for (unsigned I = 0; I < Trips.getUnsignedMax().getZExtValue() && Max.isSignedIntN(64); ++I)
        Max *= Factor;
      if (!Max.isSignedIntN(64)) continue;

      SmallVector<IntrinsicInst *, 4> Checks;
      unsigned SlowKind = F.getContext().getMDKindID("elisa.checkedmul.slow");
      for (User *U : Product.users()) {
        auto *C = dyn_cast<IntrinsicInst>(U);
        if (!C || C->getIntrinsicID() != Intrinsic::smul_with_overflow ||
            !C->getMetadata(SlowKind) || C->hasOperandBundles() || C->isConvergent() ||
            C->isUsedByMetadata() ||
            !L->contains(C) || !DT.dominates(Mul, C)) continue;
        if ((C->getArgOperand(0) == &Product && C->getArgOperand(1) == Base) ||
            (C->getArgOperand(1) == &Product && C->getArgOperand(0) == Base))
          Checks.push_back(C);
      }
      if (Checks.empty()) continue;
      Mul->setHasNoSignedWrap(true);
      for (auto *C : Checks) {
        IRBuilder<> B(C);
        Value *Pair = B.CreateInsertValue(PoisonValue::get(C->getType()), Mul, 0);
        C->replaceAllUsesWith(B.CreateInsertValue(Pair, B.getFalse(), 1));
        C->eraseFromParent();
      }
      LoopChanged = true;
    }
    if (LoopChanged && !Defined) {
      // The entry checks and the latch must observe the same input value.
      IRBuilder<> B(&*F.getEntryBlock().getFirstInsertionPt());
      Value *Frozen = B.CreateFreeze(Limit, "checkedpower.limit");
      Limit->replaceUsesWithIf(Frozen, [Frozen](Use &U) { return U.getUser() != Frozen; });
    }
    Changed |= LoopChanged;
  }
  return Changed;
}

static BasicBlock *findShortPowerLoop(Function &F) {
  if (F.size() < 3 || F.size() > 8 || F.getInstructionCount() > 64) return nullptr;
  for (BasicBlock &Header : F) {
    auto *Branch = dyn_cast<CondBrInst>(Header.getTerminator());
    if (!Branch || (Branch->getSuccessor(0) != &Header &&
                    Branch->getSuccessor(1) != &Header) ||
        Header.size() > 8) continue;
    DominatorTree DT(F);
    LoopInfo LI(DT);
    Loop *L = LI.getLoopFor(&Header);
    if (!L || L->getHeader() != &Header || L->getNumBlocks() != 1 || L->getLoopID())
      continue;
    BasicBlock *Pre = L->getLoopPredecessor();
    Value *Limit = Pre ? checkedPowerTripLimit(*L, Pre, &Header) : nullptr;
    if (!Limit) continue;
    auto Trips = checkedPowerGuardRange(Limit, &Header, DT);
    if (Trips.isEmptySet() || Trips.getUnsignedMin().isZero() ||
        Trips.getUnsignedMax().ugt(8)) continue;
    for (PHINode &P : Header.phis()) {
      auto *Mul = dyn_cast<BinaryOperator>(P.getIncomingValueForBlock(&Header));
      if (!Mul || Mul->getOpcode() != Instruction::Mul) continue;
      Value *Base = Mul->getOperand(0) == &P ? Mul->getOperand(1) :
                    Mul->getOperand(1) == &P ? Mul->getOperand(0) : nullptr;
      if (Base && L->isLoopInvariant(Base) && llvm::all_of(Header, [](Instruction &I) {
            return I.isTerminator() || !I.mayHaveSideEffects();
          }))
        return &Header;
    }
  }
  return nullptr;
}

static bool unrollShortPowerLoop(Function &F) {
  BasicBlock *Header = findShortPowerLoop(F);
  if (!Header) return false;
  PassBuilder PB;
  FunctionAnalysisManager FAM;
  PB.registerFunctionAnalyses(FAM);
  auto &DT = FAM.getResult<DominatorTreeAnalysis>(F);
  auto &LI = FAM.getResult<LoopAnalysis>(F);
  auto &SE = FAM.getResult<ScalarEvolutionAnalysis>(F);
  auto &AC = FAM.getResult<AssumptionAnalysis>(F);
  auto &TTI = FAM.getResult<TargetIRAnalysis>(F);
  auto &ORE = FAM.getResult<OptimizationRemarkEmitterAnalysis>(F);
  Loop *L = LI.getLoopFor(Header);
  unsigned Trips = SE.getSmallConstantMaxTripCount(L);
  if (Trips < 2 || Trips > 8) return false;
  bool Changed = simplifyLoop(L, &DT, &LI, &SE, &AC, nullptr, false);
  if (!L->isLoopSimplifyForm()) return Changed;
  Changed |= formLCSSA(*L, DT, &LI, &SE);
  UnrollLoopOptions Options{Trips, true, false, false, false, false, nullptr, 1000};
  return UnrollLoop(L, Options, &LI, &SE, &DT, &AC, &TTI, &ORE, true) !=
             LoopUnrollResult::Unmodified || Changed;
}
} // namespace
#endif
