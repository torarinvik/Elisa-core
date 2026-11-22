// Fold validation spread across consecutive blocks with the same rejection.
using namespace llvm;

namespace {
ICmpInst *matchNegativeTest(Value *Condition) {
  auto *Compare = dyn_cast<ICmpInst>(Condition);
  if (!Compare || Compare->getPredicate() != ICmpInst::ICMP_SLT)
    return nullptr;
  auto *Zero = dyn_cast<ConstantInt>(Compare->getOperand(1));
  return Zero && Zero->isZero() ? Compare : nullptr;
}

bool safeGuardBlock(BasicBlock &Block, BasicBlock *Predecessor) {
  if (Block.getSinglePredecessor() != Predecessor)
    return false;
  return llvm::all_of(Block, [](Instruction &Instruction) {
    return Instruction.isTerminator() ||
           isSafeToSpeculativelyExecute(&Instruction);
  });
}

SelectInst *matchRejectSelect(BranchInst &Branch, BasicBlock *Reject) {
  if (!Branch.isConditional() || Branch.getSuccessor(0) != Reject ||
      Branch.getSuccessor(1) == Reject)
    return nullptr;
  auto *Select = dyn_cast<SelectInst>(Branch.getCondition());
  auto *True = Select ? dyn_cast<ConstantInt>(Select->getTrueValue()) : nullptr;
  return True && True->isOne() ? Select : nullptr;
}

bool foldConsecutiveSignedGuards(BasicBlock &Entry) {
  auto *First = dyn_cast<BranchInst>(Entry.getTerminator());
  if (!First || !First->isConditional() ||
      First->getSuccessor(0) == First->getSuccessor(1))
    return false;
  auto *NegativeLimit = matchNegativeTest(First->getCondition());
  if (!NegativeLimit) return false;
  Value *Limit = NegativeLimit->getOperand(0);
  BasicBlock *Reject = First->getSuccessor(0);
  BasicBlock *Middle = First->getSuccessor(1);
  if (!safeGuardBlock(*Middle, &Entry)) return false;
  auto *Second = dyn_cast<BranchInst>(Middle->getTerminator());
  auto *Bounds = Second ? matchRejectSelect(*Second, Reject) : nullptr;
  if (!Bounds) return false;
  auto *Upper = dyn_cast<ICmpInst>(Bounds->getCondition());
  auto *NegativeInput = matchNegativeTest(Bounds->getFalseValue());
  if (!Upper || !NegativeInput || Upper->getOperand(0) != Limit)
    return false;
  auto *Maximum = dyn_cast<ConstantInt>(Upper->getOperand(1));
  if (!Maximum || Maximum->isNegative() ||
      (Upper->getPredicate() != ICmpInst::ICMP_SGT &&
       (Upper->getPredicate() != ICmpInst::ICMP_UGT || !Upper->hasSameSign())))
    return false;

  BasicBlock *Last = Second->getSuccessor(1);
  if (!safeGuardBlock(*Last, Middle)) return false;
  auto *Third = dyn_cast<BranchInst>(Last->getTerminator());
  auto *FinalCheck = Third ? matchRejectSelect(*Third, Reject) : nullptr;
  auto *Order = FinalCheck ? dyn_cast<ICmpInst>(FinalCheck->getCondition()) : nullptr;
  Value *Input = NegativeInput->getOperand(0);
  if (!Order || Order->getOperand(0) != Input || Order->getOperand(1) != Limit ||
      (Order->getPredicate() != ICmpInst::ICMP_SGT &&
       (Order->getPredicate() != ICmpInst::ICMP_UGT || !Order->hasSameSign())))
    return false;

  for (PHINode &Phi : Reject->phis()) {
    Value *Rejected = Phi.getIncomingValueForBlock(&Entry);
    if (Rejected != Phi.getIncomingValueForBlock(Middle) ||
        Rejected != Phi.getIncomingValueForBlock(Last))
      return false;
  }

  IRBuilder<> MiddleBuilder(Second);
  Second->setCondition(MiddleBuilder.CreateICmpUGT(Limit, Maximum));
  IRBuilder<> LastBuilder(FinalCheck);
  FinalCheck->setCondition(LastBuilder.CreateICmpUGT(Input, Limit));
  Reject->removePredecessor(&Entry);
  BranchInst::Create(Middle, First->getIterator());
  First->eraseFromParent();
  RecursivelyDeleteTriviallyDeadInstructions(NegativeLimit);
  RecursivelyDeleteTriviallyDeadInstructions(Bounds);
  RecursivelyDeleteTriviallyDeadInstructions(Order);
  return true;
}
} // namespace

extern "C" unsigned elisacoreFoldSplitSignedGuards(LLVMModuleRef Reference) {
  Module &Module = *unwrap(Reference);
  if (!Module.getTargetTriple().isAArch64()) return 0;
  unsigned Changes = 0;
  for (Function &Function : Module)
    for (BasicBlock &Block : Function)
      Changes += foldConsecutiveSignedGuards(Block);
  return Changes;
}
