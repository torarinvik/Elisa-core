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
// unwrite an element, so a claim that held earlier still holds. A push of
// k elements (X + k, an append of a pair) needs stores to X .. X + k - 1;
// a store's element is compared as an address offset, so the second store
// of a pair, addressed from the first, counts. A copy out of elements
// [0, X) of a watermark X (the buffer serialized at the end) reads only
// stored values too. A read at most X, where a push of element X reaches
// it, reads below X + 1 (a scan whose exit compares the index with X).
// umin(X, C) is a watermark when X is (a count clamped to the capacity),
// and a phi may skip an incoming -1 that a branch before the read rules
// out (a failure result tested by the caller); such a phi is a watermark
// only at that read, so the induction never assumes it. Struct elements
// count as stored once field stores in one block cover them; a field read
// reads its element. A fill a loop repeats (a scratch buffer per query)
// zeroes the buffer again on every trip: the induction then only covers
// phis below the fill, which no back edge to it passes.
#include <llvm/IR/Dominators.h>
#include <llvm/IR/PatternMatch.h>
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
#include "llvm_edge_exclusion.h"
#include "llvm_stack_watermark_loops.h"
namespace ElisaStackWatermark {
using namespace llvm;

class Watermark {
public:
  Watermark(DominatorTree &Tree, ElisaIndexBounds::Prover &Proof, ScalarEvolution &Evolution,
            AllocaInst &Buffer, uint64_t ElementSize, Instruction &Fill,
            ArrayRef<StoreInst *> Stores, ArrayRef<StoreInst *> VectorStores, bool Composite,
            const SmallDenseMap<Instruction *, uint64_t, 8> &Residues, bool FillRepeats = false)
      : tree(Tree), proof(Proof), evolution(Evolution), buffer(Buffer),
        elementSize(ElementSize), fill(Fill), stores(Stores), vectorStores(VectorStores),
        composite(Composite), fillRepeats(FillRepeats), residues(Residues) {}

  // Elements the fill itself must still write: a seed the fill provides.
  uint64_t kept() const { return keptElements; }

  // Every load reads element X - 1 of a nonzero watermark X.
  // Or a load is below a watermark X another way: a peek addressed as
  // `&buffer[X] - 1`, or a scan of element I with I <u X proved at the load.
  bool readsBelow(ArrayRef<LoadInst *> Loads) {
    using namespace PatternMatch;
    for (LoadInst *Load : Loads) {
      if (!tree.dominates(&fill, Load)) return false;
      Value *Index = element(*Load), *Mark = nullptr;
      auto *Pop = dyn_cast_or_null<Instruction>(Index);
      if (Pop && (Mark = ElisaWatermarkLoops::popped(Pop)) && nonzero(Mark, Pop, 0) &&
          attempt([&] { return holds(Mark, Load, 0); }))
        continue;
      if (!llvm::any_of(readCandidates(*Load), [&](Value *Candidate) {
            return readBelow(*Load, Candidate) || readThrough(*Load, Candidate);
          }))
        return false;
    }
    return true;
  }

  // Every copy out of the buffer reads elements [0, X) of a watermark X:
  // its length is X scaled by the element size. A product that wrapped is
  // smaller than X times the size, so the copy reads less, still below X.
  // When scalar evolution rewrote the length from a loop counter (`16 * i +
  // 16` for pairs), X is found among the stored indices and their pushes.
  bool copiesBelow(ArrayRef<MemTransferInst *> Copies) {
    using namespace PatternMatch;
    for (MemTransferInst *Copy : Copies) {
      Value *Mark = nullptr;
      const APInt *Factor = nullptr;
      Value *Length = Copy->getLength();
      bool Scaled = (match(Length, m_Shl(m_Value(Mark), m_APInt(Factor))) && Factor->ult(64) &&
                     (uint64_t(1) << Factor->getZExtValue()) == elementSize) ||
                    (match(Length, m_Mul(m_Value(Mark), m_APInt(Factor))) && *Factor == elementSize) ||
                    (elementSize == 1 && (Mark = Length));
      if (Copy->getRawSource() != &buffer || !tree.dominates(&fill, Copy)) return false;
      if (Scaled && attempt([&] { return holds(Mark, Copy, 0); })) continue;
      if (!llvm::any_of(candidates(*Copy), [&](Value *Candidate) {
            return attempt([&] { return holds(Candidate, Copy, 0); });
          }))
        return false;
    }
    return true;
  }

