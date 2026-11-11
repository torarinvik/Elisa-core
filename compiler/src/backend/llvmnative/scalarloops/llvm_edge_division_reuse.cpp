#include <llvm-c/Core.h>
#include <llvm/Config/llvm-config.h>

#if LLVM_VERSION_MAJOR >= 23
#include <llvm/IR/CFG.h>
#include <llvm/IR/ConstantRange.h>
#include <llvm/IR/Constants.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Module.h>
#include <llvm/Transforms/Utils/BasicBlockUtils.h>

using namespace llvm;

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

// A guarded factor-removal loop can start at the quotient already carried
// by its caller. The entering equality guarantees its first step continues.
static bool skipRedundantFactorDivision(BasicBlock &Loop) {
  if (Loop.size() != 5) return false;
  auto *Remaining = dyn_cast<PHINode>(&Loop.front());
  auto *Div = Remaining ? dyn_cast<BinaryOperator>(Remaining->getNextNode()) : nullptr;
  if (!Div || Remaining->getNumIncomingValues() != 2 ||
      Div->getOpcode() != Instruction::UDiv || Div->isExact() ||
      !Div->getType()->isIntegerTy(64) || Div->getOperand(0) != Remaining)
    return false;
  int Backedge = Remaining->getBasicBlockIndex(&Loop);
  if (Backedge < 0 || Remaining->getIncomingValue(Backedge) != Div) return false;

  auto IsFactorTest = [](BasicBlock &Block, Value *Dividend, Value *Factor,
                         Value *Quotient, BasicBlock &Continue) {
    auto *Branch = dyn_cast<CondBrInst>(Block.getTerminator());
    auto *Cmp = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
    if (!Cmp || Cmp->getPredicate() != ICmpInst::ICMP_EQ ||
        Branch->getSuccessor(0) != &Continue || Branch->getSuccessor(1) == &Continue ||
        Cmp->getOperand(0) != Dividend)
      return false;
    auto *Product = dyn_cast<BinaryOperator>(Cmp->getOperand(1));
    return Product && Product->getOpcode() == Instruction::Mul &&
           ((Product->getOperand(0) == Factor && Product->getOperand(1) == Quotient) ||
            (Product->getOperand(1) == Factor && Product->getOperand(0) == Quotient));
  };

  Value *Factor = Div->getOperand(1);
  if (!IsFactorTest(Loop, Remaining, Factor, Div, Loop) ||
      (isa<Instruction>(Factor) && cast<Instruction>(Factor)->getParent() == &Loop))
    return false;
  BasicBlock *Entry = Remaining->getIncomingBlock(1 - Backedge);
  Value *Dividend = Remaining->getIncomingValueForBlock(Entry);
  for (PHINode &Quotient : Entry->phis()) {
    if (Quotient.getType() != Div->getType() ||
        !IsFactorTest(*Entry, Dividend, Factor, &Quotient, Loop))
      continue;
    for (unsigned I = 0; I < Quotient.getNumIncomingValues(); ++I) {
      BasicBlock *Pred = Quotient.getIncomingBlock(I);
      Value *N = divisionOperandOnEdge(Dividend, *Entry, Pred);
      Value *D = divisionOperandOnEdge(Factor, *Entry, Pred);
      auto *Op = dyn_cast<BinaryOperator>(Quotient.getIncomingValue(I));
      if (!N || !D || !Op || Op->isExact() || Op->getOperand(0) != N) break;
      bool Matches = Op->getOpcode() == Instruction::UDiv && Op->getOperand(1) == D;
      if (auto *K = dyn_cast<ConstantInt>(D); !Matches && K && K->getValue().isPowerOf2() &&
          Op->getOpcode() == Instruction::LShr) {
        auto *Shift = dyn_cast<ConstantInt>(Op->getOperand(1));
        Matches = Shift && Shift->equalsInt(K->getValue().exactLogBase2());
      }
      if (!Matches) break;
      if (I + 1 == Quotient.getNumIncomingValues()) {
        Remaining->setIncomingValueForBlock(Entry, &Quotient);
        return true;
      }
    }
  }
  return false;
}

