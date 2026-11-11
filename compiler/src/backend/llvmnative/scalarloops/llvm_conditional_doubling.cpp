#include <llvm-c/Core.h>
#include <llvm/Config/llvm-config.h>

#if LLVM_VERSION_MAJOR >= 23
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Module.h>
#include <llvm/Transforms/Utils/Local.h>

using namespace llvm;

static bool skipConditionalDoubling(const Function &F, const Module &M) {
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

// Expose a shift and a subtract as the two recurrence values so AArch64
// can reuse the subtraction's flags for the conditional select.
static bool rewriteConditionalDoubling(PHINode &Phi, unsigned Edge) {
  auto *Sum = dyn_cast<BinaryOperator>(Phi.getIncomingValue(Edge));
  if (!Sum || Sum->getOpcode() != Instruction::Add || Sum->hasNoSignedWrap() ||
      Sum->hasNoUnsignedWrap() || Sum->getParent() != Phi.getIncomingBlock(Edge))
    return false;
  Value *Offset = Sum->getOperand(0);
  if (Sum->getOperand(1) != &Phi) {
    if (Offset != &Phi) return false;
    Offset = Sum->getOperand(1);
  }
  auto *Choice = dyn_cast<SelectInst>(Offset);
  if (!Choice || !Choice->hasOneUse() || Choice->getTrueValue() != &Phi)
    return false;
  auto *Cmp = dyn_cast<ICmpInst>(Choice->getCondition());
  auto *Neg = dyn_cast<BinaryOperator>(Choice->getFalseValue());
  if (!Cmp || Cmp->getPredicate() != ICmpInst::ICMP_ULT || Cmp->getOperand(0) != &Phi ||
      !Neg || Neg->getOpcode() != Instruction::Sub ||
      Neg->hasNoSignedWrap() || Neg->hasNoUnsignedWrap())
    return false;
  auto *Zero = dyn_cast<ConstantInt>(Neg->getOperand(0));
  Value *Distance = Cmp->getOperand(1);
  if (!Zero || !Zero->isZero() || Neg->getOperand(1) != Distance)
    return false;

  IRBuilder<> B(Sum);
  Value *Double = B.CreateShl(&Phi, B.getInt64(1));
  Value *Reduced = B.CreateSub(&Phi, Distance);
  Sum->replaceAllUsesWith(B.CreateSelect(Cmp, Double, Reduced));
  RecursivelyDeleteTriviallyDeadInstructions(Sum);
  return true;
}

extern "C" unsigned elisacoreFoldConditionalDoubling(LLVMModuleRef Ref) {
  Module &M = *unwrap(Ref);
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (skipConditionalDoubling(F, M)) continue;
    for (BasicBlock &BB : F) {
      for (PHINode &Phi : BB.phis()) {
        if (!Phi.getType()->isIntegerTy(64) || Phi.getNumIncomingValues() != 2) continue;
        for (unsigned Edge = 0; Edge < 2; ++Edge)
          Changed += rewriteConditionalDoubling(Phi, Edge);
      }
    }
  }
  return Changed;
}
#else
extern "C" unsigned elisacoreFoldConditionalDoubling(LLVMModuleRef) { return 0; }
#endif
