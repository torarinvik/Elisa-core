// Match a running maximum updated on one arm and passed through on the
// other. Its updated value must also feed max(previous, input) - input.
static IntrinsicInst *conditionalMaximumDifference(PHINode &Previous,
                                                   BasicBlock *Latch) {
  auto *Merge = dyn_cast<PHINode>(Previous.getIncomingValueForBlock(Latch));
  if (!Merge || Merge->getParent() != Latch || Merge->getNumIncomingValues() != 2)
    return nullptr;
  unsigned Unchanged = Merge->getIncomingValue(0) == &Previous ? 0 : 1;
  if (Merge->getIncomingValue(Unchanged) != &Previous) return nullptr;
  auto *Maximum = dyn_cast<IntrinsicInst>(Merge->getIncomingValue(1 - Unchanged));
  if (!Maximum || Maximum->getIntrinsicID() != Intrinsic::umax ||
      Maximum->getParent() != Merge->getIncomingBlock(1 - Unchanged))
    return nullptr;
  unsigned Prior = Maximum->getArgOperand(0) == &Previous ? 0 : 1;
  if (Maximum->getArgOperand(Prior) != &Previous) return nullptr;
  Value *Input = Maximum->getArgOperand(1 - Prior);
  for (User *Use : Maximum->users())
    if (auto *Sub = dyn_cast<BinaryOperator>(Use);
        Sub && Sub->getOpcode() == Instruction::Sub &&
        Sub->getParent() == Maximum->getParent() &&
        Sub->getOperand(0) == Maximum && Sub->getOperand(1) == Input)
      return Maximum;
  return nullptr;
}

// On Apple M5, compare-and-select is faster on the dependency chains of
// running extrema that feed a clamp or paired conditional differences.
static bool preferCompareSelectExtrema(Function &F, LoopInfo &LI,
                                       const TargetMachine &TM) {
  if (!TM.getTargetTriple().isMacOSX() || TM.getTargetCPU() != "apple-m5")
    return false;
  for (BasicBlock &BB : F)
    for (Instruction &I : BB)
      if (auto *Call = dyn_cast<CallBase>(&I); Call && Call->isInlineAsm())
        return false;
  for (Loop *L : LI.getTopLevelLoops()) {
    BasicBlock *Latch = L->getLoopLatch();
    if (!Latch) continue;
    BasicBlock *FirstConditionalArm = nullptr;
    for (PHINode &Phi : L->getHeader()->phis()) {
      if (!Phi.getType()->isIntegerTy(32)) continue;
      if (auto *Maximum = conditionalMaximumDifference(Phi, Latch);
          Maximum && L->contains(Maximum->getParent())) {
        BasicBlock *Arm = Maximum->getParent();
        if (FirstConditionalArm && FirstConditionalArm != Arm) return true;
        FirstConditionalArm = Arm;
      }
      auto *Running = dyn_cast<IntrinsicInst>(Phi.getIncomingValueForBlock(Latch));
      if (!Running || !L->contains(Running->getParent()))
        continue;
      Intrinsic::ID ClampID;
      switch (Running->getIntrinsicID()) {
      case Intrinsic::umax: ClampID = Intrinsic::umin; break;
      case Intrinsic::umin: ClampID = Intrinsic::umax; break;
      default: continue;
      }
      if (Running->getArgOperand(0) != &Phi && Running->getArgOperand(1) != &Phi)
        continue;
      for (User *Use : Running->users())
        if (auto *Clamp = dyn_cast<IntrinsicInst>(Use);
            Clamp && Clamp->getIntrinsicID() == ClampID &&
            Clamp->getParent() == Running->getParent())
          return true;
    }
  }
  return false;
}

static void useCompareSelectExtrema(Function &F, const TargetMachine &TM) {
  SmallVector<StringRef, 32> Features;
  TM.getTargetFeatureString().split(Features, ',', -1, false);
  std::string Selected;
  for (StringRef Feature : Features) {
    if (Feature == "+cssc" || Feature == "-cssc") continue;
    Selected += Feature;
    Selected += ',';
  }
  Selected += "-cssc";
  F.addFnAttr("target-features", Selected);
}
