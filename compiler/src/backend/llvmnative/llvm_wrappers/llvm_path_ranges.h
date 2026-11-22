// The unsigned values an integer can have at a block, from the branch edges
// on every path there. Walking up the dominator tree, an edge that is the
// only way into a block narrows the range by its comparisons; at a join the
// range is the union over the incoming edges, each followed to its own
// dominators, so a path whose comparisons contradict each other adds
// nothing. A loop's back edges are skipped for values defined before the
// loop: the value is unchanged since an entry edge narrowed it. Lazy value
// info computes the same lattice but gives up on values reaching a block
// through an earlier loop, the common shape after a sort.
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/PatternMatch.h>
namespace ElisaPathRanges {
using namespace llvm;

class PathRanges {
public:
  PathRanges(ScalarEvolution &Evolution, DominatorTree &Tree, LoopInfo &Loops)
      : evolution(Evolution), tree(Tree), loops(Loops) {}

  ConstantRange at(Value *V, BasicBlock *Block, unsigned Depth = 0) {
    if (auto Known = memo.find({V, Block}); Known != memo.end()) return Known->second;
    ConstantRange Range = walk(V, Block, Depth);
    memo.try_emplace({V, Block}, Range);
    return Range;
  }

private:
  ScalarEvolution &evolution;
  DominatorTree &tree;
  LoopInfo &loops;
  DenseMap<std::pair<Value *, BasicBlock *>, ConstantRange> memo;

  ConstantRange walk(Value *V, BasicBlock *Block, unsigned Depth) {
    unsigned Width = V->getType()->getIntegerBitWidth();
    ConstantRange Range = ConstantRange::getFull(Width);
    auto *Definition = dyn_cast<Instruction>(V);
    for (DomTreeNode *Node = tree.getNode(Block); Node; Node = Node->getIDom()) {
      BasicBlock *Dominator = Node->getBlock();
      // No edge above the definition can compare the value.
      if (Definition && Definition->getParent() == Dominator) break;
      SmallVector<BasicBlock *, 4> Entries;
      Loop *L = loops.getLoopFor(Dominator);
      for (BasicBlock *Predecessor : predecessors(Dominator))
        if (!(L && L->getHeader() == Dominator && L->contains(Predecessor)))
          Entries.push_back(Predecessor);
      if (Entries.size() == 1) {
        Range = Range.intersectWith(onEdge(V, Entries[0], Dominator), ConstantRange::Unsigned);
        continue;
      }
      if (Entries.empty() || Depth >= 12) continue;
      ConstantRange Merged = ConstantRange::getEmpty(Width);
      for (BasicBlock *Entry : Entries)
        Merged = Merged.unionWith(
            onEdge(V, Entry, Dominator).intersectWith(at(V, Entry, Depth + 1), ConstantRange::Unsigned),
            ConstantRange::Unsigned);
      return Range.intersectWith(Merged, ConstantRange::Unsigned);
    }
    return Range;
  }

  // The values the comparisons decided on From -> To leave V.
  ConstantRange onEdge(Value *V, BasicBlock *From, BasicBlock *To) {
    using namespace PatternMatch;
    unsigned Width = V->getType()->getIntegerBitWidth();
    ConstantRange Range = ConstantRange::getFull(Width);
    auto *Branch = dyn_cast<CondBrInst>(From->getTerminator());
    if (!Branch || Branch->getSuccessor(0) == Branch->getSuccessor(1)) return Range;
    SmallVector<std::pair<ICmpInst *, bool>, 4> Facts;
    ElisaDeadTrapExits::decided(Branch->getCondition(), Branch->getSuccessor(0) == To, Facts);
    for (auto [Compare, Truth] : Facts) {
      auto Predicate = Truth ? Compare->getPredicate() : Compare->getInversePredicate();
      Value *Left = Compare->getOperand(0), *Right = Compare->getOperand(1);
      if (Right == V || (isa<Constant>(Left) && !isa<Constant>(Right))) {
        std::swap(Left, Right);
        Predicate = ICmpInst::getSwappedPredicate(Predicate);
      }
      if (Left->getType() != V->getType()) continue;
      Range = Range.intersectWith(region(V, Predicate, Left, Right), ConstantRange::Unsigned);
    }
    return Range;
  }

  // The values of V for which `Left Predicate Right` holds.
  ConstantRange region(Value *V, CmpInst::Predicate Predicate, Value *Left, Value *Right) {
    using namespace PatternMatch;
    unsigned Width = V->getType()->getIntegerBitWidth();
    // V == Other: V lies in Other's range.
    if (Left == V && Predicate == ICmpInst::ICMP_EQ && evolution.isSCEVable(Right->getType()))
      return evolution.getUnsignedRange(evolution.getSCEV(Right));
    const APInt *Bound = nullptr, *Offset = nullptr, *Shift = nullptr;
    if (!match(Right, m_APInt(Bound))) return ConstantRange::getFull(Width);
    ConstantRange Holds = ConstantRange::makeExactICmpRegion(Predicate, *Bound);
    if (Left == V) return Holds;
    if (match(Left, m_Add(m_Specific(V), m_APInt(Offset))))
      return Holds.sub(ConstantRange(*Offset));
    // V >> k in [a, b] puts V in [a << k, (b << k) + 2^k - 1].
    if (match(Left, m_LShr(m_Specific(V), m_APInt(Shift))) && Shift->ult(Width) && !Holds.isEmptySet()) {
      unsigned K = Shift->getZExtValue();
      APInt Top = APInt::getMaxValue(Width).lshr(K);
      APInt Low = APIntOps::umin(Holds.getUnsignedMin(), Top).shl(K);
      APInt High = APIntOps::umin(Holds.getUnsignedMax(), Top).shl(K) | APInt::getLowBitsSet(Width, K);
      return ConstantRange::getNonEmpty(Low, High + 1);
    }
    return ConstantRange::getFull(Width);
  }
};
} // namespace ElisaPathRanges
