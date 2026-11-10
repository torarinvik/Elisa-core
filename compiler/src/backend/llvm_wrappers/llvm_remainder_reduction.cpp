// Keep an accumulator off the multiply/add dependency chain of a decomposed
// unsigned remainder. All reordered adds wrap; quotient/multiply flags remain.
//go:build cgo

#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>
#include <llvm/Config/llvm-config.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/TargetParser/Triple.h>

using namespace llvm;

static bool wrappingAdd(BinaryOperator *I) {
  return I && I->getOpcode() == Instruction::Add &&
         I->getType()->isIntegerTy(64) && !I->hasNoSignedWrap() &&
         !I->hasNoUnsignedWrap();
}

// For an unsigned division by a nonzero constant, N < D iff N / D == 0.
// Reuse only an already computed, non-exact quotient in the same block: an
// exact division can be poison where the original comparison was defined.
static bool compareExistingQuotient(ICmpInst *Cmp, Value *Numerator,
                                    ConstantInt *Divisor) {
  if (!Cmp->hasOneUse() || !isa<BranchInst>(*Cmp->user_begin()))
    return false;
  auto Pred = Cmp->getPredicate();
  if (Pred != ICmpInst::ICMP_ULT && Pred != ICmpInst::ICMP_UGE)
    return false;
  if (Cmp->getOperand(0) != Numerator || Cmp->getOperand(1) != Divisor)
    return false;
  for (Instruction &I : *Cmp->getParent()) {
    if (&I == Cmp)
      break;
    auto *Q = dyn_cast<BinaryOperator>(&I);
    if (!Q || Q->getOpcode() != Instruction::UDiv || Q->isExact() ||
        Q->getOperand(0) != Cmp->getOperand(0) || Q->getOperand(1) != Divisor)
      continue;
    Cmp->setOperand(0, Q);
    Cmp->setOperand(1, ConstantInt::get(Q->getType(), 0));
    Cmp->setPredicate(Pred == ICmpInst::ICMP_ULT ? ICmpInst::ICMP_EQ
                                                 : ICmpInst::ICMP_NE);
#if LLVM_VERSION_MAJOR >= 20
    Cmp->setSameSign(false);
#endif
    return true;
  }
  return false;
}

static bool groupRemainder(BinaryOperator *Sum, bool UseQuotientBranch) {
  if (!wrappingAdd(Sum))
    return false;
  for (unsigned Side = 0; Side != 2; ++Side) {
    auto *Inner = dyn_cast<BinaryOperator>(Sum->getOperand(Side));
    auto *Product = dyn_cast<BinaryOperator>(Sum->getOperand(1 - Side));
    if (!wrappingAdd(Inner) || !Inner->hasOneUse() ||
        Inner->getParent() != Sum->getParent() || !Product ||
        Product->getOpcode() != Instruction::Mul)
      continue;
    for (unsigned P = 0; P != 2; ++P) {
      auto *Quotient = dyn_cast<BinaryOperator>(Product->getOperand(P));
      auto *Negative = dyn_cast<ConstantInt>(Product->getOperand(1 - P));
      if (!Quotient || Quotient->getOpcode() != Instruction::UDiv || !Negative)
        continue;
      auto *Divisor = dyn_cast<ConstantInt>(Quotient->getOperand(1));
      if (!Divisor || Divisor->isZero() ||
          Negative->getValue() != -Divisor->getValue())
        continue;
      Value *Numerator = Quotient->getOperand(0);
      for (unsigned N = 0; N != 2; ++N) {
        auto *Accumulator = dyn_cast<PHINode>(Inner->getOperand(1 - N));
        if (Inner->getOperand(N) != Numerator || !Accumulator ||
            Accumulator->getParent() != Sum->getParent())
          continue;
        // (N + A) + P == (N + P) + A modulo 2^64. Reuse P and
        // the quotient unchanged, including their poison/overflow semantics.
        // No additional divide, multiply, freeze, or overflow assumption.
        IRBuilder<> Builder(Sum);
        Builder.SetCurrentDebugLocation(Sum->getDebugLoc());
        Value *Remainder = Builder.CreateAdd(Numerator, Product,
                                             "elisa.remainder.grouped");
        Sum->setOperand(0, Remainder);
        Sum->setOperand(1, Accumulator);
        Inner->eraseFromParent();
        if (UseQuotientBranch)
          for (Instruction &I : *Sum->getParent())
            if (auto *Cmp = dyn_cast<ICmpInst>(&I))
              compareExistingQuotient(Cmp, Numerator, Divisor);
        return true;
      }
    }
  }
  return false;
}

extern "C" unsigned elisacoreGroupRemainderReductions(LLVMModuleRef Ref) {
  if (!Ref)
    return 0;
  Module *M = unwrap(Ref);
  Triple Target(M->getTargetTriple());
  if (!Target.isAArch64())
    return 0;
  unsigned Changed = 0;
  for (Function &F : *M) {
    if (F.hasFnAttribute(Attribute::OptimizeNone))
      continue;
    for (BasicBlock &B : F)
      for (Instruction &I : B)
        if (auto *Sum = dyn_cast<BinaryOperator>(&I))
          Changed += groupRemainder(Sum, Target.isOSLinux());
  }
  return Changed;
}
