// Prove Index <u Length for integer values at a program point. The proof
// combines scalar evolution with facts SCEV does not chain on its own:
// comparisons decided on dominating branch edges (transitively), lower
// bounds read from those edges, the extremes of affine recurrences, and
// induction over loop-header phis. A header phi is below a loop-invariant
// Length when every incoming value is, assuming the phi itself is below it
// on the back edges; several such assumptions are discharged together, as
// one induction over their headers. Comparisons between header phis holding
// on every incoming edge (`left <u right`) are facts the header dominates.
#include "llvm_element_bounds.h"
#include "llvm_index_ranges.h"
namespace ElisaIndexBounds {
using namespace llvm;

class Prover : public Ranges {
public:
  Prover(ScalarEvolution &Evolution, DominatorTree &Tree, LoopInfo &Loops) : Ranges(Evolution, Tree, Loops) {}

  // Index <u Length wherever At executes, within a fixed proof budget.
  bool proveBelow(const SCEV *Index, const SCEV *Length, Instruction *At) {
    budget = 4000;
    return below(Index, Length, At);
  }

  // Low <=u High wherever At executes, within the same budget.
  bool proveAtMost(const SCEV *Low, const SCEV *High, Instruction *At) {
    budget = 4000;
    return atMost(Low, High, At);
  }

protected:
  // Reads whose values another analysis bounds.
  virtual bool loadBelow(LoadInst &, const SCEV *, unsigned) { return false; }

  bool below(const SCEV *Index, const SCEV *Length, Instruction *At, unsigned Depth = 0) override {
    if (Depth > 9 || budget == 0 || Index->getType() != Length->getType()) return false;
    --budget;
    if (evolution.isKnownPredicateAt(ICmpInst::ICMP_ULT, Index, Length, At) ||
        highest(Index, At).ult(lowest(Length, At)))
      return true;
    // Below each operand of an unsigned minimum is below the minimum.
    if (auto *Minimum = dyn_cast<SCEVUMinExpr>(Length);
        Minimum && llvm::all_of(Minimum->operands(), [&](const SCEV *Operand) {
          return below(Index, Operand, At, Depth + 1);
        }))
      return true;
    // An unsigned minimum is at most each of its operands.
    if (auto *Minimum = dyn_cast<SCEVUMinExpr>(Index))
      return llvm::any_of(Minimum->operands(), [&](const SCEV *Operand) {
        return below(Operand, Length, At, Depth + 1);
      });
    // Index = Length - c is below Length exactly when Length >= c.
    if (auto *Gap = dyn_cast<SCEVConstant>(evolution.getMinusSCEV(Length, Index));
        Gap && !Gap->isZero() && atMost(Index, Length, At, Depth + 1))
      return true;
    if (guarded(Index, Length, At, Depth, true) || scaledBelow(Index, Length, At, Depth) ||
        differenceBelow(Index, Length, At, Depth))
      return true;
    // Index = X - c with X >= c > 0 is below X.
    if (auto *Sum = dyn_cast<SCEVAddExpr>(Index); Sum && Sum->getNumOperands() == 2)
      if (auto *Constant = dyn_cast<SCEVConstant>(Sum->getOperand(0));
          Constant && Constant->getAPInt().isNegative() &&
          gapFits(evolution.getNegativeSCEV(Constant), Sum->getOperand(1), At) &&
          (below(Sum->getOperand(1), Length, At, Depth + 1) ||
           atMost(Sum->getOperand(1), Length, At, Depth + 1)))
        return true;
    // Index + 1, nonzero where At runs, is at most Length (`items[position - 1]`
    // under `position > 0`, the subtraction folded into a recurrence's start).
    if (const SCEV *Next = evolution.getAddExpr(Index, evolution.getOne(Index->getType()));
        !lowest(Next, At).isZero() && atMost(Next, Length, At, Depth + 1))
      return true;
    // a /u c with c > 1 is below a whenever a is nonzero.
    if (auto *Quotient = dyn_cast<SCEVUDivExpr>(Index))
      if (auto *Divisor = dyn_cast<SCEVConstant>(Quotient->getRHS());
          Divisor && Divisor->getAPInt().ugt(1) && !lowest(Quotient->getLHS(), At).isZero() &&
          atMost(Quotient->getLHS(), Length, At, Depth + 1))
        return true;
    if (bool Strict = true; const SCEV *Limit = ElisaIndexShapes::limit(evolution, Index, Strict))
      if (Strict ? atMost(Limit, Length, At, Depth + 1) : below(Limit, Length, At, Depth + 1)) return true;
    if (const SCEV *Low = nullptr, *High = ElisaIndexShapes::midpoint(evolution, Index, Low);
        High && below(Low, High, At, Depth + 1) && atMost(High, Length, At, Depth + 1))
      return true;
    if (auto *Recurrence = dyn_cast<SCEVAddRecExpr>(Index)) return recurrenceBelow(*Recurrence, Length, Depth);
    if (auto *Unknown = dyn_cast<SCEVUnknown>(Index)) {
      if (auto *Merge = dyn_cast<PHINode>(Unknown->getValue())) return phiBelow(*Merge, Length, Depth);
      if (auto *Choice = dyn_cast<SelectInst>(Unknown->getValue()))
        return valueBelow(Choice->getTrueValue(), Length, At, Depth) &&
               valueBelow(Choice->getFalseValue(), Length, At, Depth);
      if (auto *Load = dyn_cast<LoadInst>(Unknown->getValue())) return loadBelow(*Load, Length, Depth + 1);
    }
    return false;
  }

