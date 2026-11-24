// Prove overflow checks dead in loops that run a small, known number of
// times. Range analysis gives up on a recurrence such as
// `next = 2 * current - previous + a`: no fixed interval contains it. When
// scalar evolution bounds the loop's back edges by a small constant M,
// iterating the interval transfer over the body M + 1 times covers every
// value the header phis can take, without widening. A checked operation
// whose operand intervals cannot overflow then has a false overflow bit.
#include <llvm/Analysis/LoopIterator.h>
namespace ElisaBoundedTripRanges {
using namespace llvm;

static constexpr unsigned MaxTrips = 64;
static constexpr unsigned MaxInstructions = 256;

static ConstantRange entryRange(Value *V, Loop &L, LazyValueInfo &Values, DominatorTree &Tree, bool);

// The range V, defined before loop L, has on entry: lazy value info's range
// at the entry, narrowed by the guard edge into each single-predecessor
// block on the dominator chain above it (`count > 32` returns three blocks up), which
// lazy value info, asked at the entry alone, does not chain.
static ConstantRange entryRange(Value *V, Loop &L, LazyValueInfo &Values, DominatorTree &Tree) {
  // A guard that branched on x proved x is not poison (branching on poison is
  // undefined), so freeze(x) is x wherever those guards bound it.
  if (auto *Frozen = dyn_cast<FreezeInst>(V))
    return entryRange(Frozen->getOperand(0), L, Values, Tree)
        .intersectWith(entryRange(Frozen, L, Values, Tree, true), ConstantRange::Signed);
  return entryRange(V, L, Values, Tree, true);
}

static ConstantRange entryRange(Value *V, Loop &L, LazyValueInfo &Values, DominatorTree &Tree, bool) {
  BasicBlock *Before = L.getLoopPredecessor();
  ConstantRange Range = Values.getConstantRangeOnEdge(V, Before, L.getHeader());
  auto *Defined = dyn_cast<Instruction>(V);
  BasicBlock *Block = Before;
  for (unsigned Step = 0; Block && Step < 32; ++Step) {
    auto *Up = Tree.getNode(Block) ? Tree.getNode(Block)->getIDom() : nullptr;
    if (!Up) break;
    if (BasicBlock *From = Block->getSinglePredecessor();
        From && isa<CondBrInst>(From->getTerminator()) &&
        (!Defined || Tree.dominates(Defined, From->getTerminator())))
      Range = Range.intersectWith(Values.getConstantRangeOnEdge(V, From, Block), ConstantRange::Signed);
    if (Defined && !Tree.dominates(Defined, Up->getBlock()->getTerminator())) break;
    Block = Up->getBlock();
  }
  return Range;
}

class Interpreter {
public:
  Interpreter(Loop &L, LazyValueInfo &Values, DominatorTree &Tree)
      : loop(L), values(Values), tree(Tree) {}

  // Ranges of every integer value the loop computes, over all its trips.
  bool run(unsigned Trips) {
    BasicBlock *Before = loop.getLoopPredecessor();
    if (!Before) return false;
    for (PHINode &Phi : loop.getHeader()->phis())
      if (Phi.getType()->isIntegerTy())
        state.insert_or_assign(&Phi, outside(Phi.getIncomingValueForBlock(Before)));
    LoopBlocksRPO Order(&loop);
    Order.perform(&loopInfo());
    for (unsigned Trip = 0; Trip <= Trips; ++Trip) {
      for (BasicBlock *Block : Order)
        for (Instruction &I : *Block)
          if (!(Block == loop.getHeader() && isa<PHINode>(I))) transfer(I);
      for (PHINode &Phi : loop.getHeader()->phis())
        if (Phi.getType()->isIntegerTy())
          state.insert_or_assign(&Phi, state.find(&Phi)->second.unionWith(
              rangeOf(Phi.getIncomingValueForBlock(loop.getLoopLatch()))));
    }
    return true;
  }

