// Recover integer ranges from guards that dominate an instruction.
namespace ElisaDominatingIntegerBounds {
using Facts = DenseMap<Value *, ConstantRange>;

// Walk backwards from the definition. A forward search from the accepted
// edge would traverse the entire optimized loop nest.
static bool mayReexecute(BasicBlock &Definition, BasicBlock &EdgeTarget) {
  SmallVector<BasicBlock *, 16> Pending{&Definition};
  SmallPtrSet<BasicBlock *, 32> Visited;
  while (!Pending.empty()) {
    BasicBlock *Block = Pending.pop_back_val();
    if (!Visited.insert(Block).second) continue;
    if (Block == &EdgeTarget || Visited.size() > 256) return true;
    for (BasicBlock *Predecessor : predecessors(Block))
      if (!Visited.contains(Predecessor)) Pending.push_back(Predecessor);
  }
  return false;
}

static bool isStable(Value *Input, Instruction &At, BasicBlock &EdgeTarget,
                     DominatorTree &Tree, AssumptionCache &Assumptions) {
  auto *Definition = dyn_cast<Instruction>(Input);
  if (Definition &&
      mayReexecute(*Definition->getParent(), EdgeTarget))
    return false;
  if (auto *Field = dyn_cast<ExtractValueInst>(Input))
    if (isa<FreezeInst>(Field->getAggregateOperand())) return true;
  return isGuaranteedNotToBeUndefOrPoison(Input, &Assumptions, &At, &Tree);
}

static void collect(Value *Condition, bool Taken, Instruction &At,
                    BasicBlock &EdgeTarget, DominatorTree &Tree,
                    AssumptionCache &Assumptions, Facts &Bounds,
                    unsigned &Budget) {
  if (!Budget) return;
  --Budget;
  auto Visit = [&](Value *Operand, bool Truth) {
    collect(Operand, Truth, At, EdgeTarget, Tree, Assumptions, Bounds, Budget);
  };
  if (auto *Either = dyn_cast<BinaryOperator>(Condition)) {
    if ((Either->getOpcode() == Instruction::Or && !Taken) ||
        (Either->getOpcode() == Instruction::And && Taken))
      for (Value *Operand : Either->operands()) Visit(Operand, Taken);
    return;
  }
  if (auto *Choice = dyn_cast<SelectInst>(Condition)) {
    if (auto *True = dyn_cast<ConstantInt>(Choice->getTrueValue());
        True && True->isOne() != Taken) {
      Visit(Choice->getCondition(), false);
      Visit(Choice->getFalseValue(), Taken);
    } else if (auto *False = dyn_cast<ConstantInt>(Choice->getFalseValue());
               False && False->isOne() != Taken) {
      Visit(Choice->getCondition(), true);
      Visit(Choice->getTrueValue(), Taken);
    }
    return;
  }

  auto *Compare = dyn_cast<ICmpInst>(Condition);
  if (!Compare) return;
  auto *Limit = dyn_cast<ConstantInt>(Compare->getOperand(1));
  if (!Limit) return;
  Value *Input = Compare->getOperand(0);
  auto Predicate = Taken ? Compare->getPredicate() : Compare->getInversePredicate();
  ConstantRange Range = ConstantRange::makeExactICmpRegion(Predicate, Limit->getValue());
  if (auto *Add = dyn_cast<BinaryOperator>(Input);
      Add && Add->getOpcode() == Instruction::Add) {
    if (auto *Offset = dyn_cast<ConstantInt>(Add->getOperand(1))) {
      Range = Range.sub(ConstantRange(Offset->getValue()));
      Input = Add->getOperand(0);
    }
  }
  if (auto *Absolute = dyn_cast<IntrinsicInst>(Input);
      Absolute && Absolute->getIntrinsicID() == Intrinsic::abs &&
      Predicate == ICmpInst::ICMP_ULE && !Limit->isNegative() &&
      !Limit->getValue().isMaxSignedValue()) {
    APInt Bound = Limit->getValue();
    Input = Absolute->getArgOperand(0);
    Range = ConstantRange(-Bound, Bound + 1);
  }
  if (!isStable(Input, At, EdgeTarget, Tree, Assumptions)) return;
  auto [Fact, Inserted] = Bounds.try_emplace(Input, Range);
  if (!Inserted) Fact->second = Fact->second.intersectWith(Range);
}

static Facts dominatingBounds(Instruction &Call, DominatorTree &Tree,
                              AssumptionCache &Assumptions, const Facts &Incoming) {
  Facts Bounds = Incoming;
  unsigned Budget = 96;
  for (auto *Node = Tree.getNode(Call.getParent()); Node && Budget;
       Node = Node->getIDom()) {
    BasicBlock *Block = Node->getBlock();
    auto *Branch = dyn_cast<CondBrInst>(Block->getTerminator());
    if (!Branch || Branch->getSuccessor(0) == Branch->getSuccessor(1)) continue;
    for (unsigned Edge = 0; Edge < 2; ++Edge) {
      auto *Target = Branch->getSuccessor(Edge);
      if (Tree.dominates(BasicBlockEdge(Block, Target), Call.getParent()))
        collect(Branch->getCondition(), Edge == 0, *Branch, *Target, Tree,
                Assumptions, Bounds, Budget);
    }
  }
  return Bounds;
}
} // namespace ElisaDominatingIntegerBounds
