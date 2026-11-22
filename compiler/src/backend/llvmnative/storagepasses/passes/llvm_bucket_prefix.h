// Elide the zero fill of an array used as fixed-width buckets. A counter
// array C starts zeroed and only changes by `C[r] <- C[r] + 1` right after
// slot r * W + C[r] of the bucket array is written, so every slot
// r * W + k with k < C[r] has been written. When every read of slot
// r * W + j is guarded by j < C[r], no read observes the bucket array's own
// zero fill (adjacency lists filled by appending are the common case).
namespace ElisaBucketPrefix {
using namespace llvm;
using namespace llvm::PatternMatch;
using ElisaPrefixFacts::same;
using ElisaPrefixFacts::sameAddress;

struct Slot {
  Value *Row;
  uint64_t Width;
  Value *Column;
};

static Value *stripZExt(Value *Candidate) {
  if (auto *Widened = dyn_cast<ZExtInst>(Candidate)) return Widened->getOperand(0);
  return Candidate;
}

// r * W + k in either operand order, with W a constant.
static std::optional<Slot> slotOf(Value *Index) {
  Value *Row = nullptr, *Column = nullptr;
  ConstantInt *Width = nullptr;
  if (match(Index, m_c_Add(m_Mul(m_Value(Row), m_ConstantInt(Width)), m_Value(Column))) ||
      match(Index, m_c_Add(m_Mul(m_ConstantInt(Width), m_Value(Row)), m_Value(Column))))
    return Slot{Row, Width->getZExtValue(), stripZExt(Column)};
  if (match(Index, m_c_Add(m_Shl(m_Value(Row), m_ConstantInt(Width)), m_Value(Column))) &&
      Width->getZExtValue() < 32)
    return Slot{Row, uint64_t(1) << Width->getZExtValue(), stripZExt(Column)};
  return std::nullopt;
}

struct Access {
  Instruction *Instruction;
  Value *Index;
};

// A fixed array alloca read and written only through element GEPs, plus one
// full zero fill (a memset or an aggregate zero store) that runs once before
// every access.
struct Array {
  AllocaInst *Buffer = nullptr;
  Instruction *Fill = nullptr;
  Type *Element = nullptr;
  SmallVector<Access, 8> Reads, Writes;
};

static bool describe(AllocaInst &Buffer, Array &Out, LoopInfo &Loops,
                     DominatorTree &Tree, const DataLayout &Layout) {
  auto *Type = dyn_cast<ArrayType>(Buffer.getAllocatedType());
  if (!Type || !Type->getElementType()->isIntegerTy() || !Buffer.isStaticAlloca() ||
      Buffer.isArrayAllocation() || Buffer.isUsedByMetadata() ||
      PointerMayBeCaptured(&Buffer, /*ReturnCaptures=*/true))
    return false;
  Out.Buffer = &Buffer;
  Out.Element = Type->getElementType();
  for (User *Use : Buffer.users()) {
    auto *Zeroes = dyn_cast<StoreInst>(Use);
    if (Zeroes && Zeroes->getValueOperand()->getType() == Type) {
      if (Out.Fill || !Zeroes->isSimple() || Zeroes->getPointerOperand() != &Buffer ||
          !isa<ConstantAggregateZero>(Zeroes->getValueOperand()) ||
          Loops.getLoopFor(Zeroes->getParent()))
        return false;
      Out.Fill = Zeroes;
      continue;
    }
    if (auto *Fill = dyn_cast<MemSetInst>(Use)) {
      auto *Length = dyn_cast<ConstantInt>(Fill->getLength());
      auto *Byte = dyn_cast<ConstantInt>(Fill->getValue());
      if (Out.Fill || Fill->getRawDest() != &Buffer || Fill->isVolatile() || !Length ||
          !Byte || !Byte->isZero() ||
          !Length->equalsInt(Layout.getTypeAllocSize(Type).getFixedValue()) ||
          Loops.getLoopFor(Fill->getParent()))
        return false;
      Out.Fill = Fill;
      continue;
    }
    if (isa<LifetimeIntrinsic>(Use)) continue;
    auto *Address = dyn_cast<GetElementPtrInst>(Use);
    Value *Index = Address && Address->getPointerOperand() == &Buffer
        ? ElisaCountedPrefix::elementIndex(*Address, Layout, Out.Element) : nullptr;
    if (!Index) return false;
    for (User *AccessUse : Address->users()) {
      auto *Read = dyn_cast<LoadInst>(AccessUse);
      auto *Write = dyn_cast<StoreInst>(AccessUse);
      if (Read && Read->isSimple() && Read->getType() == Out.Element)
        Out.Reads.push_back({Read, Index});
      else if (Write && Write->isSimple() && Write->getPointerOperand() == Address &&
               Write->getValueOperand()->getType() == Out.Element)
        Out.Writes.push_back({Write, Index});
      else
        return false;
    }
  }
  if (!Out.Fill) return false;
  auto Initialized = [&](Access &Item) { return Tree.dominates(Out.Fill, Item.Instruction); };
  return llvm::all_of(Out.Reads, Initialized) && llvm::all_of(Out.Writes, Initialized);
}

class Prover : ElisaPrefixFacts::FactWalker {
public:
  Prover(DominatorTree &Tree, LoopInfo &Loops, const DataLayout &Layout)
      : FactWalker(Tree), loops(Loops), layout(Layout) {}

