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
        evolution.getUnsignedRange(Index).getUnsignedMax().ult(lowest(Length, At)))
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
    if (guarded(Index, Length, At, Depth, true)) return true;
    // Index = X - c with X >= c is at most X.
    if (auto *Sum = dyn_cast<SCEVAddExpr>(Index); Sum && Sum->getNumOperands() == 2)
      if (auto *Constant = dyn_cast<SCEVConstant>(Sum->getOperand(0));
          Constant && Constant->getAPInt().isNegative() &&
          gapFits(evolution.getNegativeSCEV(Constant), Sum->getOperand(1), At) &&
          below(Sum->getOperand(1), Length, At, Depth + 1))
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
    auto *Constant = dyn_cast<SCEVConstant>(Low);
    return Low == High || evolution.isKnownPredicateAt(ICmpInst::ICMP_ULE, Low, High, At) ||
           (Constant && lowest(High, At).uge(Constant->getAPInt())) ||
           gapFits(evolution.getMinusSCEV(High, Low), High, At) ||
           guarded(Low, High, At, Depth, false);
  }

private:
  unsigned budget = 0;
  ElisaPathRanges::PathRanges paths{evolution, tree, loops};
  SmallVector<std::pair<PHINode *, const SCEV *>, 8> assumed;
  DenseMap<BasicBlock *, SmallVector<Order, 2>> headerOrders;
  SmallPtrSet<BasicBlock *, 4> ordering;

  bool valueBelow(Value *V, const SCEV *Length, Instruction *At, unsigned Depth) {
    return evolution.isSCEVable(V->getType()) && below(evolution.getSCEV(V), Length, At, Depth + 1);
  }

  bool recurrenceBelow(const SCEVAddRecExpr &Recurrence, const SCEV *Length, unsigned Depth) {
    const SCEV *Count = evolution.getSymbolicMaxBackedgeTakenCount(Recurrence.getLoop());
    const SCEV *Start = Recurrence.getStart();
    const SCEV *Step = Recurrence.isAffine() ? Recurrence.getStepRecurrence(evolution) : nullptr;
    if (isa<SCEVCouldNotCompute>(Count) || !Step || Count->getType() != Start->getType())
      return false;
    Instruction *Entry = &*Recurrence.getLoop()->getHeader()->getFirstInsertionPt();
    if (Step->isOne()) {
      // Values Start + k for k <= Count, none wrapping past Start + Count;
      // Count is at most each operand of an unsigned minimum.
      SmallVector<const SCEV *, 4> Bounds{Count};
      if (auto *Minimum = dyn_cast<SCEVUMinExpr>(Count))
        Bounds.assign(Minimum->operands().begin(), Minimum->operands().end());
      return llvm::any_of(Bounds, [&](const SCEV *Bound) {
        const SCEV *Last = evolution.getAddExpr(Start, Bound);
        return atMost(Start, Last, Entry, Depth + 1) && below(Last, Length, Entry, Depth + 1);
      });
    }
    if (Step->isAllOnesValue())
      // Values Start - k for k <= Count <= Start never wrap below zero.
      return atMost(Count, Start, Entry, Depth + 1) && below(Start, Length, Entry, Depth + 1);
    return false;
  }

  // Every incoming value is below Length on its edge. A loop-header phi may
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
      return valueBelow(Merge.getIncomingValue(Index), Length,
                        Merge.getIncomingBlock(Index)->getTerminator(), Depth);
    });
    if (Header) assumed.pop_back();
    return Holds;
  }

  // A fact dominating At decided Index <u Bound or Index <=u Bound, and
  // Bound is in turn at most or below Length: Index <u Length when Strict,
  // else Index <=u Length. Facts come from branch edges and loop headers.
  bool guarded(const SCEV *Index, const SCEV *Length, Instruction *At, unsigned Depth,
               bool Strict) {
    if (Depth > 9 || budget == 0) return false;
    BasicBlock *Block = At->getParent();
    for (DomTreeNode *Node = tree.getNode(Block); Node; Node = Node->getIDom()) {
      BasicBlock *Dominator = Node->getBlock();
      if (loops.isLoopHeader(Dominator))
        for (const Order &Fact : orders(*Dominator))
          if (boundedBy(ICmpInst::ICMP_ULT, Fact.Low, Fact.High, Index, Length, At, Depth, Strict))
            return true;
      for (BasicBlock *Predecessor : predecessors(Dominator)) {
        if (!tree.dominates(BasicBlockEdge(Predecessor, Dominator), Block)) continue;
        for (auto [Compare, Truth] : edgeFacts(Predecessor, Dominator)) {
          auto Predicate = Truth ? Compare->getPredicate() : Compare->getInversePredicate();
          if (boundedBy(Predicate, Compare->getOperand(0), Compare->getOperand(1), Index, Length,
                        At, Depth, Strict))
            return true;
        }
      }
    }
    return false;
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
    if (Predicate == ICmpInst::ICMP_UGT || Predicate == ICmpInst::ICMP_UGE) {
      std::swap(Left, Right);
      Predicate = ICmpInst::getSwappedPredicate(Predicate);
    }
    if ((Predicate != ICmpInst::ICMP_ULT && Predicate != ICmpInst::ICMP_ULE) ||
        !evolution.isSCEVable(Left->getType()) || Left->getType() != Index->getType())
      return false;
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
    return Predicate == ICmpInst::ICMP_ULT || !Strict ? atMost(Bound, Length, At, Depth + 1)
                                                      : below(Bound, Length, At, Depth + 1);
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
    bool Holds = llvm::all_of(predecessors(&H), [&](BasicBlock *Entry) {
      if (L.contains(Entry)) return true;
      Value *LowIn = Low.getIncomingValueForBlock(Entry);
      auto *HighPhi = dyn_cast<PHINode>(High);
      Value *HighIn = HighPhi && HighPhi->getParent() == &H ? HighPhi->getIncomingValueForBlock(Entry)
                                                           : High;
      return evolution.isSCEVable(LowIn->getType()) &&
             below(evolution.getSCEV(LowIn), evolution.getSCEV(HighIn), Entry->getTerminator());
    });
    assumed = std::move(Saved);
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

  // The smallest value Value can have where At executes, from its range,
  // from comparisons on the paths to At, and from header orders that place
  // another value below it, for a value or a value plus a constant.
  APInt lowest(const SCEV *Value, Instruction *At) {
    ConstantRange Range = evolution.getUnsignedRange(Value);
    const SCEV *Base = Value;
    APInt Shift(Range.getBitWidth(), 0);
    if (auto *Sum = dyn_cast<SCEVAddExpr>(Value); Sum && Sum->getNumOperands() == 2)
      if (auto *Constant = dyn_cast<SCEVConstant>(Sum->getOperand(0))) {
        Shift = Constant->getAPInt();
        Base = Sum->getOperand(1);
      }
    if (auto *Unknown = dyn_cast<SCEVUnknown>(Base)) {
      unsigned Width = Range.getBitWidth();
      // Paths whose comparisons contradict each other never reach At.
      ConstantRange OnPaths = paths.at(Unknown->getValue(), At->getParent());
      APInt Low = OnPaths.isEmptySet() ? APInt::getMaxValue(Width) : OnPaths.getUnsignedMin();
      if (auto *Merge = dyn_cast<PHINode>(Unknown->getValue());
          Low.isZero() && Merge && loops.isLoopHeader(Merge->getParent()) &&
          tree.dominates(Merge->getParent(), At->getParent()) &&
          llvm::any_of(orders(*Merge->getParent()), [&](const Order &Fact) { return Fact.High == Merge; }))
        Low = APInt(Width, 1);
      if (!Low.isZero())
        Range = Range.intersectWith(
            ConstantRange::getNonEmpty(Low, APInt::getZero(Width)).add(ConstantRange(Shift)),
            ConstantRange::Unsigned);
    }
    return Range.getUnsignedMin();
  }
};
} // namespace ElisaIndexBounds
