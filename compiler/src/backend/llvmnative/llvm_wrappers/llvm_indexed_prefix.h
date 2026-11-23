// Elide the zero fill of local arrays that are read only inside a prefix a
// loop or a copy wrote completely. A fill loop stores element i in every
// iteration i = 0, 1, ...; once it leaves through its counted exit,
// elements [0, n) hold stored values, as they do after a copy or a
// pattern fill (a loop setting each element to -1) of n elements. If every later read indexes below n, no read
// observes the fill. Indices are proved below n from scalar evolution: a
// direct comparison, the extreme of an affine recurrence, or a load from
// another such array whose every stored value is below n (an order or
// permutation array indexing a value array is the common case); a vector
// load needs its last lane below n. A stack needs no fill loop: its reads
// stay below the pushes (ElisaStackWatermark), which also covers arrays of
// structs and fills a loop repeats. The bound
// proofs themselves are ElisaIndexBounds'.
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
#include "llvm_element_offsets.h"
#include "llvm_stack_watermark.h"
namespace ElisaIndexedPrefix {
using namespace llvm;
using ElisaArrayRanges::Accesses;

using ElisaElementOffsets::divide;



struct Array {
  AllocaInst *Buffer = nullptr;
  uint64_t ElementSize = 0;
  Accesses Uses;
  // Filled by the prefix proof: the fill loop's counted exit edge, or the
  // copy that writes the prefix, and the number of elements written.
  BasicBlock *From = nullptr, *To = nullptr;
  MemIntrinsic *Copied = nullptr;
  const SCEV *Length = nullptr;
  // Length extended by stores that write the next element after the fill,
  // one after another (a ring's sentinel), before every read.
  const SCEV *Covered = nullptr;
  // Leading elements the fill must still zero (a stack's folded seeds).
  uint64_t KeptElements = 0;
  // A struct or record element: Uses.Stores holds only the field stores
  // that complete one (wholeElementStores), and Uses.Residues each field
  // access's byte offset inside its element.
  bool Composite = false;
};

class Prover : public ElisaIndexBounds::Prover {
public:
  // A bound that the predicate accepts and below which a call's callee
  // accesses the view it is passed at an argument position, or null
  // (ElisaViewBounds::Checker::callBound).
  using ViewCheck = std::function<Value *(CallBase &, unsigned, uint64_t, function_ref<bool(Value *)>)>;

  Prover(ScalarEvolution &Evolution, DominatorTree &Tree, LoopInfo &Loops,
         SmallVectorImpl<Array> &Arrays, ViewCheck Views = nullptr)
      : ElisaIndexBounds::Prover(Evolution, Tree, Loops), arrays(Arrays), views(std::move(Views)) {}

  // Whether every read of Target lies in a completely written prefix.
  bool prefixRead(Array &Target) {
    Target.KeptElements = 0;
    if (Target.Uses.Fills.size() != 1) return false;
    // A fill a loop repeats (a scratch array per query) leaves the stack
    // proof, which checks each step against the latest fill.
    if (loops.getLoopFor(Target.Uses.Fills[0]->getParent())) return stackRead(Target, true);
    for (StoreInst *Store : Target.Uses.Stores)
      if (fillLoop(Target, *Store) && readsInside(cover(Target))) return true;
    for (MemIntrinsic *Copy : Target.Uses.Writes)
      if (fillCopy(Target, *Copy) && readsInside(cover(Target))) return true;
    return stackRead(Target);
  }

private:
  SmallVectorImpl<Array> &arrays;
  ViewCheck views;
  SmallPtrSet<AllocaInst *, 4> visiting;

  // The element a load or store accesses, at a field of it for a struct.
  const SCEV *indexOf(Instruction &Access, Array &Target) {
    const SCEV *Offset = evolution.getMinusSCEV(evolution.getSCEV(getLoadStorePointerOperand(&Access)),
                                                evolution.getSCEV(Target.Buffer));
    if (isa<SCEVCouldNotCompute>(Offset)) return nullptr;
    Offset = evolution.getMinusSCEV(
        Offset, evolution.getConstant(Offset->getType(), Target.Uses.Residues.lookup(&Access)));
    return divide(evolution, Offset, Target.ElementSize);
  }

