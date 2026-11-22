// Facts shared by the prefix-initializer proofs: comparisons decided by
// dominating branch edges, structural value equality, and whether a write
// can execute between two instructions.
#include <llvm/Analysis/CaptureTracking.h>
#include <llvm/Analysis/MemoryLocation.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/PatternMatch.h>
namespace ElisaPrefixFacts {
using namespace llvm;
using namespace llvm::PatternMatch;

// The same pure arithmetic over the same operands.
static bool same(Value *Left, Value *Right, unsigned Depth = 0) {
  if (Left == Right) return true;
  auto *A = dyn_cast<BinaryOperator>(Left);
  auto *B = dyn_cast<BinaryOperator>(Right);
  return Depth < 3 && A && B && A->getOpcode() == B->getOpcode() &&
         same(A->getOperand(0), B->getOperand(0), Depth + 1) &&
         same(A->getOperand(1), B->getOperand(1), Depth + 1);
}

static bool sameAddress(Value *Left, Value *Right) {
  if (Left == Right) return true;
  auto *A = dyn_cast<GetElementPtrInst>(Left);
  auto *B = dyn_cast<GetElementPtrInst>(Right);
  return A && B && A->getSourceElementType() == B->getSourceElementType() &&
         A->getPointerOperand() == B->getPointerOperand() &&
         A->getNumIndices() == B->getNumIndices() &&
         std::equal(A->idx_begin(), A->idx_end(), B->idx_begin(),
                    [](const Use &X, const Use &Y) { return same(X.get(), Y.get()); });
}

static bool anyWrite(Instruction &Instruction) {
  return Instruction.mayWriteToMemory() && !isa<LifetimeIntrinsic>(Instruction);
}

// Whether Instruction may write Object, an alloca whose address does not
// escape: only accesses through pointers derived from it can reach it.
static bool mayWrite(Instruction &Instruction, AllocaInst &Object) {
  if (!anyWrite(Instruction)) return false;
  std::optional<MemoryLocation> Location = MemoryLocation::getOrNone(&Instruction);
  if (!Location) {
    if (auto *Fill = dyn_cast<AnyMemIntrinsic>(&Instruction))
      Location = MemoryLocation::getForDest(Fill);
    else
      return !isa<CallBase>(Instruction);
  }
  const Value *Root = getUnderlyingObject(Location->Ptr);
  if (Root == &Object) return true;
  return !isa<AllocaInst, Argument, GlobalValue, LoadInst, CallBase>(Root);
}

// Successors of Block that can follow its entry from Predecessor (null when
// unknown). A branch on an i1 phi of Block itself is decided by the phi's
// constant incoming value: short-circuit joins take only one way per arm.
static void feasibleSuccessors(BasicBlock *Block, BasicBlock *Predecessor,
                               SmallVectorImpl<BasicBlock *> &Out) {
  auto *Branch = dyn_cast<CondBrInst>(Block->getTerminator());
  auto *Phi = Branch ? dyn_cast<PHINode>(Branch->getCondition()) : nullptr;
  if (Phi && Predecessor && Phi->getParent() == Block) {
    int Index = Phi->getBasicBlockIndex(Predecessor);
    auto *Decided = Index >= 0 ? dyn_cast<ConstantInt>(Phi->getIncomingValue(Index)) : nullptr;
    if (Decided) {
      Out.push_back(Branch->getSuccessor(Decided->isOne() ? 0 : 1));
      return;
    }
  }
  Out.append(succ_begin(Block), succ_end(Block));
}

static bool canFollow(BasicBlock *Block, BasicBlock *Predecessor, BasicBlock *Successor) {
  SmallVector<BasicBlock *, 2> Next;
  feasibleSuccessors(Block, Predecessor, Next);
  return llvm::is_contained(Next, Successor);
}

// Blocks reachable from Start, entered from EnteredFrom, without entering
// Barrier or expanding past Stop.
static SmallPtrSet<BasicBlock *, 32> reachForward(BasicBlock *Start, BasicBlock *EnteredFrom,
                                                  bool Inclusive, BasicBlock *Barrier,
                                                  BasicBlock *Stop) {
  SmallPtrSet<BasicBlock *, 32> Blocks;
  DenseSet<std::pair<BasicBlock *, BasicBlock *>> Seen;
  SmallVector<std::pair<BasicBlock *, BasicBlock *>, 32> Work;
  auto Push = [&](BasicBlock *Block, BasicBlock *From) {
    if (Block != Barrier && Seen.insert({Block, From}).second) Work.push_back({Block, From});
  };
  auto Expand = [&](BasicBlock *Block, BasicBlock *From) {
    SmallVector<BasicBlock *, 2> Next;
    feasibleSuccessors(Block, From, Next);
    for (BasicBlock *Successor : Next) Push(Successor, Block);
  };
  if (Inclusive) Push(Start, EnteredFrom);
  else if (Start != Stop) Expand(Start, EnteredFrom);
  while (!Work.empty()) {
    auto [Block, From] = Work.pop_back_val();
    Blocks.insert(Block);
    if (Block != Stop) Expand(Block, From);
  }
  return Blocks;
}

// Blocks from which Target can be reached without entering Barrier.
static SmallPtrSet<BasicBlock *, 32> reachBackward(BasicBlock *Target, BasicBlock *Barrier) {
  SmallPtrSet<BasicBlock *, 32> Blocks;
  DenseSet<std::pair<BasicBlock *, BasicBlock *>> Seen;
  SmallVector<std::pair<BasicBlock *, BasicBlock *>, 32> Work;
  for (BasicBlock *Predecessor : predecessors(Target))
    if (Predecessor != Barrier && Seen.insert({Predecessor, Target}).second)
      Work.push_back({Predecessor, Target});
  while (!Work.empty()) {
    auto [Block, Successor] = Work.pop_back_val();
    Blocks.insert(Block);
    for (BasicBlock *Predecessor : predecessors(Block))
      if (Predecessor != Barrier && canFollow(Block, Predecessor, Successor) &&
          Seen.insert({Predecessor, Block}).second)
        Work.push_back({Predecessor, Block});
  }
  return Blocks;
}

// Whether a write Relevant accepts may execute after the latest execution
// of First and before Last. Only paths that do not pass First again matter:
// a later First is the one whose value reaches Last. Exact reachability over
// feasible edges, with no exploration limit.
template <typename Relevant>
static bool writeBetween(Instruction &First, Instruction &Last, Relevant Writes) {
  BasicBlock *Origin = First.getParent();
  auto After = reachForward(Origin, nullptr, false, Origin, nullptr);
  auto Before = reachBackward(Last.getParent(), Origin);
  for (BasicBlock *Block : After)
    if (Before.count(Block) && llvm::any_of(*Block, Writes)) return true;
  // Straight-line parts of the endpoint blocks.
  bool SameBlock = First.getParent() == Last.getParent();
  for (Instruction *Step = First.getNextNode(); Step && Step != &Last; Step = Step->getNextNode())
    if (Writes(*Step)) return true;
  if (!SameBlock)
    for (Instruction &Step : *Last.getParent()) {
      if (&Step == &Last) break;
      if (Writes(Step)) return true;
    }
  return false;
}

// A comparison and the branch edge From -> To whose taking decides it.
struct Evidence {
  ICmpInst *Compare;
  BasicBlock *From, *To;
};

// Walks the comparisons decided by branch edges that dominate a block.
class FactWalker {
protected:
  explicit FactWalker(DominatorTree &Tree) : tree(Tree) {}
  DominatorTree &tree;

