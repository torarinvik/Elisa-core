// Consolidate wide OR accumulators from a vectorized single-block loop.
// AArch64 splits these vectors across several registers per accumulator.
namespace ElisaVectorOrAccumulators {
struct Accumulator {
  BinaryOperator *update;
  PHINode *phi;
  unsigned phiOperand;
};

static bool collect(Value *ValueToReduce, BasicBlock &Exit, BasicBlock &Loop,
                    BasicBlock &Preheader,
                    SmallVectorImpl<Accumulator> &Accumulators) {
  auto *Or = dyn_cast<BinaryOperator>(ValueToReduce);
  if (!Or || Or->getOpcode() != Instruction::Or ||
      cast<PossiblyDisjointInst>(Or)->isDisjoint() ||
      Accumulators.size() == 4)
    return false;
  if (Or->getParent() == &Exit)
    return Or->hasOneUse() &&
           collect(Or->getOperand(0), Exit, Loop, Preheader, Accumulators) &&
           collect(Or->getOperand(1), Exit, Loop, Preheader, Accumulators);

  if (Or->getParent() != &Loop || !Or->hasNUses(2)) return false;
  for (unsigned Operand = 0; Operand < 2; ++Operand) {
    auto *Phi = dyn_cast<PHINode>(Or->getOperand(Operand));
    if (!Phi || Phi->getParent() != &Loop || !Phi->hasOneUse() ||
        Phi->getNumIncomingValues() != 2 ||
        Phi->getIncomingValueForBlock(&Loop) != Or)
      continue;
    auto *Initial = dyn_cast<Constant>(Phi->getIncomingValueForBlock(&Preheader));
    if (!Initial || !Initial->isNullValue()) return false;
    Accumulators.push_back({Or, Phi, Operand});
    return true;
  }
  return false;
}

static void fold(IntrinsicInst &Reduction) {
  if (Reduction.getIntrinsicID() != Intrinsic::vector_reduce_or) return;
  Value *Root = Reduction.getArgOperand(0);
  auto *Vector = dyn_cast<FixedVectorType>(Root->getType());
  if (!Vector || Vector->getPrimitiveSizeInBits() < 512) return;

  BasicBlock &Exit = *Reduction.getParent();
  BasicBlock *Loop = Exit.getSinglePredecessor();
  if (!Loop || Loop == &Exit) return;
  auto *Branch = dyn_cast<CondBrInst>(Loop->getTerminator());
  if (!Branch || Branch->getSuccessor(0) == Branch->getSuccessor(1) ||
      (Branch->getSuccessor(0) != Loop && Branch->getSuccessor(1) != Loop))
    return;
  BasicBlock *Preheader = nullptr;
  for (BasicBlock *Pred : predecessors(Loop)) {
    if (Pred == Loop) continue;
    if (Preheader) return;
    Preheader = Pred;
  }
  if (!Preheader) return;

  SmallVector<Accumulator, 4> Accumulators;
  if (!collect(Root, Exit, *Loop, *Preheader, Accumulators) ||
      Accumulators.size() != 4)
    return;
  llvm::sort(Accumulators, [](const Accumulator &Left, const Accumulator &Right) {
    return Left.update->comesBefore(Right.update);
  });

  // Keeping the updates in program order ensures every replacement dominates
  // its use. Each original recurrence contributes once to the final OR.
  Value *Combined = Accumulators.front().phi;
  for (const auto &Accumulator : Accumulators) {
    Accumulator.update->setOperand(Accumulator.phiOperand, Combined);
    Combined = Accumulator.update;
  }
  Accumulators.front().phi->setIncomingValueForBlock(Loop, Combined);
  Reduction.setArgOperand(0, Combined);
  RecursivelyDeleteTriviallyDeadInstructions(Root);
  for (const auto &Accumulator : ArrayRef(Accumulators).drop_front())
    Accumulator.phi->eraseFromParent();
}
} // namespace ElisaVectorOrAccumulators

extern "C" void elisacoreFoldWideVectorOrAccumulators(LLVMModuleRef Reference) {
  Module &Module = *unwrap(Reference);
  if (!Module.getTargetTriple().isAArch64()) return;
  for (Function &Function : Module)
    for (BasicBlock &Block : Function)
      for (Instruction &Instruction : Block)
        if (auto *Intrinsic = dyn_cast<IntrinsicInst>(&Instruction))
          ElisaVectorOrAccumulators::fold(*Intrinsic);
}
