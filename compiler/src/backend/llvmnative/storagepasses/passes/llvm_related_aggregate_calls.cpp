// Recover common work from adjacent calls which share aggregate arguments.
using namespace llvm;

namespace {
bool hasSharedArithmetic(Function &Helper, CallInst &First, CallInst &Second) {
  SmallPtrSet<Value *, 32> Shared;
  unsigned SharedAggregates = 0;
  unsigned Differences = 0;
  for (Argument &Argument : Helper.args()) {
    unsigned Index = Argument.getArgNo();
    Value *Left = First.getArgOperand(Index);
    Value *Right = Second.getArgOperand(Index);
    if (Left != Right) {
      ++Differences;
    } else if (Argument.getType()->isAggregateType()) {
      Shared.insert(&Argument);
      ++SharedAggregates;
    }
  }
  if (SharedAggregates < 2 || Differences != 1) return false;

  unsigned Arithmetic = 0;
  unsigned Products = 0;
  for (Instruction &Instruction : Helper.getEntryBlock()) {
    unsigned Opcode = Instruction.getOpcode();
    if (Opcode != Instruction::ExtractValue && Opcode != Instruction::Add &&
        Opcode != Instruction::Sub && Opcode != Instruction::Mul)
      continue;
    if (!llvm::all_of(Instruction.operands(), [&](Value *Operand) {
          return isa<Constant>(Operand) || Shared.contains(Operand);
        }))
      continue;
    Shared.insert(&Instruction);
    Arithmetic += Opcode != Instruction::ExtractValue;
    Products += Opcode == Instruction::Mul;
  }
  return Arithmetic >= 8 && Products >= 2;
}

void findCallPair(Function &Helper, FunctionAnalysisManager &Analyses,
                  SmallVectorImpl<CallInst *> &Pending) {
  if (!Helper.hasLocalLinkage() || !Helper.hasNUses(2) ||
      Helper.hasOptNone() || Helper.hasFnAttribute(Attribute::NoInline) ||
      Helper.getInstructionCount() > 128)
    return;

  SmallVector<CallInst *, 2> Calls;
  for (Use &Use : Helper.uses()) {
    auto *Call = dyn_cast<CallInst>(Use.getUser());
    if (!Call || &Use != &Call->getCalledOperandUse() ||
        Call->getFunctionType() != Helper.getFunctionType() ||
        Call->getFunction() == &Helper || Call->isMustTailCall() ||
        Call->isNoInline() || Call->isConvergent() || Call->hasOperandBundles())
      return;
    Calls.push_back(Call);
  }

  Function &Caller = *Calls[0]->getFunction();
  if (Calls[0]->getParent() != Calls[1]->getParent() || Caller.hasOptNone() ||
      Caller.hasOptSize() || Caller.hasMinSize() ||
      Caller.getInstructionCount() > 1200 ||
      !hasSharedArithmetic(Helper, *Calls[0], *Calls[1]) ||
      !isInlineViable(Helper).isSuccess())
    return;

  auto &Target = Analyses.getResult<TargetIRAnalysis>(Helper);
  auto GetLibrary = [&](Function &Function) -> const TargetLibraryInfo & {
    return Analyses.getResult<TargetLibraryAnalysis>(Function);
  };
  for (CallInst *Call : Calls)
    if (auto Decision = getAttributeBasedInliningDecision(
            *Call, &Helper, Target, GetLibrary);
        Decision && !Decision->isSuccess())
      return;
  if (!Calls[0]->comesBefore(Calls[1])) std::swap(Calls[0], Calls[1]);
  Pending.append(Calls.begin(), Calls.end());
}
} // namespace

extern "C" unsigned elisacoreInlineRelatedAggregateCalls(
    LLVMModuleRef Reference, LLVMTargetMachineRef Target) {
  Module &Module = *unwrap(Reference);
  if (!Module.getTargetTriple().isAArch64()) return 0;

  FunctionAnalysisManager Analyses;
  PassBuilder Builder(reinterpret_cast<TargetMachine *>(Target));
  Builder.registerFunctionAnalyses(Analyses);
  SmallVector<CallInst *, 8> Pending;
  for (Function &Function : Module) {
    if (Pending.size() >= 8) break;
    if (!Function.isDeclaration()) findCallPair(Function, Analyses, Pending);
  }

  SmallPtrSet<Function *, 4> Changed;
  for (CallInst *Call : Pending) {
    Function *Caller = Call->getFunction();
    InlineFunctionInfo Info;
    if (InlineFunction(*Call, Info).isSuccess()) Changed.insert(Caller);
  }
  if (Changed.empty()) return 0;

  auto Options = LLVMCreatePassBuilderOptions();
  for (Function &Function : Module) {
    if (!Changed.contains(&Function)) continue;
    auto Error = LLVMRunPassesOnFunction(wrap(&Function),
        "function(early-cse,gvn,instcombine<no-verify-fixpoint>,simplifycfg)",
        Target, Options);
    if (Error) {
      LLVMConsumeError(Error);
      LLVMDisposePassBuilderOptions(Options);
      return 2;
    }
  }
  LLVMDisposePassBuilderOptions(Options);
  return 1;
}