  // Comparisons known to have a given truth when Condition has value Truth,
  // looking through and/or, selects and short-circuit phis of i1 values.
  // Via collects the single possible predecessor of each short-circuit phi:
  // facts holding at the end of that block also hold on the edge.
  static void factsOf(Value *Condition, bool Truth,
                      SmallVectorImpl<std::pair<ICmpInst *, bool>> &Facts,
                      SmallVectorImpl<BasicBlock *> &Via, unsigned Depth = 0) {
    if (Depth == 8) return;
    if (auto *Compare = dyn_cast<ICmpInst>(Condition)) {
      Facts.push_back({Compare, Truth});
      return;
    }
    Value *Left = nullptr, *Right = nullptr;
    if ((Truth && match(Condition, m_LogicalAnd(m_Value(Left), m_Value(Right)))) ||
        (!Truth && match(Condition, m_LogicalOr(m_Value(Left), m_Value(Right))))) {
      factsOf(Left, Truth, Facts, Via, Depth + 1);
      factsOf(Right, Truth, Facts, Via, Depth + 1);
      return;
    }
    auto *Phi = dyn_cast<PHINode>(Condition);
    if (!Phi || !Phi->getType()->isIntegerTy(1)) return;
    Value *Possible = nullptr;
    BasicBlock *From = nullptr;
    for (unsigned Index = 0; Index < Phi->getNumIncomingValues(); ++Index) {
      Value *Incoming = Phi->getIncomingValue(Index);
      if (auto *Constant = dyn_cast<ConstantInt>(Incoming)) {
        if (Constant->isOne() == Truth) return; // a path that proves nothing
        continue;                              // a path that cannot take this edge
      }
      if (Possible) return;
      Possible = Incoming;
      From = Phi->getIncomingBlock(Index);
    }
    if (!Possible) return;
    Via.push_back(From);
    factsOf(Possible, Truth, Facts, Via, Depth + 1);
  }

  // Visit the comparisons decided by branch edges that dominate Context,
  // with greater-than forms swapped into less-than forms. Fact also receives
  // the comparison, whose block fixes the operands' values, and the edge.
  template <typename Visit>
  bool anyFact(BasicBlock *Context, Visit Fact, unsigned Depth = 0) {
    if (Depth == 4) return false;
    for (DomTreeNode *Node = tree.getNode(Context); Node; Node = Node->getIDom()) {
      BasicBlock *Dominator = Node->getBlock();
      for (BasicBlock *Predecessor : predecessors(Dominator)) {
        auto *Branch = dyn_cast<CondBrInst>(Predecessor->getTerminator());
        if (!Branch || Branch->getSuccessor(0) == Branch->getSuccessor(1)) continue;
        for (unsigned Edge = 0; Edge < 2; ++Edge) {
          if (Branch->getSuccessor(Edge) != Dominator ||
              !tree.dominates(BasicBlockEdge(Predecessor, Dominator), Context))
            continue;
          SmallVector<std::pair<ICmpInst *, bool>, 4> Facts;
          SmallVector<BasicBlock *, 2> Via;
          factsOf(Branch->getCondition(), Edge == 0, Facts, Via);
          for (BasicBlock *Before : Via)
            if (anyFact(Before, Fact, Depth + 1)) return true;
          for (auto [Compare, Truth] : Facts) {
            auto Predicate = Truth ? Compare->getPredicate() : Compare->getInversePredicate();
            Value *Left = Compare->getOperand(0), *Right = Compare->getOperand(1);
            if (ICmpInst::isGT(Predicate) || ICmpInst::isGE(Predicate)) {
              std::swap(Left, Right);
              Predicate = ICmpInst::getSwappedPredicate(Predicate);
            }
            if (Fact(Predicate, Left, Right, Evidence{Compare, Predecessor, Dominator}))
              return true;
          }
        }
      }
    }
    return false;
  }
};
} // namespace ElisaPrefixFacts
