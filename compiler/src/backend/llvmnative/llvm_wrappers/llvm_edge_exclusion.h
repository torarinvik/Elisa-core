// Values a branch edge rules out. An edge taken only when a comparison of V
// with a constant came out one way excludes every value that would have
// sent it the other way (`count < 0` leaving on failure leaves no -1 on
// the edge into the rest). Below V's block, such an edge into a block that
// dominates a point excludes the value at that point: every path from V's
// latest execution to the point takes the edge, so no later execution of
// V can bring the value back.
#include <llvm/IR/ConstantRange.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/PatternMatch.h>
namespace ElisaEdgeExclusion {
using namespace llvm;

// From -> To is taken only when V != Excluded.
static bool excludesOnEdge(Value *V, const APInt &Excluded, BasicBlock *From, BasicBlock *To) {
  auto *Branch = dyn_cast<CondBrInst>(From->getTerminator());
  if (!Branch || Branch->getSuccessor(0) == Branch->getSuccessor(1)) return false;
  SmallVector<std::pair<ICmpInst *, bool>, 4> Facts;
  ElisaDeadTrapExits::decided(Branch->getCondition(), Branch->getSuccessor(0) == To, Facts);
  for (auto [Compare, Truth] : Facts) {
    auto Predicate = Truth ? Compare->getPredicate() : Compare->getInversePredicate();
    Value *Left = Compare->getOperand(0), *Right = Compare->getOperand(1);
    if (Right == V) {
      std::swap(Left, Right);
      Predicate = ICmpInst::getSwappedPredicate(Predicate);
    }
    auto *Bound = dyn_cast<ConstantInt>(Right);
    if (Left == V && Bound && Bound->getBitWidth() == Excluded.getBitWidth() &&
        !ConstantRange::makeExactICmpRegion(Predicate, Bound->getValue()).contains(Excluded))
      return true;
  }
  return false;
}

// The value From -> To proves V equal to: a loop left when its counter
// reaches a limit V carries the counter's value on that edge.
static Value *equalOnEdge(Value *V, BasicBlock *From, BasicBlock *To) {
  auto *Branch = dyn_cast<CondBrInst>(From->getTerminator());
  if (!Branch || Branch->getSuccessor(0) == Branch->getSuccessor(1)) return nullptr;
  SmallVector<std::pair<ICmpInst *, bool>, 4> Facts;
  ElisaDeadTrapExits::decided(Branch->getCondition(), Branch->getSuccessor(0) == To, Facts);
  for (auto [Compare, Truth] : Facts) {
    auto Predicate = Truth ? Compare->getPredicate() : Compare->getInversePredicate();
    Value *Left = Compare->getOperand(0), *Right = Compare->getOperand(1);
    if (Predicate != ICmpInst::ICMP_EQ) continue;
    if (Left == V) return Right;
    if (Right == V) return Left;
  }
  return nullptr;
}

// A W that V equals wherever At executes, and the end of the edge that
// proved it: an edge from a block below Limit into a block dominating At,
// taken only when V == W. Every path from the latest run of V's, W's or
// Limit's definitions to At takes that edge, so the equality still holds.
static std::pair<Value *, Instruction *> equalAbove(DominatorTree &Tree, Value *V, Instruction *At,
                                                    BasicBlock *Limit) {
  for (DomTreeNode *Node = Tree.getNode(At->getParent()); Node && Node->getBlock() != Limit;
       Node = Node->getIDom()) {
    BasicBlock *Block = Node->getBlock(), *From = Block->getSinglePredecessor();
    if (!Tree.dominates(Limit, Block)) break;
    if (From && Tree.dominates(Limit, From))
      if (Value *Equal = equalOnEdge(V, From, Block)) return {Equal, From->getTerminator()};
  }
  return {nullptr, nullptr};
}

// V != Excluded wherever At executes, by an edge below V's block into a
// block dominating At.
static bool excludedAt(DominatorTree &Tree, Instruction &V, const APInt &Excluded, Instruction *At) {
  BasicBlock *Home = V.getParent();
  for (DomTreeNode *Node = Tree.getNode(At->getParent()); Node && Node->getBlock() != Home;
       Node = Node->getIDom()) {
    BasicBlock *Block = Node->getBlock(), *From = Block->getSinglePredecessor();
    if (!Tree.dominates(Home, Block)) return false;
    if (From && Tree.dominates(Home, From) && excludesOnEdge(&V, Excluded, From, Block)) return true;
  }
  return false;
}

// The only values V takes, when they are a few constants.
static bool constantChoices(Value *V, SmallVectorImpl<APInt> &Choices) {
  using namespace PatternMatch;
  const APInt *Left = nullptr, *Right = nullptr;
  Value *Flag = nullptr;
  unsigned Width = V->getType()->getIntegerBitWidth();
  if (match(V, m_APInt(Left))) Choices.push_back(*Left);
  else if (match(V, m_SExt(m_Value(Flag))) && Flag->getType()->isIntegerTy(1))
    Choices.append({APInt::getZero(Width), APInt::getAllOnes(Width)});
  else if (match(V, m_ZExt(m_Value(Flag))) && Flag->getType()->isIntegerTy(1))
    Choices.append({APInt::getZero(Width), APInt(Width, 1)});
  else if (match(V, m_Select(m_Value(), m_APInt(Left), m_APInt(Right))))
    Choices.append({*Left, *Right});
  return !Choices.empty();
}
} // namespace ElisaEdgeExclusion
