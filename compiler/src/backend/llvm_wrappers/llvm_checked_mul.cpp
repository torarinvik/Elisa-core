//go:build cgo
// Avoid the high-half signed-overflow calculation for multipliers zero and one.
// Keep the original intrinsic on every other multiplier. Restrict versioning to
// innermost loops, where the extra control flow can amortize its code-size cost.
#include <llvm-c/Core.h>
#include <llvm-c/Error.h>
#include <llvm-c/TargetMachine.h>
#include <llvm-c/Transforms/PassBuilder.h>
#include <llvm/Analysis/AssumptionCache.h>
#include <llvm/Analysis/LazyValueInfo.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/ConstantRange.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>
using namespace llvm;
#include "llvm_checked_initialization.cpp"
namespace {
static bool checkedMulCandidate(IntrinsicInst &C, LoopInfo &LI) {
  if (C.getIntrinsicID() != Intrinsic::smul_with_overflow ||
      !C.getArgOperand(0)->getType()->isIntegerTy(64) ||
      C.hasOperandBundles() || C.isConvergent() || C.cannotDuplicate() ||
      C.isUsedByMetadata() || C.hasMetadataOtherThanDebugLoc() ||
      isa<Constant>(C.getArgOperand(0)) || isa<Constant>(C.getArgOperand(1))) return false;
  Loop *L = LI.getLoopFor(C.getParent());
  if (!L || !L->getSubLoops().empty()) return false;
  auto *Guard = dyn_cast<BranchInst>(C.getParent()->getTerminator());
  auto *Flag = Guard && Guard->isConditional() ? dyn_cast<ExtractValueInst>(Guard->getCondition()) : nullptr;
  if (!Flag || Flag->getAggregateOperand() != &C || Flag->getNumIndices() != 1 ||
      *Flag->idx_begin() != 1 || !Flag->hasOneUse() ||
      Guard->getSuccessor(0) == Guard->getSuccessor(1) || Guard->hasMetadataOtherThanDebugLoc()) return false;
  for (User *U : C.users()) {
    auto *E = dyn_cast<ExtractValueInst>(U);
    if (!E || E->getNumIndices() != 1 || *E->idx_begin() > 1 ||
        (*E->idx_begin() == 1 && E != Flag) ||
        E->isUsedByMetadata() || E->hasMetadataOtherThanDebugLoc()) return false;
  }
  for (Instruction *I = C.getNextNode(); I != Guard; I = I->getNextNode()) {
    auto *E = dyn_cast<ExtractValueInst>(I);
    if (!E || E->getAggregateOperand() != &C) return false;
  }
  return true;
}

#if LLVM_VERSION_MAJOR >= 23
constexpr uint64_t CheckedSquareMaxRoot = 3037000499; // floor(sqrt(INT64_MAX))
static bool hasSafeSquareLimit(const ConstantRange &Range) {
  constexpr uint64_t MaxPrevious = CheckedSquareMaxRoot - 1;
  return !Range.isEmptySet() && Range.getSignedMax().sle(APInt(64, MaxPrevious * MaxPrevious));
}

static ConstantRange checkedSquareLimitRange(Value *Limit, ICmpInst &At,
                                            DominatorTree &DT, AssumptionCache &AC,
                                            LazyValueInfo &LVI) {
  auto Range = LVI.getConstantRangeAtUse(At.getOperandUse(1), false);
  if (hasSafeSquareLimit(Range) || !isGuaranteedNotToBeUndefOrPoison(Limit, &AC, &At, &DT))
    return Range;
  // LLVM can fold the source bounds into an OR of unsigned interval tests on
  // biased operands. Recover each bound on the edge where the OR is false.
  for (auto *Node = DT.getNode(At.getParent())->getIDom(); Node; Node = Node->getIDom()) {
    auto *Block = Node->getBlock();
    auto *Guard = dyn_cast<CondBrInst>(Block->getTerminator());
    auto *Or = Guard ? dyn_cast<BinaryOperator>(Guard->getCondition()) : nullptr;
    if (!Or || Or->getOpcode() != Instruction::Or ||
        !DT.dominates(BasicBlockEdge(Block, Guard->getSuccessor(1)), At.getParent()))
      continue;
    for (Value *Operand : Or->operands()) {
      auto *Cmp = dyn_cast<ICmpInst>(Operand);
      auto *K = Cmp ? dyn_cast<ConstantInt>(Cmp->getOperand(1)) : nullptr;
      auto *Add = K ? dyn_cast<BinaryOperator>(Cmp->getOperand(0)) : nullptr;
      if (!Add || Add->getOpcode() != Instruction::Add || Add->getOperand(0) != Limit)
        continue;
      auto *Offset = dyn_cast<ConstantInt>(Add->getOperand(1));
      if (Offset) {
        auto Biased = ConstantRange::makeExactICmpRegion(Cmp->getInversePredicate(), K->getValue());
        Range = Range.intersectWith(Biased.sub(ConstantRange(Offset->getValue())));
      }
    }
    if (hasSafeSquareLimit(Range)) break;
  }
  return Range;
}

// A nonnegative counter enters the next iteration only when its square is
// within a signed upper bound. Reserve room for the increment before proving
// the checked square and increment safe by induction.
static bool foldBoundedCheckedSquare(IntrinsicInst &Square, LoopInfo &LI,
                                     DominatorTree &DT) {
  auto *Counter = dyn_cast<PHINode>(Square.getArgOperand(0));
  Loop *L = LI.getLoopFor(Square.getParent());
  BasicBlock *Header = L->getHeader(), *Pre = L->getLoopPredecessor(), *Latch = L->getLoopLatch();
  if (!Counter || Square.getArgOperand(1) != Counter || Counter->getParent() != Header ||
      Square.getParent() != Header || !Pre || !Latch || Counter->getNumIncomingValues() != 2)
    return false;
  auto *Start = dyn_cast<ConstantInt>(Counter->getIncomingValueForBlock(Pre));
  if (!Start || Start->getValue().ugt(CheckedSquareMaxRoot)) return false;

  Value *Next = Counter->getIncomingValueForBlock(Latch);
  auto *NextValue = dyn_cast<ExtractValueInst>(Next);
  auto *Increment = NextValue ? dyn_cast<IntrinsicInst>(NextValue->getAggregateOperand()) : nullptr;
  if (!Increment || Increment->getIntrinsicID() != Intrinsic::sadd_with_overflow ||
      NextValue->getNumIndices() != 1 || *NextValue->idx_begin() != 0 ||
      Increment->getArgOperand(0) != Counter || Increment->hasOperandBundles() ||
      Increment->isConvergent() || Increment->isUsedByMetadata() ||
      Increment->hasMetadataOtherThanDebugLoc())
    return false;
  auto *One = dyn_cast<ConstantInt>(Increment->getArgOperand(1));
  if (!One || !One->isOne()) return false;

  BasicBlock *Test = cast<BranchInst>(Header->getTerminator())->getSuccessor(1);
  auto *Branch = dyn_cast<CondBrInst>(Test->getTerminator());
  auto *Compare = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  if (!Compare) return false;
  auto *Product = dyn_cast<ExtractValueInst>(Compare->getOperand(0));
  if (!Product || Product->getAggregateOperand() != &Square ||
      Product->getNumIndices() != 1 || *Product->idx_begin() != 0)
    return false;
  auto Predicate = Compare->getPredicate();
  if (Predicate != ICmpInst::ICMP_SGT && Predicate != ICmpInst::ICMP_SLE) return false;
  unsigned ContinueIndex = Predicate == ICmpInst::ICMP_SGT ? 1 : 0;
  BasicBlock *Continue = Branch->getSuccessor(ContinueIndex);
  if (!L->contains(Continue) || L->contains(Branch->getSuccessor(1 - ContinueIndex)) ||
      !L->contains(Increment->getParent()) ||
      !DT.dominates(BasicBlockEdge(Test, Continue), Increment->getParent()) ||
      !DT.dominates(BasicBlockEdge(Test, Continue), Latch))
    return false;
  Value *Limit = Compare->getOperand(1);
  if (!L->isLoopInvariant(Limit)) return false;
  AssumptionCache AC(*Square.getFunction());
  LazyValueInfo LVI(Square.getFunction(), &AC);
  auto Bound = checkedSquareLimitRange(Limit, *Compare, DT, AC, LVI);
  if (!hasSafeSquareLimit(Bound)) return false;

  for (IntrinsicInst *Checked : {&Square, Increment}) {
    IRBuilder<> B(Checked);
    Value *A = Checked->getArgOperand(0), *RHS = Checked->getArgOperand(1);
    Value *Result = Checked->getIntrinsicID() == Intrinsic::smul_with_overflow
                        ? B.CreateNSWMul(A, RHS)
                        : B.CreateNSWAdd(A, RHS);
    Value *Pair = B.CreateInsertValue(PoisonValue::get(Checked->getType()), Result, 0);
    Checked->replaceAllUsesWith(B.CreateInsertValue(Pair, B.getFalse(), 1));
    Checked->eraseFromParent();
  }
  return true;
}
#endif

static void versionCheckedMul(IntrinsicInst &C) {
  BasicBlock *Prefix = C.getParent();
  Function *F = Prefix->getParent();
  auto *Guard = cast<BranchInst>(Prefix->getTerminator());
  auto *Flag = cast<ExtractValueInst>(Guard->getCondition());
  BasicBlock *Overflow = Guard->getSuccessor(0), *Success = Guard->getSuccessor(1);
  IRBuilder<> B(&C);
  B.SetCurrentDebugLocation(C.getDebugLoc());
  // Freeze both the guard and the multiplication operands. Independent undef
  // choices could otherwise make the low result disagree with the checked flag.
  Value *A = B.CreateFreeze(C.getArgOperand(0), "checkedmul.a");
  Value *RHS = B.CreateFreeze(C.getArgOperand(1), "checkedmul.b");
  Value *Low = B.CreateMul(A, RHS, "checkedmul.low");
  Value *Cheap = B.CreateICmpULE(RHS, B.getInt64(1), "checkedmul.cheap");
  SmallVector<ExtractValueInst *, 8> values;
  for (User *U : C.users()) if (U != Flag) values.push_back(cast<ExtractValueInst>(U));
  for (auto *E : values) { E->replaceAllUsesWith(Low); E->eraseFromParent(); }
  BasicBlock *Slow = BasicBlock::Create(C.getContext(), "checkedmul.slow", F, Prefix->getNextNode());
  C.moveBefore(*Slow, Slow->end());
  Flag->moveBefore(*Slow, Slow->end());
  Guard->moveBefore(*Slow, Slow->end());
  C.setArgOperand(0, A);
  C.setArgOperand(1, RHS);
  C.setMetadata("elisa.checkedmul.slow", MDNode::get(C.getContext(), {}));
  // The successful edge now has two predecessors; the overflow edge moved
  // intact to Slow. Preserve all incoming SSA values on both edges.
  for (PHINode &P : Success->phis()) P.addIncoming(P.getIncomingValueForBlock(Prefix), Slow);
  for (PHINode &P : Overflow->phis()) P.replaceIncomingBlockWith(Prefix, Slow);
  B.SetInsertPoint(Prefix);
  B.CreateCondBr(Cheap, Success, Slow);
}
}
extern "C" unsigned elisacoreFastCheckedMul(LLVMModuleRef ref, LLVMTargetMachineRef target) {
  if (!ref) return 0;
  Module &M = *unwrap(ref);
  if (M.getTargetTriple().getArch() != Triple::aarch64 || M.getNamedMetadata("llvm.dbg.cu")) return 0;
  bool changed = false;
#if LLVM_VERSION_MAJOR >= 23
  unsigned InitializerBudget = 6000, InitializerTrials = 0;
#endif
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.getSubprogram() || F.hasFnAttribute("elisa.checkedmul.versioned") || F.hasPersonalityFn() || F.hasGC() ||
        F.hasFnAttribute(Attribute::Convergent) || F.getEntryCount().has_value() ||
        F.getInstructionCount() > 5000) continue;
