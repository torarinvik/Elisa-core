// Elide the zero fill of local arrays that are read only inside a prefix a
// loop wrote completely. A fill loop stores element i in every iteration
// i = 0, 1, ...; once it leaves through its counted exit, elements
// [0, n) hold stored values. If every later read indexes below n, no read
// observes the fill. Indices are proved below n from scalar evolution: a
// direct comparison, the extreme of an affine recurrence, or a load from
// another such array whose every stored value is below n (an order or
// permutation array indexing a value array is the common case).
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
namespace ElisaIndexedPrefix {
using namespace llvm;
using ElisaArrayRanges::Accesses;

struct Array {
  AllocaInst *Buffer = nullptr;
  uint64_t ElementSize = 0;
  Accesses Uses;
  // Filled by the prefix proof: the fill loop's counted exit edge and the
  // number of elements written when it is taken.
  BasicBlock *From = nullptr, *To = nullptr;
  const SCEV *Length = nullptr;
};

class Prover {
public:
  Prover(ScalarEvolution &Evolution, DominatorTree &Tree, LoopInfo &Loops,
         SmallVectorImpl<Array> &Arrays)
      : evolution(Evolution), tree(Tree), loops(Loops), arrays(Arrays) {}

  // Whether every read of Target lies in a completely written prefix.
  bool prefixRead(Array &Target) {
    if (Target.Uses.Fills.size() != 1 || loops.getLoopFor(Target.Uses.Fills[0]->getParent()))
      return false;
    for (StoreInst *Store : Target.Uses.Stores)
      if (fillLoop(Target, *Store) && readsInside(Target)) return true;
    return false;
  }

private:
  ScalarEvolution &evolution;
  DominatorTree &tree;
  LoopInfo &loops;
  SmallVectorImpl<Array> &arrays;
  SmallPtrSet<AllocaInst *, 4> visiting;

  // Offset / Size where Offset is known to be a multiple of Size: through
  // affine recurrences, whose start and step must both divide.
  const SCEV *divide(const SCEV *Offset, uint64_t Size) {
    if (auto *Constant = dyn_cast<SCEVConstant>(Offset)) {
      const APInt &Value = Constant->getAPInt();
      APInt Divisor(Value.getBitWidth(), Size);
      return Value.srem(Divisor).isZero() ? evolution.getConstant(Value.sdiv(Divisor)) : nullptr;
    }
    if (auto *Recurrence = dyn_cast<SCEVAddRecExpr>(Offset)) {
      if (!Recurrence->isAffine()) return nullptr;
      const SCEV *Start = divide(Recurrence->getStart(), Size);
      const SCEV *Step = divide(Recurrence->getStepRecurrence(evolution), Size);
      return Start && Step ? evolution.getAddRecExpr(Start, Step, Recurrence->getLoop(),
                                                     SCEV::FlagAnyWrap)
                           : nullptr;
    }
    const SCEV *Quotient =
        evolution.getUDivExactExpr(Offset, evolution.getConstant(Offset->getType(), Size));
    return isa<SCEVUDivExpr>(Quotient) ? nullptr : Quotient;
  }

  const SCEV *indexOf(Value *Pointer, Array &Target) {
    const SCEV *Offset = evolution.getMinusSCEV(evolution.getSCEV(Pointer),
                                                evolution.getSCEV(Target.Buffer));
    if (isa<SCEVCouldNotCompute>(Offset)) return nullptr;
    return divide(Offset, Target.ElementSize);
  }