  // Elements [0, Bound) were stored after the fill when At executes.
  bool marks(Value *Bound, Instruction &At) {
    return tree.dominates(&fill, &At) && attempt([&] { return holds(Bound, &At, 0); });
  }

private:
  // Runs one alternative proof; a failed one must not leave the phis it
  // assumed behind for the next alternative to rely on.
  template <typename Proof> bool attempt(Proof Alternative) {
    SmallPtrSet<PHINode *, 8> Before = assumed;
    if (Alternative()) return true;
    assumed = std::move(Before);
    return false;
  }

  // Load reads element X - 1 (X nonzero) or an element proved below X,
  // and X is a watermark at the load.
  bool readBelow(LoadInst &Load, Value *Mark) {
    const SCEV *Bytes = offset(Load);
    if (!Bytes || Mark->getType() != Bytes->getType()) return false;
    const SCEV *Size = evolution.getConstant(Bytes->getType(), elementSize);
    const SCEV *Top = evolution.getSCEV(Mark);
    bool Peek = Bytes == evolution.getMulExpr(evolution.getAddExpr(Top, evolution.getMinusOne(Top->getType())), Size) &&
                nonzero(Mark, &Load, 0);
    // The element index: the address's own index, else the offset divided.
    Value *Index = Peek ? nullptr : element(Load);
    const SCEV *Element = Peek ? nullptr
                          : Index && Index->getType() == Top->getType() ? evolution.getSCEV(Index)
                                                                      : ElisaWatermarkLoops::quotient(evolution, Bytes, elementSize);
    if (!Peek && (evolution.getMulExpr(Element, Size) != Bytes ||
                  !(countsBelow(Element, Top, Load, Mark) || proof.proveBelow(Element, Top, &Load))))
      return false;
    return attempt([&] { return holds(Mark, &Load, 0); });
  }

  // Load reads an element at most Last, and a push of element Last after
  // [0, Last) held reaches Load: a scan `while i <= top` whose exit
  // compares i with top, or one proved below Last + 1.
  bool readThrough(LoadInst &Load, Value *Last) {
    const SCEV *Bytes = offset(Load);
    if (!Bytes || Last->getType() != Bytes->getType()) return false;
    const SCEV *Size = evolution.getConstant(Bytes->getType(), elementSize);
    const SCEV *Final = evolution.getSCEV(Last);
    Value *Index = element(Load);
    const SCEV *Element = Index && Index->getType() == Final->getType() ? evolution.getSCEV(Index)
                                                                       : ElisaWatermarkLoops::quotient(evolution, Bytes, elementSize);
    auto *Counter = dyn_cast<SCEVAddRecExpr>(Element);
    bool Counted = Counter && Counter->isAffine() && Counter->getStart()->isZero() &&
                   Counter->getStepRecurrence(evolution)->isOne() && Counter->getLoop()->contains(&Load) &&
                   ElisaWatermarkLoops::takenAtMost(evolution, *Counter->getLoop(), Final);
    const SCEV *Past = evolution.getAddExpr(Final, evolution.getOne(Final->getType()));
    return evolution.getMulExpr(Element, Size) == Bytes && (Counted || proof.proveBelow(Element, Past, &Load)) &&
           attempt([&] { return pushed(Last, 0, &Load, 0); });
  }

