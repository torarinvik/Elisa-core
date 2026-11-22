// Put the shifted one first in single-bit tests `(X & (1 << Y)) ==/!= 0`.
// AArch64 instruction selection turns `((1 << Y) & X) != 0` into a shift of
// X and a `tbz`/`tbnz` on bit 0, but only in that operand order: with X
// first it materializes the mask and compares, which the conditional-compare
// pass then merges with neighbouring tests into a longer `ccmp` chain. The
// `and` is commutative, so the swap is free; it runs after the optimization
// pipeline, which would otherwise reorder the operands again.
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/PatternMatch.h>

namespace ElisaBitTests {
using namespace llvm;
using namespace llvm::PatternMatch;

static bool isShiftedOne(Value *Candidate) {
  return match(Candidate, m_Shl(m_One(), m_Value()));
}

// Every user compares the masked value with zero.
static bool onlyTestedAgainstZero(Instruction &Mask) {
  return !Mask.use_empty() && llvm::all_of(Mask.users(), [](User *Use) {
    auto *Compare = dyn_cast<ICmpInst>(Use);
    return Compare && Compare->isEquality() && match(Compare->getOperand(1), m_Zero());
  });
}

static unsigned orderBitTestOperands(Module &Module) {
  unsigned Changed = 0;
  for (Function &Function : Module)
    for (BasicBlock &Block : Function)
      for (Instruction &Instruction : Block) {
        auto *Mask = dyn_cast<BinaryOperator>(&Instruction);
        if (!Mask || Mask->getOpcode() != Instruction::And ||
            isShiftedOne(Mask->getOperand(0)) || !isShiftedOne(Mask->getOperand(1)) ||
            !onlyTestedAgainstZero(*Mask))
          continue;
        Mask->swapOperands();
        ++Changed;
      }
  return Changed;
}
} // namespace ElisaBitTests

extern "C" unsigned elisacoreOrderBitTestOperands(LLVMModuleRef Module) {
  return Module ? ElisaBitTests::orderBitTestOperands(*llvm::unwrap(Module)) : 0;
}
