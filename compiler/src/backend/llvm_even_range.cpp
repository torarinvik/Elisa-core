// go:build cgo

// Fold a bounded even-integer test to a subtract-and-mask test. This changes
// no loads or branches, and only accepts an exactly matching arithmetic form.
#include <llvm-c/Core.h>
#include <llvm-c/Target.h>
#include <mutex>
#include <llvm/Config/llvm-config.h>
#if LLVM_VERSION_MAJOR == 23
#include <llvm/ADT/APInt.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/InlineAsm.h>
#include <llvm/IR/Dominators.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>
using namespace llvm;
namespace EvenRange {
static bool rewrite(BinaryOperator &Root) {
  if (Root.getOpcode() != Instruction::Or || !Root.getType()->isIntegerTy(1) ||
      Root.hasMetadataOtherThanDebugLoc())
    return false;
  for (unsigned order = 0; order != 2; ++order) {
    auto *Cmp = dyn_cast<ICmpInst>(Root.getOperand(order));
    auto *Odd = dyn_cast<TruncInst>(Root.getOperand(1 - order));
    if (!Cmp || Cmp->getPredicate() != ICmpInst::ICMP_ULT || !Odd ||
        !Odd->getSrcTy()->isIntegerTy(64) ||
        !Odd->getDestTy()->isIntegerTy(1) ||
        Cmp->hasMetadataOtherThanDebugLoc() ||
        Odd->hasMetadataOtherThanDebugLoc())
      continue;
    auto *Add = dyn_cast<BinaryOperator>(Cmp->getOperand(0));
    auto *Limit = dyn_cast<ConstantInt>(Cmp->getOperand(1));
    if (!Add || Add->getOpcode() != Instruction::Add || !Limit ||
        !Add->getType()->isIntegerTy(64) || Add->hasNoSignedWrap() ||
        Add->hasNoUnsignedWrap() || Add->hasMetadataOtherThanDebugLoc())
      continue;
    auto *Offset = dyn_cast<ConstantInt>(Add->getOperand(1));
    Value *Input = Add->getOperand(0);
    if (!Offset) {
      Offset = dyn_cast<ConstantInt>(Add->getOperand(0));
      Input = Add->getOperand(1);
    }
    if (!Offset || Input != Odd->getOperand(0))
      continue;
    const APInt O = Offset->getValue(), L = Limit->getValue();
    if (!O.isNegative() || !L.isNegative())
      continue;
    const APInt Upper = -O - 1;
    const APInt Size = -L;
    if (Upper.isNegative() || Size.isZero() || Size.isNegative())
      continue;
    const APInt Span = Size - 1;
    if (Span.isZero() || Span[0] || !Span.lshr(1).isMask() || Upper.ult(Span))
      continue;
    const APInt Lower = Upper - Span;
    if (Lower[0])
      continue;
    IRBuilder<> B(&Root);
    B.SetCurrentDebugLocation(Root.getDebugLoc());
    Value *Adjusted =
        B.CreateSub(Input, ConstantInt::get(Input->getType(), Lower));
    Value *Masked =
        B.CreateAnd(Adjusted, ConstantInt::get(Input->getType(), ~Span));
    Value *Outside =
        B.CreateICmpNE(Masked, ConstantInt::get(Input->getType(), 0));
    Root.replaceAllUsesWith(Outside);
    Root.eraseFromParent();
    return true;
  }
  return false;
}
// Preserve ordered exits while selecting an AArch64 range comparison that
// performs well in long unrolled scans.  The inline instruction materializes
// an exact constant and has no memory effects; its opaque result prevents the
// backend from replacing the comparison with a slower sign-extend test.
static unsigned compactSigned16Scans(Function &F) {
#if !defined(__aarch64__) && !defined(__arm64__)
  return 0;
#else
  if (F.hasFnAttribute(Attribute::SanitizeAddress) ||
      F.hasFnAttribute(Attribute::SanitizeMemory) ||
      F.hasFnAttribute(Attribute::SanitizeThread) ||
      F.hasFnAttribute(Attribute::SanitizeHWAddress)) return 0;
  DominatorTree DT(F);
  LoopInfo LI(DT);
  unsigned Changed = 0;
  for (Loop *L : LI) {
    if (!L->getSubLoops().empty() || !L->getLoopPreheader()) continue;
    SmallVector<ICmpInst *, 32> Checks;
    for (BasicBlock *BB : L->blocks()) for (Instruction &I : *BB) {
      auto *Cmp = dyn_cast<ICmpInst>(&I);
      if (!Cmp || Cmp->getPredicate() != ICmpInst::ICMP_ULT ||
          Cmp->hasMetadataOtherThanDebugLoc()) continue;
      auto *Add = dyn_cast<BinaryOperator>(Cmp->getOperand(0));
      auto *Limit = dyn_cast<ConstantInt>(Cmp->getOperand(1));
      if (!Add || Add->getOpcode() != Instruction::Add ||
          !Add->getType()->isIntegerTy(64) || Add->hasNoSignedWrap() ||
          Add->hasNoUnsignedWrap() || Add->hasMetadataOtherThanDebugLoc() ||
          !Limit || Limit->getValue() != APInt(64, uint64_t(-65536))) continue;
      auto *Offset = dyn_cast<ConstantInt>(Add->getOperand(1));
      if (!Offset || Offset->getValue() != APInt(64, uint64_t(-32768))) continue;
      Checks.push_back(Cmp);
    }
    if (Checks.size() < 8) continue;
    static std::once_flag ParserOnce;
    std::call_once(ParserOnce, [] { LLVMInitializeAArch64AsmParser(); });
    IRBuilder<> Entry(L->getLoopPreheader()->getTerminator());
    auto *Ty = Entry.getInt64Ty();
    auto *Asm = InlineAsm::get(FunctionType::get(Ty, false),
                               "mov $0, #65535", "=r", false);
    Value *Limit = Entry.CreateCall(Asm, {}, "signed16.scan.limit");
    for (ICmpInst *Cmp : Checks) {
      auto *Add = cast<BinaryOperator>(Cmp->getOperand(0));
      IRBuilder<> B(Cmp);
      B.SetCurrentDebugLocation(Cmp->getDebugLoc());
      Value *Adjusted = B.CreateAdd(Add->getOperand(0),
                                    ConstantInt::get(Ty, 32768));
      Value *Outside = B.CreateICmpUGT(Adjusted, Limit);
      Cmp->replaceAllUsesWith(Outside);
      Cmp->eraseFromParent();
      if (Add->use_empty()) Add->eraseFromParent();
      ++Changed;
    }
  }
  return Changed;
#endif
}
} // namespace EvenRange
extern "C" unsigned elisacoreCompactEvenRanges(LLVMModuleRef Ref) {
  if (!Ref)
    return 0;
  Module &M = *unwrap(Ref);
  const auto &T = M.getTargetTriple();
  if (!T.isAArch64() || (!T.isOSLinux() && !T.isOSDarwin()) ||
      M.getNamedMetadata("llvm.dbg.cu"))
    return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize())
      continue;
    SmallVector<BinaryOperator *, 16> Candidates;
    for (BasicBlock &BB : F)
      for (Instruction &I : BB)
        if (auto *Op = dyn_cast<BinaryOperator>(&I))
          if (Op->getOpcode() == Instruction::Or &&
              Op->getType()->isIntegerTy(1))
            Candidates.push_back(Op);
    for (auto *Op : Candidates)
      Changed += EvenRange::rewrite(*Op);
    Changed += EvenRange::compactSigned16Scans(F);
  }
  return Changed;
}
#else
extern "C" unsigned elisacoreCompactEvenRanges(LLVMModuleRef) { return 0; }
#endif