  // Element counts 0, 1, ... in a loop around Load that takes at most
  // Top - 1 back edges, Top nonzero there: a scan `while i < top` whose
  // exit test became `i + 1 == top`.
  bool countsBelow(const SCEV *Element, const SCEV *Top, LoadInst &Load, Value *Mark) {
    auto *Counter = dyn_cast<SCEVAddRecExpr>(Element);
    if (!Counter || !Counter->isAffine() || !Counter->getStart()->isZero() ||
        !Counter->getStepRecurrence(evolution)->isOne() || !Counter->getLoop()->contains(&Load))
      return false;
    return Top->getType() == Counter->getType() &&
           ElisaWatermarkLoops::takenAtMost(evolution, *Counter->getLoop(),
                                            evolution.getMinusSCEV(Top, evolution.getOne(Top->getType()))) &&
           nonzero(Mark, &Load, 0);
  }

  // Watermarks a read may be below: the indices in its address, the
  // indices stores write and their pushes, and the bounds its index is
  // compared with.
  SmallVector<Value *, 8> readCandidates(LoadInst &Load) {
    SmallVector<Value *, 8> Found;
    auto Consider = [&](Value *V) {
      auto *Definition = dyn_cast<Instruction>(V);
      if (Found.size() < 8 && V->getType()->isIntegerTy() && !isa<Constant>(V) &&
          (!Definition || tree.dominates(Definition, &Load)) && !llvm::is_contained(Found, V))
        Found.push_back(V);
    };
    SmallVector<Value *, 4> Indices;
    for (Value *Pointer = Load.getPointerOperand(); auto *Address = dyn_cast<GetElementPtrInst>(Pointer);
         Pointer = Address->getPointerOperand())
      for (Value *Operand : Address->indices())
        if (!isa<Constant>(Operand)) Indices.push_back(Operand);
    for (Value *Index : Indices) {
      Consider(Index);
      for (User *Use : Index->users()) {
        auto *Compare = dyn_cast<ICmpInst>(Use);
        auto *Step = dyn_cast<BinaryOperator>(Use);
        if (Step && Step->getOpcode() == Instruction::Add && isa<ConstantInt>(Step->getOperand(1)))
          for (User *Next : Step->users())
            if (auto *Exit = dyn_cast<ICmpInst>(Next))
              Consider(Exit->getOperand(Exit->getOperand(0) == Step ? 1 : 0));
        if (Compare) Consider(Compare->getOperand(Compare->getOperand(0) == Index ? 1 : 0));
      }
    }
    // A push's count leaves its loop through a phi (`phi [0, n + 1]`).
    for (StoreInst *Store : stores)
      if (Value *Index = element(*Store); Index && !isa<Constant>(Index)) {
        Consider(Index);
        for (User *Use : Index->users())
          if (auto *Compare = dyn_cast<ICmpInst>(Use)) // `at == count` before a push
            Consider(Compare->getOperand(Compare->getOperand(0) == Index ? 1 : 0));
          else if (auto *Sum = dyn_cast<BinaryOperator>(Use); Sum && Sum->getOpcode() == Instruction::Add &&
                                                       isa<ConstantInt>(Sum->getOperand(1))) {
            Consider(Sum);
            for (User *Exit : Sum->users())
              if (isa<PHINode>(Exit)) Consider(Exit);
          }
      }
    return Found;
  }