  ConstantRange of(Value *V) { return rangeOf(V); }

  // The operand ranges a checked operation sees on some trip.
  std::optional<std::pair<ConstantRange, ConstantRange>> operands(IntrinsicInst &Checked) {
    return std::make_pair(rangeOf(Checked.getArgOperand(0)), rangeOf(Checked.getArgOperand(1)));
  }

private:
  Loop &loop;
  LazyValueInfo &values;
  DominatorTree &tree;
  DenseMap<Value *, ConstantRange> state;

  LoopInfo &loopInfo() { return *loopInfoPointer; }

public:
  LoopInfo *loopInfoPointer = nullptr;

private:
  ConstantRange outside(Value *V) {
    unsigned Width = V->getType()->getIntegerBitWidth();
    if (auto *Constant = dyn_cast<ConstantInt>(V)) return ConstantRange(Constant->getValue());
    if (auto *Defined = dyn_cast<Instruction>(V); Defined && loop.contains(Defined))
      return ConstantRange::getFull(Width);
    return entryRange(V, loop, values, tree);
  }

  ConstantRange rangeOf(Value *V) {
    unsigned Width = V->getType()->getIntegerBitWidth();
    if (auto *Constant = dyn_cast<ConstantInt>(V)) return ConstantRange(Constant->getValue());
    auto *Defined = dyn_cast<Instruction>(V);
    if (!Defined || !loop.contains(Defined))
      return outside(V);
    auto Known = state.find(V);
    return Known == state.end() ? ConstantRange::getEmpty(Width) : Known->second;
  }

  void set(Instruction &I, ConstantRange Range) {
    auto Known = state.find(&I);
    state.insert_or_assign(&I, Known == state.end() ? Range : Known->second.unionWith(Range));
  }

