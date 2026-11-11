// Remove a private array's initial fill when a descending loop writes the same
// prefix that a subsequent ascending loop reads. The edge that completes the
// descending loop must dominate the read, including when the writer can fail.
namespace ElisaReverseArrayCoverage {
static Value *elementIndex(Value *Pointer, AllocaInst *Array,
                           const DataLayout &Layout, Type *Element) {
  auto *GEP = dyn_cast<GetElementPtrInst>(Pointer);
  if (!GEP || GEP->getPointerOperand() != Array || GEP->getNumIndices() != 1 ||
      Layout.getTypeAllocSize(GEP->getSourceElementType()) !=
          Layout.getTypeStoreSize(Element))
    return nullptr;
  Value *Index = GEP->getOperand(1);
  return Index->getType()->isIntegerTy(64) ? Index : nullptr;
}

// The latch tests the updated index and the loop has only one entry from
// outside. Its other exits are permitted, provided none can reach the read
// without traversing the completing latch edge.
static CondBrInst *countedLatch(Loop &L, Value *Update, Value *Limit,
                              BasicBlock *&Exit) {
  auto *Latch = L.getLoopLatch();
  auto *Branch = Latch ? dyn_cast<CondBrInst>(Latch->getTerminator()) : nullptr;
  auto *Compare = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  if (!Compare || !Compare->isEquality() ||
      !((Compare->getOperand(0) == Update && Compare->getOperand(1) == Limit) ||
        (Compare->getOperand(1) == Update && Compare->getOperand(0) == Limit)))
    return nullptr;
  unsigned ExitIndex = Compare->getPredicate() == ICmpInst::ICMP_EQ ? 0 : 1;
  Exit = Branch->getSuccessor(ExitIndex);
  if (L.contains(Exit) || Branch->getSuccessor(1 - ExitIndex) != L.getHeader())
    return nullptr;
  return Branch;
}

static bool matches(StoreInst &Write, LoadInst &Read, AllocaInst *Array,
                    const DataLayout &Layout, LoopInfo &Loops,
                    DominatorTree &Dominance) {
  Loop *Writer = Loops.getLoopFor(Write.getParent());
  Loop *Reader = Loops.getLoopFor(Read.getParent());
  if (!Writer || !Reader || Writer == Reader ||
      Writer->getParentLoop() || Reader->getParentLoop())
    return false;
  auto *StepDown = dyn_cast_or_null<BinaryOperator>(
      elementIndex(Write.getPointerOperand(), Array, Layout, Read.getType()));
  auto *ReadIndex = dyn_cast_or_null<PHINode>(
      elementIndex(Read.getPointerOperand(), Array, Layout, Read.getType()));
  if (!StepDown || !ReadIndex || ReadIndex->getParent() != Reader->getHeader())
    return false;
  auto *WriteIndex = dyn_cast<PHINode>(StepDown->getOperand(0));
  auto *Delta = dyn_cast<ConstantInt>(StepDown->getOperand(1));
  if (!WriteIndex || WriteIndex->getParent() != Writer->getHeader() ||
      !Delta || !((StepDown->getOpcode() == Instruction::Add && Delta->isMinusOne()) ||
                   (StepDown->getOpcode() == Instruction::Sub && Delta->isOne())))
    return false;
  auto *WriterEntry = Writer->getLoopPredecessor();
  auto *ReaderEntry = Reader->getLoopPredecessor();
  auto *WriterLatch = Writer->getLoopLatch();
  auto *ReaderLatch = Reader->getLoopLatch();
  if (!WriterEntry || !ReaderEntry || !WriterLatch || !ReaderLatch ||
      WriteIndex->getNumIncomingValues() != 2 || ReadIndex->getNumIncomingValues() != 2 ||
      WriteIndex->getIncomingValueForBlock(WriterLatch) != StepDown ||
      !Dominance.dominates(Write.getParent(), WriterLatch))
    return false;
  Value *Bound = WriteIndex->getIncomingValueForBlock(WriterEntry);
  auto *Zero = dyn_cast<ConstantInt>(ReadIndex->getIncomingValueForBlock(ReaderEntry));
  auto *StepUp = dyn_cast<BinaryOperator>(ReadIndex->getIncomingValueForBlock(ReaderLatch));
  if (!Zero || !Zero->isZero() || !StepUp || StepUp->getOpcode() != Instruction::Add ||
      StepUp->getOperand(0) != ReadIndex)
    return false;
  auto *One = dyn_cast<ConstantInt>(StepUp->getOperand(1));
  if (!One || !One->isOne())
    return false;
  BasicBlock *WriterExit = nullptr;
  BasicBlock *ReaderExit = nullptr;
  return countedLatch(*Writer, StepDown, Zero, WriterExit) &&
         countedLatch(*Reader, StepUp, Bound, ReaderExit) &&
         Dominance.dominates(BasicBlockEdge(WriterLatch, WriterExit), Read.getParent());
}

static bool removable(MemSetInst &Fill, const DataLayout &Layout,
                      LoopInfo &Loops, DominatorTree &Dominance) {
  auto *Array = dyn_cast<AllocaInst>(Fill.getRawDest());
  auto *Byte = dyn_cast<ConstantInt>(Fill.getValue());
  if (!Array || !Array->isStaticAlloca() || Array->isUsedWithInAlloca() ||
      Array->isSwiftError() || Fill.isVolatile() || Fill.hasOperandBundles() ||
      !Byte || !Byte->isZero() || Loops.getLoopFor(Fill.getParent()))
    return false;
  StoreInst *Write = nullptr;
  LoadInst *Read = nullptr;
  for (User *Use : Array->users()) {
    if (Use == &Fill)
      continue;
    if (auto *Lifetime = dyn_cast<IntrinsicInst>(Use)) {
      if (Lifetime->getIntrinsicID() == Intrinsic::lifetime_end ||
          (Lifetime->getIntrinsicID() == Intrinsic::lifetime_start &&
           Dominance.dominates(Lifetime, &Fill)))
        continue;
      return false;
    }
    auto *GEP = dyn_cast<GetElementPtrInst>(Use);
    if (!GEP)
      return false;
    for (User *Access : GEP->users()) {
      if (auto *Load = dyn_cast<LoadInst>(Access); Load && Load->isSimple() &&
          Load->getPointerOperand() == GEP && !Read) {
        Read = Load;
      } else if (auto *Store = dyn_cast<StoreInst>(Access); Store && Store->isSimple() &&
                 Store->getPointerOperand() == GEP && Store->getValueOperand() != GEP && !Write) {
        Write = Store;
      } else {
        return false;
      }
    }
  }
  return Read && Write && Read->getType()->isIntegerTy() &&
         Write->getValueOperand()->getType() == Read->getType() &&
         Dominance.dominates(&Fill, Write) && Dominance.dominates(&Fill, Read) &&
         matches(*Write, *Read, Array, Layout, Loops, Dominance);
}

static unsigned removeFills(Function &F, const DataLayout &Layout, DominatorTree &Dominance) {
  SmallVector<MemSetInst *, 4> Fills;
  for (BasicBlock &Block : F)
    for (Instruction &I : Block)
      if (auto *Fill = dyn_cast<MemSetInst>(&I); Fill && isa<AllocaInst>(Fill->getRawDest()))
        Fills.push_back(Fill);
  if (Fills.empty())
    return 0;
  LoopInfo Loops(Dominance);
  unsigned Removed = 0;
  for (MemSetInst *Fill : Fills)
    if (removable(*Fill, Layout, Loops, Dominance)) {
      Fill->eraseFromParent();
      ++Removed;
    }
  return Removed;
}
} // namespace ElisaReverseArrayCoverage