  // Store writes element i of iteration i of its loop, before the loop's
  // counted exit, and the fill runs before the loop.
  bool fillLoop(Array &Target, StoreInst &Store) {
    auto *Index = dyn_cast_or_null<SCEVAddRecExpr>(indexOf(Store.getPointerOperand(), Target));
    Loop *L = loops.getLoopFor(Store.getParent());
    if (!Index || !L || Index->getLoop() != L || !Index->isAffine() || !Index->getStart()->isZero() ||
        !Index->getStepRecurrence(evolution)->isOne() ||
        !tree.dominates(Target.Uses.Fills[0], L->getHeader()))
      return false;
    SmallVector<BasicBlock *, 4> Exiting;
    L->getExitingBlocks(Exiting);
    for (BasicBlock *Block : Exiting) {
      const SCEV *Count = evolution.getExitCount(L, Block);
      auto *Branch = dyn_cast<CondBrInst>(Block->getTerminator());
      if (isa<SCEVCouldNotCompute>(Count) || !Branch || !tree.dominates(Store.getParent(), Block) ||
          (Store.getParent() == Block && !Store.comesBefore(Branch)))
        continue;
      BasicBlock *Exit = Branch->getSuccessor(L->contains(Branch->getSuccessor(0)) ? 1 : 0);
      Target.From = Block;
      Target.To = Exit;
      Target.Length = evolution.getAddExpr(Count, evolution.getOne(Count->getType()));
      return true;
    }
    return false;
  }

  bool readsInside(Array &Target) {
    for (LoadInst *Load : Target.Uses.Loads) {
      const SCEV *Index = indexOf(Load->getPointerOperand(), Target);
      if (!completed(Target, *Load) || !Index || !below(Index, Target.Length, Load)) return false;
    }
    for (MemTransferInst *Copy : Target.Uses.Copies) {
      const SCEV *Bytes = evolution.getSCEV(Copy->getLength());
      const SCEV *Limit = evolution.getMulExpr(
          Target.Length, evolution.getConstant(Target.Length->getType(), Target.ElementSize));
      if (!completed(Target, *Copy) || Copy->getRawSource() != Target.Buffer ||
          Bytes->getType() != Limit->getType() ||
          !evolution.isKnownPredicateAt(ICmpInst::ICMP_ULE, Bytes, Limit, Copy))
        return false;
    }
    return true;
  }

  bool completed(Array &Target, Instruction &Read) {
    return tree.dominates(BasicBlockEdge(Target.From, Target.To), Read.getParent());
  }

  // Index <u Length wherever At executes.
  bool below(const SCEV *Index, const SCEV *Length, Instruction *At, unsigned Depth = 0) {
    if (Depth > 6 || Index->getType() != Length->getType()) return false;
    if (evolution.isKnownPredicateAt(ICmpInst::ICMP_ULT, Index, Length, At) ||
        evolution.getUnsignedRange(Index).getUnsignedMax().ult(
            evolution.getUnsignedRange(Length).getUnsignedMin()))
      return true;
    // An unsigned minimum is at most each of its operands.
    if (auto *Minimum = dyn_cast<SCEVUMinExpr>(Index))
      return llvm::any_of(Minimum->operands(), [&](const SCEV *Operand) {
        return below(Operand, Length, At, Depth + 1);
      });
    // Index = Length - c is below Length exactly when Length >= c.
    if (auto *Gap = dyn_cast<SCEVConstant>(evolution.getMinusSCEV(Length, Index));
        Gap && Gap->getAPInt().isStrictlyPositive() && lowest(Length, At).uge(Gap->getAPInt()))
      return true;
    if (auto *Recurrence = dyn_cast<SCEVAddRecExpr>(Index)) {
      const SCEV *Count = evolution.getSymbolicMaxBackedgeTakenCount(Recurrence->getLoop());
      const SCEV *Start = Recurrence->getStart();
      const SCEV *Step = Recurrence->isAffine() ? Recurrence->getStepRecurrence(evolution) : nullptr;
      if (isa<SCEVCouldNotCompute>(Count) || !Step || Count->getType() != Start->getType())
        return false;
      Instruction *Entry = &*Recurrence->getLoop()->getHeader()->getFirstInsertionPt();
      if (Step->isOne()) {
        // Values Start + k for k <= Count, none wrapping past Start + Count.
        const SCEV *Last = evolution.getAddExpr(Start, Count);
        return evolution.isKnownPredicateAt(ICmpInst::ICMP_ULE, Start, Last, Entry) &&
               below(Last, Length, Entry, Depth + 1);
      }
      if (Step->isAllOnesValue())
        // Values Start - k for k <= Count <= Start never wrap below zero.
        return evolution.isKnownPredicateAt(ICmpInst::ICMP_ULE, Count, Start, Entry) &&
               below(Start, Length, Entry, Depth + 1);
      return false;
    }
    if (auto *Unknown = dyn_cast<SCEVUnknown>(Index))
      if (auto *Load = dyn_cast<LoadInst>(Unknown->getValue()))
        return valuesBelow(*Load, Length, Depth + 1);
    return false;
  }

