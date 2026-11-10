// Assemble XOR from low bits while both operands are nonzero, then append
// the remainder of the only operand that can still have nonzero high bits.
namespace {
static bool shiftXorNonzero(Value *V, Value *Operand) {
  auto *Cmp = dyn_cast<ICmpInst>(V);
  return Cmp && Cmp->getPredicate() == ICmpInst::ICMP_NE &&
         Cmp->getOperand(0) == Operand && scalarLoopHasConstant(Cmp->getOperand(1), 0);
}

static bool shiftXorBothNonzero(Value *V, Value *Left, Value *Right) {
  Value *First = nullptr, *Second = nullptr;
  if (auto *Select = dyn_cast<SelectInst>(V);
      Select && scalarLoopHasConstant(Select->getFalseValue(), 0)) {
    First = Select->getCondition();
    Second = Select->getTrueValue();
  } else if (auto *And = dyn_cast<BinaryOperator>(V); And && And->getOpcode() == Instruction::And) {
    First = And->getOperand(0);
    Second = And->getOperand(1);
  }
  return First && ((shiftXorNonzero(First, Left) && shiftXorNonzero(Second, Right)) ||
                   (shiftXorNonzero(First, Right) && shiftXorNonzero(Second, Left)));
}

static PHINode *shiftXorTailPhi(Value *V, BasicBlock &Exit) {
  auto *P = dyn_cast<PHINode>(V);
  return P && P->getParent() == &Exit && P->getNumIncomingValues() == 2 &&
         P->hasOneUse() ? P : nullptr;
}

static PHINode *shiftXorInputPhi(PHINode &Tail, Value *Next, Value *Current,
                                BasicBlock &Loop, BasicBlock &Pre) {
  auto *P = dyn_cast<PHINode>(Current);
  return P && P->getParent() == &Loop && P->getNumIncomingValues() == 2 &&
         P->getIncomingValueForBlock(&Loop) == Next &&
         P->getIncomingValueForBlock(&Pre) == Tail.getIncomingValueForBlock(&Pre)
             ? P : nullptr;
}

static PHINode *shiftXorShiftPhi(PHINode &Tail, unsigned Opcode, BasicBlock &Loop, BasicBlock &Pre) {
  auto *Shift = scalarLoopBinOp(Tail.getIncomingValueForBlock(&Loop), Opcode, Loop);
  if (!Shift || !scalarLoopHasConstant(Shift->getOperand(1), 1)) return nullptr;
  return shiftXorInputPhi(Tail, Shift, Shift->getOperand(0), Loop, Pre);
}

static bool foldShiftXorLoop(BasicBlock &Loop) {
  auto *Back = dyn_cast<CondBrInst>(Loop.getTerminator());
  if (!Back || Back->getSuccessor(0) != &Loop || Back->hasMetadataOtherThanDebugLoc() ||
      Loop.hasAddressTaken()) return false;
  BasicBlock &Exit = *Back->getSuccessor(1);
  if (Exit.size() != 8 || &Exit == &Loop) return false;
  auto *First = dyn_cast<PHINode>(&Loop.front());
  if (!First || First->getNumIncomingValues() != 2) return false;
  BasicBlock &Pre = *First->getIncomingBlock(First->getIncomingBlock(0) == &Loop ? 1 : 0);
  auto *Guard = dyn_cast<CondBrInst>(Pre.getTerminator());
  if (&Pre == &Loop || &Pre == &Exit || !Guard || Guard->getSuccessor(0) != &Loop ||
      Guard->getSuccessor(1) != &Exit || Guard->hasMetadataOtherThanDebugLoc() ||
      !Guard->getCondition()->hasOneUse()) return false;

  auto *Sum = scalarLoopBinOp(Exit.getTerminator()->getPrevNode(), Instruction::Add, Exit);
  if (!Sum || !Sum->getType()->isIntegerTy() || Sum->getType()->isIntegerTy(1)) return false;
  auto *Saved = shiftXorTailPhi(Sum->getOperand(0), Exit);
  if (!Saved) Saved = shiftXorTailPhi(Sum->getOperand(1), Exit);
  auto *Scaled = Saved ? scalarLoopBinOp(Sum->getOperand(Sum->getOperand(0) == Saved ? 1 : 0),
                                         Instruction::Mul, Exit) : nullptr;
  if (!Scaled || !Scaled->hasOneUse()) return false;
  auto *Place = shiftXorTailPhi(Scaled->getOperand(0), Exit);
  if (!Place) Place = shiftXorTailPhi(Scaled->getOperand(1), Exit);
  auto *Tail = Place ? scalarLoopBinOp(Scaled->getOperand(Scaled->getOperand(0) == Place ? 1 : 0),
                                       Instruction::Add, Exit) : nullptr;
  if (!Tail || !Tail->hasOneUse()) return false;
  auto *LeftTail = shiftXorTailPhi(Tail->getOperand(0), Exit);
  auto *RightTail = shiftXorTailPhi(Tail->getOperand(1), Exit);
  if (!LeftTail || !RightTail ||
      !scalarLoopHasConstant(Saved->getIncomingValueForBlock(&Pre), 0) ||
      !scalarLoopHasConstant(Place->getIncomingValueForBlock(&Pre), 1)) return false;

  auto *Left = shiftXorShiftPhi(*LeftTail, Instruction::LShr, Loop, Pre);
  auto *Right = shiftXorShiftPhi(*RightTail, Instruction::LShr, Loop, Pre);
  auto *Bit = shiftXorShiftPhi(*Place, Instruction::Shl, Loop, Pre);
  if (!Left || !Right || Left == Right || !Bit) return false;
  auto *Accumulate = scalarLoopBinOp(Saved->getIncomingValueForBlock(&Loop), Instruction::Add, Loop);
  if (!Accumulate) return false;
  auto *Select = dyn_cast<SelectInst>(Accumulate->getOperand(0));
  if (!Select) Select = dyn_cast<SelectInst>(Accumulate->getOperand(1));
  auto *Acc = Select ? shiftXorInputPhi(*Saved, Accumulate,
      Accumulate->getOperand(Accumulate->getOperand(0) == Select ? 1 : 0), Loop, Pre) : nullptr;
  if (!Acc || Select->getParent() != &Loop || Select->getTrueValue() != Bit ||
      !scalarLoopHasConstant(Select->getFalseValue(), 0)) return false;
  auto *LowBit = dyn_cast<TruncInst>(Select->getCondition());
  auto *Combine = LowBit ? scalarLoopBinOp(LowBit->getOperand(0), Instruction::Add, Loop) : nullptr;
  if (!Combine || LowBit->getParent() != &Loop || !LowBit->getType()->isIntegerTy(1) ||
      !scalarLoopPair(*Combine, Left, Right)) return false;

  Value *InitialLeft = Left->getIncomingValueForBlock(&Pre);
  Value *InitialRight = Right->getIncomingValueForBlock(&Pre);
  if (!shiftXorBothNonzero(Guard->getCondition(), InitialLeft, InitialRight) ||
      !shiftXorBothNonzero(Back->getCondition(), Left->getIncomingValueForBlock(&Loop),
                          Right->getIncomingValueForBlock(&Loop))) return false;
  // The exact loop and tail shapes are pure. Decline any additional use of a
  // loop value or tail PHI, including an observation on another exit.
  for (Instruction &I : Loop) {
    for (User *U : I.users()) {
      auto *Use = dyn_cast<Instruction>(U);
      if (!Use || (Use->getParent() != &Loop && U != LeftTail && U != RightTail &&
                   U != Saved && U != Place)) return false;
    }
  }

  IRBuilder<> B(Sum);
  Value *X = B.CreateFreeze(InitialLeft), *Y = B.CreateFreeze(InitialRight);
  Sum->replaceAllUsesWith(B.CreateXor(X, Y, "shift.xor"));
  Sum->eraseFromParent();
  Scaled->eraseFromParent();
  Tail->eraseFromParent();
  B.SetInsertPoint(Guard);
  B.CreateBr(&Exit);
  Guard->eraseFromParent();
  DeleteDeadBlocks({&Loop});
  return true;
}
} // namespace

static unsigned elisacoreFoldShiftXorLoops(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.size() > 16 || BDU_SkipFunction(F, M)) continue;
    for (BasicBlock &B : make_early_inc_range(F))
      if (B.size() == 15 && foldShiftXorLoop(B)) ++Changed;
  }
  return Changed;
}
