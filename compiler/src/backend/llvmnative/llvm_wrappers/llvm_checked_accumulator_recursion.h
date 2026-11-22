// Turn linear recursion that combines its result with checked arithmetic,
// `return x op f(..., x, ...)` with `op` an overflow-checked add, sub or
// mul and `x` passed through unchanged, into two loops: a descent that
// counts the recursion depth and an ascent that applies `x op acc` that many
// times to the base case's value. LLVM's tail-recursion elimination only
// reassociates unchecked operators; reassociating a checked one could move
// an overflow trap. The ascent applies the same operation to the same
// values innermost-first, exactly as the returning calls did, so every
// intermediate value and the first overflowing step are unchanged. When
// ranges bound every value of such a loop, its check is then removed.
#include <llvm/Analysis/LazyValueInfo.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
#include <llvm/Transforms/InstCombine/InstCombine.h>
#include <llvm/Transforms/Scalar/SimplifyCFG.h>
namespace ElisaCheckedRecursion {
using namespace llvm;

struct Shape {
  CallInst *Call = nullptr;
  WithOverflowInst *Checked = nullptr;
  ExtractValueInst *Result = nullptr, *Overflow = nullptr;
  CondBrInst *Branch = nullptr;
  BasicBlock *Trap = nullptr, *Continue = nullptr;
  // Set when the overflow-free edge enters the shared return block itself.
  bool ContinueReturns = false;
  Value *Invariant = nullptr;
  unsigned InvariantOperand = 0;
};

// Exit holds only `ret (phi ...)`, and the phi takes Result from From.
static bool returnsMerged(PHINode &Merge, BasicBlock *From, Value *Result) {
  BasicBlock *Exit = Merge.getParent();
  auto *Return = dyn_cast<ReturnInst>(Exit->getTerminator());
  return Return && Return->getReturnValue() == &Merge && Exit->size() == 2 &&
         Merge.hasOneUse() && Merge.getIncomingValueForBlock(From) == Result;
}

// The recursion's value reaches a return unchanged: the overflow-free edge
// enters the shared `ret (phi ... v ...)` block, or Continue holds `ret v`,
// or Continue branches to such a block.
static bool returnedUnchanged(Shape &Match) {
  if (!Match.Result->hasOneUse()) return false;
  User *Use = *Match.Result->user_begin();
  auto *Merge = dyn_cast<PHINode>(Use);
  BasicBlock *Recurse = Match.Call->getParent();
  if (Merge && Merge->getParent() == Match.Continue) {
    Match.ContinueReturns = true;
    return Match.Result->getParent() == Recurse && returnsMerged(*Merge, Recurse, Match.Result);
  }
  if (Match.Continue->getSinglePredecessor() == nullptr) return false;
  for (Instruction &Step : *Match.Continue)
    if (&Step != Match.Result && &Step != Match.Continue->getTerminator()) return false;
  if (auto *Return = dyn_cast<ReturnInst>(Use)) return Return->getParent() == Match.Continue;
  return Merge && isa<UncondBrInst>(Match.Continue->getTerminator()) &&
         returnsMerged(*Merge, Match.Continue, Match.Result);
}

// Some return other than the recursion's own path: the base case.
static bool hasBaseReturn(Function &F, Shape &Match) {
  auto *Merge = dyn_cast<PHINode>(*Match.Result->user_begin());
  for (BasicBlock &Block : F) {
    if (!isa<ReturnInst>(Block.getTerminator()) ||
        (&Block == Match.Continue && !Match.ContinueReturns))
      continue;
    if (!Merge || Merge->getParent() != &Block || Merge->getNumIncomingValues() > 1) return true;
  }
  return false;
}

static bool isTrapExit(BasicBlock &Block) {
  return isa<UnreachableInst>(Block.getTerminator()) && !isa<PHINode>(Block.front());
}

static std::optional<Shape> match(Function &F) {
  if (!F.hasLocalLinkage() || F.isDeclaration() || F.isVarArg() || F.hasPersonalityFn() ||
      F.hasOptNone() || F.hasOptSize() || F.hasMinSize() || !F.getReturnType()->isIntegerTy())
    return std::nullopt;
  Shape Match;
  for (Instruction &Instruction : instructions(F)) {
    if (isa<AllocaInst>(Instruction)) return std::nullopt;
    auto *Call = dyn_cast<CallInst>(&Instruction);
    if (!Call || Call->getCalledFunction() != &F) continue;
    if (Match.Call || Call->isMustTailCall() || Call->hasOperandBundles()) return std::nullopt;
    Match.Call = Call;
  }
  if (!Match.Call || !Match.Call->hasOneUse()) return std::nullopt;
  Match.Checked = dyn_cast<WithOverflowInst>(*Match.Call->user_begin());
  if (!Match.Checked || Match.Checked->getParent() != Match.Call->getParent() ||
      Match.Checked->getNumUses() != 2)
    return std::nullopt;
  Match.InvariantOperand = Match.Checked->getLHS() == Match.Call ? 1 : 0;
  Match.Invariant = Match.Checked->getArgOperand(Match.InvariantOperand);
  auto *Argument = dyn_cast<llvm::Argument>(Match.Invariant);
  if (Match.Invariant == Match.Call ||
      !(isa<Constant>(Match.Invariant) ||
        (Argument && Match.Call->getArgOperand(Argument->getArgNo()) == Argument)))
    return std::nullopt;
  for (User *Use : Match.Checked->users()) {
    auto *Part = dyn_cast<ExtractValueInst>(Use);
    if (!Part || Part->getNumIndices() != 1) return std::nullopt;
    (Part->getIndices()[0] == 0 ? Match.Result : Match.Overflow) = Part;
  }
  BasicBlock *Block = Match.Call->getParent();
  Match.Branch = dyn_cast<CondBrInst>(Block->getTerminator());
  if (!Match.Result || !Match.Overflow || !Match.Branch || !Match.Overflow->hasOneUse() ||
      Match.Branch->getCondition() != Match.Overflow)
    return std::nullopt;
  for (Instruction *Step = Match.Call->getNextNode(); Step != Match.Branch; Step = Step->getNextNode())
    if (Step != Match.Checked && Step != Match.Overflow && Step != Match.Result) return std::nullopt;
  Match.Trap = Match.Branch->getSuccessor(0);
  Match.Continue = Match.Branch->getSuccessor(1);
  if (Match.Trap == Match.Continue || !isTrapExit(*Match.Trap) ||
      (Match.Result->getParent() != Block && Match.Result->getParent() != Match.Continue) ||
      Match.Continue == Block || !returnedUnchanged(Match) || !hasBaseReturn(F, Match))
    return std::nullopt;
  return Match;
}

static void rewrite(Function &F, Shape &Match) {
  LLVMContext &Context = F.getContext();
  Type *Count = Type::getInt64Ty(Context);
  BasicBlock *Header = &F.getEntryBlock();
  BasicBlock *Entry = BasicBlock::Create(Context, "recursion.entry", &F, Header);
  IRBuilder<> Builder(Entry);
  Builder.CreateBr(Header);
  // Descent: arguments that change become header phis; Depth counts calls.
  BasicBlock *Recurse = Match.Call->getParent();
  Builder.SetInsertPoint(Header, Header->begin());
  PHINode *Depth = Builder.CreatePHI(Count, 2, "recursion.depth");
  Depth->addIncoming(ConstantInt::get(Count, 0), Entry);
  for (Argument &Argument : F.args()) {
    Value *Next = Match.Call->getArgOperand(Argument.getArgNo());
    if (Next == &Argument) continue;
    PHINode *Current = Builder.CreatePHI(Argument.getType(), 2, Argument.getName() + ".recursion");
    Argument.replaceAllUsesWith(Current);
    Current->addIncoming(&Argument, Entry);
    Current->addIncoming(Match.Call->getArgOperand(Argument.getArgNo()), Recurse);
  }
  MDNode *Weights = Match.Branch->getMetadata(LLVMContext::MD_prof);
  Intrinsic::ID Operation = Match.Checked->getIntrinsicID();
  Builder.SetInsertPoint(Match.Branch);
  Depth->addIncoming(Builder.CreateAdd(Depth, ConstantInt::get(Count, 1), "recursion.depth.next"),
                     Recurse);
  Builder.CreateBr(Header);
  Match.Branch->eraseFromParent();
  // The result may live in Continue, which is deleted with it.
  SmallVector<Instruction *, 4> Dead;
  for (Instruction *Part : {cast<Instruction>(Match.Result), cast<Instruction>(Match.Overflow),
                            cast<Instruction>(Match.Checked), cast<Instruction>(Match.Call)})
    if (Part->getParent() == Recurse) Dead.push_back(Part);
  if (Match.ContinueReturns) Match.Continue->removePredecessor(Recurse);
  else DeleteDeadBlock(Match.Continue);
  for (Instruction *Part : Dead) Part->eraseFromParent();
  // Ascent: every return feeds the base value into the replayed operations.
  SmallVector<ReturnInst *, 4> Returns;
  for (BasicBlock &Block : F)
    if (auto *Return = dyn_cast<ReturnInst>(Block.getTerminator())) Returns.push_back(Return);
  Type *Result = F.getReturnType();
  BasicBlock *Ascent = BasicBlock::Create(Context, "recursion.ascent", &F);
  BasicBlock *Step = BasicBlock::Create(Context, "recursion.step", &F);
  BasicBlock *Done = BasicBlock::Create(Context, "recursion.done", &F);
  Builder.SetInsertPoint(Ascent);
  PHINode *Accumulator = Builder.CreatePHI(Result, 4, "recursion.value");
  PHINode *Remaining = Builder.CreatePHI(Count, 4, "recursion.remaining");
  for (ReturnInst *Return : Returns) {
    BasicBlock *Block = Return->getParent();
    Accumulator->addIncoming(Return->getReturnValue(), Block);
    Remaining->addIncoming(Depth, Block);
    BranchInst::Create(Ascent, Return->getIterator());
    Return->eraseFromParent();
  }
  Builder.CreateCondBr(Builder.CreateICmpEQ(Remaining, ConstantInt::get(Count, 0)), Done, Step);
  Builder.SetInsertPoint(Step);
  Value *Operands[2];
  Operands[Match.InvariantOperand] = Match.Invariant;
  Operands[1 - Match.InvariantOperand] = Accumulator;
  Value *Checked = Builder.CreateBinaryIntrinsic(Operation, Operands[0],
                                                 Operands[1]);
  Accumulator->addIncoming(Builder.CreateExtractValue(Checked, 0), Step);
  Remaining->addIncoming(Builder.CreateSub(Remaining, ConstantInt::get(Count, 1)), Step);
  Instruction *Guard = Builder.CreateCondBr(Builder.CreateExtractValue(Checked, 1), Match.Trap, Ascent);
  if (Weights) Guard->setMetadata(LLVMContext::MD_prof, Weights);
  Builder.SetInsertPoint(Done);
  Builder.CreateRet(Accumulator);
}

// Largest magnitude in a signed range, absent when it reaches the minimum.
static std::optional<APInt> magnitude(const ConstantRange &Range) {
  if (Range.isEmptySet() || Range.getSignedMin().isMinSignedValue()) return std::nullopt;
  return APIntOps::umax(Range.getSignedMin().abs(), Range.getSignedMax().abs());
}

// Whether |acc| stays below the signed maximum for Trips applications of
// `acc = x op acc` (op one of +, -, *) from |acc| <= Start with |x| <= Step.
static bool accumulatorFits(Intrinsic::ID Operation, const APInt &Start, const APInt &Step,
                            uint64_t Trips) {
  unsigned Width = Start.getBitWidth(), Wide = 2 * Width + 66;
  APInt Limit = APInt::getSignedMaxValue(Width).zext(Wide);
  APInt Value = Start.zext(Wide), Factor = Step.zext(Wide);
  if (Operation != Intrinsic::smul_with_overflow)
    return (Value + Factor * APInt(Wide, Trips)).ule(Limit);
  for (uint64_t Index = 0; Index < Trips; ++Index) {
    Value *= Factor;
    if (Value.ugt(Limit)) return false;
  }
  return Value.ule(Limit);
}

// A loop's `acc = x op acc` (checked, signed, x invariant) whose every
// value is proved in range by the ranges of x and the start value and the
// loop's maximum trip count loses its overflow check.
static bool proveLoopAccumulators(Loop &L, ScalarEvolution &Evolution, LazyValueInfo &Values,
                                  LoopInfo &Loops) {
  BasicBlock *Preheader = L.getLoopPredecessor(), *Latch = L.getLoopLatch();
  const SCEV *Backedges = Evolution.getConstantMaxBackedgeTakenCount(&L);
  auto *Count = dyn_cast<SCEVConstant>(Backedges);
  if (!Preheader || !Latch || !Count || Count->getAPInt().getActiveBits() > 32) return false;
  uint64_t Trips = Count->getAPInt().getZExtValue() + 1;
  Instruction *Context = Preheader->getTerminator();
  bool Changed = false;
  for (PHINode &Accumulator : L.getHeader()->phis()) {
    auto *Next = dyn_cast<ExtractValueInst>(Accumulator.getIncomingValueForBlock(Latch));
    auto *Checked = Next ? dyn_cast<WithOverflowInst>(Next->getAggregateOperand()) : nullptr;
    if (!Checked || !Checked->isSigned() || Next->getIndices()[0] != 0 ||
        Loops.getLoopFor(Checked->getParent()) != &L ||
        llvm::any_of(Checked->users(), [](User *Use) {
          auto *Part = dyn_cast<ExtractValueInst>(Use);
          return !Part || Part->getNumIndices() != 1;
        }))
      continue;
    unsigned Position = Checked->getLHS() == &Accumulator ? 1 : 0;
    Value *Invariant = Checked->getArgOperand(Position);
    if (Checked->getArgOperand(1 - Position) != &Accumulator || !L.isLoopInvariant(Invariant))
      continue;
    auto Start = magnitude(Values.getConstantRange(Accumulator.getIncomingValueForBlock(Preheader),
                                                   Context, false));
    auto Step = magnitude(Values.getConstantRange(Invariant, Context, false));
    if (!Start || !Step || !accumulatorFits(Checked->getIntrinsicID(), *Start, *Step, Trips))
      continue;
    IRBuilder<> Builder(Checked);
    Value *Plain = Builder.CreateBinOp(Checked->getBinaryOp(), Checked->getLHS(), Checked->getRHS());
    cast<BinaryOperator>(Plain)->setHasNoSignedWrap(true);
    for (User *Use : make_early_inc_range(Checked->users())) {
      auto *Part = cast<ExtractValueInst>(Use);
      Part->replaceAllUsesWith(Part->getIndices()[0] == 0 ? Plain : Builder.getFalse());
      Part->eraseFromParent();
    }
    Checked->eraseFromParent();
    Changed = true;
  }
  return Changed;
}

static unsigned proveBoundedAccumulators(Module &Module, FunctionAnalysisManager &Analyses) {
  unsigned Changed = 0;
  for (Function &F : Module) {
    if (F.isDeclaration()) continue;
    auto &Loops = Analyses.getResult<LoopAnalysis>(F);
    auto &Evolution = Analyses.getResult<ScalarEvolutionAnalysis>(F);
    auto &Values = Analyses.getResult<LazyValueAnalysis>(F);
    bool FunctionChanged = false;
    for (Loop *L : Loops.getLoopsInPreorder())
      FunctionChanged |= proveLoopAccumulators(*L, Evolution, Values, Loops);
    if (!FunctionChanged) continue;
    Analyses.invalidate(F, PreservedAnalyses::none());
    FunctionPassManager Cleanup;
    Cleanup.addPass(InstCombinePass());
    Cleanup.addPass(SimplifyCFGPass());
    Cleanup.run(F, Analyses);
    ++Changed;
  }
  return Changed;
}

static unsigned loopCheckedAccumulatorRecursion(Module &Module) {
  unsigned Changed = 0;
  for (Function &F : Module)
    if (std::optional<Shape> Match = match(F)) {
      rewrite(F, *Match);
      ++Changed;
    }
  return Changed;
}
} // namespace ElisaCheckedRecursion