  // Low <=u High wherever At executes: known outright, or Low = High - Gap
  // for a Gap that never exceeds High, so the subtraction does not wrap.
  bool atMost(const SCEV *Low, const SCEV *High, Instruction *At, unsigned Depth = 0) {
    return Low == High || evolution.isKnownPredicateAt(ICmpInst::ICMP_ULE, Low, High, At) ||
           (Low->getType() == High->getType() && highest(Low, At).ule(lowest(High, At))) ||
           gapFits(evolution.getMinusSCEV(High, Low), High, At) ||
           guarded(Low, High, At, Depth, false) || successorAtMost(Low, High, At, Depth) ||
           recurrenceAtMost(Low, High, Depth) || roundedAtMost(Low, High, At, Depth) ||
           phiAtMost(Low, High, Depth) || partAtMost(Low, High, At, Depth) ||
           countedAtMost(Low, High, At, Depth) || elementAtMost(Low, High, Depth);
  }

private:
  SmallVector<std::pair<PHINode *, const SCEV *>, 8> assumedAtMost;
  std::optional<ElisaElementBounds::Checker> elements;

  // A value read from a local array is at most a bound every stored value
  // is at most or is checked against after its store (ElisaElementBounds).
  bool elementAtMost(const SCEV *Low, const SCEV *High, unsigned Depth) {
    auto *Unknown = dyn_cast<SCEVUnknown>(Low);
    auto *Load = Unknown ? dyn_cast<LoadInst>(Unknown->getValue()) : nullptr;
    if (!Load || Depth > 6 || budget == 0) return false;
    --budget;
    if (!elements) elements.emplace(evolution, tree, loops);
    return elements->atMost(*Load, High, [&](const SCEV *Stored, const SCEV *Bound, Instruction *Store) {
      return Stored->getType() == Bound->getType() && atMost(Stored, Bound, Store, Depth + 1);
    });
  }