  bool removable(Array &Buckets) {
    if (Buckets.Writes.empty() || Buckets.Reads.empty()) return false;
    buckets = Buckets.Buffer;
    SmallPtrSet<Instruction *, 8> Increments;
    for (Access &Write : Buckets.Writes)
      if (!appendThenCount(Write, Increments)) return false;
    // The counter changes only through the increments paired above.
    if (Counter.Writes.size() != Increments.size()) return false;
    return llvm::all_of(Buckets.Reads, [&](Access &Read) { return guardedRead(Read); });
  }

  AllocaInst *counter() const { return Counter.Buffer; }

private:
  LoopInfo &loops;
  const DataLayout &layout;
  AllocaInst *buckets = nullptr;
  Array Counter;
  uint64_t Width = 0;

  // A load of Counter[Row] (possibly zero extended).
  LoadInst *counterLoad(Value *Candidate, Value *Row) {
    auto *Load = dyn_cast<LoadInst>(stripZExt(Candidate));
    auto *Address = Load ? dyn_cast<GetElementPtrInst>(Load->getPointerOperand()) : nullptr;
    if (!Address || !Load->isSimple() || Address->getPointerOperand() != Counter.Buffer)
      return nullptr;
    Value *Index = ElisaCountedPrefix::elementIndex(*Address, layout, Counter.Element);
    return Index && same(Index, Row) ? Load : nullptr;
  }

  bool adoptCounter(Value *Column) {
    auto *Load = dyn_cast<LoadInst>(Column);
    auto *Address = Load ? dyn_cast<GetElementPtrInst>(Load->getPointerOperand()) : nullptr;
    auto *Buffer = Address ? dyn_cast<AllocaInst>(Address->getPointerOperand()) : nullptr;
    if (!Buffer || Buffer == buckets) return false;
    if (Counter.Buffer) return Counter.Buffer == Buffer;
    return describe(*Buffer, Counter, loops, tree, layout) && Counter.Reads.size() >= 1;
  }

  // Write is `Buckets[r * W + C[r]] <- v`, followed by `C[r] <- C[r] + 1`
  // with no other write of C from the load of C[r] until the increment.
  bool appendThenCount(Access &Write, SmallPtrSetImpl<Instruction *> &Increments) {
    auto Place = slotOf(Write.Index);
    if (!Place || (Width && Place->Width != Width) || !adoptCounter(Place->Column))
      return false;
    Width = Place->Width;
    LoadInst *Count = counterLoad(Place->Column, Place->Row);
    if (!Count) return false;
    for (Access &Update : Counter.Writes) {
      auto *Store = cast<StoreInst>(Update.Instruction);
      Value *Previous = nullptr;
      if (Increments.count(Store) || !same(Update.Index, Place->Row) ||
          !match(Store->getValueOperand(), m_c_Add(m_Value(Previous), m_One())))
        continue;
      LoadInst *Reload = counterLoad(Previous, Place->Row);
      if (!Reload || Reload->getType() != Count->getType() ||
          !tree.dominates(Write.Instruction, Store))
        continue;
      Instruction *First = tree.dominates(Count, Reload) ? Count
                         : tree.dominates(Reload, Count) ? Reload : nullptr;
      if (!First || ElisaPrefixFacts::writeBetween(*First, *Store, [&](Instruction &Step) {
            return ElisaPrefixFacts::mayWrite(Step, *Counter.Buffer);
          }))
        continue;
      Increments.insert(Store);
      return true;
    }
    return false;
  }