  // Blocks of L that store element i (Index) in iteration i.
  SmallPtrSet<BasicBlock *, 8> storing(Array &Target, Loop &L, const SCEV *Index) {
    SmallPtrSet<BasicBlock *, 8> Blocks;
    for (StoreInst *Store : Target.Uses.Stores)
      if (loops.getLoopFor(Store->getParent()) == &L && indexOf(*Store, Target) == Index)
        Blocks.insert(Store->getParent());
    return Blocks;
  }

  // Whether some path through an iteration of L, from its header to a
  // block Goal accepts, passes none of the Storing blocks.
  template <typename Accept>
  static bool unstoredPath(Loop &L, const SmallPtrSetImpl<BasicBlock *> &Storing, Accept Goal) {
    SmallPtrSet<BasicBlock *, 16> Seen;
    SmallVector<BasicBlock *, 8> Work{L.getHeader()};
    while (!Work.empty()) {
      BasicBlock *Block = Work.pop_back_val();
      if (Storing.contains(Block) || !Seen.insert(Block).second) continue;
      if (Goal(Block)) return true;
      for (BasicBlock *Next : successors(Block))
        if (Next != L.getHeader() && L.contains(Next)) Work.push_back(Next);
    }
    return false;
  }

  // Stores write element i of iteration i of their loop and the fill runs
  // before the loop. When every path from the header to the counted exit
  // stores (one store dominating it, or one on each branch of an if), the
  // exit after Count back edges leaves [0, Count + 1) written; a header
  // exit tested before the body leaves [0, Count) when every path to a
  // latch stores.
  bool fillLoop(Array &Target, StoreInst &Store) {
    auto *Index = dyn_cast_or_null<SCEVAddRecExpr>(indexOf(Store, Target));
    Loop *L = loops.getLoopFor(Store.getParent());
    if (!Index || !L || Index->getLoop() != L || !Index->isAffine() || !Index->getStart()->isZero() ||
        !Index->getStepRecurrence(evolution)->isOne() ||
        !tree.dominates(Target.Uses.Fills[0], L->getHeader()))
      return false;
    SmallPtrSet<BasicBlock *, 8> Storing = storing(Target, *L, Index);
    SmallVector<BasicBlock *, 4> Exiting;
    L->getExitingBlocks(Exiting);
    for (BasicBlock *Block : Exiting) {
      const SCEV *Count = evolution.getExitCount(L, Block);
      auto *Branch = dyn_cast<CondBrInst>(Block->getTerminator());
      if (isa<SCEVCouldNotCompute>(Count) || !Branch) continue;
      const SCEV *Written = nullptr;
      if (!unstoredPath(*L, Storing, [&](BasicBlock *Visited) { return Visited == Block; }))
        Written = evolution.getAddExpr(Count, evolution.getOne(Count->getType()));
      else if (Block == L->getHeader() &&
               !unstoredPath(*L, Storing, [&](BasicBlock *Visited) { return L->isLoopLatch(Visited); }))
        Written = Count;
      if (!Written) continue;
      BasicBlock *Exit = Branch->getSuccessor(L->contains(Branch->getSuccessor(0)) ? 1 : 0);
      Target.From = Block;
      Target.To = Exit;
      Target.Copied = nullptr;
      Target.Length = Written;
      return true;
    }
    return false;
  }

  // Copy (or a fill of another byte), outside any loop and after the
  // zero fill, writes the elements [0, bytes / element size).
  bool fillCopy(Array &Target, MemIntrinsic &Copy) {
    if (loops.getLoopFor(Copy.getParent()) || !tree.dominates(Target.Uses.Fills[0], &Copy)) return false;
    const SCEV *Length = divide(evolution, evolution.getSCEV(Copy.getLength()), Target.ElementSize);
    if (!Length) return false;
    Target.Copied = &Copy;
    Target.Length = Length;
    return true;
  }

