// Prove Index <u Length for integer values at a program point. The proof
// combines scalar evolution with facts SCEV does not chain on its own:
// comparisons decided on dominating branch edges (transitively), lower
// bounds read from those edges, the extremes of affine recurrences, and
// induction over loop-header phis. A header phi is below a loop-invariant
// Length when every incoming value is, assuming the phi itself is below it
// on the back edges; several such assumptions are discharged together, as
// one induction over the execution of their headers. Comparisons between
// header phis that hold on every incoming edge (`left <u right` in a
// two-pointer scan) are facts wherever the header dominates.
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
namespace ElisaIndexBounds {
using namespace llvm;

struct Order {
  Value *Low, *High; // Low <u High
};

class Prover {
public:
  Prover(ScalarEvolution &Evolution, DominatorTree &Tree, LoopInfo &Loops)
      : evolution(Evolution), tree(Tree), loops(Loops) {}
  virtual ~Prover() = default;

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
  ScalarEvolution &evolution;
  DominatorTree &tree;
  LoopInfo &loops;

  // Reads whose values another analysis bounds.
  virtual bool loadBelow(LoadInst &, const SCEV *, unsigned) { return false; }

  bool below(const SCEV *Index, const SCEV *Length, Instruction *At, unsigned Depth = 0) {
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
    // a /u c with c > 1 is below a whenever a is nonzero.
    if (auto *Quotient = dyn_cast<SCEVUDivExpr>(Index))
      if (auto *Divisor = dyn_cast<SCEVConstant>(Quotient->getRHS());
          Divisor && Divisor->getAPInt().ugt(1) && !lowest(Quotient->getLHS(), At).isZero() &&
          atMost(Quotient->getLHS(), Length, At, Depth + 1))
        return true;
    if (auto *Recurrence = dyn_cast<SCEVAddRecExpr>(Index))
      return recurrenceBelow(*Recurrence, Length, Depth);
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
           phiAtMost(Low, High, Depth);
  }

private:
  unsigned budget = 0;
  ElisaPathRanges::PathRanges paths{evolution, tree, loops};
  SmallVector<std::pair<PHINode *, const SCEV *>, 8> assumed;
  SmallVector<std::pair<PHINode *, const SCEV *>, 8> assumedAtMost;
  DenseMap<BasicBlock *, SmallVector<Order, 2>> headerOrders;
  SmallPtrSet<BasicBlock *, 4> ordering;
  DenseMap<std::pair<const SCEV *, BasicBlock *>, ConstantRange> decidedRanges;
  DenseSet<std::pair<const SCEV *, BasicBlock *>> deciding;

  bool valueBelow(Value *V, const SCEV *Length, Instruction *At, unsigned Depth) {
    return evolution.isSCEVable(V->getType()) && below(evolution.getSCEV(V), Length, At, Depth + 1);
  }

  bool recurrenceBelow(const SCEVAddRecExpr &Recurrence, const SCEV *Length, unsigned Depth) {
    return evolution.isLoopInvariant(Length, Recurrence.getLoop()) &&
           recurrenceWithin(Recurrence, Depth, [&](const SCEV *Top, Instruction *Entry) {
             return below(Top, Length, Entry, Depth + 1);
           });
  }