  // Guarded, as compared by the evidence, has the value Used reads: the same
  // arithmetic, or a reload with no write of the (non-escaping) array after
  // the compared load. The compared load must not run again between the
  // comparison and the guard edge, or between the edge and Used.
  bool equivalent(Value *Guarded, Value *Used, ElisaPrefixFacts::Evidence Proof) {
    Guarded = stripZExt(Guarded);
    Used = stripZExt(Used);
    if (same(Guarded, Used)) return true;
    auto *A = dyn_cast<LoadInst>(Guarded);
    auto *B = dyn_cast<LoadInst>(Used);
    if (!A || !B || !A->isSimple() || !B->isSimple() || A->getType() != B->getType() ||
        !sameAddress(A->getPointerOperand(), B->getPointerOperand()))
      return false;
    auto *Object = dyn_cast<AllocaInst>(getUnderlyingObject(A->getPointerOperand()));
    if (!Object || PointerMayBeCaptured(Object, /*ReturnCaptures=*/true) ||
        A->getParent() == B->getParent() || Proof.Compare->getParent() == A->getParent())
      return false;
    // The compared load runs neither between the comparison and the guard
    // edge nor between the edge and the reload.
    auto ToEdge = ElisaPrefixFacts::reachForward(Proof.Compare->getParent(), nullptr, false,
                                                 nullptr, Proof.From);
    auto ToUse = ElisaPrefixFacts::reachForward(Proof.To, Proof.From, true, nullptr,
                                                B->getParent());
    if (ToEdge.count(A->getParent()) || ToUse.count(A->getParent())) return false;
    return !ElisaPrefixFacts::writeBetween(*A, *B, [&](Instruction &Step) {
      return ElisaPrefixFacts::mayWrite(Step, *Object);
    });
  }

  // Read is `Buckets[r * W + j]` under a dominating `j < C[r]`.
  bool guardedRead(Access &Read) {
    auto Place = slotOf(Read.Index);
    if (!Place || Place->Width != Width) return false;
    return anyFact(Read.Instruction->getParent(),
                   [&](ICmpInst::Predicate Predicate, Value *Left, Value *Right,
                       ElisaPrefixFacts::Evidence Proof) {
                     return Predicate == ICmpInst::ICMP_ULT &&
                            counterLoad(Right, Place->Row) &&
                            equivalent(Left, Place->Column, Proof);
                   });
  }
};

static unsigned removeBucketInitializers(Function &Function, LoopInfo &Loops,
                                         DominatorTree &Tree, const DataLayout &Layout) {
  SmallVector<std::pair<AllocaInst *, Instruction *>, 4> Fills;
  SmallPtrSet<AllocaInst *, 4> Counters;
  for (Instruction &Instruction : Function.getEntryBlock()) {
    auto *Buffer = dyn_cast<AllocaInst>(&Instruction);
    Array Buckets;
    Prover Proof(Tree, Loops, Layout);
    if (Buffer && describe(*Buffer, Buckets, Loops, Tree, Layout) &&
        Proof.removable(Buckets)) {
      Fills.push_back({Buffer, Buckets.Fill});
      Counters.insert(Proof.counter());
    }
  }
  // A counter must keep its zero fill, even if it is also a bucket array.
  unsigned Removed = 0;
  for (auto [Buffer, Fill] : Fills)
    if (!Counters.count(Buffer)) {
      Fill->eraseFromParent();
      ++Removed;
    }
  return Removed;
}
} // namespace ElisaBucketPrefix
