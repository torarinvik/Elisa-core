//go:build cgo
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/PatternMatch.h>
#include <llvm/Support/CBindingWrapping.h>

namespace {
using namespace llvm;
using namespace llvm::PatternMatch;

bool sameSplitSource(Value *a, Value *b) {
  if (a == b)
    return true;
  auto *first = dyn_cast<LoadInst>(a);
  auto *second = dyn_cast<LoadInst>(b);
  if (!first || !second || !first->isSimple() || !second->isSimple() ||
      first->getParent() != second->getParent() ||
      first->getPointerOperand()->stripPointerCasts() !=
          second->getPointerOperand()->stripPointerCasts())
    return false;
  if (!first->comesBefore(second))
    std::swap(first, second);
  unsigned distance = 0;
  for (auto *i = first->getNextNode(); i != second; i = i->getNextNode()) {
    auto *load = dyn_cast<LoadInst>(i);
    if (++distance > 32 || isa<CallBase, FenceInst>(i) ||
        (i->mayReadOrWriteMemory() && (!load || !load->isSimple())))
      return false;
  }
  return true;
}

// For a k-bit low limb, x = low + (high << k). Under a 2k-bit
// mask, the high limb only needs the low k bits of the multiplier.
Value *foldMaskedSplitMultiply(Instruction &root) {
  Value *sum;
  const APInt *mask;
  if (!match(&root, m_c_And(m_Value(sum), m_APInt(mask))) ||
      !mask->isMask())
    return nullptr;
  unsigned bits = mask->popcount();
  if (bits < 2 || bits % 2)
    return nullptr;
  unsigned k = bits / 2;
  APInt lowMask = APInt::getLowBitsSet(mask->getBitWidth(), k);

  Value *inner;
  ConstantInt *increment = nullptr;
  if (match(sum, m_c_Add(m_Value(inner), m_ConstantInt(increment))))
    sum = inner;
  auto *add = dyn_cast<BinaryOperator>(sum);
  if (!add || add->getOpcode() != Instruction::Add)
    return nullptr;

  for (unsigned index = 0; index < 2; ++index) {
    Value *low, *high, *loInput, *hiInput;
    const APInt *loFactor, *hiFactor, *loMask, *hiMask, *leftShift, *rightShift;
    if (!match(add->getOperand(index), m_c_Mul(m_Value(low), m_APInt(loFactor))) ||
        !match(low, m_c_And(m_Value(loInput), m_APInt(loMask))) ||
        *loMask != lowMask ||
        !match(add->getOperand(1 - index),
               m_Shl(m_Value(high), m_APInt(leftShift))) ||
        *leftShift != k ||
        !match(high, m_c_And(m_Value(high), m_APInt(hiMask))) ||
        *hiMask != lowMask ||
        !match(high, m_c_Mul(m_Value(high), m_APInt(hiFactor))) ||
        ((*loFactor ^ *hiFactor) & lowMask) != 0 ||
        !match(high, m_LShr(m_Value(hiInput), m_APInt(rightShift))) ||
        *rightShift != k || !sameSplitSource(loInput, hiInput))
      continue;
    IRBuilder<> builder(&root);
    Value *product = builder.CreateMul(loInput, ConstantInt::get(root.getType(), *loFactor));
    if (increment)
      product = builder.CreateAdd(product, increment);
    return builder.CreateAnd(product, ConstantInt::get(root.getType(), *mask));
  }
  return nullptr;
}
} // namespace

extern "C" unsigned elisacoreFoldMaskedSplitMultiplies(LLVMModuleRef module) {
  unsigned folded = 0;
  for (auto &fn : *llvm::unwrap(module))
    for (auto &block : fn)
      for (auto it = block.begin(); it != block.end();) {
        auto &inst = *it++;
        if (inst.getOpcode() != llvm::Instruction::And ||
            !inst.getType()->isIntegerTy())
          continue;
        if (auto *replacement = foldMaskedSplitMultiply(inst)) {
          inst.replaceAllUsesWith(replacement);
          inst.eraseFromParent();
          ++folded;
        }
      }
  return folded;
}
