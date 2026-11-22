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
// stay below the pushes (ElisaStackWatermark). The bound
// proofs themselves are ElisaIndexBounds'.
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
#include "llvm_stack_watermark.h"
namespace ElisaIndexedPrefix {
using namespace llvm;
using ElisaArrayRanges::Accesses;

struct Array {
  AllocaInst *Buffer = nullptr;
  uint64_t ElementSize = 0;
  Accesses Uses;
  // Filled by the prefix proof: the fill loop's counted exit edge, or the
  // copy that writes the prefix, and the number of elements written.
  BasicBlock *From = nullptr, *To = nullptr;
  MemIntrinsic *Copied = nullptr;
  const SCEV *Length = nullptr;
  // Leading elements the fill must still zero (a stack's folded seeds).
  uint64_t KeptElements = 0;
};

class Prover : public ElisaIndexBounds::Prover {
public:
  Prover(ScalarEvolution &Evolution, DominatorTree &Tree, LoopInfo &Loops,
         SmallVectorImpl<Array> &Arrays)
      : ElisaIndexBounds::Prover(Evolution, Tree, Loops), arrays(Arrays) {}

  // Whether every read of Target lies in a completely written prefix.
  bool prefixRead(Array &Target) {
    Target.KeptElements = 0;
    if (Target.Uses.Fills.size() != 1 || loops.getLoopFor(Target.Uses.Fills[0]->getParent()))
      return false;
    for (StoreInst *Store : Target.Uses.Stores)
      if (fillLoop(Target, *Store) && readsInside(Target)) return true;
    for (MemIntrinsic *Copy : Target.Uses.Writes)
      if (fillCopy(Target, *Copy) && readsInside(Target)) return true;
    return stackRead(Target);
  }

private:
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
    if (auto *Sum = dyn_cast<SCEVAddExpr>(Offset)) {
      SmallVector<SCEVUse, 4> Terms;
      for (const SCEV *Term : Sum->operands())
        if (const SCEV *Part = divide(Term, Size)) Terms.push_back(Part);
        else return nullptr;
      return evolution.getAddExpr(Terms);
    }
    if (auto *Product = dyn_cast<SCEVMulExpr>(Offset))
      if (const SCEV *Factor = divide(Product->getOperand(0), Size)) {
        SmallVector<SCEVUse, 4> Factors(Product->operands());
        Factors[0] = Factor;
        return evolution.getMulExpr(Factors);
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
      Target.Copied = nullptr;
      Target.Length = evolution.getAddExpr(Count, evolution.getOne(Count->getType()));
      return true;
    }
    return false;
  }

  // Copy (or a fill of another byte), outside any loop and after the
  // zero fill, writes the elements [0, bytes / element size).
  bool fillCopy(Array &Target, MemIntrinsic &Copy) {
    if (loops.getLoopFor(Copy.getParent()) || !tree.dominates(Target.Uses.Fills[0], &Copy)) return false;
    const SCEV *Length = divide(evolution.getSCEV(Copy.getLength()), Target.ElementSize);
    if (!Length) return false;
    Target.Copied = &Copy;
    Target.Length = Length;
    return true;
  }

  bool readsInside(Array &Target) {
    for (LoadInst *Load : Target.Uses.Loads) {
      const SCEV *Index = indexOf(Load->getPointerOperand(), Target);
      if (!completed(Target, *Load) || !Index || !proveBelow(Index, Target.Length, Load))
        return false;
    }
    // Every lane of a vector load must lie in the prefix.
    for (LoadInst *Load : Target.Uses.VectorLoads) {
      const SCEV *Index = indexOf(Load->getPointerOperand(), Target);
      unsigned Lanes = cast<FixedVectorType>(Load->getType())->getNumElements();
      if (!completed(Target, *Load) || !Index ||
          !proveBelow(evolution.getAddExpr(Index, evolution.getConstant(Index->getType(), Lanes - 1)),
                      Target.Length, Load))
        return false;
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

  // An array used as a stack: every read is below a watermark of pushes.
  bool stackRead(Array &Target) {
    if (!Target.Uses.Writes.empty() || !Target.Uses.Copies.empty() || !Target.Uses.VectorLoads.empty() ||
        Target.Uses.Loads.empty())
      return false;
    ElisaStackWatermark::Watermark Marks(tree, *this, evolution, *Target.Buffer, Target.ElementSize,
                                         *Target.Uses.Fills[0], Target.Uses.Stores);
    if (!Marks.readsBelow(Target.Uses.Loads)) return false;
    Target.KeptElements = Marks.kept();
    return true;
  }

  bool completed(Array &Target, Instruction &Read) {
    if (Target.Copied) return tree.dominates(Target.Copied, &Read);
    return tree.dominates(BasicBlockEdge(Target.From, Target.To), Read.getParent());
  }

  // Load reads an array whose reads stay in its written prefix and whose
  // every stored value is below Length or copied from the array itself.
  bool loadBelow(LoadInst &Load, const SCEV *Length, unsigned Depth) override {
    Array *Source = nullptr;
    for (Array &Candidate : arrays)
      if (llvm::is_contained(Candidate.Uses.Loads, &Load)) Source = &Candidate;
    if (!Source || visiting.count(Source->Buffer)) return false;
    visiting.insert(Source->Buffer);
    auto StoredBelow = [&](StoreInst *Store) {
      Value *Stored = Store->getValueOperand();
      return llvm::is_contained(Source->Uses.Loads, Stored) ||
             (evolution.isSCEVable(Stored->getType()) &&
              below(evolution.getSCEV(Stored), Length, Store, Depth));
    };
    // Copied values are unknown, and so are zeros a kept fill provides.
    bool Bounded = Source->Uses.Writes.empty() && prefixRead(*Source) && !Source->KeptElements &&
                   llvm::all_of(Source->Uses.Stores, StoredBelow);
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
        Candidate.Uses.Fills.size() == 1 && (!Candidate.Uses.Stores.empty() || !Candidate.Uses.Writes.empty()))
      Arrays.push_back(std::move(Candidate));
  }
  if (Arrays.empty()) return 0;
  auto &Evolution = Analyses.getResult<ScalarEvolutionAnalysis>(F);
  auto &Tree = Analyses.getResult<DominatorTreeAnalysis>(F);
  auto &Loops = Analyses.getResult<LoopAnalysis>(F);
  SmallVector<std::pair<Instruction *, uint64_t>, 4> Dead;
  for (Array &Target : Arrays) {
    Prover Proof(Evolution, Tree, Loops, Arrays);
    if (Proof.prefixRead(Target))
      Dead.push_back({Target.Uses.Fills[0], Target.KeptElements * Target.ElementSize});
  }
  for (auto [Fill, Kept] : Dead)
    if (auto *Zeros = dyn_cast<MemSetInst>(Fill); Zeros && Kept)
      Zeros->setLength(ConstantInt::get(Zeros->getLength()->getType(), Kept));
    else
      Fill->eraseFromParent();
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
