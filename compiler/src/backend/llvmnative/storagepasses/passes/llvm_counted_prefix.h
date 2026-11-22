// Elide a private array's initializer when a complete counted write loop
// covers every subsequent scalar read, including reads of shorter prefixes.
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

static bool coversReads(AllocaInst &Buffer, MemSetInst &Init, Type *Element,
                        LoopInfo &Loops, DominatorTree &Tree,
                        const DataLayout &Layout) {
  SmallVector<std::pair<LoadInst *, Value *>, 4> Reads;
  StoreInst *Writer = nullptr;
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
      auto *Write = dyn_cast<StoreInst>(Access);
      if (Writer || !Write || !Write->isSimple() ||
          Write->getPointerOperand() != Address ||
          Write->getValueOperand()->getType() != Element)
        return false;
      Writer = Write;
      WriteIndex = Index;
    }
  }

  Induction Fill;
  if (Reads.empty() || !Writer || !induction(WriteIndex, Loops, Fill) ||
      Fill.loop->getParentLoop() || !Fill.loop->getSubLoops().empty() ||
      !Tree.dominates(&Init, Writer) ||
      !Tree.dominates(Writer->getParent(), Fill.loop->getLoopLatch()) ||
      !Tree.dominates(BasicBlockEdge(Fill.loop->getHeader(), Fill.body),
                      Writer->getParent()))
    return false;
  BasicBlock *Exit = normalExit(Fill.loop);
  if (!Exit || canRecompute(Fill.bound, Exit, Tree, Loops) ||
      isPotentiallyReachable(Exit, Init.getParent(), nullptr, &Tree, &Loops))
    return false;

  BasicBlockEdge Completion(Fill.loop->getHeader(), Exit);
  return llvm::all_of(Reads, [&](auto Entry) {
    auto [Read, Index] = Entry;
    Induction Scan;
    return !Fill.loop->contains(Read->getParent()) &&
           Tree.dominates(Completion, Read->getParent()) &&
           induction(Index, Loops, Scan, false) &&
           Tree.dominates(BasicBlockEdge(Scan.loop->getHeader(), Scan.body),
                          Read->getParent()) &&
           boundIsCovered(Scan.bound, Fill.bound, *Read, Tree, Loops);
  });
}
} // namespace ElisaCountedPrefix

static Type *countedPrefixElement(MemSetInst &Init, const DataLayout &Layout) {
  auto *Buffer = dyn_cast<AllocaInst>(Init.getRawDest());
  auto *Array = Buffer ? dyn_cast<ArrayType>(Buffer->getAllocatedType()) : nullptr;
  auto *Length = dyn_cast<ConstantInt>(Init.getLength());
  if (!Array || !Buffer->isStaticAlloca() || Buffer->isArrayAllocation() ||
      Buffer->getAddressSpace() != 0 || Init.isVolatile() ||
      Init.hasOperandBundles() || Init.hasMetadataOtherThanDebugLoc() ||
      Init.getRawDest() != Buffer || !Length ||
      !Array->getElementType()->isIntegerTy(16) ||
      Array->getNumElements() < 16 || Array->getNumElements() > 4096 ||
      !Length->equalsInt(Layout.getTypeAllocSize(Array).getFixedValue()) ||
      !isa<ConstantInt>(Init.getValue()) ||
      !cast<ConstantInt>(Init.getValue())->isZero())
    return nullptr;
  return Array->getElementType();
}

// Stage0 can emit a libc call. InstCombine must independently recognize
// its builtin semantics before the proof considers deleting the fill.
static bool isCountedPrefixCandidate(Instruction &Instruction,
                                     const DataLayout &Layout) {
  if (auto *Init = dyn_cast<MemSetInst>(&Instruction))
    return countedPrefixElement(*Init, Layout) != nullptr;
  auto *Call = dyn_cast<CallInst>(&Instruction);
  auto *Callee = Call ? Call->getCalledFunction() : nullptr;
  if (!Callee || !Callee->isDeclaration() || Callee->getName() != "memset" ||
      Call->arg_size() != 3 || Call->hasOperandBundles() || Call->isNoBuiltin())
    return false;
  auto *Buffer = dyn_cast<AllocaInst>(Call->getArgOperand(0));
  auto *Array = Buffer ? dyn_cast<ArrayType>(Buffer->getAllocatedType()) : nullptr;
  auto *Length = dyn_cast<ConstantInt>(Call->getArgOperand(2));
  auto *Fill = dyn_cast<ConstantInt>(Call->getArgOperand(1));
  return Array && Array->getElementType()->isIntegerTy(16) &&
         Array->getNumElements() >= 16 && Array->getNumElements() <= 4096 &&
         Fill && Fill->isZero() && Length &&
         Length->equalsInt(Layout.getTypeAllocSize(Array).getFixedValue());
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

    if (llvm::none_of(instructions(Function), [&](Instruction &Instruction) {
          return isCountedPrefixCandidate(Instruction, Layout);
        }))
      continue;

    Canonicalize.run(Function, Analyses);
    auto &Tree = Analyses.getResult<DominatorTreeAnalysis>(Function);
    auto &Loops = Analyses.getResult<LoopAnalysis>(Function);
    for (Instruction &Instruction : make_early_inc_range(instructions(Function))) {
      auto *Init = dyn_cast<MemSetInst>(&Instruction);
      Type *Element = Init ? countedPrefixElement(*Init, Layout) : nullptr;
      if (!Element || !ElisaCountedPrefix::coversReads(
              *cast<AllocaInst>(Init->getRawDest()), *Init, Element, Loops, Tree, Layout))
        continue;
      Init->eraseFromParent();
      ++Removed;
    }
  }
  return Removed;
}