  // Values defined before Copy whose scaled value is its length: the
  // indices stores write and those indices plus a small push.
  SmallVector<Value *, 4> candidates(MemTransferInst &Copy) {
    SmallVector<Value *, 4> Found;
    Value *Length = Copy.getLength();
    if (!evolution.isSCEVable(Length->getType())) return Found;
    const SCEV *Bytes = evolution.getSCEV(Length);
    auto Consider = [&](Value *V) {
      auto *Definition = dyn_cast<Instruction>(V);
      if (!V->getType()->isIntegerTy() || V->getType() != Length->getType() ||
          (Definition && !tree.dominates(Definition, &Copy)) || llvm::is_contained(Found, V) ||
          evolution.getMulExpr(evolution.getSCEV(V), evolution.getConstant(V->getType(), elementSize)) != Bytes)
        return;
      Found.push_back(V);
    };
    for (StoreInst *Store : stores) {
      Value *Index = element(*Store);
      if (!Index || isa<Constant>(Index)) continue;
      Consider(Index);
      for (User *Use : Index->users())
        if (auto *Sum = dyn_cast<BinaryOperator>(Use); Sum && Sum->getOpcode() == Instruction::Add &&
                                                     isa<ConstantInt>(Sum->getOperand(1)))
          Consider(Sum);
      if (Found.size() >= 4) break;
    }
    return Found;
  }

  DominatorTree &tree;
  ElisaIndexBounds::Prover &proof;
  ScalarEvolution &evolution;
  AllocaInst &buffer;
  uint64_t elementSize;
  Instruction &fill;
  ArrayRef<StoreInst *> stores;
  ArrayRef<StoreInst *> vectorStores;
  bool composite, fillRepeats;
  const SmallDenseMap<Instruction *, uint64_t, 8> &residues;
  SmallPtrSet<PHINode *, 8> assumed;
  // Phis proved with an edge excluded, being proved now.
  SmallPtrSet<PHINode *, 4> guarded;
  uint64_t keptElements = 0;

  // A store of a whole element, or a struct's field store completing one.
  bool whole(StoreInst &Store) {
    return composite || Store.getValueOperand()->getType() == buffer.getAllocatedType()->getArrayElementType();
  }

  // The element a load or store accesses: a field's address, a constant
  // step past the element's own, gives way to that address.
  Value *element(Instruction &Access) {
    Value *Pointer = getLoadStorePointerOperand(&Access);
    if (uint64_t Residue = residues.lookup(&Access)) {
      auto *Field = dyn_cast<GetElementPtrInst>(Pointer);
      APInt Offset(64, 0);
      if (!Field || !Field->accumulateConstantOffset(Field->getDataLayout(), Offset) || Offset != Residue)
        return nullptr;
      Pointer = Field->getPointerOperand();
    }
    return element(Pointer);
  }

  // The element index Pointer addresses in the buffer.
  Value *element(Value *Pointer) { return ElisaWatermarkLoops::element(buffer, elementSize, Pointer); }

