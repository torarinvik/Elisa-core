// Recover scalar replacement when small local arrays travel through view aggregates.
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/InstIterator.h>
#include <llvm/Transforms/Utils/Cloning.h>
#include <llvm-c/Error.h>
#include <llvm-c/Transforms/PassBuilder.h>

namespace {
static AllocaInst *inlineStackViewStorage(Value *View) {
  auto *Type = dyn_cast<StructType>(View->getType());
  if (!Type || Type->getNumElements() != 2 || !Type->getElementType(0)->isPointerTy() ||
      !Type->getElementType(1)->isIntegerTy())
    return nullptr;
  auto *Length = dyn_cast_or_null<ConstantInt>(FindInsertedValue(View, {1}));
  Value *Data = FindInsertedValue(View, {0});
  if (!Length || Length->isZero() || Length->getValue().ugt(32) || !Data)
    return nullptr;
  auto *Storage = dyn_cast<AllocaInst>(getUnderlyingObject(Data));
  if (!Storage || !Storage->isStaticAlloca() || Storage->isArrayAllocation())
    return nullptr;
  auto *Array = dyn_cast<ArrayType>(Storage->getAllocatedType());
  if (!Array || Array->getNumElements() > 32) return nullptr;
  auto *Element = Array->getElementType();
  if (!Element->isIntegerTy() && !Element->isFloatingPointTy()) return nullptr;
  return Storage;
}

static bool hasInlineStackViewArguments(CallInst &Call) {
  SmallPtrSet<AllocaInst *, 4> Storage;
  bool HasConstantFlag = false;
  for (Value *Arg : Call.args()) {
    if (auto *Literal = dyn_cast<ConstantInt>(Arg)) {
      HasConstantFlag |= Literal->getType()->isIntegerTy(1);
      continue;
    }
    auto *Array = inlineStackViewStorage(Arg);
    if (!Array || !Storage.insert(Array).second) return false;
  }
  return Storage.size() >= 2 && HasConstantFlag;
}
} // namespace

static unsigned elisacoreInlineStackViewHelpers(Module &M, LLVMTargetMachineRef Target) {
  if (!M.getTargetTriple().isAArch64() || M.size() > 512) return 0;
  unsigned Budget = 1200;
  SmallPtrSet<Function *, 4> Callers;
  SmallVector<CallInst *, 8> Candidates;
  for (Function &F : M) {
    if (!F.hasLocalLinkage() || !F.getReturnType()->isVoidTy() || F.isVarArg() ||
        F.arg_size() < 3 || F.arg_size() > 6 || !F.doesNotThrow() ||
        !F.hasFnAttribute(Attribute::NoRecurse) || F.hasFnAttribute(Attribute::NoInline) ||
        F.hasFnAttribute(Attribute::Convergent) || F.hasFnAttribute(Attribute::NoDuplicate) ||
        !F.hasNUsesOrMore(2) || F.hasNUsesOrMore(5) || BDU_SkipFunction(F, M))
      continue;
    const unsigned Cost = F.getInstructionCount();
    if (Cost < 40 || Cost > 360) continue;
    for (User *Use : F.users()) {
      auto *Call = dyn_cast<CallInst>(Use);
      if (!Call || Call->getCalledFunction() != &F || Call->getFunction() == &F ||
          Call->getFunctionType() != F.getFunctionType() || Call->hasOperandBundles() ||
          Call->isNoInline() || Call->isMustTailCall() || Call->isConvergent() ||
          BDU_SkipFunction(*Call->getFunction(), M) || !hasInlineStackViewArguments(*Call) ||
          Call->getFunction()->getInstructionCount() > 1200)
        continue;
      if (Cost > Budget) continue;
      Budget -= Cost;
      Candidates.push_back(Call);
    }
  }
  for (CallInst *Call : Candidates) {
    Function *Caller = Call->getFunction();
    InlineFunctionInfo Info;
    if (InlineFunction(*Call, Info).isSuccess()) Callers.insert(Caller);
  }
  if (Callers.empty()) return 0;
  auto Options = LLVMCreatePassBuilderOptions();
  for (Function *Caller : Callers)
    if (auto Error = LLVMRunPassesOnFunction(wrap(Caller),
            "sroa,early-cse,instcombine,simplifycfg,gvn,slp-vectorizer,instcombine,simplifycfg,"
            "loop-simplify,lcssa,loop-unroll<O3>,sroa,instcombine,simplifycfg,gvn,slp-vectorizer",
            Target, Options)) {
      LLVMConsumeError(Error);
      LLVMDisposePassBuilderOptions(Options);
      return 2;
    }
  LLVMDisposePassBuilderOptions(Options);
  return 1;
}