// When the outer loop has already divided by this factor, reuse that quotient
// for the first divisibility test. Repeated removals still divide on their
// backedge, where the original loop would have divided again.
static bool carryFactorQuotient(BasicBlock &Loop) {
  if (Loop.size() > 12 || Loop.isEHPad() || Loop.hasAddressTaken()) return false;
  auto *Div = dyn_cast<BinaryOperator>(&*Loop.getFirstNonPHIIt());
  if (!Div || Div->getOpcode() != Instruction::UDiv || Div->isExact() ||
      !Div->getType()->isIntegerTy(64) || Div->hasMetadataOtherThanDebugLoc())
    return false;
  auto *Remaining = dyn_cast<PHINode>(Div->getOperand(0));
  auto *Branch = dyn_cast<CondBrInst>(Loop.getTerminator());
  auto *Cmp = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  if (!Remaining || Remaining->getParent() != &Loop || Remaining->getNumIncomingValues() != 2 ||
      !Cmp || Cmp->getPredicate() != ICmpInst::ICMP_EQ ||
      Branch->getSuccessor(0) != &Loop || Branch->getSuccessor(1) == &Loop)
    return false;
  Value *Factor = Div->getOperand(1);
  if (auto *I = dyn_cast<Instruction>(Factor); I && I->getParent() == &Loop) return false;
  Value *ProductValue = Cmp->getOperand(0);
  if (ProductValue == Remaining) ProductValue = Cmp->getOperand(1);
  else if (Cmp->getOperand(1) != Remaining) return false;
  auto *Product = dyn_cast<BinaryOperator>(ProductValue);
  if (!Product || Product->getOpcode() != Instruction::Mul ||
      !((Product->getOperand(0) == Div && Product->getOperand(1) == Factor) ||
        (Product->getOperand(0) == Factor && Product->getOperand(1) == Div)))
    return false;

  int BackIndex = Remaining->getBasicBlockIndex(&Loop);
  if (BackIndex < 0 || Remaining->getIncomingValue(BackIndex) != Div) return false;
  BasicBlock *Join = Remaining->getIncomingBlock(1 - BackIndex);
  Value *Dividend = Remaining->getIncomingValue(1 - BackIndex);
  auto *Jump = dyn_cast<UncondBrInst>(Join->getTerminator());
  if (!Jump || Jump->getSuccessor() != &Loop || Join->isEHPad()) return false;
  SmallVector<BasicBlock *, 2> Preds(predecessors(Join));
  if (Preds.size() != 2 || Preds[0] == Preds[1]) return false;

  Value *EntryQuotients[2] = {};
  Value *EdgeDividends[2] = {};
  int Shifts[2] = {-1, -1};
  bool ReusesDivision = false;
  for (unsigned I = 0; I < 2; ++I) {
    BasicBlock *Pred = Preds[I];
    if (Pred == Join || Pred == &Loop || Pred->size() > 32) return false;
    Value *N = divisionOperandOnEdge(Dividend, *Join, Pred);
    Value *D = divisionOperandOnEdge(Factor, *Join, Pred);
    if (!N || !D) return false;
    for (Instruction &Inst : *Pred) {
      auto *Op = dyn_cast<BinaryOperator>(&Inst);
      if (Op && Op->getOpcode() == Instruction::UDiv && !Op->isExact() &&
          Op->getOperand(0) == N && Op->getOperand(1) == D) {
        EntryQuotients[I] = Op;
        ReusesDivision = true;
        break;
      }
    }
    if (EntryQuotients[I]) continue;
    auto *K = dyn_cast<ConstantInt>(D);
    if (!K || !K->getValue().isPowerOf2()) return false;
    EdgeDividends[I] = N;
    Shifts[I] = K->getValue().exactLogBase2();
  }
  if (!ReusesDivision) return false;

  BasicBlock *Back = SplitCriticalEdge(Branch, 0, CriticalEdgeSplittingOptions(),
                                      "factor.quotient.backedge");
  if (!Back) return false;
  auto *EntryQuotient = PHINode::Create(Div->getType(), 2, "factor.entry.quotient",
                                       Join->getFirstNonPHIIt());
  for (unsigned I = 0; I < 2; ++I) {
    if (Shifts[I] >= 0) {
      IRBuilder<> B(Preds[I]->getTerminator());
      EntryQuotients[I] = B.CreateLShr(EdgeDividends[I], Shifts[I], "factor.initial.quotient");
    }
    EntryQuotient->addIncoming(EntryQuotients[I], Preds[I]);
  }
  auto *Quotient = PHINode::Create(Div->getType(), 2, "factor.quotient", Div->getIterator());
  Quotient->addIncoming(EntryQuotient, Join);
  IRBuilder<> B(Back->getTerminator());
  B.SetCurrentDebugLocation(Div->getDebugLoc());
  Quotient->addIncoming(B.CreateUDiv(Quotient, Factor, "factor.next.quotient"), Back);
  Div->replaceAllUsesWith(Quotient);
  Div->eraseFromParent();
  return true;
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

extern "C" bool elisacoreReuseEdgeDivisionsInFunction(LLVMValueRef Ref) {
  auto &F = *cast<Function>(unwrap(Ref));
  for (BasicBlock &B : F) {
    if (skipRedundantFactorDivision(B) || carryFactorQuotient(B) || reuseJoinedQuotient(B) || reuseEdgeDivision(B)) return true;
  }
  return false;
}
#endif
