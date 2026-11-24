// Value ranges for the index prover (ElisaIndexBounds::Prover): what a
// value can be where an instruction runs, from scalar evolution's range,
// the comparisons decided on dominating branch edges (transitively, through
// other values' largest values), the paths reaching the point
// (ElisaPathRanges), joins whose edges each decided their incoming value,
// and comparisons between loop-header phis that hold every time the
// header runs (`left <u right` in a two-pointer scan). Header orders are
// settled on entry by the prover's own proof.
#include "llvm_index_shapes.h"
#include <llvm/IR/InstIterator.h>
#include <llvm/IR/IntrinsicInst.h>
namespace ElisaIndexBounds {
using namespace llvm;

// Whether F writes an all-ones value (a -1 meaning "none"): a constant
// store of at least 16 bits or a 0xFF byte fill. Without one there is no
// sentinel for ElisaIndexBounds::Prover::sentinelBelow to exclude.
static bool writesAllOnes(Function &F) {
  return llvm::any_of(instructions(F), [](Instruction &I) {
    if (auto *Store = dyn_cast<StoreInst>(&I))
      if (auto *Constant = dyn_cast<ConstantInt>(Store->getValueOperand()))
        return Constant->isMinusOne() && Constant->getBitWidth() >= 16;
    if (auto *Fill = dyn_cast<MemSetInst>(&I))
      if (auto *Byte = dyn_cast<ConstantInt>(Fill->getValue())) return Byte->isMinusOne();
    return false;
  });
}

struct Order {
  Value *Low, *High; // Low <u High
};

class Ranges {
public:
  Ranges(ScalarEvolution &Evolution, DominatorTree &Tree, LoopInfo &Loops)
      : evolution(Evolution), tree(Tree), loops(Loops) {}
  virtual ~Ranges() = default;

protected:
  ScalarEvolution &evolution;
  DominatorTree &tree;
  LoopInfo &loops;
  // The prover's remaining work, and the header phis its induction assumes
  // below a length on their back edges.
  unsigned budget = 0;
  SmallVector<std::pair<PHINode *, const SCEV *>, 8> assumed;

  // Index <u Length wherever At executes (the prover's proof).
  virtual bool below(const SCEV *Index, const SCEV *Length, Instruction *At, unsigned Depth = 0) = 0;

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
      for (auto [Compare, Truth] : ElisaDeadTrapExits::edgeFacts(Latches[0], &H)) {
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
      } else if ((Predicate == ICmpInst::ICMP_UGT || Predicate == ICmpInst::ICMP_UGE) && Depth < 3 &&
                 !isa<Constant>(Left)) {
        // Above another value: at least its smallest (`left > target`
        // makes left nonzero).
        ConstantRange Below = within(evolution.getSCEV(Right), Block->getTerminator(), Depth + 1);
        if (Below.isEmptySet()) return;
        APInt Low = Below.getUnsignedMin();
        if (Predicate == ICmpInst::ICMP_UGT && Low.isMaxValue()) Holds = ConstantRange::getEmpty(Width);
        else Holds = ConstantRange::getNonEmpty(Predicate == ICmpInst::ICMP_UGT ? Low + 1 : Low, APInt::getZero(Width));
      }
      Range = Range.intersectWith(Holds.sub(ConstantRange(Offset->getAPInt())), ConstantRange::Unsigned);
    };
    for (DomTreeNode *Node = tree.getNode(Block); Node; Node = Node->getIDom())
      for (BasicBlock *Predecessor : predecessors(Node->getBlock())) {
        if (!tree.dominates(BasicBlockEdge(Predecessor, Node->getBlock()), Block)) continue;
        for (auto [Compare, Truth] : ElisaDeadTrapExits::edgeFacts(Predecessor, Node->getBlock())) {
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
    // A join takes one of its incoming values, each within what its edge decided.
    if (Merge && !loops.isLoopHeader(Merge->getParent()) && Depth < 6 && Merge->getNumIncomingValues() <= 4) {
      ConstantRange Joined = ConstantRange::getEmpty(Width);
      for (unsigned Edge = 0; Edge < Merge->getNumIncomingValues(); ++Edge)
        if (llvm::Value *In = Merge->getIncomingValue(Edge), *Base = In; BasicBlock *From = Merge->getIncomingBlock(Edge)) {
          // Scalar evolution drops an increment's `nuw` it cannot prove from context.
          const APInt *Step = ElisaIndexShapes::nuwIncrement(In, Base);
          ConstantRange Own = within(evolution.getSCEV(Base), From->getTerminator(), Depth + 1);
          Own = Step ? Own.addWithNoWrap(ConstantRange(*Step), OverflowingBinaryOperator::NoUnsignedWrap) : Own;
          Joined = Joined.unionWith(Own.intersectWith(
              ElisaIndexShapes::edgeRange(ElisaDeadTrapExits::edgeFacts(From, Merge->getParent()), In)));
        }
      OnPaths = OnPaths.intersectWith(Joined, ConstantRange::Unsigned);
    }
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

private:
  ElisaPathRanges::PathRanges paths{evolution, tree, loops};
  DenseMap<BasicBlock *, SmallVector<Order, 2>> headerOrders;
  SmallPtrSet<BasicBlock *, 4> ordering;
  DenseMap<std::pair<const SCEV *, BasicBlock *>, ConstantRange> decidedRanges;
  DenseSet<std::pair<const SCEV *, BasicBlock *>> deciding;
};
} // namespace ElisaIndexBounds
