
// A zero-seeded ring index stays below its invariant positive limit when
// its backedge increments by one and resets on reaching that limit.
namespace {
static bool ringIndexConstant(Value *V, uint64_t Expected) {
  auto *C = dyn_cast<ConstantInt>(V);
  return C && C->equalsInt(Expected);
}
static bool proveRingIndexBound(ICmpInst &Check, PHINode &Index,
                                const ConstantInt &Capacity,
                                FunctionAnalysisManager &FAM) {
  if (Index.getNumIncomingValues() != 2 || Index.getParent() != Check.getParent() ||
      Capacity.isZero())
    return false;
  unsigned Seed = 0;
  if (!ringIndexConstant(Index.getIncomingValue(Seed), 0)) ++Seed;
  if (!ringIndexConstant(Index.getIncomingValue(Seed), 0)) return false;
  auto *Wrap = dyn_cast<SelectInst>(Index.getIncomingValue(1 - Seed));
  auto *Test = Wrap ? dyn_cast<ICmpInst>(Wrap->getCondition()) : nullptr;
  auto *Next = Wrap ? dyn_cast<BinaryOperator>(Wrap->getFalseValue()) : nullptr;
  if (!Test || Test->getPredicate() != ICmpInst::ICMP_EQ ||
      !ringIndexConstant(Wrap->getTrueValue(), 0) || !Next ||
      Next->getOpcode() != Instruction::Add ||
      !((Next->getOperand(0) == &Index && ringIndexConstant(Next->getOperand(1), 1)) ||
        (Next->getOperand(1) == &Index && ringIndexConstant(Next->getOperand(0), 1))))
    return false;
  Value *Limit = Test->getOperand(0);
  if (Limit == Next) Limit = Test->getOperand(1);
  else if (Test->getOperand(1) != Next) return false;

  Function &F = *Check.getFunction();
  auto &LI = FAM.getResult<LoopAnalysis>(F);
  Loop *L = LI.getLoopFor(Index.getParent());
  BasicBlock *Entry = Index.getIncomingBlock(Seed);
  if (!L || L->getHeader() != Index.getParent() ||
      L->getLoopPredecessor() != Entry ||
      L->getLoopLatch() != Index.getIncomingBlock(1 - Seed) ||
      !L->isLoopInvariant(Limit))
    return false;
  auto &DT = FAM.getResult<DominatorTreeAnalysis>(F);
  Instruction *EntryTerminator = Entry->getTerminator();
  if (auto *Def = dyn_cast<Instruction>(Limit))
    if (!DT.dominates(Def, EntryTerminator)) return false;
  if (!isGuaranteedNotToBeUndefOrPoison(Limit, nullptr, EntryTerminator, &DT))
    return false;

  auto &LVI = FAM.getResult<LazyValueAnalysis>(F);
  auto Range = LVI.getConstantRange(Limit, EntryTerminator, false);
  return !Range.isEmptySet() && !Range.contains(APInt(Range.getBitWidth(), 0)) &&
         Range.getUnsignedMax().ule(Capacity.getValue());
}
} // namespace

static unsigned elisacoreFoldRingIndexBounds(Module &M) {
  PassBuilder PB;
  LoopAnalysisManager LAM;
  FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM;
  ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM);
  PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM);
  PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
  unsigned Changed = 0;
  for (Function &F : M) {
    if (BDU_SkipFunction(F, M)) continue;
    SmallVector<ICmpInst *, 4> Proven;
    for (BasicBlock &BB : F) {
      auto *Branch = dyn_cast<CondBrInst>(BB.getTerminator());
      auto *Check = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
      if (!Check || Check->getPredicate() != ICmpInst::ICMP_ULT) continue;
      auto *Index = dyn_cast<PHINode>(Check->getOperand(0));
      auto *Capacity = dyn_cast<ConstantInt>(Check->getOperand(1));
      if (Index && Capacity && proveRingIndexBound(*Check, *Index, *Capacity, FAM))
        Proven.push_back(Check);
    }
    for (ICmpInst *Check : Proven)
      Check->replaceAllUsesWith(ConstantInt::getTrue(M.getContext()));
    Changed += Proven.size();
  }
  return Changed;
}
