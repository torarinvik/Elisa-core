// Two unsigned remainder steps establish x > y > 0 on entry to the loop.
// Thereafter x - y is the remainder whenever it is smaller than y.
#include <llvm/Analysis/LazyValueInfo.h>
namespace {
static bool remainderZeroTest(Value *V, Value *Remainder) {
  auto *Compare = dyn_cast<ICmpInst>(V);
  auto *Zero = Compare ? dyn_cast<ConstantInt>(Compare->getOperand(1)) : nullptr;
  return Zero && Zero->isZero() && Compare->getPredicate() == ICmpInst::ICMP_EQ &&
         Compare->getOperand(0) == Remainder && Compare->hasOneUse();
}

static ICmpInst *remainderDivisorGuard(CondBrInst *Guard, Value *Divisor) {
  Value *Condition = Guard->getCondition();
  // A false short-circuit OR also proves the divisor is nonzero.
  if (auto *Select = dyn_cast<SelectInst>(Condition)) {
    auto *OnTrue = dyn_cast<ConstantInt>(Select->getTrueValue());
    if (!Select->hasOneUse() || !OnTrue || !OnTrue->isOne()) return nullptr;
    Condition = Select->getFalseValue();
  }
  return remainderZeroTest(Condition, Divisor) ? cast<ICmpInst>(Condition) : nullptr;
}

static bool splitRemainderRecurrence(Function &F) {
  BasicBlock &Entry = F.getEntryBlock();
  auto *Guard = dyn_cast<CondBrInst>(Entry.getTerminator());
  if (!Guard || Guard->hasMetadataOtherThanDebugLoc())
    return false;
  BasicBlock *Loop = Guard->getSuccessor(1);
  auto *Back = dyn_cast<CondBrInst>(Loop->getTerminator());
  if (!Back) return false;
  BasicBlock *Exit = Back->getSuccessor(0);
  if (Exit == Loop || Exit == &Entry || Loop == &Entry || Loop->size() != 5 ||
      Back->getSuccessor(1) != Loop || Back->hasMetadataOtherThanDebugLoc() ||
      Loop->hasAddressTaken() || Exit->hasAddressTaken())
    return false;

  auto *OldRem = dyn_cast<BinaryOperator>(&*Loop->getFirstNonPHIIt());
  if (!OldRem || OldRem->getOpcode() != Instruction::URem ||
      !OldRem->getType()->isIntegerTy(64) || OldRem->hasMetadataOtherThanDebugLoc() ||
      !remainderZeroTest(Back->getCondition(), OldRem) || !OldRem->hasNUses(2))
    return false;
  auto *X = dyn_cast<PHINode>(OldRem->getOperand(0));
  auto *Y = dyn_cast<PHINode>(OldRem->getOperand(1));
  if (!X || !Y || X == Y || X->getParent() != Loop || Y->getParent() != Loop ||
      !X->hasOneUse() || X->getNumIncomingValues() != 2 ||
      Y->getNumIncomingValues() != 2 || X->getIncomingValueForBlock(Loop) != Y ||
      Y->getIncomingValueForBlock(Loop) != OldRem)
    return false;
  Value *InitialX = X->getIncomingValueForBlock(&Entry);
  Value *InitialY = Y->getIncomingValueForBlock(&Entry);
  if (!InitialX || !InitialY) return false;
  auto *EntryTest = remainderDivisorGuard(Guard, InitialY);
  if (!EntryTest) return false;

  PHINode *Result;
  if (Guard->getSuccessor(0) == Exit) {
    auto *Return = dyn_cast<ReturnInst>(Exit->getTerminator());
    Result = dyn_cast<PHINode>(&Exit->front());
    if (Exit->size() != 2 || !Return || !Result || !Y->hasNUses(3) ||
        Return->getReturnValue() != Result || Result->getNumIncomingValues() != 2 ||
        Result->getIncomingValueForBlock(Loop) != Y ||
        Result->getIncomingValueForBlock(&Entry) != InitialX)
      return false;
  } else {
    // Preserve the loop result for the computation that follows an inlined loop.
    if (Exit->getSinglePredecessor() != Loop || isa<PHINode>(Exit->front()))
      return false;
    Result = PHINode::Create(Y->getType(), 3, "remainder.result", &Exit->front());
    Y->replaceUsesWithIf(Result, [Loop](Use &U) {
      return cast<Instruction>(U.getUser())->getParent() != Loop;
    });
    Result->addIncoming(Y, Loop);
  }

  LLVMContext &Context = F.getContext();
  BasicBlock *First = BasicBlock::Create(Context, "remainder.first", &F, Loop);
  BasicBlock *Second = BasicBlock::Create(Context, "remainder.second", &F, Loop);
  BasicBlock *Slow = BasicBlock::Create(Context, "remainder.divide", &F, Exit);
  BasicBlock *Latch = BasicBlock::Create(Context, "remainder.next", &F, Exit);
  IRBuilder<> Builder(EntryTest);
  // Use the same divisor in the zero check and in the initial division.
  Value *Divisor = Builder.CreateFreeze(InitialY);
  EntryTest->setOperand(0, Divisor);
  Guard->setSuccessor(1, First);

  Builder.SetInsertPoint(First);
  Value *FirstRem = Builder.CreateURem(Builder.CreateFreeze(InitialX), Divisor);
  Value *Zero = Builder.getInt64(0);
  Builder.CreateCondBr(Builder.CreateICmpEQ(FirstRem, Zero), Exit, Second);
  Builder.SetInsertPoint(Second);
  Value *SecondRem = Builder.CreateURem(Divisor, FirstRem);
  Builder.CreateCondBr(Builder.CreateICmpEQ(SecondRem, Zero), Exit, Loop);

  auto *OldTest = cast<Instruction>(Back->getCondition());
  Back->eraseFromParent();
  OldTest->eraseFromParent();
  Builder.SetInsertPoint(Loop);
  Value *Difference = Builder.CreateSub(X, Y);
  Builder.CreateCondBr(Builder.CreateICmpULT(Difference, Y), Latch, Slow);
  Builder.SetInsertPoint(Slow);
  Value *FullRem = Builder.CreateURem(X, Y);
  Builder.CreateBr(Latch);
  Builder.SetInsertPoint(Latch);
  auto *Remainder = Builder.CreatePHI(OldRem->getType(), 2);
  Remainder->addIncoming(Difference, Loop);
  Remainder->addIncoming(FullRem, Slow);
  Builder.CreateCondBr(Builder.CreateICmpEQ(Remainder, Zero), Exit, Loop);

  X->setIncomingValueForBlock(&Entry, FirstRem);
  Y->setIncomingValueForBlock(&Entry, SecondRem);
  Y->setIncomingValueForBlock(Loop, Remainder);
  for (auto *P : {X, Y}) {
    P->setIncomingBlock(P->getBasicBlockIndex(&Entry), Second);
    P->setIncomingBlock(P->getBasicBlockIndex(Loop), Latch);
  }
  Result->setIncomingBlock(Result->getBasicBlockIndex(Loop), Latch);
  Result->addIncoming(Divisor, First);
  Result->addIncoming(FirstRem, Second);
  OldRem->eraseFromParent();
  return true;
}

static uint64_t smallRemainderDividend(BinaryOperator &Rem, LazyValueInfo &LVI) {
  constexpr uint64_t MaxDividend = 65535;
  Value *N = Rem.getOperand(0);
  auto Range = LVI.getConstantRange(N, &Rem, false);
  if (Range.isEmptySet()) return 0;
  if (Range.getUnsignedMax().ule(MaxDividend))
    return Range.getUnsignedMax().getZExtValue();

  // The guard may bound the input to a multiply rather than its result.
  auto *Mul = dyn_cast<BinaryOperator>(N);
  auto *Factor = Mul && Mul->getOpcode() == Instruction::Mul
      ? dyn_cast<ConstantInt>(Mul->getOperand(1)) : nullptr;
  if (!Factor || Factor->isZero() || Factor->getValue().ugt(MaxDividend))
    return 0;
  uint64_t Scale = Factor->getZExtValue();
  Range = LVI.getConstantRange(Mul->getOperand(0), &Rem, false);
  if (Range.isEmptySet() || Range.getUnsignedMax().ugt(MaxDividend / Scale))
    return 0;
  return Range.getUnsignedMax().getZExtValue() * Scale;
}

static bool hoistSmallRemainder(BinaryOperator &Rem, LoopInfo &LI, LazyValueInfo &LVI) {
  Loop *L = LI.getLoopFor(Rem.getParent());
  if (!L || !L->getParentLoop() || !L->getSubLoops().empty()) return false;
  BasicBlock *Pre = L->getLoopPreheader();
  Value *D = Rem.getOperand(1);
  if (!Pre || !L->isLoopInvariant(D) || L->isLoopInvariant(Rem.getOperand(0)))
    return false;
  uint64_t Max = smallRemainderDividend(Rem, LVI);
  if (!Max) return false;
  unsigned Bits = APInt(64, Max).getActiveBits();
  uint64_t Limit = uint64_t{1} << Bits;
  uint64_t Unit = Limit * Limit;

  // Clamping zero makes the hoisted division safe even when the original
  // remainder would never execute.
  IRBuilder<> Setup(Pre->getTerminator());
  D = Setup.CreateFreeze(D);
  if (Rem.getOpcode() == Instruction::SRem)
    D = Setup.CreateIntrinsic(Intrinsic::abs, {D->getType()}, {D, Setup.getFalse()});
  D = Setup.CreateBinaryIntrinsic(Intrinsic::umax, D, Setup.getInt64(1));
  Value *Magic = Setup.CreateAdd(Setup.CreateUDiv(Setup.getInt64(Unit - 1), D),
                                 Setup.getInt64(1), "remainder.reciprocal");

  // Magic = ceil(Unit / D). For D < Limit, N*D < Unit makes the quotient
  // error strictly less than 1/D. Otherwise Magic <= Limit and the shifted
  // product is zero, matching the quotient for N < Limit <= D.
  IRBuilder<> B(&Rem);
  B.SetCurrentDebugLocation(Rem.getDebugLoc());
  Value *Narrow = B.CreateTrunc(Rem.getOperand(0), B.getIntNTy(Bits));
  Value *N = B.CreateZExt(B.CreateFreeze(Narrow), Rem.getType());
  Value *Q = B.CreateLShr(B.CreateMul(N, Magic), B.getInt64(2 * Bits));
  Value *Result = B.CreateSub(N, B.CreateMul(Q, D), "remainder.with.reciprocal");
  Rem.replaceAllUsesWith(Result);
  Rem.eraseFromParent();
  return true;
}
} // namespace

static unsigned elisacoreSplitRemainderRecurrences(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (BDU_SkipFunction(F, M)) continue;
    if ((F.size() == 3 || F.size() == 4) && splitRemainderRecurrence(F)) {
      ++Changed;
      continue;
    }
    if (F.size() < 5 || F.size() > 32 || F.getInstructionCount() > 384) continue;
    SmallVector<BinaryOperator *, 4> Candidates;
    for (Instruction &I : instructions(F))
      if (auto *Rem = dyn_cast<BinaryOperator>(&I);
          Rem && (Rem->getOpcode() == Instruction::SRem || Rem->getOpcode() == Instruction::URem) &&
          Rem->getType()->isIntegerTy(64) && !isa<Constant>(Rem->getOperand(1)) &&
          !Rem->hasMetadataOtherThanDebugLoc())
        Candidates.push_back(Rem);
    if (Candidates.empty() || Candidates.size() > 4) continue;
    DominatorTree DT(F);
    LoopInfo LI(DT);
    AssumptionCache AC(F);
    LazyValueInfo LVI(&F, &AC);
    for (auto *Rem : Candidates)
      if (hoistSmallRemainder(*Rem, LI, LVI)) {
        ++Changed;
        break;
      }
  }
  return Changed;
}
