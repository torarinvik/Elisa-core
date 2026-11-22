// A counted-prefix buffer may escape as a {data, length} view argument when
// the view starts at element zero, its length is at most the prefix count,
// and the private callee reaches the data only through accesses guarded by
// index < length. The callee can then neither read nor write the suffix.
#include <llvm/Transforms/Utils/Cloning.h>
namespace ElisaCountedPrefix {
static Value *stripFreeze(Value *Candidate) {
  while (auto *Frozen = dyn_cast<FreezeInst>(Candidate))
    Candidate = Frozen->getOperand(0);
  return Candidate;
}

static bool isViewField(Value *Candidate, Argument &View, unsigned Field) {
  auto *Extract = dyn_cast<ExtractValueInst>(stripFreeze(Candidate));
  return Extract && Extract->getAggregateOperand() == &View &&
         Extract->getNumIndices() == 1 && Extract->getIndices()[0] == Field;
}

// True when a dominating branch edge establishes Index < View.length.
static bool accessGuarded(Instruction &Access, Value *Index, Argument &View,
                          DominatorTree &Tree) {
  Index = stripFreeze(Index);
  for (BasicBlock &Block : *Access.getFunction()) {
    auto *Branch = dyn_cast<CondBrInst>(Block.getTerminator());
    auto *Compare = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
    if (!Compare || Branch->getSuccessor(0) == Branch->getSuccessor(1)) continue;
    for (unsigned Edge = 0; Edge < 2; ++Edge) {
      if (!Tree.dominates(BasicBlockEdge(&Block, Branch->getSuccessor(Edge)),
                          Access.getParent()))
        continue;
      auto Predicate = Edge == 0 ? Compare->getPredicate()
                                 : Compare->getInversePredicate();
      Value *Left = Compare->getOperand(0), *Right = Compare->getOperand(1);
      if (Predicate == ICmpInst::ICMP_UGT) {
        std::swap(Left, Right);
        Predicate = ICmpInst::ICMP_ULT;
      }
      if (Predicate == ICmpInst::ICMP_ULT && stripFreeze(Left) == Index &&
          isViewField(Right, View, 1))
        return true;
    }
  }
  return false;
}

static bool viewUsesGuarded(Function &Clone, unsigned Position,
                            Function &Original, unsigned ElementBytes) {
  Argument &View = *Clone.getArg(Position);
  const DataLayout &Layout = Clone.getParent()->getDataLayout();
  DominatorTree Tree(Clone);
  for (User *Use : View.users()) {
    if (auto *Call = dyn_cast<CallInst>(Use)) {
      Function *Callee = Call->getCalledFunction();
      if (Callee != &Original && Callee != &Clone) return false;
      for (unsigned Index = 0; Index < Call->arg_size(); ++Index)
        if ((Call->getArgOperand(Index) == &View) != (Index == Position))
          return false;
      continue;
    }
    auto *Field = dyn_cast<ExtractValueInst>(Use);
    if (!Field || Field->getNumIndices() != 1 || Field->getIndices()[0] > 1)
      return false;
    if (Field->getIndices()[0] == 1) continue;
    for (User *DataUse : Field->users()) {
      auto *Address = dyn_cast<GetElementPtrInst>(DataUse);
      if (!Address || Address->getPointerOperand() != Field ||
          Address->getNumIndices() != 1 ||
          Layout.getTypeAllocSize(Address->getSourceElementType()) != ElementBytes)
        return false;
      for (User *AccessUse : Address->users()) {
        auto *Access = dyn_cast<Instruction>(AccessUse);
        auto *Read = dyn_cast<LoadInst>(AccessUse);
        auto *Write = dyn_cast<StoreInst>(AccessUse);
        Type *Accessed = Read ? Read->getType()
                       : Write ? Write->getValueOperand()->getType() : nullptr;
        if (!Accessed || getLoadStorePointerOperand(Access) != Address ||
            Layout.getTypeStoreSize(Accessed) > ElementBytes ||
            !accessGuarded(*Access, Address->idx_begin()->get(), View, Tree))
          return false;
      }
    }
  }
  return true;
}

// Prove the property on a scalarized clone, since the callee may still keep
// its view parameter in a local slot at this point of the pipeline.
static bool calleeGuardsView(Function &Callee, unsigned Position,
                             unsigned ElementBytes,
                             FunctionAnalysisManager &Analyses,
                             PassBuilder &Builder) {
  if (!Callee.hasLocalLinkage() || Callee.isDeclaration() || Callee.isVarArg() ||
      Position >= Callee.arg_size() || Callee.getInstructionCount() > 2000)
    return false;
  FunctionPassManager Simplify;
  if (Error Failure = Builder.parsePassPipeline(Simplify, "sroa,early-cse")) {
    consumeError(std::move(Failure));
    return false;
  }
  ValueToValueMapTy Map;
  Function *Clone = CloneFunction(&Callee, Map);
  Simplify.run(*Clone, Analyses);
  bool Guarded = viewUsesGuarded(*Clone, Position, Callee, ElementBytes);
  Analyses.clear(*Clone, Clone->getName());
  Clone->eraseFromParent();
  return Guarded;
}

// Read `select` or Stage0's branchless form B ^ ((A ^ B) & -zext(C)), which
// is A when C holds and B otherwise.
static bool asSelect(Value *Candidate, ICmpInst *&Condition, Value *&True,
                     Value *&False) {
  if (auto *Select = dyn_cast<SelectInst>(Candidate)) {
    Condition = dyn_cast<ICmpInst>(Select->getCondition());
    True = Select->getTrueValue();
    False = Select->getFalseValue();
    return Condition;
  }
  auto *Outer = dyn_cast<BinaryOperator>(Candidate);
  if (!Outer || Outer->getOpcode() != Instruction::Xor) return false;
  for (unsigned Side = 0; Side < 2; ++Side) {
    Value *Base = Outer->getOperand(Side);
    auto *Masked = dyn_cast<BinaryOperator>(Outer->getOperand(1 - Side));
    if (!Masked || Masked->getOpcode() != Instruction::And) continue;
    for (unsigned Part = 0; Part < 2; ++Part) {
      auto *Difference = dyn_cast<BinaryOperator>(Masked->getOperand(Part));
      auto *Negated = dyn_cast<BinaryOperator>(Masked->getOperand(1 - Part));
      auto *Zero = Negated ? dyn_cast<ConstantInt>(Negated->getOperand(0)) : nullptr;
      auto *Extend = Negated ? dyn_cast<ZExtInst>(Negated->getOperand(1)) : nullptr;
      if (!Difference || Difference->getOpcode() != Instruction::Xor ||
          Negated->getOpcode() != Instruction::Sub || !Zero || !Zero->isZero() ||
          !Extend || !Extend->getOperand(0)->getType()->isIntegerTy(1))
        continue;
      Value *Other = Difference->getOperand(0) == Base ? Difference->getOperand(1)
                   : Difference->getOperand(1) == Base ? Difference->getOperand(0)
                   : nullptr;
      Condition = dyn_cast<ICmpInst>(Extend->getOperand(0));
      if (!Other || !Condition) continue;
      True = Other;
      False = Base;
      return true;
    }
  }
  return false;
}

static bool isZero(Value *Candidate) {
  auto *Constant = dyn_cast<ConstantInt>(Candidate);
  return Constant && Constant->isZero();
}

// Zero, or a clamp of zero whose condition `0 <= x` always holds.
static bool isZeroStart(Value *Start) {
  if (isZero(Start)) return true;
  ICmpInst *Condition = nullptr;
  Value *True = nullptr, *False = nullptr;
  if (!asSelect(Start, Condition, True, False) || !isZero(True)) return false;
  auto Predicate = Condition->getPredicate();
  return (Predicate == ICmpInst::ICMP_ULE && isZero(Condition->getOperand(0))) ||
         (Predicate == ICmpInst::ICMP_UGE && isZero(Condition->getOperand(1)));
}

// Length is the prefix count, or a value no larger than it: a subtraction of
// a zero start, a select that is always its operand, or an unsigned minimum.
static bool lengthWithin(Value *Length, Value *Bound) {
  for (unsigned Depth = 0; Depth < 6; ++Depth) {
    if (Length == Bound) return true;
    if (auto *Difference = dyn_cast<BinaryOperator>(Length);
        Difference && Difference->getOpcode() == Instruction::Sub) {
      if (!isZeroStart(Difference->getOperand(1))) return false;
      Length = Difference->getOperand(0);
      continue;
    }
    ICmpInst *Compare = nullptr;
    Value *True = nullptr, *False = nullptr;
    if (!asSelect(Length, Compare, True, False)) return false;
    Value *Left = Compare->getOperand(0), *Right = Compare->getOperand(1);
    auto Predicate = Compare->getPredicate();
    if (Predicate == ICmpInst::ICMP_UGE && Left == True && isZero(Right) &&
        isZero(False))
      Length = True;
    else if (Predicate == ICmpInst::ICMP_UGT && Left == False && Right == True)
      Length = False;
    else if ((Predicate == ICmpInst::ICMP_ULT || Predicate == ICmpInst::ICMP_ULE) &&
             Left == True && Right == False)
      Length = True;
    else
      return false;
  }
  return false;
}

// Accept `insertvalue (insertvalue undef, data, 0), length, 1` passed only as
// view arguments to guarded callees after the prefix is complete.
static bool viewEscapeCovered(InsertValueInst &Data, Value *Bound,
                              BasicBlockEdge &Completion, unsigned ElementBytes,
                              DominatorTree &Tree,
                              FunctionAnalysisManager &Analyses,
                              PassBuilder &Builder) {
  auto *Aggregate = dyn_cast<StructType>(Data.getType());
  if (!Aggregate || Aggregate->getNumElements() != 2 ||
      Data.getNumIndices() != 1 || Data.getIndices()[0] != 0 ||
      !isa<UndefValue>(Data.getAggregateOperand()) || !Data.hasOneUse())
    return false;
  auto *View = dyn_cast<InsertValueInst>(*Data.user_begin());
  if (!View || View->getAggregateOperand() != &Data || View->getNumIndices() != 1 ||
      View->getIndices()[0] != 1 || !lengthWithin(View->getInsertedValueOperand(), Bound))
    return false;
  for (User *Use : View->users()) {
    auto *Call = dyn_cast<CallInst>(Use);
    Function *Callee = Call ? Call->getCalledFunction() : nullptr;
    if (!Callee || !Tree.dominates(Completion, Call->getParent())) return false;
    for (unsigned Position = 0; Position < Call->arg_size(); ++Position)
      if (Call->getArgOperand(Position) == View &&
          !calleeGuardsView(*Callee, Position, ElementBytes, Analyses, Builder))
        return false;
  }
  return true;
}
} // namespace ElisaCountedPrefix
