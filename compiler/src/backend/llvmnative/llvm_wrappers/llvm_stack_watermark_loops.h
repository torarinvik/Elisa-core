// Loops that push a run of elements into a stack buffer
// (ElisaStackWatermark): the bytes each iteration of a single-block loop
// stores, and the count at which such a loop leaves.
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
#include <llvm/IR/PatternMatch.h>
namespace ElisaWatermarkLoops {
using namespace llvm;

// The first byte offset of the Step-byte block every iteration of L, a
// single block Body, stores completely; null when the stores leave a gap.
static const SCEV *coveredStart(ScalarEvolution &Evolution, AllocaInst &Buffer,
                                ArrayRef<ArrayRef<StoreInst *>> Groups, BasicBlock &Body,
                                const Loop &L, const SCEVConstant *Step) {
  if (!Step || Step->getAPInt().isZero() || Step->getAPInt().ugt(1024)) return nullptr;
  const SCEV *Start = nullptr;
  SmallVector<std::pair<int64_t, int64_t>, 8> Pieces; // (offset from Start, bytes)
  for (ArrayRef<StoreInst *> Group : Groups)
    for (StoreInst *Store : Group) {
      if (Store->getParent() != &Body) continue;
      auto *Address = dyn_cast<SCEVAddRecExpr>(
          Evolution.getMinusSCEV(Evolution.getSCEV(Store->getPointerOperand()), Evolution.getSCEV(&Buffer)));
      if (!Address || Address->getLoop() != &L || Address->getStepRecurrence(Evolution) != Step) continue;
      if (!Start) Start = Address->getStart();
      auto *Shift = dyn_cast<SCEVConstant>(Evolution.getMinusSCEV(Address->getStart(), Start));
      if (!Shift) continue;
      int64_t Size = Store->getDataLayout().getTypeStoreSize(Store->getValueOperand()->getType());
      Pieces.push_back({Shift->getAPInt().getSExtValue(), Size});
    }
  if (!Start) return nullptr;
  llvm::sort(Pieces);
  // Pieces may begin below the first store seen: shift to the lowest.
  int64_t Low = Pieces.front().first, Reached = Low;
  for (auto [Begin, Size] : Pieces) {
    if (Begin > Reached) return nullptr;
    Reached = std::max(Reached, Begin + Size);
  }
  if (Reached - Low != int64_t(Step->getAPInt().getZExtValue())) return nullptr;
  return Evolution.getAddExpr(Start, Evolution.getConstant(Start->getType(), Low, true));
}

// The N at which Body's loop exits through Latch, setting Stride to its
// counter's C: the counter is a phi starting at zero, stepped by C
// without unsigned wrap, and the exit is taken when the next value
// equals a loop-invariant N, after N / C iterations.
static const SCEV *counted(ScalarEvolution &Evolution, CondBrInst &Latch, BasicBlock &Body, uint64_t &Stride) {
  using namespace PatternMatch;
  Value *Next = nullptr, *Limit = nullptr, *Counter = nullptr;
  const APInt *Step = nullptr;
  CmpPredicate Predicate;
  if (!match(Latch.getCondition(), m_ICmp(Predicate, m_Value(Next), m_Value(Limit))))
    return nullptr;
  bool ExitsOnTrue = Latch.getSuccessor(0) != &Body;
  if (!((Predicate == ICmpInst::ICMP_EQ && ExitsOnTrue) || (Predicate == ICmpInst::ICMP_NE && !ExitsOnTrue)))
    return nullptr;
  auto *Increment = dyn_cast<OverflowingBinaryOperator>(Next);
  auto *Phi = Increment && match(Next, m_Add(m_Value(Counter), m_APInt(Step)))
                  ? dyn_cast<PHINode>(Counter) : nullptr;
  auto *Defined = dyn_cast<Instruction>(Limit);
  if (!Phi || Phi->getParent() != &Body || !Increment->hasNoUnsignedWrap() || Step->isZero() ||
      Step->ugt(1024) || Phi->getNumIncomingValues() != 2 || (Defined && Defined->getParent() == &Body))
    return nullptr;
  for (unsigned Edge = 0; Edge < 2; ++Edge) {
    bool Back = Phi->getIncomingBlock(Edge) == &Body;
    Value *Incoming = Phi->getIncomingValue(Edge);
    if (Back ? Incoming != Next : !match(Incoming, m_Zero())) return nullptr;
  }
  if (!Evolution.isSCEVable(Limit->getType())) return nullptr;
  Stride = Step->getZExtValue();
  return Evolution.getSCEV(Limit);
}
} // namespace ElisaWatermarkLoops
