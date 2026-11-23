// Bound the values a load from a local integer array reads by checks made
// after each store: validation code often stores an input value first and
// tests it afterwards (`right[k] <- input[off]; return -1 if right[k] > n`).
// A load of such an array is at most H when the array starts from a zero
// fill and, for every store of a value V into it, either V is at most H, or
// every path from the store returns, or decides V <=u H, before it reads
// the array anywhere else or stores V's element again. Along a path, loads
// of one address with no store to that object between them are one value,
// so a test that reloads the element (and a neighbour it is compared with)
// decides the stored value itself. Signed tests count when they also show
// V >=s 0: `0 <=s left <=s V <=s n` puts V in [0, n] unsigned.
#include <llvm/Analysis/ValueTracking.h>
namespace ElisaElementBounds {
using namespace llvm;

class Checker {
public:
  Checker(ScalarEvolution &Evolution, DominatorTree &Tree, LoopInfo &Loops)
      : evolution(Evolution), tree(Tree), loops(Loops) {}

  // Load <=u High wherever Load executes; StoredAtMost proves a stored
  // value at its store before the path walk is tried.
  bool atMost(LoadInst &Load, const SCEV *High,
              function_ref<bool(const SCEV *, const SCEV *, Instruction *)> StoredAtMost) {
    auto *Array = dyn_cast<AllocaInst>(getUnderlyingObject(Load.getPointerOperand()));
    auto *Type = Array ? dyn_cast<ArrayType>(Array->getAllocatedType()) : nullptr;
    if (!Type || Type->getElementType() != Load.getType() || !Load.getType()->isIntegerTy() ||
        !fixedEverywhere(High, Load))
      return false;
    auto Key = std::make_pair(Array, High);
    if (auto Known = proved.find(Key); Known != proved.end()) return Known->second && filledBefore(Load);
    proved[Key] = false;
    const DataLayout &Layout = Array->getDataLayout();
    ElisaArrayRanges::Accesses Uses;
    uint64_t ElementSize = Layout.getTypeAllocSize(Load.getType()).getFixedValue();
    bool Bounded = ElisaArrayRanges::collect(Array, Load.getType(), ElementSize, Layout, Uses) &&
                   Uses.Filled && Uses.Writes.empty() && Uses.Views.empty() &&
                   Uses.VectorStores.empty() && Uses.VectorLoads.empty() && Uses.Fills.size() == 1 &&
                   !Uses.Stores.empty() &&
                   llvm::all_of(Uses.Stores, [&](StoreInst *Store) {
                     return fixedEverywhere(High, *Store) &&
                            (StoredAtMost(evolution.getSCEV(Store->getValueOperand()), High, Store) ||
                             checked(*Store, *Array, High));
                   });
    fills[Array] = Uses.Fills.empty() ? nullptr : Uses.Fills[0];
    proved[Key] = Bounded;
    return Bounded && filledBefore(Load);
  }

private:
  ScalarEvolution &evolution;
  DominatorTree &tree;
  LoopInfo &loops;
  DenseMap<std::pair<AllocaInst *, const SCEV *>, bool> proved;
  DenseMap<AllocaInst *, Instruction *> fills;

  // A path from the store: the facts its edges decided, and the load that
  // stands for each address read since the last store to its object.
  struct Path {
    SmallVector<std::pair<ICmpInst *, bool>, 8> Facts;
    SmallVector<std::pair<const SCEV *, Value *>, 8> Reads;
    SmallVector<BasicBlock *, 16> Blocks;
    // The block the path came from, which picks each phi's value.
    BasicBlock *From = nullptr;
  };

  bool filledBefore(LoadInst &Load) {
    auto *Array = cast<AllocaInst>(getUnderlyingObject(Load.getPointerOperand()));
    Instruction *Fill = fills.lookup(Array);
    return Fill && tree.dominates(Fill, &Load);
  }

  // High names one value everywhere: each value in it is an argument or is
  // defined outside every loop, before At.
  bool fixedEverywhere(const SCEV *High, Instruction &At) {
    return !SCEVExprContains(High, [&](const SCEV *Term) {
      if (isa<SCEVAddRecExpr>(Term)) return true;
      auto *Unknown = dyn_cast<SCEVUnknown>(Term);
      auto *Definition = Unknown ? dyn_cast<Instruction>(Unknown->getValue()) : nullptr;
      return Unknown && !isa<Argument>(Unknown->getValue()) && !isa<Constant>(Unknown->getValue()) &&
             (!Definition || loops.getLoopFor(Definition->getParent()) || !tree.dominates(Definition, &At));
    });
  }

  Value *standing(Path &State, Value *Read) {
    auto *Load = dyn_cast<LoadInst>(Read);
    if (!Load) return Read;
    const SCEV *Address = evolution.getSCEV(Load->getPointerOperand());
    for (auto &[Seen, Value] : State.Reads)
      if (Seen == Address) return Value;
    return Read;
  }

  bool checked(StoreInst &Store, AllocaInst &Array, const SCEV *High) {
    Path Start;
    Start.Reads.push_back({evolution.getSCEV(Store.getPointerOperand()), Store.getValueOperand()});
    unsigned Budget = 2000;
    return walk(Start, *Store.getParent(), std::next(Store.getIterator()), Store, Array, High, Budget);
  }

