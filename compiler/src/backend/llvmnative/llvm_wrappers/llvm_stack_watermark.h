// Prove that a local array used as a stack is read only below a watermark:
// a value X such that elements [0, X) were all stored after the array's
// zero fill. Watermarks are 0; a small constant C where stores to elements
// [0, C) dominate (a stack seeded with its first entries; a zero seed whose
// store folded into the fill keeps the fill, shortened to the seeds); a phi whose incoming values are watermarks
// on their edges (assumed for the phis being proved, an induction over the
// order the phis execute); X - 1 for a nonzero watermark X (a pop); and
// X + 1 at points a store to element X dominates, X being a watermark at
// that store (a push). A dominating store executed after X's latest
// definition, so it wrote element X of the current watermark. A load of
// element X - 1 for a nonzero watermark X then reads a stored value, and
// the fill is dead. The array is written only by stores; stores never
// unwrite an element, so a claim that held earlier still holds.
#include <llvm/IR/Dominators.h>
#include <llvm/IR/PatternMatch.h>
namespace ElisaStackWatermark {
using namespace llvm;

class Watermark {
public:
  Watermark(DominatorTree &Tree, ElisaIndexBounds::Prover &Proof, ScalarEvolution &Evolution,
            AllocaInst &Buffer, uint64_t ElementSize, Instruction &Fill,
            ArrayRef<StoreInst *> Stores)
      : tree(Tree), proof(Proof), evolution(Evolution), buffer(Buffer),
        elementSize(ElementSize), fill(Fill), stores(Stores) {}

  // Elements the fill itself must still write: a seed the fill provides.
  uint64_t kept() const { return keptElements; }

  // Every load reads element X - 1 of a nonzero watermark X.
  bool readsBelow(ArrayRef<LoadInst *> Loads) {
    using namespace PatternMatch;
    for (LoadInst *Load : Loads) {
      Value *Index = element(Load->getPointerOperand()), *Mark = nullptr;
      auto *Pop = dyn_cast_or_null<Instruction>(Index);
      if (!Pop || !tree.dominates(&fill, Load) || !match(Pop, m_Add(m_Value(Mark), m_AllOnes())) ||
          !nonzero(Mark, Pop, 0) || !holds(Mark, Load, 0))
        return false;
    }
    return true;
  }

private:
  DominatorTree &tree;
  ElisaIndexBounds::Prover &proof;
  ScalarEvolution &evolution;
  AllocaInst &buffer;
  uint64_t elementSize;
  Instruction &fill;
  ArrayRef<StoreInst *> stores;
  SmallPtrSet<PHINode *, 8> assumed;
  uint64_t keptElements = 0;

  // The element index Pointer addresses in the buffer.
  Value *element(Value *Pointer) {
    using namespace PatternMatch;
    Type *Index = Type::getInt64Ty(buffer.getContext());
    if (Pointer == &buffer) return ConstantInt::get(Index, 0);
    auto *Address = dyn_cast<GetElementPtrInst>(Pointer);
    if (!Address || Address->getPointerOperand() != &buffer) return nullptr;
    const DataLayout &Layout = Address->getDataLayout();
    APInt Offset(Layout.getIndexTypeSizeInBits(Address->getType()), 0);
    if (Address->accumulateConstantOffset(Layout, Offset))
      return Offset.urem(elementSize) == 0 ? ConstantInt::get(Index, Offset.udiv(elementSize)) : nullptr;
    uint64_t Stride = Layout.getTypeAllocSize(Address->getSourceElementType());
    if (Address->getNumIndices() == 1 && Stride == elementSize) return Address->getOperand(1);
    // A byte offset `X << k` or `X * size` of element X, without wrapping.
    Value *Element = nullptr;
    const APInt *Factor = nullptr;
    auto *Scaled = dyn_cast<OverflowingBinaryOperator>(Address->getOperand(1));
    if (Address->getNumIndices() == 1 && Stride == 1 && Scaled && Scaled->hasNoUnsignedWrap() &&
        ((match(Scaled, m_Shl(m_Value(Element), m_APInt(Factor))) && Factor->ult(64) &&
          (uint64_t(1) << Factor->getZExtValue()) == elementSize) ||
         (match(Scaled, m_Mul(m_Value(Element), m_APInt(Factor))) && *Factor == elementSize)))
      return Element;
    auto *First = dyn_cast<ConstantInt>(Address->getOperand(1));
    if (Address->getNumIndices() == 2 && First && First->isZero() &&
        Address->getSourceElementType() == buffer.getAllocatedType())
      return Address->getOperand(2);
    return nullptr;
  }

