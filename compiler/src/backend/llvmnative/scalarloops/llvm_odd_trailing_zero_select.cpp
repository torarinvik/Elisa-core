//go:build cgo

#include <llvm/Config/llvm-config.h>
#include <llvm-c/Core.h>

#if LLVM_VERSION_MAJOR >= 23
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Transforms/Utils/Local.h>

using namespace llvm;

namespace {
static bool isConstant(Value *ValueToCheck, uint64_t Expected) {
  auto *Integer = dyn_cast<ConstantInt>(ValueToCheck);
  return Integer && Integer->equalsInt(Expected);
}

// An odd word has zero trailing zeros. Loop idiom recognition may
// retain the odd exit as a select because the count on the even edge has
// a return range excluding zero. Use a fresh count without that restriction.
static bool foldOddTrailingZeroSelect(SelectInst &Select) {
  auto *Test = dyn_cast<ICmpInst>(Select.getCondition());
  if (!Test || !Test->isEquality() || Select.hasMetadataOtherThanDebugLoc())
    return false;
  const bool ZeroOnTrue = Test->getPredicate() == ICmpInst::ICMP_NE;
  if (!isConstant(Select.getOperand(ZeroOnTrue ? 1 : 2), 0))
    return false;
  auto *Masked = dyn_cast<BinaryOperator>(Test->getOperand(0));
  if (!Masked || Masked->getOpcode() != Instruction::And ||
      !isConstant(Masked->getOperand(1), 1) ||
      !isConstant(Test->getOperand(1), 0))
    return false;

  Value *EvenValue = Select.getOperand(ZeroOnTrue ? 2 : 1);
  auto *Shift = dyn_cast<BinaryOperator>(EvenValue);
  if (Shift && (Shift->getOpcode() != Instruction::LShr || Shift->isExact() ||
                !isa<ConstantInt>(Shift->getOperand(1)) ||
                cast<ConstantInt>(Shift->getOperand(1))->getValue().uge(
                    Shift->getType()->getIntegerBitWidth()) ||
                !Shift->hasOneUse() || Shift->hasMetadataOtherThanDebugLoc()))
    return false;
  auto *Count = dyn_cast<IntrinsicInst>(Shift ? Shift->getOperand(0) : EvenValue);
  if (!Count || Count->getIntrinsicID() != Intrinsic::cttz ||
      !Count->hasOneUse() || Count->hasOperandBundles() ||
      Count->getArgOperand(0) != Masked->getOperand(0) ||
      !Count->getType()->isIntegerTy() || Count->getType()->isIntegerTy(1))
    return false;

  IRBuilder<> Builder(&Select);
  Value *Result = Builder.CreateIntrinsic(Intrinsic::cttz, {Count->getType()},
      {Count->getArgOperand(0), Count->getArgOperand(1)});
  if (Shift) Result = Builder.CreateLShr(Result, Shift->getOperand(1));
  Select.replaceAllUsesWith(Result);
  RecursivelyDeleteTriviallyDeadInstructions(&Select);
  return true;
}

} // namespace

extern "C" unsigned elisacoreFoldOddTrailingZeroSelects(LLVMModuleRef Ref) {
  if (!Ref) return 0;
  Module &M = *unwrap(Ref);
  if (!M.getTargetTriple().isAArch64() || M.getNamedMetadata("llvm.dbg.cu")) return 0;
  for (StringRef Flag : {"SanitizeAddress", "SanitizeHWAddress", "SanitizeMemory",
                         "SanitizeThread", "SanitizeRealtime"})
    if (M.getModuleFlag(Flag)) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.size() > 48 || F.isDeclaration() || F.hasOptNone() ||
        F.hasOptSize() || F.hasMinSize() || F.getSubprogram()) continue;
    bool Sanitized = false;
    for (Attribute::AttrKind Kind : {Attribute::SanitizeAddress, Attribute::SanitizeHWAddress,
         Attribute::SanitizeMemory, Attribute::SanitizeThread, Attribute::SanitizeMemTag,
         Attribute::SanitizeRealtime, Attribute::SanitizeRealtimeBlocking})
      Sanitized |= F.hasFnAttribute(Kind);
    if (Sanitized) continue;
    SmallVector<WeakTrackingVH, 8> Selects;
    for (BasicBlock &Block : F)
      for (Instruction &I : Block)
        if (isa<SelectInst>(I)) Selects.push_back(&I);
    for (Value *Selected : Selects)
      if (auto *Select = dyn_cast_or_null<SelectInst>(Selected))
        Changed += foldOddTrailingZeroSelect(*Select);
  }
  return Changed;
}
#else
extern "C" unsigned elisacoreFoldOddTrailingZeroSelects(LLVMModuleRef) { return 0; }
#endif
