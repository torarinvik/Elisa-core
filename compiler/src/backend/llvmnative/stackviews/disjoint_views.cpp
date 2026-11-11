// Expose disjoint local view arguments without changing their calling convention.
#include <llvm-c/Core.h>
#include <llvm-c/Error.h>
#include <llvm-c/TargetMachine.h>
#include <llvm-c/Transforms/PassBuilder.h>
#include <llvm/Config/llvm-config.h>

#if LLVM_VERSION_MAJOR >= 23
#include <llvm/ADT/SmallVector.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>

using namespace llvm;
namespace {

static bool skipStackViewFunction(const Function &F, const Module &M) {
  if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
      F.getSubprogram() || M.getNamedMetadata("llvm.dbg.cu")) return true;
  for (StringRef Flag : {"SanitizeAddress", "SanitizeHWAddress", "SanitizeMemory",
                         "SanitizeThread", "SanitizeRealtime"})
    if (M.getModuleFlag(Flag)) return true;
  for (Attribute::AttrKind Attr : {Attribute::SanitizeAddress, Attribute::SanitizeHWAddress,
        Attribute::SanitizeMemory, Attribute::SanitizeThread, Attribute::SanitizeMemTag,
        Attribute::SanitizeRealtime, Attribute::SanitizeRealtimeBlocking})
    if (F.hasFnAttribute(Attr)) return true;
  return false;
}

static AllocaInst *viewStackArray(Value *View) {
  auto *Layout = dyn_cast<StructType>(View->getType());
  if (!Layout || Layout->getNumElements() != 2 ||
      !Layout->getElementType(0)->isPointerTy() ||
      !Layout->getElementType(1)->isIntegerTy())
    return nullptr;
  auto *Length = dyn_cast_or_null<ConstantInt>(FindInsertedValue(View, {1}));
  auto *Storage = dyn_cast_or_null<AllocaInst>(FindInsertedValue(View, {0}));
  if (!Length || Length->isZero() || Length->getValue().ugt(32) ||
      !Storage || !Storage->isStaticAlloca() || Storage->isArrayAllocation())
    return nullptr;
  auto *Array = dyn_cast<ArrayType>(Storage->getAllocatedType());
  if (!Array || Array->getNumElements() < Length->getZExtValue() ||
      Array->getNumElements() > 32)
    return nullptr;
  auto *Element = Array->getElementType();
  return Element->isIntegerTy() || Element->isFloatingPointTy() ? Storage : nullptr;
}

static bool assumeDisjointStackViews(Function &F) {
  if (!F.hasLocalLinkage() || F.isVarArg() || F.arg_size() != 3 ||
      !F.getReturnType()->isVoidTy() || F.getNumUses() < 2 ||
      F.getNumUses() > 4 || !F.getArg(2)->getType()->isIntegerTy(1) ||
      F.hasFnAttribute("elisa.disjoint-stack-views") ||
      skipStackViewFunction(F, *F.getParent()))
    return false;
  for (Use &U : F.uses()) {
    auto *Call = dyn_cast<CallInst>(U.getUser());
    if (!Call || &Call->getCalledOperandUse() != &U ||
        Call->getFunctionType() != F.getFunctionType() ||
        !isa<ConstantInt>(Call->getArgOperand(2)))
      return false;
    auto *First = viewStackArray(Call->getArgOperand(0));
    auto *Second = viewStackArray(Call->getArgOperand(1));
    if (!First || !Second || First == Second) return false;
  }
  IRBuilder<> B(&*F.getEntryBlock().getFirstInsertionPt());
  Value *Pointers[] = {B.CreateExtractValue(F.getArg(0), 0),
                      B.CreateExtractValue(F.getArg(1), 0)};
  B.CreateAssumption({OperandBundleDef("separate_storage", Pointers)});
  F.addFnAttr("elisa.disjoint-stack-views");
  return true;
}

} // namespace

extern "C" unsigned elisacoreExposeDisjointStackViews(
    LLVMModuleRef Module, LLVMTargetMachineRef Target) {
  auto &M = *unwrap(Module);
  if (!M.getTargetTriple().isAArch64() || M.size() > 512) return 0;
  SmallVector<Function *, 4> Changed;
  for (Function &F : M)
    if (assumeDisjointStackViews(F)) Changed.push_back(&F);
  if (Changed.empty()) return 0;
  auto Options = LLVMCreatePassBuilderOptions();
  for (Function *F : Changed)
    if (auto Error = LLVMRunPassesOnFunction(wrap(F),
            "sroa,instcombine,simplifycfg,gvn,slp-vectorizer,instcombine,simplifycfg",
            Target, Options)) {
      LLVMConsumeError(Error);
      LLVMDisposePassBuilderOptions(Options);
      return 2;
    }
  LLVMDisposePassBuilderOptions(Options);
  return 1;
}
#else
extern "C" unsigned elisacoreExposeDisjointStackViews(
    LLVMModuleRef, LLVMTargetMachineRef) { return 0; }
#endif