  // Elements [0, Mark) were stored after the fill whenever At executes.
  bool holds(Value *Mark, Instruction *At, unsigned Depth) {
    using namespace PatternMatch;
    Value *Inner = nullptr;
    if (match(Mark, m_Zero())) return true;
    if (auto *Constant = dyn_cast<ConstantInt>(Mark)) return storedBelow(Constant->getValue(), At);
    if (Depth > 16) return false;
    if (auto *Phi = dyn_cast<PHINode>(Mark)) {
      if (!assumed.insert(Phi).second) return true;
      for (unsigned Edge = 0; Edge < Phi->getNumIncomingValues(); ++Edge)
        if (!holds(Phi->getIncomingValue(Edge), Phi->getIncomingBlock(Edge)->getTerminator(), Depth + 1))
          return false;
      return true;
    }
    auto *Step = dyn_cast<Instruction>(Mark);
    if (Step && match(Step, m_Add(m_Value(Inner), m_AllOnes())))
      return nonzero(Inner, Step, 0) && holds(Inner, At, Depth + 1);
    if (!Step || !match(Step, m_Add(m_Value(Inner), m_One()))) return false;
    for (StoreInst *Store : stores) {
      if (element(Store->getPointerOperand()) != Inner || !stored(*Store, At)) continue;
      // A failed attempt must not leave its phi assumptions behind.
      SmallPtrSet<PHINode *, 8> Before = assumed;
      if (holds(Inner, Store, Depth + 1)) return true;
      assumed = std::move(Before);
    }
    return false;
  }

  // Stores to each element below a small constant dominate At.
  bool storedBelow(const APInt &Count, Instruction *At) {
    if (Count.ugt(8)) return false;
    for (uint64_t Element = 0; Element < Count.getZExtValue(); ++Element) {
      bool Stored = false;
      for (StoreInst *Store : stores) {
        auto *Index = dyn_cast_or_null<ConstantInt>(element(Store->getPointerOperand()));
        Stored = Stored || (Index && Index->equalsInt(Element) && stored(*Store, At));
      }
      // The fill wrote it and keeps writing it, shortened to the seeds.
      auto *Zeros = dyn_cast<MemSetInst>(&fill);
      if (!Stored && Zeros && Zeros->getRawDest() == &buffer && tree.dominates(&fill, At)) {
        keptElements = std::max(keptElements, Element + 1);
        Stored = true;
      }
      if (!Stored) return false;
    }
    return true;
  }

  // Store writes a whole element after the fill, before At.
  bool stored(StoreInst &Store, Instruction *At) {
    return Store.getValueOperand()->getType() == buffer.getAllocatedType()->getArrayElementType() &&
           tree.dominates(&fill, &Store) && tree.dominates(&Store, At);
  }

  // V != 0 wherever At, which V's definition dominates, executes.
  bool nonzero(Value *V, Instruction *At, unsigned Depth) {
    using namespace PatternMatch;
    Value *Inner = nullptr;
    if (auto *Constant = dyn_cast<ConstantInt>(V)) return !Constant->isZero();
    if (auto *Sum = dyn_cast<OverflowingBinaryOperator>(V))
      if (Sum->hasNoUnsignedWrap() && match(V, m_Add(m_Value(Inner), m_One()))) return true;
    if (auto *Phi = dyn_cast<PHINode>(V); Phi && Depth < 4) {
      bool All = true;
      for (unsigned Edge = 0; All && Edge < Phi->getNumIncomingValues(); ++Edge) {
        BasicBlock *From = Phi->getIncomingBlock(Edge);
        Value *Incoming = Phi->getIncomingValue(Edge);
        All = nonzeroOnEdge(Incoming, From, Phi->getParent()) ||
              nonzero(Incoming, From->getTerminator(), Depth + 1);
      }
      if (All) return true;
    }
    if (!evolution.isSCEVable(V->getType())) return false;
    const SCEV *Value = evolution.getSCEV(V);
    return proof.proveBelow(evolution.getZero(Value->getType()), Value, At);
  }

  // From -> To is taken only when V != 0.
  static bool nonzeroOnEdge(Value *V, BasicBlock *From, BasicBlock *To) {
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
      if (Left != V || !Bound) continue;
      if ((Predicate == ICmpInst::ICMP_NE && Bound->isZero()) ||
          (Predicate == ICmpInst::ICMP_EQ && !Bound->isZero()) || Predicate == ICmpInst::ICMP_UGT ||
          (Predicate == ICmpInst::ICMP_UGE && !Bound->isZero()))
        return true;
    }
    return false;
  }
};
} // namespace ElisaStackWatermark
