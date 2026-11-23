// Remove loop exits to a trap that another exit always beats. An exit's
// count is the number of backedges taken before its condition first holds.
// When the largest possible count of one exit is below the smallest
// possible count of a trapping exit, the loop leaves by the first before the
// second's condition ever holds, in whatever order the two are tested. The
// counts come from scalar evolution, and their ranges from the facts that
// hold on the loop's entry edge (bounds checks on an index whose loop limit
// is itself checked against the array size are the common case).
#include <llvm/Analysis/LazyValueInfo.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/PatternMatch.h>
namespace ElisaDeadTrapExits {
using namespace llvm;

// Comparisons a branch condition decides when it has value Truth, through
// logical and/or (and their select forms).
static void decided(Value *Condition, bool Truth,
                    SmallVectorImpl<std::pair<ICmpInst *, bool>> &Out, unsigned Depth = 0) {
  using namespace PatternMatch;
  Value *Left = nullptr, *Right = nullptr;
  if (auto *Compare = dyn_cast<ICmpInst>(Condition)) {
    Out.push_back({Compare, Truth});
  } else if (Depth < 6 && ((Truth && match(Condition, m_LogicalAnd(m_Value(Left), m_Value(Right)))) ||
                           (!Truth && match(Condition, m_LogicalOr(m_Value(Left), m_Value(Right)))))) {
    decided(Left, Truth, Out, Depth + 1);
    decided(Right, Truth, Out, Depth + 1);
  }
}

// Comparisons decided on the edge From -> To.
static SmallVector<std::pair<ICmpInst *, bool>, 4> edgeFacts(BasicBlock *From, BasicBlock *To) {
  SmallVector<std::pair<ICmpInst *, bool>, 4> Facts;
  auto *Branch = dyn_cast<CondBrInst>(From->getTerminator());
  if (Branch && Branch->getSuccessor(0) != Branch->getSuccessor(1))
    decided(Branch->getCondition(), Branch->getSuccessor(0) == To, Facts);
  return Facts;
}

// The range of V implied by `V op constant` decided on branch edges that
// dominate Block. Lazy value info gives up on values reaching a block
// through an earlier loop's exit; these dominating edges still hold there.
static ConstantRange guardRange(Value *V, BasicBlock *Block, DominatorTree &Tree) {
  ConstantRange Range = ConstantRange::getFull(V->getType()->getIntegerBitWidth());
  for (DomTreeNode *Node = Tree.getNode(Block); Node; Node = Node->getIDom()) {
    BasicBlock *Dominator = Node->getBlock();
    for (BasicBlock *Predecessor : predecessors(Dominator)) {
      auto *Branch = dyn_cast<CondBrInst>(Predecessor->getTerminator());
      if (!Branch || Branch->getSuccessor(0) == Branch->getSuccessor(1)) continue;
      for (unsigned Edge = 0; Edge < 2; ++Edge) {
        if (Branch->getSuccessor(Edge) != Dominator ||
            !Tree.dominates(BasicBlockEdge(Predecessor, Dominator), Block))
          continue;
        SmallVector<std::pair<ICmpInst *, bool>, 4> Facts;
        decided(Branch->getCondition(), Edge == 0, Facts);
        for (auto [Compare, Truth] : Facts) {
          // `V op c`, or `(V + offset) op c` as range checks are folded.
          auto *Bound = dyn_cast<ConstantInt>(Compare->getOperand(1));
          const APInt *Offset = nullptr;
          bool Shifted = PatternMatch::match(
              Compare->getOperand(0), PatternMatch::m_Add(PatternMatch::m_Specific(V),
                                                          PatternMatch::m_APInt(Offset)));
          if (!Bound || (Compare->getOperand(0) != V && !Shifted)) continue;
          auto Predicate = Truth ? Compare->getPredicate() : Compare->getInversePredicate();
          ConstantRange Region = ConstantRange::makeExactICmpRegion(Predicate, Bound->getValue());
          Range = Range.intersectWith(Shifted ? Region.sub(ConstantRange(*Offset)) : Region);
        }
      }
    }
  }
  return Range;
}

class Counts {
public:
  Counts(ScalarEvolution &Evolution, LazyValueInfo &Values, DominatorTree &Tree,
         BasicBlock *From, BasicBlock *To)
      : evolution(Evolution), values(Values), tree(Tree), from(From), to(To) {}