  // A count rising by at most one per iteration from zero is at most the
  // iterations run: k (plus one after the increment) in iteration k.
  bool countedAtMost(const SCEV *Low, const SCEV *High, Instruction *At, unsigned Depth) {
    auto *Unknown = dyn_cast<SCEVUnknown>(Low);
    unsigned Extra = 0;
    PHINode *Header = Unknown && Depth < 8 ? ElisaIndexShapes::risingCount(loops, Unknown->getValue(), Extra)
                                           : nullptr;
    if (!Header || !evolution.isSCEVable(Header->getType())) return false;
    const Loop *L = loops.getLoopFor(Header->getParent());
    const SCEV *Bound = evolution.getAddRecExpr(evolution.getConstant(Header->getType(), Extra),
                                                evolution.getOne(Header->getType()), L, SCEV::FlagNUW);
    // After the loop, the latest iteration is at most the largest back-edge
    // count (exits that trap count too, so it bounds the normal ones).
    if (!L->contains(At)) {
      const SCEV *Taken = evolution.getSymbolicMaxBackedgeTakenCount(L);
      if (isa<SCEVCouldNotCompute>(Taken) || Taken->getType() != Header->getType()) return false;
      Bound = evolution.getAddExpr(Taken, evolution.getConstant(Header->getType(), Extra));
    }
    return atMost(Bound, High, At, Depth + 1);
  }

  // An unsigned minimum is at most each operand, a select at most its two
  // values' bound, and a quotient (half of a prefix) at most its dividend.
  bool partAtMost(const SCEV *Low, const SCEV *High, Instruction *At, unsigned Depth) {
    if (Depth > 6 || budget == 0) return false;
    --budget;
    if (auto *Minimum = dyn_cast<SCEVUMinExpr>(Low))
      return llvm::any_of(Minimum->operands(), [&](const SCEV *Part) { return atMost(Part, High, At, Depth + 1); });
    if (SelectInst *Choice = ElisaIndexShapes::choice(Low)) {
      // A minimum is at most either value; any other choice needs both.
      bool Minimum = ElisaIndexShapes::chooseMinimum(*Choice);
      bool True = atMost(evolution.getSCEV(Choice->getTrueValue()), High, At, Depth + 1);
      if (True == Minimum) return True;
      return atMost(evolution.getSCEV(Choice->getFalseValue()), High, At, Depth + 1);
    }
    if (const SCEV *Start = nullptr, *End = ElisaIndexShapes::midpoint(evolution, Low, Start); End)
      return atMost(Start, End, At, Depth + 1) && atMost(End, High, At, Depth + 1);
    auto *Quotient = dyn_cast<SCEVUDivExpr>(Low);
    return Quotient && evolution.isKnownNonZero(Quotient->getRHS()) &&
           atMost(Quotient->getLHS(), High, At, Depth + 1);
  }

  bool valueBelow(Value *V, const SCEV *Length, Instruction *At, unsigned Depth) {
    return evolution.isSCEVable(V->getType()) && below(evolution.getSCEV(V), Length, At, Depth + 1);
  }

  // By the recurrence's largest value, else by induction over its header
  // phi (a loop whose count scalar evolution cannot compute).
  bool recurrenceBelow(const SCEVAddRecExpr &Recurrence, const SCEV *Length, unsigned Depth) {
    PHINode *Merge = ElisaIndexShapes::headerPhi(evolution, Recurrence);
    return (evolution.isLoopInvariant(Length, Recurrence.getLoop()) &&
            recurrenceWithin(Recurrence, Depth, [&](const SCEV *Top, Instruction *Entry) {
              return below(Top, Length, Entry, Depth + 1);
            })) || (Merge && phiBelow(*Merge, Length, Depth + 1));
  }