  // Whether every value of Recurrence's loop is at most its largest, Top,
  // and Top satisfies Within at the loop's entry.
  template <typename Predicate>
  bool recurrenceWithin(const SCEVAddRecExpr &Recurrence, unsigned Depth, Predicate Within) {
    if (Depth > 9) return false;
    const SCEV *Count = evolution.getSymbolicMaxBackedgeTakenCount(Recurrence.getLoop());
    const SCEV *Start = Recurrence.getStart();
    const SCEV *Step = Recurrence.isAffine() ? Recurrence.getStepRecurrence(evolution) : nullptr;
    if (isa<SCEVCouldNotCompute>(Count) || !Step || Count->getType() != Start->getType())
      return false;
    Instruction *Entry = &*Recurrence.getLoop()->getHeader()->getFirstInsertionPt();
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
    auto *Quotient = Product && Product->getNumOperands() == 2 ? dyn_cast<SCEVUDivExpr>(Product->getOperand(1))
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

  // X + 1 <=u High when X <u High: the increment cannot wrap.
  bool successorAtMost(const SCEV *Low, const SCEV *High, Instruction *At, unsigned Depth) {
    auto *Sum = dyn_cast<SCEVAddExpr>(Low);
    auto *One = Sum && Sum->getNumOperands() == 2 ? dyn_cast<SCEVConstant>(Sum->getOperand(0)) : nullptr;
    return One && One->isOne() && below(Sum->getOperand(1), High, At, Depth + 1);
  }

  // Index != Bound on a dominating edge, with Bound settling to Length and
  // Index <=u Length: then Index <u Length. Loop exits test `!=` against
  // the trip limit once the comparison is canonicalized.
  bool differsBelow(Value *Left, Value *Right, const SCEV *Index, const SCEV *Length,
                    Instruction *At, unsigned Depth, bool Strict) {
    if (!Strict || Depth > 8) return false;
    const SCEV *First = evolution.getSCEV(Left), *Second = evolution.getSCEV(Right);
    const SCEV *Other = First == Index ? Second : Second == Index ? First : nullptr;
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
              llvm::any_of(edgeFacts(From, Merge.getParent()), [&](auto Fact) {
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
        for (auto [Compare, Truth] : edgeFacts(Predecessor, Dominator))
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

  static SmallVector<std::pair<ICmpInst *, bool>, 4> edgeFacts(BasicBlock *From, BasicBlock *To) {
    SmallVector<std::pair<ICmpInst *, bool>, 4> Facts;
    auto *Branch = dyn_cast<CondBrInst>(From->getTerminator());
    if (Branch && Branch->getSuccessor(0) != Branch->getSuccessor(1))
      ElisaDeadTrapExits::decided(Branch->getCondition(), Branch->getSuccessor(0) == To, Facts);
    return Facts;
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
    // or one more than a strictly guarded value: then Index <=u Bound.
    if (Index != Guarded) {
      if (Predicate == ICmpInst::ICMP_ULT && Excess->isOne())
        Predicate = ICmpInst::ICMP_ULE;
      else if (!isa<SCEVConstant>(Excess) ||
               !gapFits(evolution.getNegativeSCEV(Excess), Guarded, At))
        return false;
    }
    if (Predicate == ICmpInst::ICMP_ULE)
      return Strict ? below(Bound, Length, At, Depth + 1) : atMost(Bound, Length, At, Depth + 1);
    // Index <u Bound also puts Index at most Bound - 1, which cannot wrap.
    return atMost(Bound, Length, At, Depth + 1) ||
           (!Strict && atMost(evolution.getMinusSCEV(Bound, evolution.getOne(Bound->getType())),
                              Length, At, Depth + 1));
  }

  bool nonNegative(Value *V, Instruction *At) {
    return evolution.isSCEVable(V->getType()) && !highest(evolution.getSCEV(V), At).isSignBitSet();
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

  // Comparisons Low <u High between values at loop header H that hold
  // every time H runs: decided on each back edge for the incoming values,
  // and proved on the entry edges. Proofs run without assumptions, so the
  // facts are unconditional and can be cached.
  ArrayRef<Order> orders(BasicBlock &H) {
    if (auto Found = headerOrders.find(&H); Found != headerOrders.end()) return Found->second;
    if (!ordering.insert(&H).second) return {};
    Loop *L = loops.getLoopFor(&H);
    SmallVector<Order, 2> Facts;
    SmallVector<BasicBlock *, 2> Latches;
    L->getLoopLatches(Latches);
    if (Latches.size() == 1)
      for (auto [Compare, Truth] : edgeFacts(Latches[0], &H)) {
        auto Predicate = Truth ? Compare->getPredicate() : Compare->getInversePredicate();
        Value *Low = Compare->getOperand(0), *High = Compare->getOperand(1);
        if (Predicate == ICmpInst::ICMP_UGT) std::swap(Low, High);
        if (Predicate != ICmpInst::ICMP_ULT && Predicate != ICmpInst::ICMP_UGT) continue;
        PHINode *LowPhi = headerPhi(H, *Latches[0], Low);
        Value *HighAtHeader = headerPhi(H, *Latches[0], High);
        if (!HighAtHeader && L->isLoopInvariant(High)) HighAtHeader = High;
        if (LowPhi && HighAtHeader && holdsOnEntry(H, *L, *LowPhi, HighAtHeader))
          Facts.push_back({LowPhi, HighAtHeader});
      }
    ordering.erase(&H);
    return headerOrders[&H] = std::move(Facts);
  }

  // The header phi whose incoming value from Latch is V.
  static PHINode *headerPhi(BasicBlock &H, BasicBlock &Latch, Value *V) {
    for (PHINode &Merge : H.phis())
      if (Merge.getIncomingValueForBlock(&Latch) == V) return &Merge;
    return nullptr;
  }

  bool holdsOnEntry(BasicBlock &H, Loop &L, PHINode &Low, Value *High) {
    auto Saved = std::move(assumed);
    assumed.clear();
    // The result is cached, so it must not depend on the budget the
    // current proof has left.
    unsigned Remaining = std::exchange(budget, 4000);
    bool Holds = llvm::all_of(predecessors(&H), [&](BasicBlock *Entry) {
      if (L.contains(Entry)) return true;
      Value *LowIn = Low.getIncomingValueForBlock(Entry);
      auto *HighPhi = dyn_cast<PHINode>(High);
      Value *HighIn = HighPhi && HighPhi->getParent() == &H ? HighPhi->getIncomingValueForBlock(Entry)
                                                           : High;
      // With a single entry, the entry edge's own comparisons hold at H.
      Instruction *At = L.getLoopPredecessor() == Entry ? &*H.getFirstInsertionPt() : Entry->getTerminator();
      return evolution.isSCEVable(LowIn->getType()) &&
             below(evolution.getSCEV(LowIn), evolution.getSCEV(HighIn), At);
    });
    assumed = std::move(Saved);
    budget = Remaining;
    return Holds;
  }

  // Gap <=u High: a constant High is known to reach, or an unsigned minimum
  // one of whose operands is High itself or such a gap.
  bool gapFits(const SCEV *Gap, const SCEV *High, Instruction *At, unsigned Depth = 0) {
    if (Gap == High) return true;
    if (auto *Constant = dyn_cast<SCEVConstant>(Gap))
      return !Constant->getAPInt().isNegative() && lowest(High, At).uge(Constant->getAPInt());
    if (auto *Minimum = dyn_cast<SCEVUMinExpr>(Gap); Minimum && Depth < 4)
      return llvm::any_of(Minimum->operands(), [&](const SCEV *Operand) {
        return gapFits(Operand, High, At, Depth + 1);
      });
    return false;
  }

  // The values Expression can have after the dominating edges that compared
  // Expression plus a constant with a constant, or bounded it from above by
  // another value's largest value.
  ConstantRange decided(const SCEV *Expression, BasicBlock *Block, unsigned Depth) {
    unsigned Width = evolution.getTypeSizeInBits(Expression->getType());
    if (isa<SCEVConstant>(Expression) || !deciding.insert({Expression, Block}).second)
      return ConstantRange::getFull(Width);
    if (auto Known = decidedRanges.find({Expression, Block}); Depth == 0 && Known != decidedRanges.end()) {
      deciding.erase({Expression, Block});
      return Known->second;
    }
    ConstantRange Range = ConstantRange::getFull(Width);
    auto Narrow = [&](CmpInst::Predicate Predicate, Value *Left, Value *Right) {
      if (!evolution.isSCEVable(Left->getType()) || evolution.getTypeSizeInBits(Left->getType()) != Width) return;
      auto *Offset = dyn_cast<SCEVConstant>(evolution.getMinusSCEV(evolution.getSCEV(Left), Expression));
      if (!Offset) return;
      ConstantRange Holds = ConstantRange::getFull(Width);
      if (const APInt *Bound = nullptr; PatternMatch::match(Right, PatternMatch::m_APInt(Bound))) {
        Holds = ConstantRange::makeExactICmpRegion(Predicate, *Bound);
      } else if ((Predicate == ICmpInst::ICMP_ULT || Predicate == ICmpInst::ICMP_ULE) && Depth < 3 &&
                 !isa<Constant>(Left)) {
        ConstantRange Above = within(evolution.getSCEV(Right), Block->getTerminator(), Depth + 1);
        if (Above.isEmptySet()) return;
        APInt High = Above.getUnsignedMax();
        if (Predicate == ICmpInst::ICMP_ULT && High.isZero()) Holds = ConstantRange::getEmpty(Width);
        else Holds = ConstantRange::getNonEmpty(APInt::getZero(Width), Predicate == ICmpInst::ICMP_ULT ? High : High + 1);
      }
      Range = Range.intersectWith(Holds.sub(ConstantRange(Offset->getAPInt())), ConstantRange::Unsigned);
    };
    for (DomTreeNode *Node = tree.getNode(Block); Node; Node = Node->getIDom())
      for (BasicBlock *Predecessor : predecessors(Node->getBlock())) {
        if (!tree.dominates(BasicBlockEdge(Predecessor, Node->getBlock()), Block)) continue;
        for (auto [Compare, Truth] : edgeFacts(Predecessor, Node->getBlock())) {
          auto Predicate = Truth ? Compare->getPredicate() : Compare->getInversePredicate();
          Narrow(Predicate, Compare->getOperand(0), Compare->getOperand(1));
          Narrow(ICmpInst::getSwappedPredicate(Predicate), Compare->getOperand(1), Compare->getOperand(0));
        }
      }
    deciding.erase({Expression, Block});
    if (Depth == 0) decidedRanges.try_emplace({Expression, Block}, Range);
    return Range;
  }

  // Where At never executes, every bound holds: lowest is the maximum and
  // highest is zero.
  APInt lowest(const SCEV *Value, Instruction *At) {
    ConstantRange Range = within(Value, At);
    return Range.isEmptySet() ? APInt::getMaxValue(Range.getBitWidth()) : Range.getUnsignedMin();
  }
  APInt highest(const SCEV *Value, Instruction *At) {
    ConstantRange Range = within(Value, At);
    return Range.isEmptySet() ? APInt::getZero(Range.getBitWidth()) : Range.getUnsignedMax();
  }

  // The values Value can have where At executes, from its range, from
  // comparisons on the paths to At, and from header orders that place
  // another value below it, through sums, products and quotients with a
  // constant.
  ConstantRange within(const SCEV *Value, Instruction *At, unsigned Depth = 0) {
    ConstantRange Range = evolution.getUnsignedRange(Value).intersectWith(decided(Value, At->getParent(), Depth),
                                                                          ConstantRange::Unsigned);
    if (auto *Quotient = dyn_cast<SCEVUDivExpr>(Value); Quotient && Depth < 8)
      if (auto *Divisor = dyn_cast<SCEVConstant>(Quotient->getRHS()); Divisor && !Divisor->isZero())
        return Range.intersectWith(within(Quotient->getLHS(), At, Depth + 1).udiv(ConstantRange(Divisor->getAPInt())),
                                   ConstantRange::Unsigned);
    if (auto *Sum = dyn_cast<SCEVAddExpr>(Value); Sum && Sum->getNumOperands() == 2 && Depth < 8)
      if (auto *Constant = dyn_cast<SCEVConstant>(Sum->getOperand(0)))
        return Range.intersectWith(within(Sum->getOperand(1), At, Depth + 1).add(ConstantRange(Constant->getAPInt())),
                                   ConstantRange::Unsigned);
    if (auto *Product = dyn_cast<SCEVMulExpr>(Value); Product && Product->getNumOperands() == 2 && Depth < 8)
      if (auto *Constant = dyn_cast<SCEVConstant>(Product->getOperand(0)))
        return Range.intersectWith(
            within(Product->getOperand(1), At, Depth + 1).multiply(ConstantRange(Constant->getAPInt())),
            ConstantRange::Unsigned);
    auto *Unknown = dyn_cast<SCEVUnknown>(Value);
    if (!Unknown) return Range;
    unsigned Width = Range.getBitWidth();
    // Paths whose comparisons contradict each other never reach At.
    ConstantRange OnPaths = paths.at(Unknown->getValue(), At->getParent());
    if (OnPaths.isEmptySet()) return ConstantRange::getEmpty(Width);
    auto *Merge = dyn_cast<PHINode>(Unknown->getValue());
    if (!Merge || !loops.isLoopHeader(Merge->getParent()) || !tree.dominates(Merge->getParent(), At->getParent()))
      return Range.intersectWith(OnPaths, ConstantRange::Unsigned);
    // A header order Low <u High puts High at least one and Low at most
    // High's largest value less one.
    for (const Order &Fact : orders(*Merge->getParent())) {
      if (Fact.High == Merge)
        OnPaths = OnPaths.intersectWith(ConstantRange::getNonEmpty(APInt(Width, 1), APInt::getZero(Width)),
                                        ConstantRange::Unsigned);
      if (Fact.Low == Merge && Depth < 8 && evolution.isSCEVable(Fact.High->getType()))
        if (ConstantRange Above = within(evolution.getSCEV(Fact.High), At, Depth + 1);
            !Above.isEmptySet() && !Above.getUnsignedMax().isZero())
          OnPaths = OnPaths.intersectWith(ConstantRange::getNonEmpty(APInt::getZero(Width), Above.getUnsignedMax()),
                                          ConstantRange::Unsigned);
    }
    return Range.intersectWith(OnPaths, ConstantRange::Unsigned);
  }
};
} // namespace ElisaIndexBounds
