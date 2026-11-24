// Bound reads of input elements an earlier loop validated. A program that
// checks every element of an argument range (`input[2 + i]` for i < n + m
// against [0, 1e9], returning on a bad one) and then reads those elements
// again gets no range for the second reads: they load memory again. When
// the validating loop finished, every element below its bound passed the
// check, and a later load reads one of them if its element index is
// provably below that bound and no write that could reach the argument's
// memory runs between them. Such loads get the validated range as `!range`,
// which removes the overflow checks of arithmetic over input values.
#include <llvm/Analysis/CFG.h>
namespace ElisaValidatedReads {
using namespace llvm;
using namespace llvm::PatternMatch;

// One validating loop: elements Start + Step * i for i < Bound passed.
struct Validation {
  const SCEV *Start;
  uint64_t Step;
  Value *Bound;
  BasicBlock *Exit;
  ConstantRange Range;
};

// The values Loaded keeps when Condition does not leave the loop (Leaves:
// the loop leaves when Condition holds). Conditions are compares of Loaded
// with constants, joined by a short-circuit `or`, as a select or as an i1
// phi whose `true` inputs each come from a dominating branch that did not
// take that edge while staying.
static void staying(Value *Condition, Value *Loaded, bool Leaves, ConstantRange &Range,
                    DominatorTree &Tree, unsigned Depth = 0) {
  if (Depth > 6) return;
  Value *Left = nullptr, *Right = nullptr;
  if (Leaves && match(Condition, m_LogicalOr(m_Value(Left), m_Value(Right)))) {
    staying(Left, Loaded, true, Range, Tree, Depth + 1);
    staying(Right, Loaded, true, Range, Tree, Depth + 1);
    return;
  }
  if (auto *Merge = dyn_cast<PHINode>(Condition); Merge && Leaves) {
    Value *Only = nullptr;
    SmallVector<std::pair<llvm::Value *, bool>, 4> Skipped;
    for (unsigned Index = 0; Index < Merge->getNumIncomingValues(); ++Index) {
      Value *In = Merge->getIncomingValue(Index);
      BasicBlock *From = Merge->getIncomingBlock(Index);
      auto *Known = dyn_cast<ConstantInt>(In);
      if (!Known) {
        if (Only) return;
        Only = In;
        continue;
      }
      auto *Branch = dyn_cast<CondBrInst>(From->getTerminator());
      if (!Known->isOne() || !Branch || Branch->getSuccessor(0) == Branch->getSuccessor(1) ||
          !Tree.dominates(From, Merge->getParent()))
        return;
      Skipped.push_back({Branch->getCondition(), Branch->getSuccessor(0) == Merge->getParent()});
    }
    if (!Only) return;
    for (auto [Taken, WhenTrue] : Skipped) staying(Taken, Loaded, WhenTrue, Range, Tree, Depth + 1);
    staying(Only, Loaded, true, Range, Tree, Depth + 1);
    return;
  }
  auto *Compare = dyn_cast<ICmpInst>(Condition);
  auto *Constant = Compare ? dyn_cast<ConstantInt>(Compare->getOperand(1)) : nullptr;
  if (!Constant || Compare->getOperand(0) != Loaded) return;
  auto Predicate = Leaves ? Compare->getInversePredicate() : Compare->getPredicate();
  Range = Range.intersectWith(
      ConstantRange::makeExactICmpRegion(Predicate, Constant->getValue()));
}

// How a counted loop ends: i counts from 0 by 1, and the loop leaves to
// Exit from Exiting once every element below Bound passed. Either the latch
// leaves when i + 1 == Bound (a rotated loop), or the header leaves when
// i < Bound fails or i == Bound holds (a loop still tested at the top).
struct Counted {
  Value *Bound;
  BasicBlock *Exiting;
  BasicBlock *Exit;
};

static PHINode *counter(Loop &L, BasicBlock &Before, Value &Next) {
  for (PHINode &Phi : L.getHeader()->phis()) {
    auto *Start = dyn_cast<ConstantInt>(Phi.getIncomingValueForBlock(&Before));
    if (Start && Start->isZero() && Phi.getIncomingValueForBlock(L.getLoopLatch()) == &Next &&
        match(&Next, m_Add(m_Specific(&Phi), m_One())))
      return &Phi;
  }
  return nullptr;
}

static std::optional<Counted> counted(Loop &L, ScalarEvolution &Evolution) {
  BasicBlock *Latch = L.getLoopLatch(), *Before = L.getLoopPredecessor();
  if (!Latch || !Before) return std::nullopt;
  for (BasicBlock *Exiting : {Latch, L.getHeader()}) {
    auto *Branch = dyn_cast<CondBrInst>(Exiting->getTerminator());
    auto *Compare = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
    if (!Compare || !Compare->getOperand(1)->getType()->isIntegerTy() ||
        !Evolution.isLoopInvariant(Evolution.getSCEV(Compare->getOperand(1)), &L))
      continue;
    unsigned Leaving = L.contains(Branch->getSuccessor(0)) ? 1 : 0;
    if (L.contains(Branch->getSuccessor(Leaving)) || !L.contains(Branch->getSuccessor(1 - Leaving)))
      continue;
    Value *Tested = Compare->getOperand(0);
    auto Predicate = Leaving == 0 ? Compare->getPredicate() : Compare->getInversePredicate();
    bool Counts = false;
    if (Exiting == Latch && Exiting != L.getHeader())
      Counts = Predicate == ICmpInst::ICMP_EQ && isa<Instruction>(Tested) &&
               counter(L, *Before, *Tested);
    else if (Exiting == L.getHeader() && Exiting != Latch)
      for (PHINode &Phi : L.getHeader()->phis())
        if (&Phi == Tested && (Predicate == ICmpInst::ICMP_UGE || Predicate == ICmpInst::ICMP_EQ))
          if (auto *Next = dyn_cast<Instruction>(Phi.getIncomingValueForBlock(Latch)))
            Counts = counter(L, *Before, *Next) == &Phi;
    if (Counts) return Counted{Compare->getOperand(1), Exiting, Branch->getSuccessor(Leaving)};
  }
  return std::nullopt;
}

// Every path into Exit other than the counted exit decided Bound == 0.
static bool exitCoversBound(Loop &L, const Counted &Loop) {
  for (BasicBlock *From : predecessors(Loop.Exit)) {
    if (From == Loop.Exiting) continue;
    if (L.contains(From)) return false;
    auto *Branch = dyn_cast<CondBrInst>(From->getTerminator());
    auto *Compare = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
    if (!Compare || Compare->getPredicate() != ICmpInst::ICMP_EQ ||
        Compare->getOperand(0) != Loop.Bound || !match(Compare->getOperand(1), m_Zero()) ||
        Branch->getSuccessor(0) != Loop.Exit || Branch->getSuccessor(1) == Loop.Exit)
      return false;
  }
  return true;
}

// The writes the proof skips are stores to stack objects, so the
// validated memory must not be one (an argument or a view's data).
static bool outsideStack(ScalarEvolution &Evolution, const SCEV *Pointer) {
  auto *Base = dyn_cast<SCEVUnknown>(Evolution.getPointerBase(Pointer));
  return Base && !isa<AllocaInst>(getUnderlyingObject(Base->getValue()));
}

static std::optional<Validation> validation(Loop &L, ScalarEvolution &Evolution,
                                            DominatorTree &Tree, LoadInst &Load) {
  std::optional<Counted> Loop = counted(L, Evolution);
  if (!Loop || !Tree.dominates(Load.getParent(), L.getLoopLatch())) return std::nullopt;
  auto *Address = dyn_cast<SCEVAddRecExpr>(Evolution.getSCEV(Load.getPointerOperand()));
  auto *Step = Address ? dyn_cast<SCEVConstant>(Address->getStepRecurrence(Evolution)) : nullptr;
  uint64_t Size = Load.getDataLayout().getTypeStoreSize(Load.getType()).getFixedValue();
  if (!Step || Address->getLoop() != &L || !Address->isAffine() ||
      Step->getAPInt() != Size ||
      !outsideStack(Evolution, Address->getStart()))
    return std::nullopt;
  unsigned Width = Load.getType()->getIntegerBitWidth();
  ConstantRange Range = ConstantRange::getFull(Width);
  for (BasicBlock *Block : L.blocks()) {
    auto *Branch = dyn_cast<CondBrInst>(Block->getTerminator());
    if (!Branch || !Tree.dominates(Block, L.getLoopLatch()) || Block == Loop->Exiting) continue;
    for (unsigned Side = 0; Side < 2; ++Side)
      if (!L.contains(Branch->getSuccessor(Side)) && L.contains(Branch->getSuccessor(1 - Side)))
        staying(Branch->getCondition(), &Load, Side == 0, Range, Tree);
  }
  if (Range.isFullSet() || Range.isEmptySet() || !exitCoversBound(L, *Loop)) return std::nullopt;
  return Validation{Address->getStart(), Size, Loop->Bound, Loop->Exit, Range};
}

// Whether a write that may reach Base's memory can run before Load after
// any earlier point: writes to local stack objects cannot.
static bool unwritten(Function &F, Instruction &Load, DominatorTree &Tree, LoopInfo &Loops) {
  for (Instruction &Writer : instructions(F)) {
    if (!Writer.mayWriteToMemory()) continue;
    Value *Target = nullptr;
    if (auto *Store = dyn_cast<StoreInst>(&Writer)) Target = Store->getPointerOperand();
    if (auto *Set = dyn_cast<MemIntrinsic>(&Writer)) Target = Set->getRawDest();
    if (isa<LifetimeIntrinsic>(&Writer)) continue;
    if (Target && isa<AllocaInst>(getUnderlyingObject(Target))) continue;
    if (isPotentiallyReachable(&Writer, &Load, nullptr, &Tree, &Loops)) return false;
  }
  return true;
}

// Offset / Size term by term, when every term is a multiple of Size. The
// quotient times Size equals Offset modulo 2^64, so a quotient below the
// bound addresses a validated element even where `/u` would not fold.
static const SCEV *divided(ScalarEvolution &Evolution, const SCEV *Offset, uint64_t Size,
                           unsigned Depth = 0) {
  if (Depth > 6) return nullptr;
  if (auto *Constant = dyn_cast<SCEVConstant>(Offset)) {
    const APInt &Value = Constant->getAPInt();
    if (Value.urem(Size)) return nullptr;
    return Evolution.getConstant(Value.udiv(Size));
  }
  if (auto *Sum = dyn_cast<SCEVAddExpr>(Offset)) {
    const SCEV *Total = Evolution.getZero(Offset->getType());
    for (const SCEV *Term : Sum->operands()) {
      const SCEV *Part = divided(Evolution, Term, Size, Depth + 1);
      if (!Part) return nullptr;
      Total = Evolution.getAddExpr(Total, Part);
    }
    return Total;
  }
  if (auto *Product = dyn_cast<SCEVMulExpr>(Offset)) {
    auto *Factor = dyn_cast<SCEVConstant>(Product->getOperand(0));
    if (!Factor || Factor->getAPInt().urem(Size)) return nullptr;
    const SCEV *Result = Evolution.getConstant(Factor->getAPInt().udiv(Size));
    for (const SCEV *Other : drop_begin(Product->operands()))
      Result = Evolution.getMulExpr(Result, Other);
    return Result;
  }
  if (auto *Recurrence = dyn_cast<SCEVAddRecExpr>(Offset); Recurrence && Recurrence->isAffine()) {
    const SCEV *Start = divided(Evolution, Recurrence->getStart(), Size, Depth + 1);
    const SCEV *Step = divided(Evolution, Recurrence->getStepRecurrence(Evolution), Size, Depth + 1);
    if (!Start || !Step) return nullptr;
    return Evolution.getAddRecExpr(Start, Step, Recurrence->getLoop(), SCEV::FlagAnyWrap);
  }
  return nullptr;
}

// Index < Bound. A bound `x + y` that cannot wrap (two input counts) holds
// every index below x, and x + d for every d below y: no sum wraps.
static bool below(const SCEV *Index, Value &Bound, ScalarEvolution &Evolution,
                  LazyValueInfo &Values, ElisaIndexBounds::Prover &Proof, LoadInst &Load) {
  if (Proof.proveBelow(Index, Evolution.getSCEV(&Bound), &Load)) return true;
  auto *Sum = dyn_cast<BinaryOperator>(&Bound);
  if (!Sum || Sum->getOpcode() != Instruction::Add) return false;
  if (!Sum->hasNoUnsignedWrap() &&
      Values.getConstantRange(Sum->getOperand(0), &Load, false)
              .unsignedAddMayOverflow(Values.getConstantRange(Sum->getOperand(1), &Load, false)) !=
          ConstantRange::OverflowResult::NeverOverflows)
    return false;
  for (unsigned Side = 0; Side < 2; ++Side) {
    const SCEV *Part = Evolution.getSCEV(Sum->getOperand(Side));
    const SCEV *Other = Evolution.getSCEV(Sum->getOperand(1 - Side));
    if (Proof.proveBelow(Index, Part, &Load) ||
        Proof.proveBelow(Evolution.getMinusSCEV(Index, Part), Other, &Load))
      return true;
  }
  return false;
}

static bool annotateLoad(LoadInst &Load, const Validation &Check, ScalarEvolution &Evolution,
                         LazyValueInfo &Values, ElisaIndexBounds::Prover &Proof,
                         DominatorTree &Tree) {
  if (Load.getMetadata(LLVMContext::MD_range) || !Load.isSimple() ||
      !Load.getType()->isIntegerTy() ||
      Check.Range.getBitWidth() != Load.getType()->getIntegerBitWidth() ||
      !Tree.dominates(Check.Exit, Load.getParent()))
    return false;
  const SCEV *Offset = Evolution.getMinusSCEV(Evolution.getSCEV(Load.getPointerOperand()),
                                              Check.Start);
  if (isa<SCEVCouldNotCompute>(Offset)) return false;
  const SCEV *Index = divided(Evolution, Offset, Check.Step);
  if (!Index) return false;
  if (Check.Bound->getType() != Index->getType() ||
      !below(Index, *Check.Bound, Evolution, Values, Proof, Load))
    return false;
  MDBuilder Builder(Load.getContext());
  Load.setMetadata(LLVMContext::MD_range,
                   Builder.createRange(Check.Range.getLower(), Check.Range.getUpper()));
  return true;
}

static unsigned annotate(Function &F, FunctionAnalysisManager &Analyses) {
  auto &Loops = Analyses.getResult<LoopAnalysis>(F);
  if (Loops.empty()) return 0;
  auto &Evolution = Analyses.getResult<ScalarEvolutionAnalysis>(F);
  auto &Tree = Analyses.getResult<DominatorTreeAnalysis>(F);
  SmallVector<std::pair<LoadInst *, Validation>, 4> Checks;
  for (Loop *L : Loops.getLoopsInPreorder())
    for (BasicBlock *Block : L->blocks())
      for (Instruction &I : *Block)
        if (auto *Load = dyn_cast<LoadInst>(&I); Load && Load->isSimple() &&
                                                 Load->getType()->isIntegerTy())
          if (auto Check = validation(*L, Evolution, Tree, *Load))
            Checks.push_back({Load, *Check});
  if (Checks.empty()) return 0;
  ElisaIndexBounds::Prover Proof(Evolution, Tree, Loops);
  auto &Values = Analyses.getResult<LazyValueAnalysis>(F);
  unsigned Changed = 0;
  for (Instruction &I : instructions(F)) {
    auto *Load = dyn_cast<LoadInst>(&I);
    if (!Load) continue;
    for (auto &[Validator, Check] : Checks)
      if (Load != Validator &&
          Evolution.getPointerBase(Evolution.getSCEV(Load->getPointerOperand())) ==
              Evolution.getPointerBase(Check.Start) &&
          unwritten(F, *Load, Tree, Loops) &&
          annotateLoad(*Load, Check, Evolution, Values, Proof, Tree)) {
        ++Changed;
        break;
      }
  }
  if (Changed) Analyses.invalidate(F, PreservedAnalyses::none());
  return Changed;
}

static unsigned annotateValidatedReads(Module &Module) {
  PassBuilder Builder;
  FunctionAnalysisManager Analyses;
  Builder.registerFunctionAnalyses(Analyses);
  unsigned Changed = 0;
  for (Function &F : Module)
    if (!F.isDeclaration() && !F.hasOptNone()) Changed += annotate(F, Analyses);
  return Changed;
}
} // namespace ElisaValidatedReads
