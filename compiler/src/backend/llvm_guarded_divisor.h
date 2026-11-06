#include <llvm/IR/IRBuilder.h>

// Clone the suffix after a frozen, repeatedly used loop divisor. The dispatch
// runs after the original load and branches only on its frozen value; all
// earlier side effects execute exactly once. Only simple non-EH graphs and
// bounded functions are eligible. Unknown intrinsic and alias metadata cases
// are declined rather than speculated through.
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/IR/CFG.h>
#include <llvm/IR/Verifier.h>
#include <llvm/Transforms/Utils/BasicBlockUtils.h>
#include <llvm/Transforms/Utils/Cloning.h>
#include <llvm/Transforms/Utils/ValueMapper.h>
using namespace llvm;
static bool divisorSpecialize(Function &F) {
  if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
      F.hasPersonalityFn() || F.hasGC() || F.getSubprogram() ||
      F.hasFnAttribute(Attribute::Convergent) ||
      F.hasFnAttribute(Attribute::NoDuplicate) ||
      F.hasFnAttribute(Attribute::Naked) || F.getInstructionCount() > 3000 ||
      F.hasFnAttribute("elisa.divisor-versioned") ||
      F.hasFnAttribute(Attribute::SanitizeAddress) ||
      F.hasFnAttribute(Attribute::SanitizeHWAddress) ||
      F.hasFnAttribute(Attribute::SanitizeMemory) ||
      F.hasFnAttribute(Attribute::SanitizeThread))
    return false;
  DominatorTree DT(F);
  LoopInfo LI(DT);
  FreezeInst *V = nullptr;
  for (BasicBlock &B : F)
    for (Instruction &I : B) {
      if (B.hasAddressTaken() || B.isEHPad())
        return false;
      if (I.isTerminator() && !isa<BranchInst>(I) && !isa<SwitchInst>(I) &&
          !isa<ReturnInst>(I) && !isa<UnreachableInst>(I))
        return false;
      if (I.getMetadata(LLVMContext::MD_alias_scope) ||
          I.getMetadata(LLVMContext::MD_noalias) ||
          I.getMetadata(LLVMContext::MD_access_group))
        return false;
      if (auto *ID = I.getMetadata(LLVMContext::MD_loop)) {
        if (!ID->isDistinct() || ID->getNumOperands() < 1 ||
            ID->getOperand(0).get() != ID)
          return false;
        for (unsigned K = 1; K < ID->getNumOperands(); ++K) {
          auto *Property = dyn_cast_or_null<MDNode>(ID->getOperand(K).get());
          if (!Property || !Property->getNumOperands() ||
              !isa_and_nonnull<MDString>(Property->getOperand(0).get()))
            return false;
          for (unsigned J = 1; J < Property->getNumOperands(); ++J)
            if (!isa_and_nonnull<ConstantAsMetadata>(
                    Property->getOperand(J).get()) &&
                !isa_and_nonnull<MDString>(Property->getOperand(J).get()))
              return false;
        }
      }
      if (auto *CB = dyn_cast<CallBase>(&I)) {
        auto *II = dyn_cast<IntrinsicInst>(CB);
        if (!II || CB->isConvergent() || CB->cannotDuplicate() ||
            CB->hasOperandBundles() || isa<InvokeInst>(CB))
          return false;
        switch (II->getIntrinsicID()) {
        case Intrinsic::trap:
        case Intrinsic::assume:
        case Intrinsic::lifetime_start:
        case Intrinsic::lifetime_end:
        case Intrinsic::memcpy:
        case Intrinsic::memmove:
        case Intrinsic::memset:
        case Intrinsic::umax:
        case Intrinsic::vector_reduce_fadd:
          break;
        default:
          return false;
        }
      }
      if (auto *A = dyn_cast<AllocaInst>(&I))
        if (!A->isStaticAlloca())
          return false;
      if (auto *X = dyn_cast<FreezeInst>(&I)) {
        auto *L = dyn_cast<LoadInst>(X->getOperand(0));
        if (V || !L || !L->isSimple() || L->getParent() != &B ||
            !X->getType()->isIntegerTy(64) || LI.getLoopFor(&B))
          continue;
        unsigned Count = 0;
        for (User *U : X->users())
          if (auto *BO = dyn_cast<BinaryOperator>(U))
            if (BO->getOpcode() == Instruction::URem &&
                BO->getOperand(1) == X && LI.getLoopFor(BO->getParent()))
              ++Count;
        if (Count >= 2)
          V = X;
      }
    }
  if (!V || !V->getNextNode())
    return false;
  BasicBlock *Dispatch = V->getParent();
  SmallPtrSet<BasicBlock *, 32> Seen;
  SmallVector<BasicBlock *, 128> Work;
  for (auto *B : successors(Dispatch))
    Work.push_back(B);
  while (!Work.empty()) {
    auto *B = Work.pop_back_val();
    if (!Seen.insert(B).second)
      continue;
    if (B == Dispatch || DT.dominates(B, Dispatch) || Seen.size() > 200)
      return false;
    auto *T = B->getTerminator();
    if (!isa<BranchInst>(T) && !isa<SwitchInst>(T) && !isa<ReturnInst>(T) &&
        !isa<UnreachableInst>(T))
      return false;
    for (auto *S : successors(B)) {
      if (Work.size() >= 1024)
        return false;
      Work.push_back(S);
    }
  }
  BasicBlock *Start = SplitBlock(Dispatch, V->getNextNode());
  SmallVector<BasicBlock *, 128> Blocks;
  Blocks.push_back(Start);
  for (BasicBlock &B : F)
    if (Seen.contains(&B))
      Blocks.push_back(&B);
  ValueToValueMapTy Map;
  for (auto *B : Blocks) {
    auto *N = CloneBasicBlock(B, Map, ".divisor2", &F);
    Map[B] = N;
  }
  Map[V] = ConstantInt::get(V->getType(), 2);
  DenseMap<MDNode *, MDNode *> LoopIDs;
  for (auto *B : Blocks) {
    auto *N = cast<BasicBlock>(Map[B]);
    for (auto &I : *N) {
      if (auto *P = dyn_cast<PHINode>(&I))
        for (int J = P->getNumIncomingValues() - 1; J >= 0; --J) {
          auto *Pred = P->getIncomingBlock(J);
          if (!Seen.contains(Pred) && Pred != Start &&
              !(B == Start && Pred == Dispatch))
            P->removeIncomingValue(J, false);
        }
      RemapInstruction(&I, Map, RF_IgnoreMissingLocals);
      if (auto *ID = I.getMetadata(LLVMContext::MD_loop)) {
        auto It = LoopIDs.find(ID);
        MDNode *New;
        if (It == LoopIDs.end()) {
          SmallVector<Metadata *, 8> Ops;
          Ops.push_back(nullptr);
          for (unsigned J = 1; J < ID->getNumOperands(); ++J)
            Ops.push_back(ID->getOperand(J).get());
          New = MDNode::getDistinct(F.getContext(), Ops);
          New->replaceOperandWith(0, New);
          LoopIDs[ID] = New;
        } else
          New = It->second;
        I.setMetadata(LLVMContext::MD_loop, New);
      }
    }
  }
  Dispatch->getTerminator()->eraseFromParent();
  IRBuilder<> B(Dispatch);
  B.CreateCondBr(B.CreateICmpEQ(V, ConstantInt::get(V->getType(), 2)),
                 cast<BasicBlock>(Map[Start]), Start);
  F.addFnAttr("elisa.divisor-versioned");
  return true;
}
