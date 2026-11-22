// The unsigned values an integer can have at a block, from the branch edges
// on every path there. Walking up the dominator tree, an edge that is the
// only way into a block narrows the range by its comparisons; at a join the
// range is the union over the incoming edges, each followed to its own
// dominators, so a path whose comparisons contradict each other adds
// nothing. A loop's back edges are skipped for values defined before the
// loop: the value is unchanged since an entry edge narrowed it. Lazy value
// info computes the same lattice but gives up on values reaching a block
// through an earlier loop, the common shape after a sort. An edge taken
// only when `trunc V to i1` is false leaves V's even values.
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
    bool Even = false;
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
        Even = Even || evenOnEdge(V, Entries[0], Dominator);
        continue;
      }
      if (Entries.empty() || Depth >= 12) continue;
      ConstantRange Merged = ConstantRange::getEmpty(Width);
      for (BasicBlock *Entry : Entries)
        Merged = Merged.unionWith(
            onEdge(V, Entry, Dominator).intersectWith(at(V, Entry, Depth + 1), ConstantRange::Unsigned),
            ConstantRange::Unsigned);
      return evenOnly(Range.intersectWith(Merged, ConstantRange::Unsigned), Even);
    }
    return evenOnly(Range, Even);
  }

  // The even values of Range, for a value whose low bit an edge cleared.
  static ConstantRange evenOnly(const ConstantRange &Range, bool Even) {
    if (!Even || Range.isEmptySet()) return Range;
    APInt Low = Range.getUnsignedMin(), High = Range.getUnsignedMax();
    if (Low[0]) {
      if (Low.isMaxValue()) return ConstantRange::getEmpty(Low.getBitWidth());
      ++Low;
    }
    High.clearBit(0);
    if (Low.ugt(High)) return ConstantRange::getEmpty(Low.getBitWidth());
    return Range.intersectWith(ConstantRange::getNonEmpty(Low, High + 1), ConstantRange::Unsigned);
  }

  // Whether From -> To is taken only when `trunc V to i1` is false.
  bool evenOnEdge(Value *V, BasicBlock *From, BasicBlock *To) {
    auto *Branch = dyn_cast<CondBrInst>(From->getTerminator());
    return Branch && Branch->getSuccessor(0) != Branch->getSuccessor(1) &&
           lowBitClear(Branch->getCondition(), Branch->getSuccessor(0) == To, V, 0);
  }

  static bool lowBitClear(Value *Condition, bool Truth, Value *V, unsigned Depth) {
    using namespace PatternMatch;
    Value *Left = nullptr, *Right = nullptr;
    if (!Truth && Condition->getType()->isIntegerTy(1) && match(Condition, m_Trunc(m_Specific(V))))
      return true;
    if (Depth < 6 && ((Truth && match(Condition, m_LogicalAnd(m_Value(Left), m_Value(Right)))) ||
                      (!Truth && match(Condition, m_LogicalOr(m_Value(Left), m_Value(Right))))))
      return lowBitClear(Left, Truth, V, Depth + 1) || lowBitClear(Right, Truth, V, Depth + 1);
    return false;
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
    const APInt *Bound = nullptr;
    if (!match(Right, m_APInt(Bound))) return ConstantRange::getFull(Width);
    return preimage(V, Left, ConstantRange::makeExactICmpRegion(Predicate, *Bound), 0);
  }

  // The values of V for which Image, a chain of constant additions and
  // right shifts of V, lies in Holds.
  static ConstantRange preimage(Value *V, Value *Image, ConstantRange Holds, unsigned Depth) {
    using namespace PatternMatch;
    unsigned Width = V->getType()->getIntegerBitWidth();
    const APInt *Offset = nullptr, *Shift = nullptr;
    Value *Inner = nullptr;
    if (Image == V) return Holds;
    if (Depth >= 4 || Holds.isEmptySet()) return ConstantRange::getFull(Width);
    if (match(Image, m_Add(m_Value(Inner), m_APInt(Offset))))
      return preimage(V, Inner, Holds.sub(ConstantRange(*Offset)), Depth + 1);
    // Inner >> k in [a, b] puts Inner in [a << k, (b << k) + 2^k - 1].
    if (match(Image, m_LShr(m_Value(Inner), m_APInt(Shift))) && Shift->ult(Width)) {
      unsigned K = Shift->getZExtValue();
      APInt Top = APInt::getMaxValue(Width).lshr(K);
      APInt Low = APIntOps::umin(Holds.getUnsignedMin(), Top).shl(K);
      APInt High = APIntOps::umin(Holds.getUnsignedMax(), Top).shl(K) | APInt::getLowBitsSet(Width, K);
      return preimage(V, Inner, ConstantRange::getNonEmpty(Low, High + 1), Depth + 1);
    }
    return ConstantRange::getFull(Width);
  }
};
} // namespace ElisaPathRanges
