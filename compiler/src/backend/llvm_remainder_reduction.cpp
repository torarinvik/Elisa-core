// Keep an accumulator off the multiply/add dependency chain of a decomposed
// unsigned remainder. All reordered adds wrap; quotient/multiply flags remain.
//go:build cgo

#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/TargetParser/Triple.h>

using namespace llvm;

static bool wrappingAdd(BinaryOperator *I) {
  return I && I->getOpcode() == Instruction::Add &&
         I->getType()->isIntegerTy(64) && !I->hasNoSignedWrap() &&
         !I->hasNoUnsignedWrap();
}

static bool groupRemainder(BinaryOperator *Sum) {
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
  if (!Triple(M->getTargetTriple()).isAArch64())
    return 0;
  unsigned Changed = 0;
  for (Function &F : *M) {
    if (F.hasFnAttribute(Attribute::OptimizeNone))
      continue;
    for (BasicBlock &B : F)
      for (Instruction &I : B)
        if (auto *Sum = dyn_cast<BinaryOperator>(&I))
          Changed += groupRemainder(Sum);
  }
  return Changed;
}