  // Elements [0, Mark) were stored after the fill whenever At executes.
  bool holds(Value *Mark, Instruction *At, unsigned Depth) {
    using namespace PatternMatch;
    Value *Inner = nullptr;
    if (match(Mark, m_Zero())) return true;
    if (auto *Constant = dyn_cast<ConstantInt>(Mark)) return storedBelow(Constant->getValue(), At);
    if (Depth > 16) return false;
    if (auto *Phi = dyn_cast<PHINode>(Mark)) {
      // A fill inside a loop zeroes the buffer on every trip. Below the
      // fill no back edge to Phi passes it, so the induction holds there.
      if (fillRepeats && (Phi->getParent() == fill.getParent() ||
                          !tree.dominates(fill.getParent(), Phi->getParent())))
        return false;
      // A constant too large to be a watermark that At never sees (a -1
      // failure result tested before the reads) needs no proof, but then
      // Phi is a watermark only at At, not wherever the induction would
      // assume it: it must not recur. An incoming value that takes only a
      // few constants (`sext` of a flag) is proved value by value.
      if (guarded.contains(Phi)) return false;
      if (assumed.contains(Phi)) return true;
      // An incoming limit equal to a counter on its edge may be proved as
      // the counter.
      struct Obligation {
        Value *Mark, *Equal;
        Instruction *End;
      };
      SmallVector<Obligation, 4> Obligations;
      bool Guarded = false;
      for (unsigned Edge = 0; Edge < Phi->getNumIncomingValues(); ++Edge) {
        Value *Incoming = Phi->getIncomingValue(Edge);
        BasicBlock *From = Phi->getIncomingBlock(Edge);
        SmallVector<APInt, 2> Choices;
        if (!ElisaEdgeExclusion::constantChoices(Incoming, Choices))
          Obligations.push_back(
              {Incoming, ElisaEdgeExclusion::equalOnEdge(Incoming, From, Phi->getParent()), From->getTerminator()});
        for (const APInt &Choice : Choices)
          if (Choice.ugt(8) && ElisaEdgeExclusion::excludedAt(tree, *Phi, Choice, At)) Guarded = true;
          else Obligations.push_back({ConstantInt::get(Incoming->getType(), Choice), nullptr, From->getTerminator()});
      }
      if (Guarded) guarded.insert(Phi);
      else assumed.insert(Phi);
      bool All = llvm::all_of(Obligations, [&](const Obligation &Next) {
        return attempt([&] { return holds(Next.Mark, Next.End, Depth + 1); }) ||
               (Next.Equal && Next.Equal->getType() == Next.Mark->getType() &&
                attempt([&] { return holds(Next.Equal, Next.End, Depth + 1); }));
      });
      if (Guarded) guarded.erase(Phi);
      return All;
    }
    // A count clamped to the capacity is at most the count.
    Value *Other = nullptr;
    if (ElisaWatermarkLoops::unsignedMinimum(Mark, Inner, Other))
      return attempt([&] { return holds(Inner, At, Depth + 1); }) ||
             attempt([&] { return holds(Other, At, Depth + 1); });
    auto *Step = dyn_cast<Instruction>(Mark);
    const APInt *Pushed = nullptr;
    if (Step && (Inner = ElisaWatermarkLoops::popped(Step)))
      return nonzero(Inner, Step, 0) && holds(Inner, At, Depth + 1);
    if (Step && match(Step, m_Add(m_Value(Inner), m_Value(Other))) && !isa<Constant>(Other))
      return attempt([&] { return equalPushed(Inner, Other, Step, Depth); }) ||
             attempt([&] { return equalPushed(Other, Inner, Step, Depth); }) ||
             attempt([&] { return branchPushed(Inner, Other, Step, Depth); }) ||
             attempt([&] { return branchPushed(Other, Inner, Step, Depth); }) ||
             attempt([&] { return loopPushed(Inner, Step, At, Depth); }) ||
             attempt([&] { return loopPushed(Other, Step, At, Depth); });
    if (!Step || !match(Step, m_Add(m_Value(Inner), m_APInt(Pushed))) || Pushed->isZero() ||
        Pushed->ugt(8)) {
      // Mark equals a watermark on an edge below the fill that At's latest
      // run took (a loop left when its counter reached the limit Mark).
      std::pair<Value *, Instruction *> Equal = ElisaEdgeExclusion::equalAbove(tree, Mark, At, fill.getParent());
      return Equal.first && Equal.first->getType() == Mark->getType() &&
             attempt([&] { return holds(Equal.first, Equal.second, Depth + 1); });
    }
    for (uint64_t Offset = 0; Offset < Pushed->getZExtValue(); ++Offset)
      if (!pushed(Inner, Offset, At, Depth)) return false;
    return true;
  }

  // Mark = Inner + Count where Count is a phi, in Mark's block, of small
  // constants (branches that pushed different numbers of elements, their
  // increments sunk into the join): each branch pushed its own count by
  // the end of its incoming block, and the latest entry into that block
  // chose Mark's value.
  bool branchPushed(Value *Inner, Value *Count, Instruction *Mark, unsigned Depth) {
    auto *Phi = dyn_cast<PHINode>(Count);
    if (!Phi || Phi->getParent() != Mark->getParent()) return false;
    for (unsigned Edge = 0; Edge < Phi->getNumIncomingValues(); ++Edge) {
      auto *Pushed = dyn_cast<ConstantInt>(Phi->getIncomingValue(Edge));
      Instruction *End = Phi->getIncomingBlock(Edge)->getTerminator();
      if (!Pushed || Pushed->getValue().ugt(8)) return false;
      if (Pushed->isZero() && !holds(Inner, End, Depth + 1)) return false;
      for (uint64_t Offset = 0; Offset < Pushed->getZExtValue(); ++Offset)
        if (!pushed(Inner, Offset, End, Depth)) return false;
    }
    return true;
  }