  // Extends the written prefix by stores to its next element that run after
  // the fill and before every read.
  Array &cover(Array &Target) {
    Target.Covered = Target.Length;
    SmallVector<Instruction *, 16> Reads(Target.Uses.Loads.begin(), Target.Uses.Loads.end());
    Reads.append(Target.Uses.VectorLoads.begin(), Target.Uses.VectorLoads.end());
    Reads.append(Target.Uses.Copies.begin(), Target.Uses.Copies.end());
    for (InsertValueInst *View : Target.Uses.Views)
      for (User *Call : View->users()) Reads.push_back(cast<Instruction>(Call));
    auto After = [&](StoreInst &Store) {
      return Target.Copied ? tree.dominates(Target.Copied, &Store)
                           : tree.dominates(BasicBlockEdge(Target.From, Target.To), Store.getParent());
    };
    for (unsigned Extra = 0; Extra < 4; ++Extra) {
      auto Next = llvm::find_if(Target.Uses.Stores, [&](StoreInst *Store) {
        return indexOf(*Store, Target) == Target.Covered && After(*Store) &&
               llvm::all_of(Reads, [&](Instruction *Read) { return tree.dominates(Store, Read); });
      });
      if (Next == Target.Uses.Stores.end()) break;
      Target.Covered = evolution.getAddExpr(Target.Covered, evolution.getOne(Target.Covered->getType()));
    }
    return Target;
  }

  // Every call handed a view of Target has a bound below which its callee
  // reads it and that Accept takes at the call.
  bool viewBounds(Array &Target, function_ref<bool(Instruction &, Value *)> Accept) {
    for (InsertValueInst *View : Target.Uses.Views)
      for (Use &Passed : View->uses()) {
        auto &Call = *cast<CallBase>(Passed.getUser());
        if (!views || !views(Call, Call.getArgOperandNo(&Passed), Target.ElementSize,
                             [&](Value *Bound) { return evolution.isSCEVable(Bound->getType()) && Accept(Call, Bound); }))
          return false;
      }
    return true;
  }

  bool readsInside(Array &Target) {
    for (LoadInst *Load : Target.Uses.Loads) {
      const SCEV *Index = indexOf(*Load, Target);
      if (!completed(Target, *Load) || !Index || !proveBelow(Index, Target.Covered, Load))
        return false;
    }
    // Every lane of a vector load must lie in the prefix.
    for (LoadInst *Load : Target.Uses.VectorLoads) {
      const SCEV *Index = indexOf(*Load, Target);
      unsigned Lanes = cast<FixedVectorType>(Load->getType())->getNumElements();
      if (!completed(Target, *Load) || !Index ||
          !proveBelow(evolution.getAddExpr(Index, evolution.getConstant(Index->getType(), Lanes - 1)),
                      Target.Covered, Load))
        return false;
    }
    // A callee reaches the elements below its call's bound and no others.
    if (!viewBounds(Target, [&](Instruction &Call, Value *Bound) {
          return completed(Target, Call) &&
                 evolution.getTypeSizeInBits(Bound->getType()) ==
                     evolution.getTypeSizeInBits(Target.Covered->getType()) &&
                 proveAtMost(evolution.getSCEV(Bound), Target.Covered, &Call);
        }))
      return false;
    for (MemTransferInst *Copy : Target.Uses.Copies) {
      const SCEV *Bytes = evolution.getSCEV(Copy->getLength());
      const SCEV *Limit = evolution.getMulExpr(
          Target.Covered, evolution.getConstant(Target.Covered->getType(), Target.ElementSize));
      if (!completed(Target, *Copy) || Copy->getRawSource() != Target.Buffer ||
          Bytes->getType() != Limit->getType() ||
          !evolution.isKnownPredicateAt(ICmpInst::ICMP_ULE, Bytes, Limit, Copy))
        return false;
    }
    return true;
  }

