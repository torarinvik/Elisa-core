// Elide a private array's initializer when a complete counted write loop
// covers every subsequent scalar read, including reads of shorter prefixes.
#include "llvm_counted_prefix_view.h"
namespace ElisaCountedPrefix {
using ElisaCoveredArray::Induction;
using ElisaCoveredArray::induction;
using ElisaCoveredArray::normalExit;

static void collectBoundFacts(Value *Condition, bool Taken, Value *Bound,
                              Value *Limit, bool &Nonnegative,
                              bool &SignedOrder, bool &UnsignedOrder,
                              unsigned Depth = 0) {
  if (Depth == 12) return;
  auto Visit = [&](Value *Operand, bool Truth) {
    collectBoundFacts(Operand, Truth, Bound, Limit, Nonnegative,
                      SignedOrder, UnsignedOrder, Depth + 1);
  };
  if (auto *Select = dyn_cast<SelectInst>(Condition)) {
    auto *True = dyn_cast<ConstantInt>(Select->getTrueValue());
    auto *False = dyn_cast<ConstantInt>(Select->getFalseValue());
    if (True && True->isOne() != Taken) {
      Visit(Select->getCondition(), false);
      Visit(Select->getFalseValue(), Taken);
    } else if (False && False->isOne() != Taken) {
      Visit(Select->getCondition(), true);
      Visit(Select->getTrueValue(), Taken);
    }
    return;
  }
  if (auto *Binary = dyn_cast<BinaryOperator>(Condition)) {
    if ((Binary->getOpcode() == Instruction::Or && !Taken) ||
        (Binary->getOpcode() == Instruction::And && Taken))
      for (Value *Operand : Binary->operands()) Visit(Operand, Taken);
    return;
  }

  auto *Compare = dyn_cast<ICmpInst>(Condition);
  if (!Compare) return;
  auto Predicate = Taken ? Compare->getPredicate() : Compare->getInversePredicate();
  Value *Other = Compare->getOperand(1);
  if (Other == Bound) {
    Predicate = ICmpInst::getSwappedPredicate(Predicate);
    Other = Compare->getOperand(0);
  } else if (Compare->getOperand(0) != Bound) {
    return;
  }
  auto *Zero = dyn_cast<ConstantInt>(Other);
  Nonnegative |= Zero && Zero->isZero() && Predicate == ICmpInst::ICMP_SGE;
  SignedOrder |= Other == Limit && (Predicate == ICmpInst::ICMP_SLE ||
                                  Predicate == ICmpInst::ICMP_SLT);
  UnsignedOrder |= Other == Limit && (Predicate == ICmpInst::ICMP_ULE ||
                                    Predicate == ICmpInst::ICMP_ULT ||
                                    Predicate == ICmpInst::ICMP_EQ);
}

static bool canRecompute(Value *ValueToCheck, BasicBlock *After,
                         DominatorTree &Tree, LoopInfo &Loops) {
  auto *Definition = dyn_cast<Instruction>(ValueToCheck);
  return Definition && isPotentiallyReachable(After, Definition->getParent(),
                                              nullptr, &Tree, &Loops);
}

static bool boundIsCovered(Value *Bound, Value *Limit, LoadInst &Read,
                           DominatorTree &Tree, LoopInfo &Loops) {
  if (Bound == Limit) return true;
  bool Nonnegative = false, SignedOrder = false, UnsignedOrder = false;
  for (auto &Block : *Read.getFunction()) {
    auto *Branch = dyn_cast<CondBrInst>(Block.getTerminator());
    if (!Branch || Branch->getSuccessor(0) == Branch->getSuccessor(1)) continue;
    for (unsigned Edge = 0; Edge < 2; ++Edge)
      if (Tree.dominates(BasicBlockEdge(&Block, Branch->getSuccessor(Edge)),
                         Read.getParent()) &&
          !canRecompute(Bound, Branch->getSuccessor(Edge), Tree, Loops) &&
          !canRecompute(Limit, Branch->getSuccessor(Edge), Tree, Loops))
        collectBoundFacts(Branch->getCondition(), Edge == 0, Bound, Limit,
                          Nonnegative, SignedOrder, UnsignedOrder);
    if (UnsignedOrder || (Nonnegative && SignedOrder)) return true;
  }
  return false;
}

static Value *elementIndex(GetElementPtrInst &Address, const DataLayout &Layout,
                           Type *Element) {
  if (Address.getNumIndices() == 1 &&
      Layout.getTypeAllocSize(Address.getSourceElementType()) ==
          Layout.getTypeAllocSize(Element))
    return Address.idx_begin()->get();
  if (Address.getNumIndices() == 2) {
    auto *Array = dyn_cast<ArrayType>(Address.getSourceElementType());
    auto *Zero = dyn_cast<ConstantInt>(Address.idx_begin()->get());
    if (Array && Array->getElementType() == Element && Zero && Zero->isZero())
      return (Address.idx_begin() + 1)->get();
  }
  return nullptr;
}

// Match a read immediately guarded by index < prefix length. Following a
// chain of single predecessors ensures each read takes this guard again.
static bool guardedPrefixIndex(Value *Index, Value *Length, LoadInst &Read) {
  BasicBlock *Block = Read.getParent();
  for (unsigned Depth = 0; Depth < 8; ++Depth) {
    BasicBlock *Predecessor = Block->getSinglePredecessor();
    if (!Predecessor || Predecessor == Block) return false;
    auto *Branch = dyn_cast<CondBrInst>(Predecessor->getTerminator());
    auto *Compare = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
    if (Compare && Branch->getSuccessor(0) != Branch->getSuccessor(1) &&
        Compare->getOperand(0) == Index && Compare->getOperand(1) == Length) {
      auto Predicate = Branch->getSuccessor(0) == Block
          ? Compare->getPredicate() : Compare->getInversePredicate();
      if (Predicate == ICmpInst::ICMP_ULT) return true;
    }
    Block = Predecessor;
  }
  return false;
}

static bool stabilizeIfNeeded(Value *ValueToCheck,
                              SmallVectorImpl<Instruction *> &Stabilize) {
  if (isa<FreezeInst, ConstantInt>(ValueToCheck)) return true;
  auto *Definition = dyn_cast<Instruction>(ValueToCheck);
  if (!Definition || (!isa<PHINode>(Definition) && !Definition->getNextNode()) ||
      Definition->getParent()->getFirstInsertionPt() == Definition->getParent()->end())
    return false;
  if (!llvm::is_contained(Stabilize, Definition)) Stabilize.push_back(Definition);
  return true;
}

// Every path from the fill body to the latch stores the induction index.
static bool writersCoverIteration(ArrayRef<StoreInst *> Writers, Induction &Fill,
                                  DominatorTree &Tree) {
  BasicBlock *Latch = Fill.loop->getLoopLatch();
  SmallPtrSet<BasicBlock *, 4> Blocks;
  for (StoreInst *Writer : Writers) {
    if (Tree.dominates(Writer->getParent(), Latch)) return true;
    Blocks.insert(Writer->getParent());
  }
  return !Blocks.count(Fill.body) && !Blocks.count(Latch) &&
         !isPotentiallyReachable(Fill.body, Latch, &Blocks, nullptr, nullptr);
}

static bool coversReads(AllocaInst &Buffer, MemSetInst &Init, Type *Element,
                        LoopInfo &Loops, DominatorTree &Tree,
                        const DataLayout &Layout, SmallVectorImpl<Instruction *> &Stabilize,
                        FunctionAnalysisManager &Analyses, PassBuilder &Builder) {
  SmallVector<std::pair<LoadInst *, Value *>, 4> Reads;
  SmallVector<StoreInst *, 2> Writers;
  SmallVector<InsertValueInst *, 2> Views;
  Value *WriteIndex = nullptr;
  if (Loops.getLoopFor(Init.getParent()) || Buffer.isUsedByMetadata()) return false;
  for (User *Use : Buffer.users()) {
    if (Use == &Init) continue;
    auto *Address = dyn_cast<GetElementPtrInst>(Use);
    if (!Address || Address->getPointerOperand() != &Buffer ||
        Address->isUsedByMetadata()) return false;
    Value *Index = elementIndex(*Address, Layout, Element);
    if (!Index || !Index->getType()->isIntegerTy(Layout.getIndexSizeInBits(0)))
      return false;
    for (User *Access : Address->users()) {
      auto *Read = dyn_cast<LoadInst>(Access);
      if (Read && Read->isSimple() && Read->getPointerOperand() == Address &&
          Read->getType() == Element && Reads.size() < 16) {
        Reads.emplace_back(Read, Index);
        continue;
      }
      auto *Data = dyn_cast<InsertValueInst>(Access);
      if (Data && Element->isIntegerTy(32) && isZeroStart(Index) &&
          Data->getInsertedValueOperand() == Address && Views.size() < 2) {
        Views.push_back(Data);
        continue;
      }
      auto *Write = dyn_cast<StoreInst>(Access);
      if (!Write || !Write->isSimple() || Writers.size() == 2 ||
          (WriteIndex && WriteIndex != Index) ||
          Write->getPointerOperand() != Address ||
          Write->getValueOperand()->getType() != Element)
        return false;
      Writers.push_back(Write);
      WriteIndex = Index;
    }
  }

  Induction Fill;
  if (Reads.empty() || Writers.empty() || !induction(WriteIndex, Loops, Fill) ||
      Fill.loop->getParentLoop() || !Fill.loop->getSubLoops().empty() ||
      !writersCoverIteration(Writers, Fill, Tree))
    return false;
  for (StoreInst *Writer : Writers)
    if (!Tree.dominates(&Init, Writer) ||
        !Tree.dominates(BasicBlockEdge(Fill.loop->getHeader(), Fill.body),
                        Writer->getParent()))
      return false;
  BasicBlock *Exit = normalExit(Fill.loop);
  if (!Exit || canRecompute(Fill.bound, Exit, Tree, Loops) ||
      isPotentiallyReachable(Exit, Init.getParent(), nullptr, &Tree, &Loops))
    return false;

  BasicBlockEdge Completion(Fill.loop->getHeader(), Exit);
  for (InsertValueInst *Data : Views)
    if (!viewEscapeCovered(*Data, Fill.bound, Completion,
                           Layout.getTypeAllocSize(Element), Tree, Analyses, Builder))
      return false;
  SmallVector<LoadInst *, 2> Clamped;
  bool Covered = llvm::all_of(Reads, [&](auto Entry) {
    auto [Read, Index] = Entry;
    if (Fill.loop->contains(Read->getParent()) ||
        !Tree.dominates(Completion, Read->getParent()))
      return false;
    if (Element->isIntegerTy(32) && isa<ConstantInt>(Index)) {
      Clamped.push_back(Read);
      return stabilizeIfNeeded(Fill.bound, Stabilize);
    }
    if (Element->isIntegerTy(32))
      return guardedPrefixIndex(Index, Fill.bound, *Read) &&
             stabilizeIfNeeded(Index, Stabilize) &&
             stabilizeIfNeeded(Fill.bound, Stabilize);
    Induction Scan;
    return induction(Index, Loops, Scan, false) &&
           Tree.dominates(BasicBlockEdge(Scan.loop->getHeader(), Scan.body),
                          Read->getParent()) &&
           boundIsCovered(Scan.bound, Fill.bound, *Read, Tree, Loops);
  });
  if (!Covered) return false;
  // A fixed-position read beyond the count observed the zero initializer.
  for (LoadInst *Read : Clamped) {
    IRBuilder<> Insert(Read->getNextNode());
    auto *Position = cast<ConstantInt>(
        elementIndex(*cast<GetElementPtrInst>(Read->getPointerOperand()), Layout, Element));
    Value *Inside = Insert.CreateICmpULT(
        ConstantInt::get(Fill.bound->getType(), Position->getZExtValue()), Fill.bound,
        "prefix.fixed.inside");
    Value *Result = Insert.CreateSelect(Inside, Read, Constant::getNullValue(Element),
                                         "prefix.fixed.read");
    Read->replaceUsesWithIf(Result, [&](Use &Use) {
      return Use.getUser() != Inside && Use.getUser() != Result;
    });
  }
  return true;
}
} // namespace ElisaCountedPrefix
#include "llvm_linked_prefix.h"