  // Every continuation of State from Position returns or decides the stored
  // value at most High before it touches the array again.
  bool walk(Path State, BasicBlock &Block, BasicBlock::iterator Position, StoreInst &Store,
            AllocaInst &Array, const SCEV *High, unsigned &Budget) {
    if (Budget == 0 || State.Blocks.size() > 48) return false;
    --Budget;
    auto *Stored = dyn_cast<Instruction>(Store.getValueOperand());
    for (auto Step = Position; Step != Block.end(); ++Step) {
      Instruction &Current = *Step;
      if (auto *Load = dyn_cast<LoadInst>(&Current)) {
        const SCEV *Address = evolution.getSCEV(Load->getPointerOperand());
        bool Same = llvm::any_of(State.Reads, [&](auto &Read) { return Read.first == Address; });
        if (getUnderlyingObject(Load->getPointerOperand()) == &Array &&
            Address != State.Reads.front().first)
          return false;
        if (!Same) State.Reads.push_back({Address, Load});
      } else if (auto *Write = dyn_cast<StoreInst>(&Current)) {
        const Value *Object = getUnderlyingObject(Write->getPointerOperand());
        if (Object == &Array) return false;
        forget(State, Object);
      } else if (auto *Call = dyn_cast<CallBase>(&Current)) {
        if (Call->getIntrinsicID() == Intrinsic::trap) return true;
        if (auto *Copy = dyn_cast<MemIntrinsic>(Call); Copy && getUnderlyingObject(Copy->getRawDest()) == &Array)
          return false;
        if (auto *Copy = dyn_cast<MemTransferInst>(Call); Copy && getUnderlyingObject(Copy->getRawSource()) == &Array)
          return false;
        if (!Call->onlyReadsMemory()) State.Reads.resize(1);
      }
    }
    Instruction *Exit = Block.getTerminator();
    if (isa<ReturnInst>(Exit) || isa<UnreachableInst>(Exit)) return true;
    auto *Branch = dyn_cast<BranchInst>(Exit);
    if (!Branch) return false;
    // A short-circuit result is the phi's value on the edge the path took;
    // a constant one leaves a single successor.
    Value *Condition = Branch->isConditional() ? Branch->getCondition() : nullptr;
    if (auto *Merge = dyn_cast_or_null<PHINode>(Condition); Merge && Merge->getParent() == &Block && State.From)
      Condition = Merge->getIncomingValueForBlock(State.From);
    auto *Known = dyn_cast_or_null<ConstantInt>(Condition);
    for (unsigned Index = 0; Index < Branch->getNumSuccessors(); ++Index) {
      if (Known && Known->isOne() != (Index == 0)) continue;
      BasicBlock *Next = Branch->getSuccessor(Index);
      if (Next == Store.getParent() || (Stored && Next == Stored->getParent()) ||
          llvm::is_contained(State.Blocks, Next))
        return false;
      Path Taken = State;
      if (Condition && !Known) ElisaDeadTrapExits::decided(Condition, Index == 0, Taken.Facts);
      if (decides(Taken, High)) continue;
      // Past a loop header the same address or value may name another
      // iteration's: the store's own loop ends the proof, and an inner loop
      // keeps only the stored value.
      if (Loop *Around = loops.getLoopFor(Next); Around && Around->getHeader() == Next) {
        if (Around->contains(Store.getParent())) return false;
        Taken.Reads.resize(1);
        Taken.Facts.clear();
      }
      Taken.Blocks.push_back(Next);
      Taken.From = &Block;
      if (!walk(std::move(Taken), *Next, Next->begin(), Store, Array, High, Budget)) return false;
    }
    return true;
  }

  // A store to Object makes its reads stale; the stored value stays.
  static void forget(Path &State, const Value *Object) {
    for (unsigned Index = State.Reads.size(); Index-- > 1;)
      if (auto *Load = dyn_cast<LoadInst>(State.Reads[Index].second);
          Load && getUnderlyingObject(Load->getPointerOperand()) == Object)
        State.Reads.erase(State.Reads.begin() + Index);
  }

  // The facts on the path hold the stored value at most High.
  bool decides(Path &State, const SCEV *High) {
    Value *Stored = State.Reads.front().second;
    for (auto [Left, Predicate, Right] : relations(State)) {
      if (Left != Stored || evolution.getSCEV(Right) != High) continue;
      if (Predicate == ICmpInst::ICMP_ULE || Predicate == ICmpInst::ICMP_ULT) return true;
      if ((Predicate == ICmpInst::ICMP_SLE || Predicate == ICmpInst::ICMP_SLT) && nonnegative(State, Stored, 0))
        return true;
    }
    return false;
  }

  // Every decided comparison as Left Predicate Right, both ways round,
  // with each read replaced by the value that stands for its address.
  SmallVector<std::tuple<Value *, ICmpInst::Predicate, Value *>, 16> relations(Path &State) {
    SmallVector<std::tuple<Value *, ICmpInst::Predicate, Value *>, 16> Out;
    for (auto [Compare, Truth] : State.Facts) {
      auto Predicate = Truth ? Compare->getPredicate() : Compare->getInversePredicate();
      Value *Left = standing(State, Compare->getOperand(0)), *Right = standing(State, Compare->getOperand(1));
      Out.push_back({Left, Predicate, Right});
      Out.push_back({Right, ICmpInst::getSwappedPredicate(Predicate), Left});
    }
    return Out;
  }

  bool nonnegative(Path &State, Value *Term, unsigned Depth) {
    if (Depth > 3) return false;
    for (auto [Left, Predicate, Right] : relations(State)) {
      if (Left != Term || Right == Term) continue;
      auto *Constant = dyn_cast<ConstantInt>(Right);
      if (Predicate == ICmpInst::ICMP_SGE &&
          ((Constant && !Constant->isNegative()) || (!Constant && nonnegative(State, Right, Depth + 1))))
        return true;
      if (Predicate == ICmpInst::ICMP_SGT &&
          ((Constant && Constant->getSExtValue() >= -1) || (!Constant && nonnegative(State, Right, Depth + 1))))
        return true;
    }
    return false;
  }
};
} // namespace ElisaElementBounds