  // The smallest value Length can have where At executes, from its range
  // and from comparisons on the branch edges that dominate At.
  APInt lowest(const SCEV *Length, Instruction *At) {
    ConstantRange Range = evolution.getUnsignedRange(Length);
    if (auto *Unknown = dyn_cast<SCEVUnknown>(Length))
      Range = Range.intersectWith(
          ElisaDeadTrapExits::guardRange(Unknown->getValue(), At->getParent(), tree),
          ConstantRange::Unsigned);
    return Range.getUnsignedMin();
  }

  // Load reads an array whose reads stay in its written prefix and whose
  // every stored value is below Length or copied from the array itself.
  bool valuesBelow(LoadInst &Load, const SCEV *Length, unsigned Depth) {
    Array *Source = nullptr;
    for (Array &Candidate : arrays)
      if (llvm::is_contained(Candidate.Uses.Loads, &Load)) Source = &Candidate;
    if (!Source || visiting.count(Source->Buffer)) return false;
    visiting.insert(Source->Buffer);
    bool Bounded = prefixRead(*Source) && llvm::all_of(Source->Uses.Stores, [&](StoreInst *Store) {
      Value *Stored = Store->getValueOperand();
      return llvm::is_contained(Source->Uses.Loads, Stored) ||
             (evolution.isSCEVable(Stored->getType()) &&
              below(evolution.getSCEV(Stored), Length, Store, Depth));
    });
    visiting.erase(Source->Buffer);
    return Bounded;
  }
};

static unsigned removeIndexedPrefixFills(Function &F, FunctionAnalysisManager &Analyses) {
  const DataLayout &Layout = F.getDataLayout();
  SmallVector<Array, 4> Arrays;
  for (Instruction &Instruction : F.getEntryBlock()) {
    auto *Buffer = dyn_cast<AllocaInst>(&Instruction);
    auto *Type = Buffer ? dyn_cast<ArrayType>(Buffer->getAllocatedType()) : nullptr;
    if (!Type || !Type->getElementType()->isIntegerTy() || !Buffer->isStaticAlloca()) continue;
    Array Candidate;
    Candidate.Buffer = Buffer;
    Candidate.ElementSize = Layout.getTypeAllocSize(Type->getElementType()).getFixedValue();
    if (ElisaArrayRanges::collect(Buffer, Type->getElementType(), Candidate.ElementSize, Layout,
                                  Candidate.Uses) &&
        Candidate.Uses.Fills.size() == 1 && !Candidate.Uses.Stores.empty())
      Arrays.push_back(std::move(Candidate));
  }
  if (Arrays.empty()) return 0;
  auto &Evolution = Analyses.getResult<ScalarEvolutionAnalysis>(F);
  auto &Tree = Analyses.getResult<DominatorTreeAnalysis>(F);
  auto &Loops = Analyses.getResult<LoopAnalysis>(F);
  SmallVector<Instruction *, 4> Dead;
  for (Array &Target : Arrays) {
    Prover Proof(Evolution, Tree, Loops, Arrays);
    if (Proof.prefixRead(Target)) Dead.push_back(Target.Uses.Fills[0]);
  }
  for (Instruction *Fill : Dead) Fill->eraseFromParent();
  if (!Dead.empty()) Analyses.invalidate(F, PreservedAnalyses::none());
  return Dead.size();
}

static unsigned removeIndexedPrefixFills(Module &Module) {
  PassBuilder Builder;
  FunctionAnalysisManager Analyses;
  Builder.registerFunctionAnalyses(Analyses);
  unsigned Removed = 0;
  for (Function &F : Module)
    if (!F.isDeclaration() && !F.hasOptNone()) Removed += removeIndexedPrefixFills(F, Analyses);
  return Removed;
}
} // namespace ElisaIndexedPrefix