static Type *countedPrefixElement(MemSetInst &Init, const DataLayout &Layout) {
  auto *Buffer = dyn_cast<AllocaInst>(Init.getRawDest());
  auto *Array = Buffer ? dyn_cast<ArrayType>(Buffer->getAllocatedType()) : nullptr;
  auto *Length = dyn_cast<ConstantInt>(Init.getLength());
  if (!Array || !Buffer->isStaticAlloca() || Buffer->isArrayAllocation() ||
      Buffer->getAddressSpace() != 0 || Init.isVolatile() ||
      Init.hasOperandBundles() || Init.hasMetadataOtherThanDebugLoc() ||
      Init.getRawDest() != Buffer || !Length ||
      (!Array->getElementType()->isIntegerTy(16) &&
       !Array->getElementType()->isIntegerTy(32)) ||
      Array->getNumElements() < 16 || Array->getNumElements() > 4096 ||
      !Length->equalsInt(Layout.getTypeAllocSize(Array).getFixedValue()) ||
      !isa<ConstantInt>(Init.getValue()) ||
      !cast<ConstantInt>(Init.getValue())->isZero())
    return nullptr;
  return Array->getElementType();
}

// Stage0 can emit a libc call. InstCombine must independently recognize
// its builtin semantics before the proof considers deleting the fill.
static Type *countedPrefixCandidate(Instruction &Instruction,
                                    const DataLayout &Layout) {
  if (auto *Init = dyn_cast<MemSetInst>(&Instruction)) {
    Type *Element = countedPrefixElement(*Init, Layout);
    return Element ? Element : ElisaLinkedPrefix::linkedElement(*Init, Layout);
  }
  auto *Call = dyn_cast<CallInst>(&Instruction);
  auto *Callee = Call ? Call->getCalledFunction() : nullptr;
  if (!Callee || !Callee->isDeclaration() || Callee->getName() != "memset" ||
      Call->arg_size() != 3 || Call->hasOperandBundles() || Call->isNoBuiltin())
    return nullptr;
  auto *Buffer = dyn_cast<AllocaInst>(Call->getArgOperand(0));
  auto *Array = Buffer ? dyn_cast<ArrayType>(Buffer->getAllocatedType()) : nullptr;
  auto *Length = dyn_cast<ConstantInt>(Call->getArgOperand(2));
  auto *Fill = dyn_cast<ConstantInt>(Call->getArgOperand(1));
  return Array && (Array->getElementType()->isIntegerTy(16) ||
                   Array->getElementType()->isIntegerTy(32) ||
                   Array->getElementType()->isIntegerTy(64)) &&
         Array->getNumElements() >= 16 && Array->getNumElements() <= 4096 &&
         Fill && Fill->isZero() && Length &&
         Length->equalsInt(Layout.getTypeAllocSize(Array).getFixedValue())
             ? Array->getElementType() : nullptr;
}

