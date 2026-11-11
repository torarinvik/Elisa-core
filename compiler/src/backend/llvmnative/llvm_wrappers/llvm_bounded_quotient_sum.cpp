// Version a bounded unsigned quotient sum into independent constant divisions.
namespace {
static bool versionBoundedQuotientSum(BasicBlock &Body) {
  if (Body.size() != 6 || Body.hasAddressTaken()) return false;
  auto *Latch = dyn_cast<CondBrInst>(Body.getTerminator());
  auto *Done = Latch ? dyn_cast<ICmpInst>(Latch->getCondition()) : nullptr;
  if (!Done || Done->getParent() != &Body || Done->getPredicate() != ICmpInst::ICMP_ULT ||
      Latch->getSuccessor(1) != &Body || Latch->getSuccessor(0) == &Body)
    return false;
  auto *Word = dyn_cast<PHINode>(Done->getOperand(0));
  auto *Base = dyn_cast<ConstantInt>(Done->getOperand(1));
  if (!Word || Word->getParent() != &Body || !Word->getType()->isIntegerTy(64) ||
      !Base || Base->getValue().ult(3) || Base->getValue().ugt(16) ||
      Word->getNumIncomingValues() != 2 || Word->getBasicBlockIndex(&Body) < 0)
    return false;
  auto *Div = dyn_cast<BinaryOperator>(Word->getIncomingValueForBlock(&Body));
  if (!Div || Div->getParent() != &Body || Div->getOpcode() != Instruction::UDiv ||
      Div->isExact() || Div->getOperand(0) != Word || Div->getOperand(1) != Base)
    return false;
  auto *Sum = dyn_cast<PHINode>(&Body.front());
  if (Sum == Word) Sum = dyn_cast<PHINode>(Word->getNextNode());
  if (!Sum || Sum->getNumIncomingValues() != 2 || Sum->getBasicBlockIndex(&Body) < 0)
    return false;
  auto *Add = dyn_cast<BinaryOperator>(Sum->getIncomingValueForBlock(&Body));
  if (!Add || Add->getParent() != &Body || Add->getOpcode() != Instruction::Add ||
      !((Add->getOperand(0) == Sum && Add->getOperand(1) == Div) ||
        (Add->getOperand(1) == Sum && Add->getOperand(0) == Div)))
    return false;

  BasicBlock *Exit = Latch->getSuccessor(0);
  auto *Result = dyn_cast<PHINode>(&Exit->front());
  if (!Result || !isa<ReturnInst>(Result->getNextNode()) ||
      cast<ReturnInst>(Result->getNextNode())->getReturnValue() != Result ||
      Result->getBasicBlockIndex(&Body) < 0 ||
      Result->getIncomingValueForBlock(&Body) != Add)
    return false;
  for (Instruction &I : Body)
    for (User *U : I.users()) {
      auto *Use = dyn_cast<Instruction>(U);
      if (!Use || (Use != Result && Use->getParent() != &Body)) return false;
    }

  BasicBlock *Pre = Word->getIncomingBlock(Word->getIncomingBlock(0) == &Body ? 1 : 0);
  if (Sum->getBasicBlockIndex(Pre) < 0 || !Pre->phis().empty()) return false;
  auto *Zero = dyn_cast<ConstantInt>(Sum->getIncomingValueForBlock(Pre));
  if (!Zero || !Zero->isZero()) return false;
  Value *Input = Word->getIncomingValueForBlock(Pre);
  auto *Enter = dyn_cast<CondBrInst>(Pre->getTerminator());
  auto *IsZero = Enter ? dyn_cast<ICmpInst>(Enter->getCondition()) : nullptr;
  auto *PreZero = IsZero ? dyn_cast<ConstantInt>(IsZero->getOperand(1)) : nullptr;
  auto *ZeroResult = Result->getBasicBlockIndex(Pre) >= 0
      ? dyn_cast<ConstantInt>(Result->getIncomingValueForBlock(Pre)) : nullptr;
  if (Pre->size() != 2 || !PreZero || IsZero->getParent() != Pre || !PreZero->isZero() ||
      !ZeroResult || !ZeroResult->isZero() || IsZero->getOperand(0) != Input ||
      IsZero->getPredicate() != ICmpInst::ICMP_EQ ||
      Enter->getSuccessor(0) != Exit || Enter->getSuccessor(1) != &Body)
    return false;
  BasicBlock *Guard = Pre->getSinglePredecessor();
  auto *BoundBranch = Guard ? dyn_cast<CondBrInst>(Guard->getTerminator()) : nullptr;
  auto *Compare = BoundBranch ? dyn_cast<ICmpInst>(BoundBranch->getCondition()) : nullptr;
  auto *Bound = Compare ? dyn_cast<ConstantInt>(Compare->getOperand(1)) : nullptr;
  if (!Bound || Compare->getOperand(0) != Input ||
      Compare->getPredicate() != ICmpInst::ICMP_UGT ||
      BoundBranch->getSuccessor(0) == Pre || BoundBranch->getSuccessor(1) != Pre ||
      Bound->getValue().ugt(0x7fffffff))
    return false;

  SmallVector<uint32_t, 12> Powers;
  uint64_t Radix = Base->getZExtValue();
  for (uint64_t Power = Radix; Power <= Bound->getZExtValue(); Power *= Radix) {
    if (Powers.size() == 12) return false;
    Powers.push_back(Power);
  }
  if (Powers.size() < 7) return false;

  LLVMContext &C = Body.getContext();
  Function *F = Body.getParent();
  auto *Dispatch = BasicBlock::Create(C, "quotient.sum.dispatch", F, Pre);
  auto *Fast = BasicBlock::Create(C, "quotient.sum.parallel", F, Pre);
  BoundBranch->setSuccessor(1, Dispatch);
  IRBuilder<> Check(Dispatch);
  Check.CreateCondBr(Check.CreateICmpUGE(Input, Check.getInt64(Powers[5])), Fast, Pre);
  IRBuilder<> B(Fast);
  Value *N = B.CreateTrunc(Input, B.getInt32Ty());
  SmallVector<Value *, 12> Terms;
  for (uint32_t Power : Powers)
    Terms.push_back(B.CreateUDiv(N, B.getInt32(Power)));
  // Pair independent terms so the sum does not introduce a serial chain.
  for (unsigned Stride = 1; Stride < Terms.size(); Stride *= 2)
    for (unsigned I = 0; I + Stride < Terms.size(); I += 2 * Stride)
      Terms[I] = B.CreateAdd(Terms[I], Terms[I + Stride]);
  Result->addIncoming(B.CreateZExt(Terms.front(), B.getInt64Ty()), Fast);
  B.CreateBr(Exit);
  return true;
}
} // namespace

static unsigned elisacoreVersionBoundedQuotientSums(Module &M) {
  if (!M.getTargetTriple().isAArch64() || M.size() > 512) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.size() < 4 || F.size() > 32 || BDU_SkipFunction(F, M)) continue;
    for (BasicBlock &Body : F)
      if (versionBoundedQuotientSum(Body)) {
        ++Changed;
        break;
      }
  }
  return Changed;
}