  // An array used as a stack: every read is below a watermark of pushes.
  bool stackRead(Array &Target, bool FillRepeats = false) {
    if (!Target.Uses.Writes.empty() ||
        (Target.Uses.Loads.empty() && Target.Uses.VectorLoads.empty() && Target.Uses.Copies.empty() &&
         Target.Uses.Views.empty()))
      return false;
    ElisaStackWatermark::Watermark Marks(tree, *this, evolution, *Target.Buffer, Target.ElementSize,
                                         *Target.Uses.Fills[0], Target.Uses.Stores,
                                         Target.Uses.VectorStores, Target.Composite,
                                         Target.Uses.Residues, FillRepeats);
    // A vector load in the vectorizer's main loop reads below the limit its
    // last lane is below (ElisaIndexShapes), which must be a watermark.
    for (LoadInst *Load : Target.Uses.VectorLoads) {
      const SCEV *Index = indexOf(*Load, Target);
      unsigned Lanes = cast<FixedVectorType>(Load->getType())->getNumElements();
      auto *Last = Index ? dyn_cast<SCEVAddRecExpr>(evolution.getAddExpr(
                               Index, evolution.getConstant(Index->getType(), Lanes - 1)))
                         : nullptr;
      Value *Limit = Last ? ElisaIndexShapes::roundedLimit(evolution, *Last) : nullptr;
      if (!Limit || !Marks.marks(Limit, *Load)) return false;
    }
    if (!Marks.readsBelow(Target.Uses.Loads) || !Marks.copiesBelow(Target.Uses.Copies) ||
        !viewBounds(Target, [&](Instruction &Call, Value *Bound) { return Marks.marks(Bound, Call); }))
      return false;
    Target.KeptElements = Marks.kept();
    return true;
  }

  // Every path to Read leaves the fill loop through its counted exit, or
  // takes an edge on which the loop wrote nothing because Length == 0
  // there (a zero-trip guard): no read proved below Length follows such
  // an edge. Walking back from Read, a path into the loop's body or the
  // function entry that avoids both fails.
  bool completed(Array &Target, Instruction &Read) {
    if (Target.Copied) return tree.dominates(Target.Copied, &Read);
    if (tree.dominates(BasicBlockEdge(Target.From, Target.To), Read.getParent())) return true;
    // Length must keep its value from the guard to the read: every value
    // in it is defined before the fill, which runs once.
    Loop *L = loops.getLoopFor(Target.From);
    Instruction *Fill = Target.Uses.Fills[0];
    if (!L || SCEVExprContains(Target.Length, [&](const SCEV *Term) {
          auto *Unknown = dyn_cast<SCEVUnknown>(Term);
          auto *Definition = Unknown ? dyn_cast<Instruction>(Unknown->getValue()) : nullptr;
          return isa<SCEVAddRecExpr>(Term) || (Definition && !tree.dominates(Definition, Fill));
        }))
      return false;
    SmallPtrSet<BasicBlock *, 32> Seen;
    SmallVector<BasicBlock *, 16> Work{Read.getParent()};
    while (!Work.empty()) {
      BasicBlock *Block = Work.pop_back_val();
      if (!Seen.insert(Block).second) continue;
      if (L->contains(Block) || Block->isEntryBlock() || Seen.size() > 4096) return false;
      for (BasicBlock *Entry : predecessors(Block))
        if (!(Entry == Target.From && Block == Target.To) && !emptyOn(Target.Length, *Entry, *Block))
          Work.push_back(Entry);
    }
    return true;
  }

  // From -> To is taken only when Length is zero.
  bool emptyOn(const SCEV *Length, BasicBlock &From, BasicBlock &To) {
    auto *Branch = dyn_cast<CondBrInst>(From.getTerminator());
    if (!Branch || Branch->getSuccessor(0) == Branch->getSuccessor(1)) return false;
    SmallVector<std::pair<ICmpInst *, bool>, 4> Facts;
    ElisaDeadTrapExits::decided(Branch->getCondition(), Branch->getSuccessor(0) == &To, Facts);
    return llvm::any_of(Facts, [&](auto Fact) {
      auto [Compare, Truth] = Fact;
      auto Predicate = Truth ? Compare->getPredicate() : Compare->getInversePredicate();
      Value *Left = Compare->getOperand(0), *Right = Compare->getOperand(1);
      if (isa<Constant>(Left)) {
        std::swap(Left, Right);
        Predicate = ICmpInst::getSwappedPredicate(Predicate);
      }
      auto *Bound = dyn_cast<ConstantInt>(Right);
      if (!Bound || !evolution.isSCEVable(Left->getType()) || evolution.getSCEV(Left) != Length) return false;
      ConstantRange Region = ConstantRange::makeExactICmpRegion(Predicate, Bound->getValue());
      return Region.isSingleElement() && Region.getSingleElement()->isZero();
    });
  }