static unsigned removeCountedPrefixInitializers(Module &Module,
                                               FunctionAnalysisManager &Analyses,
                                               PassBuilder &Builder) {
  if (!Module.getTargetTriple().isAArch64()) return 0;
  const DataLayout &Layout = Module.getDataLayout();
  FunctionPassManager Canonicalize;
  if (Error Error = Builder.parsePassPipeline(
          Canonicalize, "simplifycfg,instcombine<no-verify-fixpoint>,loop-simplify")) {
    consumeError(std::move(Error));
    return 0;
  }

  unsigned Removed = 0;
  for (Function &Function : Module) {
    if (Function.isDeclaration() || Function.hasOptNone() ||
        Function.hasOptSize() || Function.hasMinSize() ||
        Function.hasFnAttribute(Attribute::SanitizeAddress) ||
        Function.hasFnAttribute(Attribute::SanitizeHWAddress) ||
        Function.hasFnAttribute(Attribute::SanitizeMemory) ||
        Function.hasFnAttribute(Attribute::SanitizeThread) ||
        Function.getInstructionCount() > 2000)
      continue;

    SmallVector<CallInst *, 4> LibraryFills;
    bool HasCandidate = false, NeedsCanonicalCFG = false;
    for (Instruction &Instruction : instructions(Function)) {
      Type *Element = countedPrefixCandidate(Instruction, Layout);
      if (!Element) continue;
      HasCandidate = true;
      if (Element->isIntegerTy(16)) {
        NeedsCanonicalCFG = true;
      } else if (!isa<MemSetInst>(Instruction)) {
        LibraryFills.push_back(cast<CallInst>(&Instruction));
      }
    }
    if (!HasCandidate) continue;
    if (NeedsCanonicalCFG) Canonicalize.run(Function, Analyses);
    auto &Tree = Analyses.getResult<DominatorTreeAnalysis>(Function);
    if (!NeedsCanonicalCFG && !LibraryFills.empty()) {
      auto &Library = Analyses.getResult<TargetLibraryAnalysis>(Function);
      auto &Assumptions = Analyses.getResult<AssumptionAnalysis>(Function);
      auto &Remarks = Analyses.getResult<OptimizationRemarkEmitterAnalysis>(Function);
      LibCallSimplifier Simplifier(Layout, &Library, &Tree, nullptr, &Assumptions,
                                   Remarks, nullptr, nullptr);
      for (CallInst *Call : LibraryFills)
        canonicalizeTrustedMemset(Call, cast<AllocaInst>(Call->getArgOperand(0)),
                                  Layout, Library, Simplifier);
    }
    auto &Loops = Analyses.getResult<LoopAnalysis>(Function);
    for (Instruction &Instruction : make_early_inc_range(instructions(Function))) {
      auto *Init = dyn_cast<MemSetInst>(&Instruction);
      Type *Element = Init ? countedPrefixElement(*Init, Layout) : nullptr;
      SmallVector<llvm::Instruction *, 4> Stabilize;
      if (!Element || !ElisaCountedPrefix::coversReads(
              *cast<AllocaInst>(Init->getRawDest()), *Init, Element, Loops, Tree,
              Layout, Stabilize, Analyses, Builder))
        continue;
      if (!Stabilize.empty()) {
        SmallVector<llvm::Instruction *, 4> InOrder;
        for (llvm::Instruction &Definition : instructions(Function))
          if (llvm::is_contained(Stabilize, &Definition)) InOrder.push_back(&Definition);
        Stabilize = std::move(InOrder);
      }
      for (llvm::Instruction *Definition : Stabilize) {
        auto Insertion = isa<PHINode>(Definition)
            ? Definition->getParent()->getFirstInsertionPt()
            : std::next(Definition->getIterator());
        IRBuilder<> Builder(&*Insertion);
        Value *Stable = Builder.CreateFreeze(Definition, "prefix.index");
        Definition->replaceUsesWithIf(Stable, [&](Use &Use) {
          return Use.getUser() != Stable;
        });
      }
      Init->eraseFromParent();
      ++Removed;
    }
    Removed += ElisaLinkedPrefix::removeLinkedPrefixInitializers(Function, Loops, Tree, Layout);
  }
  return Removed;
}