#if LLVM_VERSION_MAJOR >= 23
    if (InitializerBudget && InitializerTrials < 8 && mayFoldCheckedInitializers(F)) {
      ++InitializerTrials;
      unsigned Folded = tryFoldCheckedInitializers(F, target, InitializerBudget);
      if (Folded == 2) return 2;
      changed |= Folded == 1;
    }
#endif
    bool hasCheckedMul = false;
    for (BasicBlock &BB : F) for (Instruction &I : BB)
      if (auto *C = dyn_cast<IntrinsicInst>(&I))
        if (C->getIntrinsicID() == Intrinsic::smul_with_overflow &&
            C->getArgOperand(0)->getType()->isIntegerTy(64)) hasCheckedMul = true;
    // Most compiler functions have no signed checked multiply. Avoid building
    // dominance and loop analyses for them on every command-line rebuild.
    if (!hasCheckedMul) continue;
    SmallVector<IntrinsicInst *, 16> work;
    DominatorTree DT(F);
    LoopInfo LI(DT);
    // Snapshot candidates before changing the CFG.
    for (BasicBlock &BB : F) for (Instruction &I : BB)
      if (auto *C = dyn_cast<IntrinsicInst>(&I))
        if (checkedMulCandidate(*C, LI)) work.push_back(C);
    if (work.size() > 32) continue;
    // These proofs change instructions only, leaving the CFG analyses valid.
#if LLVM_VERSION_MAJOR >= 23
    llvm::erase_if(work, [&](IntrinsicInst *C) {
      if (!foldBoundedCheckedSquare(*C, LI, DT)) return false;
      changed = true;
      return true;
    });
#endif
    for (auto *C : work) { versionCheckedMul(*C); changed = true; }
    if (!work.empty()) F.addFnAttr("elisa.checkedmul.versioned");
  }
  if (!changed) return 0;
  auto options = LLVMCreatePassBuilderOptions();
  auto error = LLVMRunPasses(ref, "function(instcombine<no-verify-fixpoint>,simplifycfg)", target, options);
  LLVMDisposePassBuilderOptions(options);
  if (error) { LLVMConsumeError(error); return 2; }
  return 1;
}