  // Mark = Inner + [X == Inner] after a store of element X at a point where
  // [0, Inner) holds: the store pushed element Inner exactly when X is Inner
  // (an insertion point that extends the stack only at its top, as in
  // patience sorting), and otherwise left [0, Inner) written.
  bool equalPushed(Value *Inner, Value *Flag, Instruction *Mark, unsigned Depth) {
    using namespace PatternMatch;
    Value *Index = nullptr;
    CmpPredicate Predicate;
    if (!match(Flag, m_ZExt(m_c_ICmp(Predicate, m_Value(Index), m_Specific(Inner)))) ||
        Predicate != ICmpInst::ICMP_EQ)
      return false;
    auto *Definition = dyn_cast<Instruction>(Inner);
    return llvm::any_of(stores, [&](StoreInst *Store) {
      return writesElement(*Store, Index, 0) && stored(*Store, Mark) &&
             (!Definition || tree.dominates(Definition, Store)) &&
             attempt([&] { return holds(Inner, Store, Depth + 1); });
    });
  }

  // A store of element Inner + Offset, executed after Inner's definition
  // and at a point where [0, Inner) holds, dominates At.
  bool pushed(Value *Inner, uint64_t Offset, Instruction *At, unsigned Depth) {
    auto *Definition = dyn_cast<Instruction>(Inner);
    for (StoreInst *Store : stores) {
      if (!writesElement(*Store, Inner, Offset) || !stored(*Store, At) ||
          (Definition && !tree.dominates(Definition, Store)))
        continue;
      if (attempt([&] { return holds(Inner, Store, Depth + 1); })) return true;
    }
    return coveredOnEveryPath(Inner, Offset, At) && attempt([&] { return holds(Inner, At, Depth + 1); });
  }

  // Every path from Inner's definition (or from the fill) to At passes a
  // store of element Inner + Offset: no single store dominates At when
  // the branches that each pushed were threaded apart.
  bool coveredOnEveryPath(Value *Inner, uint64_t Offset, Instruction *At) {
    auto *Definition = dyn_cast<Instruction>(Inner);
    auto Writes = [&](StoreInst *Store) {
      return whole(*Store) && writesElement(*Store, Inner, Offset) &&
             tree.dominates(&fill, Store) && (!Definition || tree.dominates(Definition, Store));
    };
    SmallPtrSet<BasicBlock *, 8> Writing;
    for (StoreInst *Store : stores)
      if (Writes(Store)) Writing.insert(Store->getParent());
    if (Writing.empty()) return false;
    BasicBlock *Start = At->getParent();
    for (Instruction &I : *Start) {
      if (&I == At) break;
      if (auto *Store = dyn_cast<StoreInst>(&I); Store && llvm::is_contained(stores, Store) && Writes(Store))
        return true;
    }
    BasicBlock *Origin = Definition ? Definition->getParent() : fill.getParent();
    if (Start == Origin || Start == fill.getParent()) return false;
    SmallPtrSet<BasicBlock *, 32> Seen;
    SmallVector<BasicBlock *, 16> Work(pred_begin(Start), pred_end(Start));
    while (!Work.empty()) {
      BasicBlock *Block = Work.pop_back_val();
      if (!Seen.insert(Block).second || Writing.contains(Block)) continue;
      if (Block == Origin || Block == fill.getParent() || Block->isEntryBlock() || Seen.size() > 2048)
        return false;
      Work.append(pred_begin(Block), pred_end(Block));
    }
    return true;
  }