  // Whether every value of Recurrence's loop is at most its largest, Top,
  // and Top satisfies Within at the loop's entry.
  template <typename Predicate>
  bool recurrenceWithin(const SCEVAddRecExpr &Recurrence, unsigned Depth, Predicate Within) {
    if (Depth > 9) return false;
    const SCEV *Count = evolution.getSymbolicMaxBackedgeTakenCount(Recurrence.getLoop());
    const SCEV *Start = Recurrence.getStart();
    const SCEV *Step = Recurrence.isAffine() ? Recurrence.getStepRecurrence(evolution) : nullptr;
    Instruction *Entry = &*Recurrence.getLoop()->getHeader()->getFirstInsertionPt();
    // A countdown whose back edge runs only while it is nonzero never wraps.
    if (BasicBlock *Latch = Recurrence.getLoop()->getLoopLatch();
        Step && Step->isAllOnesValue() && Latch && !lowest(&Recurrence, Latch->getTerminator()).isZero())
      return Within(Start, Entry);
    if (isa<SCEVCouldNotCompute>(Count) || !Step || Count->getType() != Start->getType())
      return false;
    Count = settle(Count, Entry, Depth);
    if (Step->isOne()) {
      // Values Start + k for k <= Count, none wrapping past Start + Count;
      // Count is at most each operand of an unsigned minimum.
      SmallVector<const SCEV *, 4> Bounds{Count};
      if (auto *Minimum = dyn_cast<SCEVUMinExpr>(Count))
        Bounds.assign(Minimum->operands().begin(), Minimum->operands().end());
      return llvm::any_of(Bounds, [&](const SCEV *Bound) {
        const SCEV *Last = evolution.getAddExpr(Start, Bound);
        return atMost(Start, Last, Entry, Depth + 1) && Within(Last, Entry);
      });
    }
    // Start + Step * Count does not overflow even at their largest values,
    // so no value wraps and the last is the largest.
    if (auto *Stride = dyn_cast<SCEVConstant>(Step); Stride && !Stride->getAPInt().isNegative()) {
      bool Overflow = false;
      APInt Largest = highest(Count, Entry).umul_ov(Stride->getAPInt(), Overflow);
      Largest = Largest.uadd_ov(highest(Start, Entry), Overflow);
      return !Overflow && Within(evolution.getAddExpr(Start, evolution.getMulExpr(Step, Count)), Entry);
    }
    if (Step->isAllOnesValue())
      // Values Start - k for k <= Count <= Start never wrap below zero.
      return atMost(Count, Start, Entry, Depth + 1) && Within(Start, Entry);
    return false;
  }

  // k * (a /u k) <=u a: rounding down to a multiple never exceeds a.
  bool roundedAtMost(const SCEV *Low, const SCEV *High, Instruction *At, unsigned Depth) {
    auto *Product = dyn_cast<SCEVMulExpr>(Low);
    auto *Quotient = Product && Product->getNumOperands() == 2 ? ElisaIndexShapes::quotient(Product->getOperand(1))
                                                               : nullptr;
    return Quotient && Quotient->getRHS() == Product->getOperand(0) && Depth <= 9 &&
           atMost(Quotient->getLHS(), High, At, Depth + 1);
  }

  // A recurrence over a loop High does not change is at most High when its
  // largest value is.
  bool recurrenceAtMost(const SCEV *Low, const SCEV *High, unsigned Depth) {
    auto *Recurrence = dyn_cast<SCEVAddRecExpr>(Low);
    return Recurrence && evolution.isLoopInvariant(High, Recurrence->getLoop()) &&
           recurrenceWithin(*Recurrence, Depth, [&](const SCEV *Top, Instruction *Entry) {
             return atMost(Top, High, Entry, Depth + 1);
           });
  }

  // X + 1 <=u High when X <u High: the increment cannot wrap. A recurrence
  // is a header phi's successor ({s + 1,+,1} after {s,+,1}).
  bool successorAtMost(const SCEV *Low, const SCEV *High, Instruction *At, unsigned Depth) {
    auto *Sum = dyn_cast<SCEVAddExpr>(Low);
    auto *One = Sum && Sum->getNumOperands() == 2 ? dyn_cast<SCEVConstant>(Sum->getOperand(0)) : nullptr;
    return ((One && One->isOne()) || isa<SCEVAddRecExpr>(Low)) &&
           below(evolution.getMinusSCEV(Low, evolution.getOne(Low->getType())), High, At, Depth + 1);
  }

