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

// Recover an immediate subtraction from InstCombine's wrapped range check.
// Rotating the interval turns (x + bias) < limit into
// (x + bias - limit) > ~limit, with all arithmetic modulo 2^64.
static unsigned preferImmediateRangeBias(Module &M) {
  if (M.getNamedMetadata("llvm.dbg.cu")) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.hasOptNone() || F.hasOptSize() || F.hasMinSize())
      continue;
    for (BasicBlock &Block : F) {
      auto *Branch = dyn_cast_or_null<CondBrInst>(Block.getTerminator());
      if (!Branch) continue;
      auto *Guard = dyn_cast<SelectInst>(Branch->getCondition());
      auto *Reject = Guard ? dyn_cast<ConstantInt>(Guard->getTrueValue()) : nullptr;
      SmallVector<Value *, 2> Conditions;
      if (Reject && Reject->isOne() && Guard->hasOneUse()) {
        Conditions.push_back(Guard->getCondition());
        Conditions.push_back(Guard->getFalseValue());
      } else {
        Conditions.push_back(Branch->getCondition());
      }
      for (Value *Condition : Conditions) {
        auto *Compare = dyn_cast<ICmpInst>(Condition);
        if (!Compare || !Compare->hasOneUse() || Compare->hasSameSign() ||
            Compare->getPredicate() != ICmpInst::ICMP_ULT ||
            !Compare->getOperand(0)->getType()->isIntegerTy(64))
          continue;
        auto *Add = dyn_cast<BinaryOperator>(Compare->getOperand(0));
        auto *Limit = dyn_cast<ConstantInt>(Compare->getOperand(1));
        if (!Add || !Limit || !Limit->isNegative() ||
            Add->getOpcode() != Instruction::Add || !Add->hasOneUse() ||
            Add->hasNoUnsignedWrap() || Add->hasNoSignedWrap())
          continue;
        auto *Bias = dyn_cast<ConstantInt>(Add->getOperand(1));
        if (!Bias || !Bias->isNegative()) continue;
        APInt Lower = Limit->getValue() - Bias->getValue();
        APInt OldSubtract = -Bias->getValue();
        APInt NewLimit = ~Limit->getValue();
        // The old subtraction needs a register. The replacement fits the
        // unshifted 12-bit immediate, and the new limit is at most 32 bits.
        if (Lower.isZero() || Lower.ugt(4095) ||
            OldSubtract.ule(4095) ||
            (OldSubtract.isIntN(24) && OldSubtract.getLoBits(12).isZero()) ||
            NewLimit.getActiveBits() > 32)
          continue;
        Add->setOperand(1, ConstantInt::get(M.getContext(), -Lower));
        Compare->setPredicate(ICmpInst::ICMP_UGT);
        Compare->setOperand(1, ConstantInt::get(M.getContext(), NewLimit));
        ++Changed;
      }
    }
  }
  return Changed;
}

// Tune CSSC lowering and map the unsigned range-check idiom for [-B, B] to llvm.abs plus an
// unsigned comparison when the selected AArch64 CPU has CSSC. INT_MIN remains
// the same wrapped bit pattern and is still rejected by the unsigned compare.
extern "C" unsigned elisacoreCsscAbsoluteRangeChecks(
    LLVMModuleRef moduleRef, LLVMTargetMachineRef targetRef) {
  if (!moduleRef || !targetRef) return 0;
  auto &TM = *reinterpret_cast<TargetMachine *>(targetRef);
  if (!TM.getTargetTriple().isAArch64()) return 0;
  Module &M = *unwrap(moduleRef);
  unsigned Changed = preferImmediateRangeBias(M);
  if (!hasCSSC(TM)) return Changed;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.hasFnAttribute("target-cpu") || F.hasFnAttribute("target-features")) continue;
    DominatorTree DT(F); LoopInfo LI(DT);
    if (preferCompareSelectExtrema(F, LI, TM)) {
      useCompareSelectExtrema(F, TM);
      ++Changed;
      continue;
    }
    DenseMap<Loop *, unsigned> ScanCounts;
    SmallVector<ICmpInst *, 8> Candidates;
    for (BasicBlock &BB : F)
      for (Instruction &I : BB)
        if (auto *Cmp = dyn_cast<ICmpInst>(&I)) Candidates.push_back(Cmp);
    for (ICmpInst *Cmp : Candidates) {
      // Wide unrolled validation scans favor the original range arithmetic on
      // measured AArch64 targets. Keep the absolute-value fold for other loops.
      if (Loop *L = LI.getLoopFor(Cmp->getParent())) {
        auto It = ScanCounts.find(L);
        if (It == ScanCounts.end()) {
          unsigned RangeChecks = 0;
          for (BasicBlock *B : L->blocks()) for (Instruction &I : *B)
            if (auto *C = dyn_cast<ICmpInst>(&I)) {
              BinaryOperator *Add = nullptr; Value *Input = nullptr; APInt Bound(64, 0);
              if (matchSymmetricRangeCheck(*C, Add, Input, Bound)) ++RangeChecks;
            }
          It = ScanCounts.try_emplace(L, RangeChecks).first;
        }
        if (It->second >= 4) continue;
      }
      BinaryOperator *Add = nullptr;
      Value *Input = nullptr;
      APInt Bound(64, 0);
      if (!matchSymmetricRangeCheck(*Cmp, Add, Input, Bound)) continue;
      IRBuilder<> Builder(Cmp);
      Builder.SetCurrentDebugLocation(Cmp->getDebugLoc());
      auto *AbsFn = Intrinsic::getOrInsertDeclaration(&M, Intrinsic::abs,
                                                      {Input->getType()});
      Value *Abs = Builder.CreateCall(AbsFn,
          {Input, ConstantInt::getFalse(M.getContext())}, "range.abs");
      Value *Invalid = Builder.CreateICmpUGT(Abs,
          ConstantInt::get(cast<IntegerType>(Input->getType()), Bound), "range.invalid");
      Cmp->replaceAllUsesWith(Invalid);
      Cmp->eraseFromParent();
      if (Add->use_empty()) Add->eraseFromParent();
      ++Changed;
    }
  }
  return Changed;
}
