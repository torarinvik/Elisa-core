// Value shapes the index prover (ElisaIndexBounds) reads beyond scalar
// evolution's own algebra.
//
// The vectorizer's main loop runs Index = c, c + k, ... (c <u k, k a power
// of two) and leaves when Index - c + k, computed with `add nuw`, equals
// N = A - R, where R = A & (k - 1), or that remainder with zero replaced by
// k so a scalar tail always runs, or N = A & M with M's low bits clear. N
// is a multiple of k at most A, so every iteration has Index - c + k <=u N
// and Index <u A. The replaced form needs A nonzero: A = 0 would make N
// wrap.
//
// A binary search's midpoint L + (H - L) /u 2 lies below H when L <u H, and
// a select is at most a bound when both its values are.
#pragma once
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
#include <llvm/IR/PatternMatch.h>
namespace ElisaIndexShapes {
using namespace llvm;
using namespace llvm::PatternMatch;

// Whether R is A & (k - 1) for the step's mask, or that remainder with zero
// replaced by k (then A must be nonzero). Either way A - R is a multiple of k
// at most A, and below A in the replaced form.
inline bool remainder(ScalarEvolution &Evolution, Value *R, Value *A, uint64_t Mask, bool &Replaced) {
  Value *Low = nullptr;
  Replaced = !match(R, m_And(m_Specific(A), m_SpecificInt(Mask)));
  return !Replaced ||
         (match(R, m_Select(m_SpecificICmp(ICmpInst::ICMP_EQ, m_Value(Low), m_ZeroInt()),
                            m_SpecificInt(Mask + 1), m_Deferred(Low))) &&
          match(Low, m_And(m_Specific(A), m_SpecificInt(Mask))) &&
          Evolution.isKnownNonZero(Evolution.getSCEV(A)));
}

// A when Recurrence is such an index, else null.
inline Value *roundedLimit(ScalarEvolution &Evolution, const SCEVAddRecExpr &Recurrence) {
  const Loop *L = Recurrence.getLoop();
  auto *Start = dyn_cast<SCEVConstant>(Recurrence.getStart());
  auto *Step = Recurrence.isAffine() ? dyn_cast<SCEVConstant>(Recurrence.getStepRecurrence(Evolution)) : nullptr;
  BasicBlock *Latch = L->getLoopLatch();
  if (!Start || !Step || !Step->getAPInt().isPowerOf2() || !Start->getAPInt().ult(Step->getAPInt()) ||
      !Latch || L->getExitingBlock() != Latch)
    return nullptr;
  auto *Branch = dyn_cast<CondBrInst>(Latch->getTerminator());
  auto *Compare = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  if (!Compare || !Compare->isEquality() ||
      L->contains(Branch->getSuccessor(Compare->getPredicate() == ICmpInst::ICMP_EQ ? 0 : 1)))
    return nullptr;
  const SCEV *Next = Evolution.getAddExpr(
      &Recurrence, Evolution.getConstant(Step->getAPInt() - Start->getAPInt()));
  for (unsigned Side : {0u, 1u}) {
    auto *Increment = dyn_cast<BinaryOperator>(Compare->getOperand(Side));
    if (!Increment || Increment->getOpcode() != Instruction::Add || !Increment->hasNoUnsignedWrap() ||
        Evolution.getSCEV(Increment) != Next)
      continue;
    Value *Limit = nullptr, *Remainder = nullptr;
    const APInt *Kept = nullptr;
    bool Replaced = false;
    const uint64_t Mask = Step->getAPInt().getZExtValue() - 1;
    // A masked with its low bits clear (`A & 252` for A below 256) is
    // another multiple of k at most A.
    if (match(Compare->getOperand(1 - Side), m_And(m_Value(Limit), m_APInt(Kept))))
      return (*Kept & Mask).isZero() ? Limit : nullptr;
    return match(Compare->getOperand(1 - Side), m_Sub(m_Value(Limit), m_Value(Remainder))) &&
                   remainder(Evolution, Remainder, Limit, Mask, Replaced)
               ? Limit
               : nullptr;
  }
  return nullptr;
}

// The quotient a /u k in S, also seen through a truncation and zero
// extension back (`a & 252` for a known below 256 is 4 * zext(trunc(a /u 4)))
// that can only make it smaller.
inline const SCEVUDivExpr *quotient(const SCEV *S) {
  if (auto *Extended = dyn_cast<SCEVZeroExtendExpr>(S))
    if (auto *Truncated = dyn_cast<SCEVTruncateExpr>(Extended->getOperand());
        Truncated && Truncated->getOperand()->getType() == S->getType())
      S = Truncated->getOperand();
  return dyn_cast<SCEVUDivExpr>(S);
}
// A value above Index: the A of a rounded loop's index, or of the rounded
// count A - R itself in the replaced form (the vectorizer's n.vec, which
// leaves the scalar tail at least one iteration). With Strict cleared, a
// value at least Index: the a of a count k * (a /u k) rounded down. Null
// when none.
inline const SCEV *limit(ScalarEvolution &Evolution, const SCEV *Index, bool &Strict) {
  Strict = true;
  if (auto *Product = dyn_cast<SCEVMulExpr>(Index); Product && Product->getNumOperands() == 2)
    if (const SCEVUDivExpr *Quotient = quotient(Product->getOperand(1));
        Quotient && Quotient->getRHS() == Product->getOperand(0)) {
      Strict = false;
      return Quotient->getLHS();
    }
  if (auto *Recurrence = dyn_cast<SCEVAddRecExpr>(Index)) {
    Value *A = roundedLimit(Evolution, *Recurrence);
    return A ? Evolution.getSCEV(A) : nullptr;
  }
  // Sums flatten, so A's own terms sit beside -R: A = Index + R.
  auto *Sum = dyn_cast<SCEVAddExpr>(Index);
  for (unsigned Position = 0; Sum && Position < Sum->getNumOperands(); ++Position) {
    const SCEV *Term = Sum->getOperand(Position);
    auto *Negated = dyn_cast<SCEVMulExpr>(Term);
    auto *Unknown = Negated && Negated->getNumOperands() == 2 && Negated->getOperand(0)->isAllOnesValue()
                        ? dyn_cast<SCEVUnknown>(Negated->getOperand(1))
                        : nullptr;
    auto *Choice = Unknown ? dyn_cast<SelectInst>(Unknown->getValue()) : nullptr;
    auto *Multiple = Choice ? dyn_cast<ConstantInt>(Choice->getTrueValue()) : nullptr;
    Value *A = nullptr;
    bool Replaced = false;
    if (Multiple && Multiple->getValue().isPowerOf2() &&
        match(Choice->getFalseValue(), m_And(m_Value(A), m_Value())) &&
        Evolution.getSCEV(A) == Evolution.getAddExpr(Index, Unknown) &&
        remainder(Evolution, Choice, A, Multiple->getZExtValue() - 1, Replaced) && Replaced)
      return Evolution.getSCEV(A);
  }
  return nullptr;
}
// The phi of Recurrence's loop header that Recurrence describes, if any: an
// induction over it can use the loop's exit tests, which a recurrence whose
// trip count scalar evolution cannot compute loses.
inline PHINode *headerPhi(ScalarEvolution &Evolution, const SCEVAddRecExpr &Recurrence) {
  for (PHINode &Merge : Recurrence.getLoop()->getHeader()->phis())
    if (Evolution.isSCEVable(Merge.getType()) && Evolution.getSCEV(&Merge) == &Recurrence) return &Merge;
  return nullptr;
}
// H for a midpoint L + (H - L) /u 2, with Low set to L; null otherwise.
inline const SCEV *midpoint(ScalarEvolution &Evolution, const SCEV *Index, const SCEV *&Low) {
  auto *Sum = dyn_cast<SCEVAddExpr>(Index);
  for (unsigned Position = 0; Sum && Sum->getNumOperands() == 2 && Position < 2; ++Position) {
    auto *Half = dyn_cast<SCEVUDivExpr>(Sum->getOperand(Position));
    auto *Two = Half ? dyn_cast<SCEVConstant>(Half->getRHS()) : nullptr;
    if (!Two || Two->getAPInt() != 2) continue;
    Low = Sum->getOperand(1 - Position);
    return Evolution.getAddExpr(Half->getLHS(), Low);
  }
  return nullptr;
}

// The select S stands for, if any.
inline SelectInst *choice(const SCEV *S) {
  auto *Unknown = dyn_cast<SCEVUnknown>(S);
  return Unknown ? dyn_cast<SelectInst>(Unknown->getValue()) : nullptr;
}
// The values V can have on an edge whose decided comparisons Facts compare
// V with a constant.
inline ConstantRange edgeRange(ArrayRef<std::pair<ICmpInst *, bool>> Facts, Value *V) {
  ConstantRange Range = ConstantRange::getFull(V->getType()->getIntegerBitWidth());
  for (auto [Compare, Truth] : Facts) {
    CmpInst::Predicate Predicate = Truth ? Compare->getPredicate() : Compare->getInversePredicate();
    const APInt *Constant = nullptr;
    if (Compare->getOperand(1) != V) {
      if (Compare->getOperand(0) == V && PatternMatch::match(Compare->getOperand(1), PatternMatch::m_APInt(Constant)))
        Range = Range.intersectWith(ConstantRange::makeExactICmpRegion(Predicate, *Constant));
    } else if (PatternMatch::match(Compare->getOperand(0), PatternMatch::m_APInt(Constant))) {
      Range = Range.intersectWith(
          ConstantRange::makeExactICmpRegion(CmpInst::getSwappedPredicate(Predicate), *Constant));
    }
  }
  return Range;
}
// The constant C of V = add nuw Base, C, with Base set; null otherwise.
inline const APInt *nuwIncrement(Value *V, Value *&Base) {
  const APInt *Step = nullptr;
  return PatternMatch::match(V, PatternMatch::m_NUWAdd(PatternMatch::m_Value(Base), PatternMatch::m_APInt(Step)))
             ? Step
             : nullptr;
}
// A count that starts at zero and rises by at most one per iteration of
// its loop: V is the loop's header phi, or a join after it of the phi and
// the phi plus one (`add nuw`), and the phi's back edge brings such a value.
// Returns the header phi, with Extra set to 1 for the join (the count after
// an iteration's increment) and 0 for the phi itself; null otherwise.
inline PHINode *risingCount(LoopInfo &Loops, Value *V, unsigned &Extra) {
  auto *Merge = dyn_cast<PHINode>(V);
  Loop *L = Merge ? Loops.getLoopFor(Merge->getParent()) : nullptr;
  BasicBlock *Latch = L ? L->getLoopLatch() : nullptr;
  if (!Latch || !L->getLoopPreheader()) return nullptr;
  PHINode *Header = Merge->getParent() == L->getHeader() ? Merge : nullptr;
  auto StepOf = [&](Value *Candidate, PHINode *Phi) {
    Value *Base = nullptr;
    const APInt *Step = nuwIncrement(Candidate, Base);
    return Candidate == Phi || (Step && Step->isOne() && Base == Phi);
  };
  auto Joins = [&](Value *Candidate, PHINode *Phi) {
    auto *Join = dyn_cast<PHINode>(Candidate);
    return StepOf(Candidate, Phi) ||
           (Join && Join->getParent() != L->getHeader() && L->contains(Join) &&
            llvm::all_of(Join->incoming_values(), [&](Value *In) { return StepOf(In, Phi); }));
  };
  if (!Header)
    for (Value *In : Merge->incoming_values())
      if (Value *Base = nullptr; auto *Phi = dyn_cast<PHINode>(In == Merge ? nullptr : In))
        Header = Phi->getParent() == L->getHeader() ? Phi : Header;
      else if (nuwIncrement(In, Base) && isa<PHINode>(Base) && cast<PHINode>(Base)->getParent() == L->getHeader())
        Header = cast<PHINode>(Base);
  auto *Start = Header ? dyn_cast<ConstantInt>(Header->getIncomingValueForBlock(L->getLoopPreheader())) : nullptr;
  if (!Start || !Start->isZero() || !Joins(Header->getIncomingValueForBlock(Latch), Header) ||
      (Merge != Header && !Joins(Merge, Header)))
    return nullptr;
  Extra = Merge == Header ? 0 : 1;
  return Header;
}
} // namespace ElisaIndexShapes