  // Load reads an array whose reads stay in its written prefix and whose
  // every stored value is below Length or copied from the array itself.
  bool loadBelow(LoadInst &Load, const SCEV *Length, unsigned Depth) override {
    Array *Source = nullptr;
    for (Array &Candidate : arrays)
      if (llvm::is_contained(Candidate.Uses.Loads, &Load)) Source = &Candidate;
    // A struct's grouped stores leave out the values its fields hold.
    if (!Source || Source->Composite || visiting.count(Source->Buffer)) return false;
    visiting.insert(Source->Buffer);
    auto StoredBelow = [&](StoreInst *Store) {
      Value *Stored = Store->getValueOperand();
      return llvm::is_contained(Source->Uses.Loads, Stored) ||
             (evolution.isSCEVable(Stored->getType()) &&
              below(evolution.getSCEV(Stored), Length, Store, Depth));
    };
    // Copied values are unknown, and so are zeros a kept fill provides and
    // values a view's callees store.
    bool Bounded = Source->Uses.Writes.empty() && Source->Uses.VectorStores.empty() &&
                   Source->Uses.Views.empty() &&
                   prefixRead(*Source) && !Source->KeptElements &&
                   llvm::all_of(Source->Uses.Stores, StoredBelow);
    visiting.erase(Source->Buffer);
    return Bounded;
  }
};

// A struct of scalar fields with no padding: every byte belongs to a field.
static bool denseStruct(Type *Element, const DataLayout &Layout) {
  auto *Struct = dyn_cast<StructType>(Element);
  if (!Struct || Struct->isOpaque() || Struct->getNumElements() == 0) return false;
  uint64_t Fields = 0;
  for (Type *Field : Struct->elements()) {
    if (!Field->isIntegerTy() && !Field->isFloatingPointTy() && !Field->isPointerTy()) return false;
    Fields += Layout.getTypeStoreSize(Field).getFixedValue();
  }
  uint64_t Size = Layout.getTypeAllocSize(Struct).getFixedValue();
  return Fields == Size && Size <= 64;
}

// The stores that complete an element of a struct array: a field store
// after the fill counts as a store of the whole element when it and the
// field stores to the same element that dominate it cover every byte. A
// dominating store ran in the same trip of every loop around both, so equal
// address expressions name the same element.
static SmallVector<StoreInst *, 16> wholeElementStores(Array &Target, ScalarEvolution &Evolution,
                                                       DominatorTree &Tree) {
  const DataLayout &Layout = Target.Buffer->getDataLayout();
  uint64_t Full = Target.ElementSize == 64 ? ~uint64_t(0) : (uint64_t(1) << Target.ElementSize) - 1;
  SmallVector<std::tuple<StoreInst *, const SCEV *, uint64_t>, 16> Fields;
  for (StoreInst *Store : Target.Uses.Stores) {
    if (!Tree.dominates(Target.Uses.Fills[0], Store) || Fields.size() >= 256) continue;
    uint64_t Residue = Target.Uses.Residues.lookup(Store);
    uint64_t Bytes = Layout.getTypeStoreSize(Store->getValueOperand()->getType()).getFixedValue();
    const SCEV *Start = Evolution.getMinusSCEV(
        Evolution.getSCEV(Store->getPointerOperand()),
        Evolution.getConstant(Type::getInt64Ty(Store->getContext()), Residue));
    Fields.push_back({Store, Start, (Bytes == 64 ? ~uint64_t(0) : (uint64_t(1) << Bytes) - 1) << Residue});
  }
  SmallVector<StoreInst *, 16> Whole;
  for (auto [Store, Start, Mask] : Fields) {
    uint64_t Covered = Mask;
    for (auto [Earlier, EarlierStart, EarlierMask] : Fields)
      if (EarlierStart == Start && Earlier != Store && Tree.dominates(Earlier, Store)) Covered |= EarlierMask;
    if (Covered == Full) Whole.push_back(Store);
  }
  return Whole;
}

// An integer array accessed in records of Group elements (`records[3 * i +
// 1]` and a copy of `3 * n` elements), seen as an array of such records:
// each access splits, through scalar evolution, into a whole record and a
// constant offset inside it, so the struct proof applies to the records.
static std::optional<Array> regrouped(Array &Target, unsigned Group, ScalarEvolution &Evolution,
                                      DominatorTree &Tree) {
  // A view's callees are checked against the plain element size.
  if (!Target.Uses.Views.empty()) return std::nullopt;
  const DataLayout &Layout = Target.Buffer->getDataLayout();
  Array Records;
  Records.Buffer = Target.Buffer;
  Records.Composite = true;
  Records.ElementSize = Target.ElementSize * Group;
  Accesses &Uses = Records.Uses;
  Uses.Fills = Target.Uses.Fills;
  Uses.Copies = Target.Uses.Copies;
  Uses.Writes = Target.Uses.Writes;
  auto Place = [&](Instruction &Access, Type *Accessed) {
    const SCEV *Offset = Evolution.getMinusSCEV(Evolution.getSCEV(getLoadStorePointerOperand(&Access)),
                                                Evolution.getSCEV(Target.Buffer));
    uint64_t Bytes = Layout.getTypeStoreSize(Accessed).getFixedValue();
    if (isa<SCEVCouldNotCompute>(Offset)) return false;
    for (uint64_t Residue = 0; Residue + Bytes <= Records.ElementSize; Residue += Target.ElementSize)
      if (divide(Evolution, Evolution.getMinusSCEV(Offset, Evolution.getConstant(Offset->getType(), Residue)),
                 Records.ElementSize)) {
        if (Residue) Uses.Residues[&Access] = Residue;
        return true;
      }
    return false;
  };
  using LoadList = ArrayRef<LoadInst *>;
  using StoreList = ArrayRef<StoreInst *>;
  for (LoadList Loads : {LoadList(Target.Uses.Loads), LoadList(Target.Uses.VectorLoads)})
    for (LoadInst *Load : Loads) {
      if (!Place(*Load, Load->getType())) return std::nullopt;
      Uses.Loads.push_back(Load);
    }
  for (StoreList Stores : {StoreList(Target.Uses.Stores), StoreList(Target.Uses.VectorStores)})
    for (StoreInst *Store : Stores) {
      if (!Place(*Store, Store->getValueOperand()->getType())) return std::nullopt;
      Uses.Stores.push_back(Store);
    }
  Uses.Stores = wholeElementStores(Records, Evolution, Tree);
  return Records;
}

static unsigned removeIndexedPrefixFills(Function &F, FunctionAnalysisManager &Analyses) {
  const DataLayout &Layout = F.getDataLayout();
  SmallVector<Array, 4> Arrays;
  for (Instruction &Instruction : F.getEntryBlock()) {
    auto *Buffer = dyn_cast<AllocaInst>(&Instruction);
    auto *Type = Buffer ? dyn_cast<ArrayType>(Buffer->getAllocatedType()) : nullptr;
    if (!Type || !Buffer->isStaticAlloca() ||
        !(Type->getElementType()->isIntegerTy() || denseStruct(Type->getElementType(), Layout)))
      continue;
    Array Candidate;
    Candidate.Buffer = Buffer;
    Candidate.Composite = Type->getElementType()->isStructTy();
    Candidate.ElementSize = Layout.getTypeAllocSize(Type->getElementType()).getFixedValue();
    // Vector stores only ever add written elements to a proof (ElisaStackWatermark).
    if (ElisaArrayRanges::collect(Buffer, Type->getElementType(), Candidate.ElementSize, Layout,
                                  Candidate.Uses, 0, true) &&
        Candidate.Uses.Fills.size() == 1 &&
        (!Candidate.Uses.Stores.empty() || !Candidate.Uses.VectorStores.empty() ||
         !Candidate.Uses.Writes.empty()))
      Arrays.push_back(std::move(Candidate));
  }
  if (Arrays.empty()) return 0;
  auto &Evolution = Analyses.getResult<ScalarEvolutionAnalysis>(F);
  auto &Tree = Analyses.getResult<DominatorTreeAnalysis>(F);
  auto &Loops = Analyses.getResult<LoopAnalysis>(F);
  for (Array &Target : Arrays)
    if (Target.Composite) Target.Uses.Stores = wholeElementStores(Target, Evolution, Tree);
  SmallVector<std::tuple<Instruction *, uint64_t, AllocaInst *>, 4> Dead;
  // Callees are checked on cleaned copies that the checker deletes.
  auto Views = [&](CallBase &Call, unsigned Position, uint64_t ElementSize, function_ref<bool(Value *)> Accept) {
    ElisaViewBounds::Checker Callees(Analyses, ElementSize);
    return Callees.callBound(Call, Position, F, Accept);
  };
  for (Array &Target : Arrays) {
    Prover Proof(Evolution, Tree, Loops, Arrays, Views);
    Array *Proved = Proof.prefixRead(Target) ? &Target : nullptr;
    std::optional<Array> Records;
    for (unsigned Group = 2; !Proved && !Target.Composite && !Target.Uses.Stores.empty() &&
                             Target.ElementSize * Group <= 64;
         ++Group)
      if ((Records = regrouped(Target, Group, Evolution, Tree)) && Proof.prefixRead(*Records)) Proved = &*Records;
    if (Proved) Dead.push_back({Target.Uses.Fills[0], Proved->KeptElements * Proved->ElementSize, Target.Buffer});
  }
  for (auto [Fill, Kept, Buffer] : Dead)
    if (auto *Zeros = dyn_cast<MemSetInst>(Fill); Zeros && Kept) {
      // Seeds of a record past a short array's end are not the fill's to keep.
      auto *Length = dyn_cast<ConstantInt>(Zeros->getLength());
      if (Length && Length->getZExtValue() > Kept)
        Zeros->setLength(ConstantInt::get(Zeros->getLength()->getType(), Kept));
    } else {
      // Every read still sees a stored element, so the array keeps the
      // value ranges of its stores (ElisaArrayRanges).
      Buffer->setMetadata(ElisaArrayRanges::WrittenPrefix, MDNode::get(F.getContext(), {}));
      Fill->eraseFromParent();
    }
  if (!Dead.empty()) Analyses.invalidate(F, PreservedAnalyses::none());
  return Dead.size();
}

static unsigned removeIndexedPrefixFills(Module &Module) {
  // A view's callees are cleaned by function passes that consult module
  // analyses (ElisaViewBounds), so every manager is registered and linked.
  PassBuilder Builder;
  LoopAnalysisManager LoopAnalyses;
  FunctionAnalysisManager Analyses;
  CGSCCAnalysisManager CallGraphAnalyses;
  ModuleAnalysisManager ModuleAnalyses;
  Builder.registerModuleAnalyses(ModuleAnalyses);
  Builder.registerCGSCCAnalyses(CallGraphAnalyses);
  Builder.registerFunctionAnalyses(Analyses);
  Builder.registerLoopAnalyses(LoopAnalyses);
  Builder.crossRegisterProxies(LoopAnalyses, Analyses, CallGraphAnalyses, ModuleAnalyses);
  unsigned Removed = 0;
  for (Function &F : Module)
    if (!F.isDeclaration() && !F.hasOptNone()) Removed += removeIndexedPrefixFills(F, Analyses);
  return Removed;
}
} // namespace ElisaIndexedPrefix
