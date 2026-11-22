// Prove a recursive index is in bounds by checking the initial calls and
// each recursive edge. Recursive calls must forward the same view and an
// index already checked against its length in the current invocation.
namespace ElisaRecursiveArgumentBounds {
struct BoundsCheck {
  CondBrInst *branch;
  Argument *index;
  ExtractValueInst *length;
};

static std::optional<BoundsCheck> matchBoundsCheck(BasicBlock &Block) {
  auto *Branch = dyn_cast<CondBrInst>(Block.getTerminator());
  auto *Compare = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
  if (!Compare || Compare->getPredicate() != ICmpInst::ICMP_ULT ||
      Branch->getSuccessor(0) == Branch->getSuccessor(1))
    return std::nullopt;
  auto *Index = dyn_cast<Argument>(Compare->getOperand(0));
  auto *Length = dyn_cast<ExtractValueInst>(Compare->getOperand(1));
  if (!Index || !Length || !Index->getType()->isIntegerTy(64) ||
      !isa<Argument>(Length->getAggregateOperand()))
    return std::nullopt;
  return BoundsCheck{Branch, Index, Length};
}

static bool isTrapBlock(BasicBlock &Block) {
  if (!isa<UnreachableInst>(Block.getTerminator())) return false;
  for (Instruction &Instruction : Block) {
    if (auto *Intrinsic = dyn_cast<IntrinsicInst>(&Instruction))
      if (Intrinsic->getIntrinsicID() == Intrinsic::trap) return true;
    if (!isa<DbgInfoIntrinsic>(Instruction)) return false;
  }
  return false;
}

static bool initialIndexIsBounded(CallBase &Call, BoundsCheck Check,
                                  Argument &View) {
  auto *Index = dyn_cast<ConstantInt>(Call.getArgOperand(Check.index->getArgNo()));
  Value *Length = FindInsertedValue(Call.getArgOperand(View.getArgNo()),
                                    Check.length->getIndices());
  if (!Index || !Length || Length->getType() != Index->getType() ||
      Call.getFunction()->getInstructionCount() > 1500)
    return false;
  if (auto *Constant = dyn_cast<ConstantInt>(Length))
    return Index->getValue().ult(Constant->getValue());

  DominatorTree Tree(*Call.getFunction());
  AssumptionCache Assumptions(*Call.getFunction());
  auto Bounds = ElisaDominatingIntegerBounds::dominatingBounds(
      Call, Tree, Assumptions, ElisaDominatingIntegerBounds::Facts());
  auto Known = Bounds.find(Length);
  return Known != Bounds.end() && !Known->second.isEmptySet() &&
         Index->getValue().ult(Known->second.getUnsignedMin());
}

static Argument *knownAtRecursiveCall(CallBase &Call, BoundsCheck Check,
                                      ArrayRef<BoundsCheck> Checks,
                                      DominatorTree &Tree) {
  auto *Forwarded = dyn_cast<Argument>(Call.getArgOperand(Check.index->getArgNo()));
  if (!Forwarded || Forwarded == Check.index) return nullptr;
  for (BoundsCheck Guard : Checks) {
    if (Guard.index != Forwarded ||
        Guard.length->getAggregateOperand() != Check.length->getAggregateOperand() ||
        Guard.length->getIndices() != Check.length->getIndices())
      continue;
    if (Tree.dominates(BasicBlockEdge(Guard.branch->getParent(),
                                     Guard.branch->getSuccessor(0)),
                       Call.getParent()))
      return Forwarded;
  }
  return nullptr;
}

static unsigned fold(Function &Function) {
  if (!Function.hasLocalLinkage() || Function.use_empty() ||
      Function.getNumUses() > 8 || Function.getInstructionCount() > 160 ||
      Function.hasOptNone())
    return 0;

  SmallVector<CallBase *, 4> Initial, Recursive;
  for (Use &Use : Function.uses()) {
    auto *Call = dyn_cast<CallBase>(Use.getUser());
    if (!Call || &Use != &Call->getCalledOperandUse() ||
        Call->getFunctionType() != Function.getFunctionType())
      return 0;
    (Call->getFunction() == &Function ? Recursive : Initial).push_back(Call);
  }
  if (Initial.empty() || Recursive.empty()) return 0;

  SmallVector<BoundsCheck, 8> Checks;
  for (BasicBlock &Block : Function)
    if (auto Check = matchBoundsCheck(Block)) Checks.push_back(*Check);
  if (Checks.size() < 2 || Checks.size() > 12) return 0;

  DominatorTree Tree(Function);
  for (BoundsCheck Candidate : Checks) {
    if (!isTrapBlock(*Candidate.branch->getSuccessor(1))) continue;
    auto &View = *cast<Argument>(Candidate.length->getAggregateOperand());
    if (!llvm::all_of(Initial, [&](CallBase *Call) {
          return initialIndexIsBounded(*Call, Candidate, View);
        }))
      continue;

    SmallPtrSet<Argument *, 4> Forwarded;
    if (!llvm::all_of(Recursive, [&](CallBase *Call) {
          if (Call->getArgOperand(View.getArgNo()) != &View) return false;
          auto *Index = knownAtRecursiveCall(*Call, Candidate, Checks, Tree);
          if (!Index) return false;
          Forwarded.insert(Index);
          return true;
        }))
      continue;

    // The entry tests and recursive calls must agree on the same index even
    // when the original formal argument could contain an undefined value.
    IRBuilder<> Builder(&*Function.getEntryBlock().getFirstInsertionPt());
    for (Argument &Argument : Function.args()) {
      if (!Forwarded.contains(&Argument) || Argument.hasAttribute(Attribute::NoUndef))
        continue;
      Value *Frozen = Builder.CreateFreeze(&Argument, "checked.index");
      Argument.replaceUsesWithIf(Frozen, [&](Use &Use) {
        return Use.getUser() != Frozen;
      });
    }

    SmallVector<CondBrInst *, 4> Redundant;
    for (BoundsCheck Check : Checks) {
      if (Check.index != Candidate.index ||
          Check.length->getAggregateOperand() != &View ||
          Check.length->getIndices() != Candidate.length->getIndices() ||
          !isTrapBlock(*Check.branch->getSuccessor(1)))
        continue;
      Redundant.push_back(Check.branch);
    }
    for (CondBrInst *Branch : Redundant) {
      Branch->setCondition(Builder.getTrue());
      ConstantFoldTerminator(Branch->getParent(), true);
    }
    return Redundant.size();
  }
  return 0;
}
} // namespace ElisaRecursiveArgumentBounds

extern "C" unsigned elisacoreFoldRecursiveArgumentBounds(
    LLVMModuleRef Reference, LLVMTargetMachineRef Target) {
  Module &Module = *unwrap(Reference);
  if (!Module.getTargetTriple().isAArch64()) return 0;

  SmallVector<Function *, 4> Changed;
  for (Function &Function : Module)
    if (!Function.isDeclaration() && ElisaRecursiveArgumentBounds::fold(Function))
      Changed.push_back(&Function);
  if (Changed.empty()) return 0;

  auto Options = LLVMCreatePassBuilderOptions();
  for (Function *Function : Changed) {
    auto Error = LLVMRunPassesOnFunction(wrap(Function),
        "function(instcombine<no-verify-fixpoint>,simplifycfg)", Target, Options);
    if (Error) {
      LLVMConsumeError(Error);
      LLVMDisposePassBuilderOptions(Options);
      return 2;
    }
  }
  LLVMDisposePassBuilderOptions(Options);
  return 1;
}