  // V has the value Expected: directly, or as a join of such values (two
  // paths that each computed `Inner + 1`).
  bool equals(Value *V, const SCEV *Expected, unsigned Depth) {
    if (evolution.getSCEV(V) == Expected) return true;
    auto *Phi = dyn_cast<PHINode>(V);
    if (!Phi || Depth >= 4) return false;
    for (Value *Incoming : Phi->incoming_values())
      if (Incoming == Phi || !equals(Incoming, Expected, Depth + 1)) return false;
    return true;
  }

  // Mark = Inner + K where a single-block loop, exited before At, stored
  // elements [Inner, Mark) after [0, Inner) held: each iteration's stores
  // cover one block of Step bytes, consecutive iterations' blocks follow
  // each other from element Inner, and the loop leaves when a counter
  // stepping by C from zero without wrapping reaches N (a vectorized push
  // loop), so it ran N / C iterations and Mark = Inner + Step * N / (C * size).
  // Inner and N are defined before the loop and the exit edge dominates
  // Mark, so the run that stored them is the latest one before Mark.
  bool loopPushed(Value *Inner, Instruction *Mark, Instruction *At, unsigned Depth) {
    using namespace PatternMatch;
    auto *Definition = dyn_cast<Instruction>(Inner);
    // A struct's grouped stores do not show the bytes each iteration covers.
    if (composite || !evolution.isSCEVable(Inner->getType()) || !evolution.isSCEVable(Mark->getType()))
      return false;
    for (ArrayRef<StoreInst *> Group : {stores, vectorStores})
      for (StoreInst *First : Group) {
        auto *Block = dyn_cast_or_null<SCEVAddRecExpr>(offset(*First));
        const Loop *L = Block ? Block->getLoop() : nullptr;
        BasicBlock *Body = L && L->getNumBlocks() == 1 ? L->getHeader() : nullptr;
        BasicBlock *Preheader = Body ? L->getLoopPreheader() : nullptr;
        auto *Latch = Body ? dyn_cast<CondBrInst>(Body->getTerminator()) : nullptr;
        if (!Latch || !Preheader || !Block->isAffine() || !tree.dominates(&fill, First) ||
            L->contains(fill.getParent()) ||
            (Definition && !tree.dominates(Definition, Preheader->getTerminator())))
          continue;
        auto *Step = dyn_cast<SCEVConstant>(Block->getStepRecurrence(evolution));
        const SCEV *Start =
            ElisaWatermarkLoops::coveredStart(evolution, buffer, {stores, vectorStores}, *Body, *L, Step);
        uint64_t Stride = 0;
        const SCEV *Limit = Start ? ElisaWatermarkLoops::counted(evolution, *Latch, *Body, Stride) : nullptr;
        uint64_t PerStep = Stride * elementSize;
        if (!Limit || Limit->getType() != Inner->getType() || Mark->getType() != Inner->getType() ||
            Step->getAPInt().getZExtValue() % PerStep != 0)
          continue;
        Type *Bytes = Start->getType();
        const SCEV *Elements = evolution.getMulExpr(
            Limit, evolution.getConstant(Limit->getType(), Step->getAPInt().getZExtValue() / PerStep));
        BasicBlock *Exit = Latch->getSuccessor(Latch->getSuccessor(0) == Body ? 1 : 0);
        // Compared in elements: SCEV keeps `size * (a + b + c)` undistributed.
        if (Start != evolution.getMulExpr(evolution.getTruncateOrZeroExtend(evolution.getSCEV(Inner), Bytes),
                                          evolution.getConstant(Bytes, elementSize)) ||
            evolution.getAddExpr(evolution.getSCEV(Inner), Elements) != evolution.getSCEV(Mark) ||
            !tree.dominates(BasicBlockEdge(Body, Exit), Mark->getParent()) ||
            !tree.dominates(BasicBlockEdge(Body, Exit), At->getParent()))
          continue;
        if (attempt([&] { return holds(Inner, Preheader->getTerminator(), Depth + 1); })) return true;
      }
    return false;
  }