  // Index != Bound on a dominating edge, with Bound settling to Length and
  // Index <=u Length: then Index <u Length. Loop exits test `!=` against
  // the trip limit once the comparison is canonicalized.
  bool differsBelow(Value *Left, Value *Right, const SCEV *Index, const SCEV *Length,
                    Instruction *At, unsigned Depth, bool Strict) {
    if (!Strict || Depth > 8) return false;
    // Adding a constant is a bijection, so `Index + c != Other` is exactly
    // `Index != Other - c`.
    const SCEV *First = evolution.getSCEV(Left), *Second = evolution.getSCEV(Right);
    const SCEV *Other = nullptr;
    if (auto *Shift = dyn_cast<SCEVConstant>(evolution.getMinusSCEV(First, Index)))
      Other = evolution.getMinusSCEV(Second, Shift);
    else if (auto *Shift = dyn_cast<SCEVConstant>(evolution.getMinusSCEV(Second, Index)))
      Other = evolution.getMinusSCEV(First, Shift);
    return Other && settle(Other, At, Depth + 1) == Length && atMost(Index, Length, At, Depth + 1);
  }

  // S with each two-operand unsigned maximum, also inside sums, replaced by
  // its larger operand where one is known at most the other at At. Trip
  // counts carry umax(start, limit) for loops entered under start < limit.
  const SCEV *settle(const SCEV *S, Instruction *At, unsigned Depth) {
    if (auto *Maximum = dyn_cast<SCEVUMaxExpr>(S); Maximum && Maximum->getNumOperands() == 2) {
      const SCEV *First = Maximum->getOperand(0), *Second = Maximum->getOperand(1);
      if (atMost(First, Second, At, Depth + 1)) return Second;
      if (atMost(Second, First, At, Depth + 1)) return First;
      return S;
    }
    if (auto *Sum = dyn_cast<SCEVAddExpr>(S)) {
      SmallVector<SCEVUse, 4> Terms;
      bool Changed = false;
      for (const SCEV *Term : Sum->operands()) {
        Terms.push_back(settle(Term, At, Depth));
        Changed |= Terms.back() != Term;
      }
      return Changed ? evolution.getAddExpr(Terms) : S;
    }
    return S;
  }

  // Every incoming value is below Length on its edge, by the facts that
  // reach the edge or by the edge's own comparison. A loop-header phi may
  // assume its own bound on the back edges when Length is loop-invariant.
  bool phiBelow(PHINode &Merge, const SCEV *Length, unsigned Depth) {
    Loop *L = loops.getLoopFor(Merge.getParent());
    bool Header = L && L->getHeader() == Merge.getParent();
    if (Header) {
      if (llvm::is_contained(assumed, std::make_pair(&Merge, Length))) return true;
      if (!evolution.isLoopInvariant(Length, L)) return false;
      assumed.push_back({&Merge, Length});
    }
    bool Holds = llvm::all_of(llvm::seq(Merge.getNumIncomingValues()), [&](unsigned Index) {
      Value *Incoming = Merge.getIncomingValue(Index);
      BasicBlock *From = Merge.getIncomingBlock(Index);
      return valueBelow(Incoming, Length, From->getTerminator(), Depth) ||
             (evolution.isSCEVable(Incoming->getType()) &&
              llvm::any_of(ElisaDeadTrapExits::edgeFacts(From, Merge.getParent()), [&](auto Fact) {
                auto [Compare, Truth] = Fact;
                return boundedBy(Truth ? Compare->getPredicate() : Compare->getInversePredicate(),
                                 Compare->getOperand(0), Compare->getOperand(1),
                                 evolution.getSCEV(Incoming), Length, From->getTerminator(),
                                 Depth + 1, true);
              }));
    });
    if (Header) assumed.pop_back();
    return Holds;
  }

  // A phi is at most a High defined outside it when each incoming value
  // is at most High at the end of its edge; header phis by induction, as
  // in phiBelow (a scan count leaving a countdown loop).
  bool phiAtMost(const SCEV *Low, const SCEV *High, unsigned Depth) {
    auto *Unknown = dyn_cast<SCEVUnknown>(Low);
    auto *Merge = Unknown ? dyn_cast<PHINode>(Unknown->getValue()) : nullptr;
    if (!Merge || Depth > 9 || budget == 0) return false;
    Loop *L = loops.getLoopFor(Merge->getParent());
    bool Header = L && L->getHeader() == Merge->getParent();
    if (Header) {
      if (llvm::is_contained(assumedAtMost, std::make_pair(Merge, High))) return true;
      if (!evolution.isLoopInvariant(High, L)) return false;
    }
    assumedAtMost.push_back({Merge, High});
    bool Holds = llvm::all_of(llvm::seq(Merge->getNumIncomingValues()), [&](unsigned Index) {
      Value *Incoming = Merge->getIncomingValue(Index);
      return evolution.isSCEVable(Incoming->getType()) &&
             atMost(evolution.getSCEV(Incoming), High, Merge->getIncomingBlock(Index)->getTerminator(),
                    Depth + 1);
    });
    assumedAtMost.pop_back();
    return Holds;
  }

