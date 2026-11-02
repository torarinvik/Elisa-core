//go:build cgo
// Avoid the high-half signed-overflow calculation for multipliers zero and one.
// Keep the original intrinsic on every other multiplier. Restrict versioning to
// innermost loops, where the extra control flow can amortize its code-size cost.
#include <llvm-c/Core.h>
#include <llvm-c/Error.h>
#include <llvm-c/TargetMachine.h>
#include <llvm-c/Transforms/PassBuilder.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>
using namespace llvm;
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
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.getSubprogram() || F.hasFnAttribute("elisa.checkedmul.versioned") || F.hasPersonalityFn() || F.hasGC() ||
        F.hasFnAttribute(Attribute::Convergent) || F.getEntryCount().has_value() ||
        F.getInstructionCount() > 5000) continue;
    bool hasCheckedMul = false;
    for (BasicBlock &BB : F) for (Instruction &I : BB)
      if (auto *C = dyn_cast<IntrinsicInst>(&I))
        if (C->getIntrinsicID() == Intrinsic::smul_with_overflow &&
            C->getArgOperand(0)->getType()->isIntegerTy(64)) hasCheckedMul = true;
    // Most compiler functions have no signed checked multiply. Avoid building
    // dominance and loop analyses for them on every command-line rebuild.
    if (!hasCheckedMul) continue;
    SmallVector<IntrinsicInst *, 16> work;
    {
      // Snapshot candidates before changing the CFG; never reuse stale loop or
      // dominance information after the first split.
      DominatorTree DT(F);
      LoopInfo LI(DT);
      for (BasicBlock &BB : F) for (Instruction &I : BB)
        if (auto *C = dyn_cast<IntrinsicInst>(&I))
          if (checkedMulCandidate(*C, LI)) work.push_back(C);
    }
    if (work.size() > 32) continue;
    for (auto *C : work) { versionCheckedMul(*C); changed = true; }
    if (!work.empty()) F.addFnAttr("elisa.checkedmul.versioned");
  }
  if (!changed) return 0;
  auto options = LLVMCreatePassBuilderOptions();
  auto error = LLVMRunPasses(ref, "function(instcombine,simplifycfg)", target, options);
  LLVMDisposePassBuilderOptions(options);
  if (error) { LLVMConsumeError(error); return 2; }
  return 1;
}