  // The byte offset of the element a load or store accesses, at a field
  // of it for a struct.
  const SCEV *offset(Instruction &Access) {
    const SCEV *Bytes = offset(getLoadStorePointerOperand(&Access));
    uint64_t Residue = residues.lookup(&Access);
    return Bytes && Residue ? evolution.getMinusSCEV(Bytes, evolution.getConstant(Bytes->getType(), Residue))
                            : Bytes;
  }
  // The byte offset of an address in the buffer.
  const SCEV *offset(Value *Pointer) {
    const SCEV *Bytes = evolution.getMinusSCEV(evolution.getSCEV(Pointer),
                                               evolution.getSCEV(&buffer));
    return isa<SCEVCouldNotCompute>(Bytes) ? nullptr : Bytes;
  }

  // Store writes element Inner + Offset: by index, or by address offset.
  bool writesElement(StoreInst &Store, Value *Inner, uint64_t Offset) {
    Value *Index = element(Store);
    if (Offset == 0 && Index == Inner) return true;
    if (!evolution.isSCEVable(Inner->getType())) return false;
    if (Index && Index->getType() == Inner->getType() &&
        equals(Index, evolution.getAddExpr(evolution.getSCEV(Inner),
                                           evolution.getConstant(Inner->getType(), Offset)), 0))
      return true;
    const SCEV *Bytes = offset(Store);
    if (!Bytes) return false;
    const SCEV *Scaled = evolution.getTruncateOrZeroExtend(evolution.getSCEV(Inner), Bytes->getType());
    const SCEV *Expected = evolution.getMulExpr(
        evolution.getAddExpr(Scaled, evolution.getConstant(Bytes->getType(), Offset)),
        evolution.getConstant(Bytes->getType(), elementSize));
    return Bytes == Expected;
  }

  // Stores to each element below a small constant dominate At.
  bool storedBelow(const APInt &Count, Instruction *At) {
    if (Count.ugt(8)) return false;
    for (uint64_t Element = 0; Element < Count.getZExtValue(); ++Element) {
      bool Stored = false;
      for (StoreInst *Store : stores) {
        auto *Index = dyn_cast_or_null<ConstantInt>(element(*Store));
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
    return whole(Store) && tree.dominates(&fill, &Store) && tree.dominates(&Store, At);
  }

  // V != 0 when the edge From -> To is taken: the edge's own comparison,
  // or for a count clamped by `umin`, both operands.
  bool nonzeroAcross(Value *V, BasicBlock *From, BasicBlock *To, unsigned Depth) {
    using namespace PatternMatch;
    Value *Left = nullptr, *Right = nullptr;
    if (ElisaEdgeExclusion::excludesOnEdge(V, APInt::getZero(V->getType()->getIntegerBitWidth()), From, To))
      return true;
    if (match(V, m_UMin(m_Value(Left), m_Value(Right))))
      return nonzeroAcross(Left, From, To, Depth) && nonzeroAcross(Right, From, To, Depth);
    return nonzero(V, From->getTerminator(), Depth + 1);
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
      for (unsigned Edge = 0; All && Edge < Phi->getNumIncomingValues(); ++Edge)
        All = nonzeroAcross(Phi->getIncomingValue(Edge), Phi->getIncomingBlock(Edge), Phi->getParent(), Depth);
      if (All) return true;
    }
    if (!evolution.isSCEVable(V->getType())) return false;
    const SCEV *Value = evolution.getSCEV(V);
    return proof.proveBelow(evolution.getZero(Value->getType()), Value, At);
  }
};
} // namespace ElisaStackWatermark