  // A fact dominating At decided Index <u Bound or Index <=u Bound, and
  // Bound is in turn at most or below Length: Index <u Length when Strict,
  // else Index <=u Length. Facts come from branch edges and loop headers.
  bool guarded(const SCEV *Index, const SCEV *Length, Instruction *At, unsigned Depth,
               bool Strict) {
    if (Depth > 9 || budget == 0) return false;
    return anyFact(At, [&](CmpInst::Predicate Predicate, Value *Left, Value *Right) {
      return boundedBy(Predicate, Left, Right, Index, Length, At, Depth, Strict);
    });
  }

  // Whether Holds accepts a comparison `Left Predicate Right` known where At
  // executes: an order at a dominating loop header or a decided edge.
  template <typename Accept> bool anyFact(Instruction *At, Accept Holds) {
    BasicBlock *Block = At->getParent();
    for (DomTreeNode *Node = tree.getNode(Block); Node; Node = Node->getIDom()) {
      BasicBlock *Dominator = Node->getBlock();
      if (loops.isLoopHeader(Dominator))
        for (const Order &Fact : orders(*Dominator))
          if (Holds(ICmpInst::ICMP_ULT, Fact.Low, Fact.High)) return true;
      for (BasicBlock *Predecessor : predecessors(Dominator)) {
        if (!tree.dominates(BasicBlockEdge(Predecessor, Dominator), Block)) continue;
        for (auto [Compare, Truth] : ElisaDeadTrapExits::edgeFacts(Predecessor, Dominator))
          if (Holds(Truth ? Compare->getPredicate() : Compare->getInversePredicate(), Compare->getOperand(0),
                    Compare->getOperand(1)))
            return true;
      }
    }
    return false;
  }

  // Index = c + k * X with c <u k and X <u a /u k is below any Length >= a:
  // k * X + c < k * (a /u k) <= a, so nothing wraps.
  bool scaledBelow(const SCEV *Index, const SCEV *Length, Instruction *At, unsigned Depth) {
    APInt Offset = APInt::getZero(evolution.getTypeSizeInBits(Index->getType()));
    const SCEV *Scaled = Index;
    if (auto *Sum = dyn_cast<SCEVAddExpr>(Index); Sum && Sum->getNumOperands() == 2)
      if (auto *Constant = dyn_cast<SCEVConstant>(Sum->getOperand(0))) {
        Offset = Constant->getAPInt();
        Scaled = Sum->getOperand(1);
      }
    auto *Product = dyn_cast<SCEVMulExpr>(Scaled);
    auto *Factor = Product && Product->getNumOperands() == 2 ? dyn_cast<SCEVConstant>(Product->getOperand(0))
                                                             : nullptr;
    if (!Factor || !Factor->getAPInt().ugt(Offset) || Depth > 9) return false;
    const SCEV *Multiplied = Product->getOperand(1);
    return anyFact(At, [&](CmpInst::Predicate Predicate, Value *Left, Value *Right) {
      if (Predicate == ICmpInst::ICMP_UGT) std::swap(Left, Right);
      if ((Predicate != ICmpInst::ICMP_ULT && Predicate != ICmpInst::ICMP_UGT) ||
          !evolution.isSCEVable(Left->getType()) || evolution.getSCEV(Left) != Multiplied)
        return false;
      auto *Quotient = dyn_cast<SCEVUDivExpr>(evolution.getSCEV(Right));
      return Quotient && Quotient->getRHS() == Factor && atMost(Quotient->getLHS(), Length, At, Depth + 1);
    });
  }

