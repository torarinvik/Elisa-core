// Let bounds checks in binary searches be unswitched out of the loop. In
//
//     while begin < end:
//         middle = begin + (end - begin) / 2
//         check middle < N
//         begin <- middle + 1   or   end <- middle
//
// `middle < end` on every trip, and `end` only ever moves down to `middle`,
// so `middle < end <= end_on_entry`. When end_on_entry <= N the check can
// never fail; otherwise it still may. The failing condition becomes
// `end_on_entry > N and middle >= N`, whose first half is loop invariant,
// and loop unswitching then gives the common case a loop without the check.
// The check stays exactly where it was for every other entry value.
namespace ElisaBisectionChecks {
using namespace llvm;
using namespace llvm::PatternMatch;

static bool trapBlock(BasicBlock *Block) {
  return Block && isa<UnreachableInst>(Block->getTerminator());
}

// Whether Value, followed through selects and in-loop phis, is only ever
// End itself or Middle.
static bool movesDownTo(Value *Value, PHINode *End, Instruction *Middle, Loop &L,
                        SmallPtrSetImpl<llvm::Value *> &Seen) {
  if (Value == End || Value == Middle || !Seen.insert(Value).second) return true;
  if (auto *Select = dyn_cast<SelectInst>(Value))
    return movesDownTo(Select->getTrueValue(), End, Middle, L, Seen) &&
           movesDownTo(Select->getFalseValue(), End, Middle, L, Seen);
  auto *Phi = dyn_cast<PHINode>(Value);
  if (!Phi || !L.contains(Phi) || Phi->getParent() == L.getHeader())
    return false;
  for (llvm::Value *Incoming : Phi->incoming_values())
    if (!movesDownTo(Incoming, End, Middle, L, Seen)) return false;
  return true;
}

// Whether Block only runs after a branch inside L found `Begin <u End`.
static bool guardedBelow(BasicBlock *Block, PHINode *Begin, PHINode *End, Loop &L,
                         DominatorTree &Tree) {
  for (DomTreeNode *Node = Tree.getNode(Block); Node && Node->getIDom();
       Node = Node->getIDom()) {
    BasicBlock *Child = Node->getBlock(), *Parent = Node->getIDom()->getBlock();
    if (!L.contains(Parent)) return false;
    auto *Branch = dyn_cast<CondBrInst>(Parent->getTerminator());
    if (Branch && Branch->getSuccessor(0) != Branch->getSuccessor(1) &&
        Child->getSinglePredecessor() == Parent) {
      CmpPredicate Predicate;
      Value *Left = nullptr, *Right = nullptr;
      if (match(Branch->getCondition(), m_ICmp(Predicate, m_Value(Left), m_Value(Right)))) {
        bool Truth = Branch->getSuccessor(0) == Child;
        ICmpInst::Predicate Holds = Truth ? ICmpInst::Predicate(Predicate)
                                          : ICmpInst::getInversePredicate(Predicate);
        if ((Holds == ICmpInst::ICMP_ULT && Left == Begin && Right == End) ||
            (Holds == ICmpInst::ICMP_UGT && Left == End && Right == Begin))
          return true;
      }
    }
  }
  return false;
}

// The midpoint Begin + (End - Begin) / 2 of two header phis, if Value is one.
static bool midpoint(Value *Value, PHINode *&Begin, PHINode *&End, Loop &L) {
  llvm::Value *Half = nullptr, *Base = nullptr, *Low = nullptr, *High = nullptr;
  if (!match(Value, m_c_Add(m_Value(Half), m_Value(Base)))) return false;
  auto halfOf = [&](llvm::Value *Candidate) {
    return match(Candidate, m_LShr(m_Sub(m_Value(High), m_Value(Low)), m_SpecificInt(1))) ||
           match(Candidate, m_UDiv(m_Sub(m_Value(High), m_Value(Low)), m_SpecificInt(2)));
  };
  if (!halfOf(Half)) {
    std::swap(Half, Base);
    if (!halfOf(Half)) return false;
  }
  Begin = dyn_cast<PHINode>(Low);
  End = dyn_cast<PHINode>(High);
  return Begin && End && Base == Low && Begin->getParent() == L.getHeader() &&
         End->getParent() == L.getHeader();
}

// A limit loaded in the loop (a view's length field still in its alloca,
// before SROA) moves to the preheader when nothing in the loop writes
// memory and the load cannot fault there.
static bool hoistLimitLoad(Value *Limit, Loop &L, BasicBlock &Preheader) {
  auto *Load = dyn_cast<LoadInst>(Limit);
  if (!Load || !Load->isSimple() || !L.contains(Load)) return false;
  for (BasicBlock *Block : L.blocks())
    for (Instruction &I : *Block)
      if (I.mayWriteToMemory()) return false;
  bool Hoisted = false;
  if (!L.makeLoopInvariant(Load->getPointerOperand(), Hoisted, Preheader.getTerminator()) ||
      !isSafeToSpeculativelyExecute(Load, Preheader.getTerminator()))
    return false;
  Load->moveBefore(Preheader.getTerminator()->getIterator());
  return true;
}

static bool versionCheck(CondBrInst &Branch, Loop &L, DominatorTree &Tree) {
  BasicBlock *Preheader = L.getLoopPreheader();
  auto *Compare = dyn_cast<ICmpInst>(Branch.getCondition());
  if (!Preheader || !Compare) return false;
  unsigned Fail = trapBlock(Branch.getSuccessor(0)) ? 0 : 1;
  if (!trapBlock(Branch.getSuccessor(Fail)) || trapBlock(Branch.getSuccessor(1 - Fail)))
    return false;
  // The passing condition, as `Index <u Limit`.
  ICmpInst::Predicate Passes = Fail == 1 ? Compare->getPredicate()
                                         : Compare->getInversePredicate();
  Value *Index = Compare->getOperand(0), *Limit = Compare->getOperand(1);
  if (Passes == ICmpInst::ICMP_UGT) {
    std::swap(Index, Limit);
    Passes = ICmpInst::ICMP_ULT;
  }
  auto *Middle = dyn_cast<Instruction>(Index);
  if (Passes != ICmpInst::ICMP_ULT || !Middle) return false;
  PHINode *Begin = nullptr, *End = nullptr;
  if (!midpoint(Middle, Begin, End, L) ||
      !guardedBelow(Branch.getParent(), Begin, End, L, Tree))
    return false;
  Value *Entry = nullptr;
  for (unsigned I = 0; I < End->getNumIncomingValues(); ++I) {
    SmallPtrSet<Value *, 8> Seen;
    if (!L.contains(End->getIncomingBlock(I))) {
      if (Entry && Entry != End->getIncomingValue(I)) return false;
      Entry = End->getIncomingValue(I);
    } else if (!movesDownTo(End->getIncomingValue(I), End, Middle, L, Seen)) {
      return false;
    }
  }
  if (!Entry || (isa<Constant>(Entry) && isa<Constant>(Limit))) return false;
  // A limit computed in the loop from invariant values (a view's length
  // field) moves to the preheader; anything else is not versioned.
  bool Hoisted = false;
  if (!L.makeLoopInvariant(Limit, Hoisted, Preheader->getTerminator()) &&
      !hoistLimitLoad(Limit, L, *Preheader))
    return false;
  if (auto *LimitDef = dyn_cast<Instruction>(Limit);
      LimitDef && !Tree.dominates(LimitDef, Preheader->getTerminator()))
    return false;
  IRBuilder<> Builder(Preheader->getTerminator());
  Value *Frozen = Builder.CreateFreeze(Entry, "bisect.entry");
  Value *MayFail = Builder.CreateICmpUGT(Frozen, Limit, "bisect.may.fail");
  Builder.SetInsertPoint(&Branch);
  Value *Failing = Builder.CreateICmpUGE(Index, Limit, "bisect.out");
  Value *Taken = Builder.CreateAnd(MayFail, Failing, "bisect.fail");
  Branch.setCondition(Taken);
  if (Fail == 1) Branch.swapSuccessors();
  return true;
}

static unsigned versionFunction(Function &F, FunctionAnalysisManager &Analyses) {
  auto &Loops = Analyses.getResult<LoopAnalysis>(F);
  auto &Tree = Analyses.getResult<DominatorTreeAnalysis>(F);
  unsigned Changed = 0;
  for (Loop *L : Loops.getLoopsInPreorder())
    for (BasicBlock *Block : L->blocks())
      if (auto *Branch = dyn_cast<CondBrInst>(Block->getTerminator());
          Branch && Loops.getLoopFor(Block) == L &&
          versionCheck(*Branch, *L, Tree))
        ++Changed;
  if (Changed) Analyses.invalidate(F, PreservedAnalyses::none());
  return Changed;
}

static unsigned versionBisectionChecks(Module &Module) {
  PassBuilder Builder;
  FunctionAnalysisManager Analyses;
  Builder.registerFunctionAnalyses(Analyses);
  unsigned Changed = 0;
  for (Function &F : Module)
    if (!F.isDeclaration() && !F.hasOptNone()) Changed += versionFunction(F, Analyses);
  return Changed;
}
} // namespace ElisaBisectionChecks
