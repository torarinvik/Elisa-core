// Helpers of the stack proof (ElisaStackWatermark) that need no proof
// state: the bytes each iteration of a single-block push loop stores and
// the count at which it leaves, the element an address names, and the
// shapes of pops, clamps and trip-count bounds.
#include "llvm_element_offsets.h"
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
#include <llvm/IR/PatternMatch.h>
namespace ElisaWatermarkLoops {
using namespace llvm;

// V is the unsigned minimum of A and B: the intrinsic, or a select on their
// comparison (a slice end clamped to the capacity, before instcombine).
static bool unsignedMinimum(Value *V, Value *&A, Value *&B) {
  using namespace PatternMatch;
  if (match(V, m_UMin(m_Value(A), m_Value(B)))) return true;
  auto *Choice = dyn_cast<SelectInst>(V);
  auto *Test = Choice ? dyn_cast<ICmpInst>(Choice->getCondition()) : nullptr;
  if (!Test) return false;
  A = Choice->getTrueValue();
  B = Choice->getFalseValue();
  auto Predicate = Test->getPredicate();
  return ((Predicate == ICmpInst::ICMP_ULT || Predicate == ICmpInst::ICMP_ULE) && A == Test->getOperand(0) &&
          B == Test->getOperand(1)) ||
         ((Predicate == ICmpInst::ICMP_UGT || Predicate == ICmpInst::ICMP_UGE) && A == Test->getOperand(1) &&
          B == Test->getOperand(0));
}

// The value V is one less than (a pop: `add X, -1`, or `sub X, 1` before
// instcombine), or null.
static Value *popped(Value *V) {
  using namespace PatternMatch;
  Value *Mark = nullptr;
  return match(V, m_CombineOr(m_Add(m_Value(Mark), m_AllOnes()), m_Sub(m_Value(Mark), m_One()))) ? Mark : nullptr;
}

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
// The element index Pointer addresses in Buffer, of elements ElementSize
// bytes long.
static Value *element(AllocaInst &Buffer, uint64_t ElementSize, Value *Pointer) {
  using namespace PatternMatch;
  Type *Index = Type::getInt64Ty(Buffer.getContext());
  if (Pointer == &Buffer) return ConstantInt::get(Index, 0);
  auto *Address = dyn_cast<GetElementPtrInst>(Pointer);
  if (!Address || Address->getPointerOperand() != &Buffer) return nullptr;
  const DataLayout &Layout = Address->getDataLayout();
  APInt Offset(Layout.getIndexTypeSizeInBits(Address->getType()), 0);
  if (Address->accumulateConstantOffset(Layout, Offset))
    return Offset.urem(ElementSize) == 0 ? ConstantInt::get(Index, Offset.udiv(ElementSize)) : nullptr;
  uint64_t Stride = Layout.getTypeAllocSize(Address->getSourceElementType());
  if (Address->getNumIndices() == 1 && Stride == ElementSize) return Address->getOperand(1);
  // A byte offset `X << k` or `X * size` of element X, without wrapping.
  Value *Element = nullptr;
  const APInt *Factor = nullptr;
  auto *Scaled = dyn_cast<OverflowingBinaryOperator>(Address->getOperand(1));
  if (Address->getNumIndices() == 1 && Stride == 1 && Scaled && Scaled->hasNoUnsignedWrap() &&
      ((match(Scaled, m_Shl(m_Value(Element), m_APInt(Factor))) && Factor->ult(64) &&
        (uint64_t(1) << Factor->getZExtValue()) == ElementSize) ||
       (match(Scaled, m_Mul(m_Value(Element), m_APInt(Factor))) && *Factor == ElementSize)))
    return Element;
  auto *First = dyn_cast<ConstantInt>(Address->getOperand(1));
  if (Address->getNumIndices() == 2 && First && First->isZero() &&
      Address->getSourceElementType() == Buffer.getAllocatedType())
    return Address->getOperand(2);
  return nullptr;
}

// L takes at most Bound back edges: its count is Bound, or a minimum with
// Bound among its operands (a bounds check's exit at the capacity).
static bool takenAtMost(ScalarEvolution &Evolution, const Loop &L, const SCEV *Bound) {
  const SCEV *Taken = Evolution.getSymbolicMaxBackedgeTakenCount(&L);
  if (isa<SCEVCouldNotCompute>(Taken) || Taken->getType() != Bound->getType()) return false;
  if (Taken == Bound) return true;
  auto *Minimum = dyn_cast<SCEVMinMaxExpr>(Taken);
  auto *Sequential = dyn_cast<SCEVSequentialMinMaxExpr>(Taken);
  ArrayRef<SCEVUse> Operands = Minimum && Minimum->getSCEVType() == scUMinExpr ? Minimum->operands()
                               : Sequential ? Sequential->operands()
                                            : ArrayRef<SCEVUse>();
  return llvm::any_of(Operands, [&](SCEVUse Operand) { return static_cast<const SCEV *>(Operand) == Bound; });
}

// The element at byte offset Bytes: SCEV's exact division leaves an
// unrolled recurrence ({48,+,64} /u 16) undivided where its start and step
// both divide.
static const SCEV *quotient(ScalarEvolution &Evolution, const SCEV *Bytes, uint64_t Size) {
  if (const SCEV *Divided = ElisaElementOffsets::divide(Evolution, Bytes, Size)) return Divided;
  return Evolution.getUDivExactExpr(Bytes, Evolution.getConstant(Bytes->getType(), Size));
}
} // namespace ElisaWatermarkLoops