  void transfer(Instruction &I) {
    if (auto *Checked = dyn_cast<WithOverflowInst>(&I)) {
      set(I, rangeOf(Checked->getLHS()).binaryOp(Checked->getBinaryOp(), rangeOf(Checked->getRHS())));
      return;
    }
    if (auto *Part = dyn_cast<ExtractValueInst>(&I)) {
      auto *Checked = dyn_cast<WithOverflowInst>(Part->getAggregateOperand());
      if (Checked && Part->getIndices()[0] == 0 && state.count(Checked))
        set(I, state.find(Checked)->second);
      else if (I.getType()->isIntegerTy())
        set(I, ConstantRange::getFull(I.getType()->getIntegerBitWidth()));
      return;
    }
    if (!I.getType()->isIntegerTy()) return;
    unsigned Width = I.getType()->getIntegerBitWidth();
    // A shift by a constant without signed wrap doubles signed values too,
    // where the shift range rule gives up on negative ones.
    if (auto *Shift = I.getOpcode() == Instruction::Shl ? dyn_cast<ConstantInt>(I.getOperand(1)) : nullptr;
        Shift && cast<BinaryOperator>(I).hasNoSignedWrap() && Shift->getValue().ult(Width - 1)) {
      set(I, rangeOf(I.getOperand(0)).multiply(ConstantRange(APInt(Width, 1).shl(Shift->getValue()))));
      return;
    }
    if (auto *Operation = dyn_cast<BinaryOperator>(&I)) {
      set(I, rangeOf(Operation->getOperand(0))
                 .binaryOp(Operation->getOpcode(), rangeOf(Operation->getOperand(1))));
    } else if (auto *Cast = dyn_cast<CastInst>(&I); Cast && Cast->getSrcTy()->isIntegerTy()) {
      set(I, rangeOf(Cast->getOperand(0)).castOp(Cast->getOpcode(), Width));
    } else if (auto *Choice = dyn_cast<SelectInst>(&I)) {
      set(I, rangeOf(Choice->getTrueValue()).unionWith(rangeOf(Choice->getFalseValue())));
    } else if (auto *Merge = dyn_cast<PHINode>(&I)) {
      ConstantRange Range = ConstantRange::getEmpty(Width);
      for (Value *In : Merge->incoming_values()) Range = Range.unionWith(rangeOf(In));
      set(I, Range);
    } else if (auto *Frozen = dyn_cast<FreezeInst>(&I)) {
      // A `!range` load never yields a value outside its range, so freezing
      // it (checked multiplication freezes its operands) keeps that range.
      auto *Load = dyn_cast<LoadInst>(Frozen->getOperand(0));
      set(I, Load && Load->getMetadata(LLVMContext::MD_range) ? rangeOf(Load) : ConstantRange::getFull(Width));
    } else if (auto *Load = dyn_cast<LoadInst>(&I); Load && Load->getMetadata(LLVMContext::MD_range)) {
      set(I, getConstantRangeFromMetadata(*Load->getMetadata(LLVMContext::MD_range)));
    } else {
      set(I, ConstantRange::getFull(Width));
    }
  }
};

static bool cannotOverflow(WithOverflowInst &Checked, const ConstantRange &Left,
                           const ConstantRange &Right) {
  if (Left.isEmptySet() || Right.isEmptySet()) return false;
  auto Never = ConstantRange::OverflowResult::NeverOverflows;
  switch (Checked.getIntrinsicID()) {
  case Intrinsic::sadd_with_overflow: return Left.signedAddMayOverflow(Right) == Never;
  case Intrinsic::ssub_with_overflow: return Left.signedSubMayOverflow(Right) == Never;
  case Intrinsic::uadd_with_overflow: return Left.unsignedAddMayOverflow(Right) == Never;
  case Intrinsic::usub_with_overflow: return Left.unsignedSubMayOverflow(Right) == Never;
  case Intrinsic::umul_with_overflow: return Left.unsignedMulMayOverflow(Right) == Never;
  case Intrinsic::smul_with_overflow: {
    ConstantRange Wide = Left.signExtend(128).multiply(Right.signExtend(128));
    unsigned Width = Left.getBitWidth();
    return Wide.getSignedMin().sge(APInt::getSignedMinValue(Width).sext(128)) &&
           Wide.getSignedMax().sle(APInt::getSignedMaxValue(Width).sext(128));
  }
  default: return false;
  }
}

// The most back edges the loop takes: scalar evolution's constant bound,
// or its symbolic count (`count - 3`) bounded by the ranges its values
// have on the edge into the loop.
static std::optional<uint64_t> maxTrips(Loop &L, ScalarEvolution &Evolution, LazyValueInfo &Values,
                                        DominatorTree &Tree) {
  if (auto *Count = dyn_cast<SCEVConstant>(Evolution.getConstantMaxBackedgeTakenCount(&L));
      Count && Count->getAPInt().ult(MaxTrips))
    return Count->getAPInt().getZExtValue();
  const SCEV *Symbolic = Evolution.getSymbolicMaxBackedgeTakenCount(&L);
  if (isa<SCEVCouldNotCompute>(Symbolic) || !Symbolic->getType()->isIntegerTy()) return std::nullopt;
  ConstantRange Total(APInt(Symbolic->getType()->getIntegerBitWidth(), 0));
  SmallVector<const SCEV *, 4> Terms;
  if (auto *Sum = dyn_cast<SCEVAddExpr>(Symbolic))
    for (const SCEV *Operand : Sum->operands()) Terms.push_back(Operand);
  else Terms.push_back(Symbolic);
  for (const SCEV *Term : Terms) {
    ConstantRange Part = ConstantRange::getFull(Total.getBitWidth());
    if (auto *Constant = dyn_cast<SCEVConstant>(Term)) Part = ConstantRange(Constant->getAPInt());
    else if (auto *Unknown = dyn_cast<SCEVUnknown>(Term)) {
      Part = entryRange(Unknown->getValue(), L, Values, Tree);
    }
    Total = Total.add(Part);
  }
  if (Total.isFullSet() || Total.isWrappedSet() || Total.getUnsignedMax().uge(MaxTrips))
    return std::nullopt;
  return Total.getUnsignedMax().getZExtValue();
}

static unsigned foldLoop(Loop &L, LoopInfo &Loops, ScalarEvolution &Evolution, LazyValueInfo &Values,
                         DominatorTree &Tree) {
  if (!L.isInnermost() || !L.getLoopLatch() || !L.getLoopPredecessor()) return 0;
  std::optional<uint64_t> Trips = maxTrips(L, Evolution, Values, Tree);
  if (!Trips) return 0;
  SmallVector<WithOverflowInst *, 8> Checks;
  unsigned Size = 0;
  for (BasicBlock *Block : L.blocks())
    for (Instruction &I : *Block) {
      if (++Size > MaxInstructions) return 0;
      if (auto *Checked = dyn_cast<WithOverflowInst>(&I)) Checks.push_back(Checked);
    }
  SmallVector<ICmpInst *, 8> Compares;
  for (BasicBlock *Block : L.blocks())
    for (Instruction &I : *Block)
      if (auto *Compare = dyn_cast<ICmpInst>(&I); Compare && Compare->getOperand(0)->getType()->isIntegerTy())
        Compares.push_back(Compare);
  if (Checks.empty() && Compares.empty()) return 0;
  Interpreter Run(L, Values, Tree);
  Run.loopInfoPointer = &Loops;
  if (!Run.run(unsigned(*Trips))) return 0;
  unsigned Folded = 0;
  for (WithOverflowInst *Checked : Checks) {
    auto Ranges = Run.operands(*Checked);
    if (!Ranges || !cannotOverflow(*Checked, Ranges->first, Ranges->second)) continue;
    for (User *U : make_early_inc_range(Checked->users()))
      if (auto *Part = dyn_cast<ExtractValueInst>(U); Part && Part->getIndices()[0] == 1) {
        Part->replaceAllUsesWith(ConstantInt::getFalse(Part->getContext()));
        ++Folded;
      }
  }
  // Overflow tests lowered to compares (`current + 2^62 < 0` guards `2 * current`)
  // and any other compare the trip ranges decide.
  for (ICmpInst *Compare : Compares) {
    ConstantRange Left = Run.of(Compare->getOperand(0)), Right = Run.of(Compare->getOperand(1));
    if (Left.isEmptySet() || Right.isEmptySet()) continue;
    std::optional<bool> Decided;
    if (ConstantRange::makeSatisfyingICmpRegion(Compare->getPredicate(), Right).contains(Left)) Decided = true;
    else if (ConstantRange::makeSatisfyingICmpRegion(Compare->getInversePredicate(), Right).contains(Left))
      Decided = false;
    if (!Decided) continue;
    Compare->replaceAllUsesWith(ConstantInt::getBool(Compare->getContext(), *Decided));
    ++Folded;
  }
  return Folded;
}

static unsigned foldBoundedTripChecks(Module &Module) {
  PassBuilder Builder;
  FunctionAnalysisManager Analyses;
  Builder.registerFunctionAnalyses(Analyses);
  unsigned Folded = 0;
  for (Function &F : Module) {
    if (F.isDeclaration() || F.hasOptNone()) continue;
    auto &Loops = Analyses.getResult<LoopAnalysis>(F);
    if (Loops.empty()) continue;
    auto &Evolution = Analyses.getResult<ScalarEvolutionAnalysis>(F);
    auto &Values = Analyses.getResult<LazyValueAnalysis>(F);
    auto &Tree = Analyses.getResult<DominatorTreeAnalysis>(F);
    unsigned Here = 0;
    for (Loop *L : Loops.getLoopsInPreorder())
      Here += foldLoop(*L, Loops, Evolution, Values, Tree);
    if (Here) Analyses.invalidate(F, PreservedAnalyses::none());
    Folded += Here;
  }
  return Folded;
}
} // namespace ElisaBoundedTripRanges