  bool boundedBy(CmpInst::Predicate Predicate, Value *Left, Value *Right, const SCEV *Index,
                 const SCEV *Length, Instruction *At, unsigned Depth, bool Strict) {
    // A signed comparison of values whose sign bits are clear is unsigned.
    if (ICmpInst::isSigned(Predicate)) {
      if (!nonNegative(Left, At) || !nonNegative(Right, At)) return false;
      Predicate = ICmpInst::getUnsignedPredicate(Predicate);
    }
    if (Predicate == ICmpInst::ICMP_UGT || Predicate == ICmpInst::ICMP_UGE) {
      std::swap(Left, Right);
      Predicate = ICmpInst::getSwappedPredicate(Predicate);
    }
    if (!evolution.isSCEVable(Left->getType()) || Left->getType() != Index->getType()) return false;
    if (Predicate == ICmpInst::ICMP_NE) return differsBelow(Left, Right, Index, Length, At, Depth, Strict);
    if (Predicate != ICmpInst::ICMP_ULT && Predicate != ICmpInst::ICMP_ULE) return false;
    const SCEV *Guarded = evolution.getSCEV(Left);
    const SCEV *Bound = evolution.getSCEV(Right);
    const SCEV *Excess = evolution.getMinusSCEV(Index, Guarded);
    // Index may be the guarded value minus a constant that does not wrap,
    // so Index <u Guarded <=u Bound, or one more than a strictly guarded
    // value: then Index <=u Bound.
    if (Index != Guarded) {
      if (Predicate == ICmpInst::ICMP_ULT && Excess->isOne())
        Predicate = ICmpInst::ICMP_ULE;
      else if (!isa<SCEVConstant>(Excess) ||
               !gapFits(evolution.getNegativeSCEV(Excess), Guarded, At))
        return false;
      else
        Predicate = ICmpInst::ICMP_ULT;
    }
    if (Predicate == ICmpInst::ICMP_ULE)
      return Strict ? below(Bound, Length, At, Depth + 1) : atMost(Bound, Length, At, Depth + 1);
    // Index <u Bound also puts Index at most Bound - 1, which cannot wrap.
    return atMost(Bound, Length, At, Depth + 1) ||
           (!Strict && atMost(evolution.getMinusSCEV(Bound, evolution.getOne(Bound->getType())),
                              Length, At, Depth + 1));
  }

  bool nonNegative(Value *V, Instruction *At) {
    if (!evolution.isSCEVable(V->getType())) return false;
    const SCEV *Value = evolution.getSCEV(V);
    return evolution.isKnownNonNegative(Value) || !highest(Value, At).isSignBitSet();
  }

  // Index = X - K with 1 <=u K <=u X lies in [0, X), below any Length >= X.
  // K is a negated term, or one plus the negated constant of the sum.
  bool differenceBelow(const SCEV *Index, const SCEV *Length, Instruction *At, unsigned Depth) {
    auto *Sum = dyn_cast<SCEVAddExpr>(Index);
    if (!Sum || Depth > 9) return false;
    auto *Constant = dyn_cast<SCEVConstant>(Sum->getOperand(0));
    return llvm::any_of(Sum->operands(), [&](const SCEV *Term) {
      auto *Negated = dyn_cast<SCEVMulExpr>(Term);
      if (!Negated || Negated->getNumOperands() != 2 || !Negated->getOperand(0)->isAllOnesValue()) return false;
      SmallVector<const SCEV *, 2> Subtracted{Negated->getOperand(1)};
      if (Constant && Constant->getAPInt().isNegative())
        Subtracted.push_back(evolution.getMinusSCEV(Negated->getOperand(1), Constant));
      return llvm::any_of(Subtracted, [&](const SCEV *K) {
        const SCEV *Minuend = evolution.getAddExpr(Index, K);
        return !lowest(K, At).isZero() && atMost(K, Minuend, At, Depth + 1) &&
               atMost(Minuend, Length, At, Depth + 1);
      });
    });
  }
};
} // namespace ElisaIndexBounds
