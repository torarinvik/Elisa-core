// Preserve constant-divisor lowering when a shared helper survives inlining.
#include <llvm/ADT/STLExtras.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/InstIterator.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/Transforms/Utils/Cloning.h>
#include <llvm-c/Error.h>
#include <llvm-c/Transforms/PassBuilder.h>

namespace {
static Argument *literalDivisorArgument(Function &F) {
  unsigned Counts[8] = {};
  for (BasicBlock &Block : F)
    if (Block.hasAddressTaken() || Block.isEHPad()) return nullptr;
  for (Instruction &I : instructions(F)) {
    if (auto *Call = dyn_cast<CallBase>(&I)) {
      auto *IntrinsicCall = dyn_cast<IntrinsicInst>(Call);
      if (!IntrinsicCall || Call->hasOperandBundles() || Call->isConvergent() ||
          Call->cannotDuplicate() ||
          (!isa<MemIntrinsic>(IntrinsicCall) &&
           IntrinsicCall->getIntrinsicID() != Intrinsic::trap))
        return nullptr;
    }
    if (I.getOpcode() != Instruction::UDiv && I.getOpcode() != Instruction::URem)
      continue;
    Value *Divisor = I.getOperand(1);
    if (auto *Freeze = dyn_cast<FreezeInst>(Divisor))
      Divisor = Freeze->getOperand(0);
    if (auto *Arg = dyn_cast<Argument>(Divisor)) ++Counts[Arg->getArgNo()];
  }
  for (Argument &Arg : F.args())
    if (Counts[Arg.getArgNo()] >= 2 && Arg.getType()->isIntegerTy(32))
      return &Arg;
  return nullptr;
}
} // namespace

static unsigned elisacoreSpecializeLiteralDivisors(Module &M, LLVMTargetMachineRef Target) {
  if (!M.getTargetTriple().isAArch64() || M.size() > 2048) return 0;
  unsigned RemainingGrowth = 1024;
  SmallVector<Function *, 8> Clones;
  for (Function &F : llvm::make_early_inc_range(M)) {
    if (!F.hasLocalLinkage() || F.isVarArg() || F.arg_size() > 8 || F.hasComdat() ||
        F.hasFnAttribute(Attribute::Convergent) || F.hasFnAttribute(Attribute::NoDuplicate) ||
        F.hasFnAttribute(Attribute::ReturnsTwice) || F.getNumUses() < 2 ||
        F.getNumUses() > 8 || BDU_SkipFunction(F, M))
      continue;
    unsigned Cost = F.getInstructionCount();
    if (Cost < 12 || Cost > 320 || Cost > RemainingGrowth) continue;
    Argument *Arg = literalDivisorArgument(F);
    if (!Arg) continue;
    unsigned Index = Arg->getArgNo();
    SmallVector<std::pair<CallInst *, ConstantInt *>, 8> Calls;
    SmallVector<ConstantInt *, 2> Literals;
    for (Use &UseOfFunction : F.uses()) {
      auto *Call = dyn_cast<CallInst>(UseOfFunction.getUser());
      if (!Call || &Call->getCalledOperandUse() != &UseOfFunction ||
          Call->getFunction() == &F || Call->getFunctionType() != F.getFunctionType() ||
          Call->isMustTailCall() || Call->isConvergent() || Call->hasOperandBundles())
        break;
      auto *Literal = dyn_cast<ConstantInt>(Call->getArgOperand(Index));
      if (!Literal || Literal->getValue().ule(1) || Literal->getValue().ugt(65535))
        break;
      if (!llvm::is_contained(Literals, Literal)) {
        if (Literals.size() == 2) break;
        Literals.push_back(Literal);
      }
      Calls.push_back({Call, Literal});
    }
    if (Calls.size() != F.getNumUses() || Literals.size() != 2) continue;
    for (ConstantInt *Literal : Literals) {
      ValueToValueMapTy Map;
      Function *Clone = CloneFunction(&F, Map);
      Clone->setName(F.getName() + ".literal_divisor");
      Clone->getArg(Index)->replaceAllUsesWith(Literal);
      for (auto [Call, Value] : Calls)
        if (Value == Literal) Call->setCalledFunction(Clone);
      Clones.push_back(Clone);
    }
    RemainingGrowth -= Cost;
    F.eraseFromParent();
  }
  if (Clones.empty()) return 0;
  auto Options = LLVMCreatePassBuilderOptions();
  for (Function *Clone : Clones)
    if (auto Error = LLVMRunPassesOnFunction(wrap(Clone),
            "instcombine<no-verify-fixpoint>,simplifycfg", Target, Options)) {
      LLVMConsumeError(Error);
      LLVMDisposePassBuilderOptions(Options);
      return 2;
    }
  LLVMDisposePassBuilderOptions(Options);
  return 1;
}
