// Reuse a division computed by one predecessor. Translate the operands
// through the join's PHIs and compute a new quotient only on the other edge.
namespace {
static Value *divisionOperandOnEdge(Value *Operand, BasicBlock &Join,
                                    BasicBlock *Predecessor) {
  auto *Definition = dyn_cast<Instruction>(Operand);
  if (!Definition || Definition->getParent() != &Join) return Operand;
  auto *Phi = dyn_cast<PHINode>(Definition);
  return Phi ? Phi->getIncomingValueForBlock(Predecessor) : nullptr;
}

// A signed division by a positive power of two is a logical shift on an
// edge whose comparison proves the dividend nonnegative.
static int nonnegativeDivisionShift(const BinaryOperator &Div, Value *Dividend,
                                    Value *Divisor, BasicBlock *Pred, BasicBlock &Join) {
  auto *K = dyn_cast<ConstantInt>(Divisor);
  auto *Branch = dyn_cast<CondBrInst>(Pred->getTerminator());
  auto *Guard = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  if (Div.getOpcode() != Instruction::SDiv || !K || !K->getValue().isStrictlyPositive() ||
      !K->getValue().isPowerOf2() || !Guard || Guard->getOperand(0) != Dividend ||
      !isa<FreezeInst>(Dividend))
    return -1;
  auto *Bound = dyn_cast<ConstantInt>(Guard->getOperand(1));
  if (!Bound) return -1;
  auto PredOnEdge = Branch->getSuccessor(0) == &Join ? Guard->getPredicate()
                                                    : Guard->getInversePredicate();
  auto Range = ConstantRange::makeAllowedICmpRegion(PredOnEdge, ConstantRange(Bound->getValue()));
  if (Range.isEmptySet() || Range.getSignedMin().isNegative()) return -1;
  return K->getValue().exactLogBase2();
}

// A preheader can carry an already computed quotient through its predecessor's
// PHI, even when the incoming dividend and divisor are themselves PHIs.
static bool reuseJoinedQuotient(BasicBlock &Block) {
  auto *Div = dyn_cast<BinaryOperator>(&*Block.getFirstNonPHIIt());
  BasicBlock *Join = Block.getSinglePredecessor();
  if (!Join || Join == &Block || !Div || Div->getOpcode() != Instruction::SDiv ||
      !Div->getType()->isIntegerTy(64) || Div->isExact() ||
      Div->hasMetadataOtherThanDebugLoc())
    return false;

  for (PHINode &Phi : Join->phis()) {
    if (Phi.getType() != Div->getType() || Phi.getNumIncomingValues() != 2)
      continue;
    bool Matches = true;
    for (unsigned I = 0; I < 2; ++I) {
      BasicBlock *Pred = Phi.getIncomingBlock(I);
      if (Pred == Join || Pred == Phi.getIncomingBlock(1 - I)) {
        Matches = false;
        break;
      }
      Value *Dividend = divisionOperandOnEdge(Div->getOperand(0), *Join, Pred);
      Value *Divisor = divisionOperandOnEdge(Div->getOperand(1), *Join, Pred);
      auto *Quotient = dyn_cast<BinaryOperator>(Phi.getIncomingValue(I));
      if (!Dividend || !Divisor || !Quotient || Quotient->isExact() ||
          Quotient->getOperand(0) != Dividend) {
        Matches = false;
        break;
      }
      if (Quotient->getOpcode() == Instruction::SDiv && Quotient->getOperand(1) == Divisor)
        continue;

      auto *Shift = dyn_cast<ConstantInt>(Quotient->getOperand(1));
      auto *Denominator = dyn_cast<ConstantInt>(Divisor);
      // In the shift case require a stable dividend and a guard dominating
      // the incoming edge. A samesign comparison with a nonnegative constant
      // is poison for negative dividends, so any taken branch proves the sign.
      auto *Entry = dyn_cast<UncondBrInst>(Pred->getTerminator());
      BasicBlock *GuardBlock = Entry && Entry->getSuccessor() == Join
                                  ? Pred->getSinglePredecessor() : nullptr;
      auto *Branch = GuardBlock ? dyn_cast<CondBrInst>(GuardBlock->getTerminator()) : nullptr;
      auto *Guard = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
      auto *Bound = Guard ? dyn_cast<ConstantInt>(Guard->getOperand(1)) : nullptr;
      if (Quotient->getOpcode() != Instruction::LShr || !Shift || !Denominator ||
          !Denominator->getValue().isStrictlyPositive() ||
          !Denominator->getValue().isPowerOf2() ||
          !Shift->equalsInt(Denominator->getValue().exactLogBase2()) ||
          !isa<FreezeInst>(Dividend) || !Guard || !Guard->hasSameSign() ||
          Guard->getOperand(0) != Dividend || !Bound || Bound->isNegative()) {
        Matches = false;
        break;
      }
    }
    if (!Matches) continue;
    Div->replaceAllUsesWith(&Phi);
    Div->eraseFromParent();
    return true;
  }
  return false;
}

static bool reuseEdgeDivision(BasicBlock &Join) {
  auto *Div = dyn_cast<BinaryOperator>(&*Join.getFirstNonPHIIt());
  if (!Div || (Div->getOpcode() != Instruction::SDiv &&
               Div->getOpcode() != Instruction::UDiv) ||
      !Div->getType()->isIntegerTy(64) || Div->hasMetadataOtherThanDebugLoc() ||
      Join.isEHPad() || Join.hasAddressTaken())
    return false;

  auto Preds = predecessors(&Join);
  auto Iter = Preds.begin();
  if (Iter == Preds.end()) return false;
  BasicBlock *Edges[] = {*Iter++, nullptr};
  if (Iter == Preds.end()) return false;
  Edges[1] = *Iter++;
  if (Iter != Preds.end() || Edges[0] == Edges[1] ||
      Edges[0] == &Join || Edges[1] == &Join)
    return false;

  Value *Inputs[2][2];
  BinaryOperator *Available[2] = {};
  for (unsigned I = 0; I < 2; ++I) {
    BasicBlock *Pred = Edges[I];
    auto *Jump = Pred->getTerminator();
    if (!isa<UncondBrInst, CondBrInst>(Jump)) return false;
    for (unsigned J = 0; J < 2; ++J) {
      Inputs[I][J] = divisionOperandOnEdge(Div->getOperand(J), Join, Pred);
      if (!Inputs[I][J]) return false;
    }
    unsigned Scanned = 0;
    for (Instruction &Op : llvm::reverse(*Pred)) {
      if (++Scanned > 16) break;
      auto *Quotient = dyn_cast<BinaryOperator>(&Op);
      if (Quotient && Quotient->getOpcode() == Div->getOpcode() &&
          Quotient->getOperand(0) == Inputs[I][0] &&
          Quotient->getOperand(1) == Inputs[I][1] &&
          Quotient->isExact() == Div->isExact()) {
        Available[I] = Quotient;
        break;
      }
    }
  }
  if (!Available[0] && !Available[1]) return false;

  for (unsigned I = 0; I < 2; ++I) {
    if (Available[I]) continue;
    // A division may be undefined, so keep it on the incoming edge even if
    // the predecessor can branch elsewhere.
    int Shift = nonnegativeDivisionShift(*Div, Inputs[I][0], Inputs[I][1], Edges[I], Join);
    if (isa<CondBrInst>(Edges[I]->getTerminator())) {
      BasicBlock *Edge = SplitEdge(Edges[I], &Join);
      if (!Edge) return false;
      Edges[I] = Edge;
    }
    if (Shift >= 0) {
      Available[I] = BinaryOperator::CreateLShr(Inputs[I][0], ConstantInt::get(Div->getType(), Shift));
      Available[I]->setIsExact(Div->isExact());
    } else {
      Available[I] = cast<BinaryOperator>(Div->clone());
      Available[I]->setOperand(0, Inputs[I][0]);
      Available[I]->setOperand(1, Inputs[I][1]);
    }
    Available[I]->insertBefore(Edges[I]->getTerminator()->getIterator());
  }
  auto *Quotient = PHINode::Create(Div->getType(), 2, "edge.quotient", Div->getIterator());
  for (unsigned I = 0; I < 2; ++I) Quotient->addIncoming(Available[I], Edges[I]);
  Div->replaceAllUsesWith(Quotient);
  Div->eraseFromParent();
  return true;
}
} // namespace

static unsigned elisacoreReuseEdgeDivisions(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (BDU_SkipFunction(F, M) || F.size() > 128) continue;
    for (BasicBlock &B : F) {
      if (!reuseJoinedQuotient(B) && !reuseEdgeDivision(B)) continue;
      ++Changed;
      break;
    }
  }
  return Changed;
}