  // The values Count can take when the loop is entered.
  ConstantRange range(const SCEV *Count, unsigned Depth = 0) {
    ConstantRange Known = evolution.getUnsignedRange(Count);
    if (Depth > 8) return Known;
    if (auto *Constant = dyn_cast<SCEVConstant>(Count)) return ConstantRange(Constant->getAPInt());
    if (auto *Unknown = dyn_cast<SCEVUnknown>(Count))
      return Known
          .intersectWith(values.getConstantRangeOnEdge(Unknown->getValue(), from, to),
                         ConstantRange::Unsigned)
          .intersectWith(guardRange(Unknown->getValue(), to, tree), ConstantRange::Unsigned);
    if (auto *Cast = dyn_cast<SCEVCastExpr>(Count)) {
      ConstantRange Inner = range(Cast->getOperand(), Depth + 1);
      unsigned Width = Known.getBitWidth();
      if (isa<SCEVZeroExtendExpr>(Cast)) return Known.intersectWith(Inner.zeroExtend(Width));
      if (isa<SCEVSignExtendExpr>(Cast)) return Known.intersectWith(Inner.signExtend(Width));
      if (isa<SCEVTruncateExpr>(Cast)) return Known.intersectWith(Inner.truncate(Width));
      return Known;
    }
    auto *Nary = dyn_cast<SCEVNAryExpr>(Count);
    if (!Nary) return Known;
    ConstantRange Result = range(Nary->getOperand(0), Depth + 1);
    for (const SCEV *Operand : drop_begin(Nary->operands())) {
      ConstantRange Next = range(Operand, Depth + 1);
      switch (Nary->getSCEVType()) {
      case scAddExpr: Result = Result.add(Next); break;
      case scMulExpr: Result = Result.multiply(Next); break;
      case scUMinExpr: Result = Result.umin(Next); break;
      case scUMaxExpr: Result = Result.umax(Next); break;
      case scSMinExpr: Result = Result.smin(Next); break;
      case scSMaxExpr: Result = Result.smax(Next); break;
      default: return Known;
      }
    }
    return Known.intersectWith(Result, ConstantRange::Unsigned);
  }

private:
  ScalarEvolution &evolution;
  LazyValueInfo &values;
  DominatorTree &tree;
  BasicBlock *from, *to;
};

static bool isTrap(BasicBlock &Block) { return isa<UnreachableInst>(Block.getTerminator()); }

static bool removeLoopExits(Loop &L, ScalarEvolution &Evolution, LazyValueInfo &Values,
                            DominatorTree &Tree) {
  BasicBlock *Entry = L.getLoopPredecessor();
  if (!Entry) return false;
  Counts Facts(Evolution, Values, Tree, Entry, L.getHeader());
  SmallVector<BasicBlock *, 4> Exiting;
  L.getExitingBlocks(Exiting);
  // The largest count of any exit that never traps bounds the trip count.
  std::optional<APInt> Bound;
  for (BasicBlock *Block : Exiting) {
    auto *Branch = dyn_cast<CondBrInst>(Block->getTerminator());
    if (!Branch) continue;
    unsigned Out = L.contains(Branch->getSuccessor(0)) ? 1 : 0;
    const SCEV *Count = Evolution.getExitCount(&L, Block);
    if (isTrap(*Branch->getSuccessor(Out)) || isa<SCEVCouldNotCompute>(Count) ||
        !Count->getType()->isIntegerTy(64))
      continue;
    APInt Most = Facts.range(Count).getUnsignedMax();
    if (!Bound || Most.ult(*Bound)) Bound = Most;
  }
  if (!Bound) return false;
  bool Changed = false;
  for (BasicBlock *Block : Exiting) {
    auto *Branch = dyn_cast<CondBrInst>(Block->getTerminator());
    if (!Branch || L.contains(Branch->getSuccessor(0)) == L.contains(Branch->getSuccessor(1)))
      continue;
    unsigned Out = L.contains(Branch->getSuccessor(0)) ? 1 : 0;
    const SCEV *Count = Evolution.getExitCount(&L, Block);
    if (!isTrap(*Branch->getSuccessor(Out)) || isa<SCEVCouldNotCompute>(Count) ||
        !Count->getType()->isIntegerTy(64) || !Bound->ult(Facts.range(Count).getUnsignedMin()))
      continue;
    Branch->setCondition(ConstantInt::getBool(Branch->getContext(), Out == 1));
    Changed = true;
  }
  return Changed;
}

static unsigned removeDeadTrapExits(Module &Module) {
  PassBuilder Builder;
  FunctionAnalysisManager Analyses;
  Builder.registerFunctionAnalyses(Analyses);
  unsigned Changed = 0;
  for (Function &F : Module) {
    if (F.isDeclaration()) continue;
    auto &Loops = Analyses.getResult<LoopAnalysis>(F);
    if (Loops.empty()) continue;
    auto &Evolution = Analyses.getResult<ScalarEvolutionAnalysis>(F);
    auto &Values = Analyses.getResult<LazyValueAnalysis>(F);
    auto &Tree = Analyses.getResult<DominatorTreeAnalysis>(F);
    bool FunctionChanged = false;
    for (Loop *L : Loops.getLoopsInPreorder())
      FunctionChanged |= removeLoopExits(*L, Evolution, Values, Tree);
    if (FunctionChanged) {
      Analyses.invalidate(F, PreservedAnalyses::none());
      ++Changed;
    }
  }
  return Changed;
}
} // namespace ElisaDeadTrapExits
