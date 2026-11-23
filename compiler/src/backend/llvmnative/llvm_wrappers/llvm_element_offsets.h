// Element counts from byte offsets, shared by the zero-fill proofs
// (ElisaIndexedPrefix, ElisaViewBounds).
#pragma once
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
namespace ElisaElementOffsets {
using namespace llvm;

// Offset / Size where Offset is known to be a multiple of Size: through
// affine recurrences, whose start and step must both divide.
static const SCEV *divide(ScalarEvolution &Evolution, const SCEV *Offset, uint64_t Size) {
  if (auto *Constant = dyn_cast<SCEVConstant>(Offset)) {
    const APInt &Value = Constant->getAPInt();
    APInt Divisor(Value.getBitWidth(), Size);
    return Value.srem(Divisor).isZero() ? Evolution.getConstant(Value.sdiv(Divisor)) : nullptr;
  }
  if (auto *Recurrence = dyn_cast<SCEVAddRecExpr>(Offset)) {
    if (!Recurrence->isAffine()) return nullptr;
    const SCEV *Start = divide(Evolution, Recurrence->getStart(), Size);
    const SCEV *Step = divide(Evolution, Recurrence->getStepRecurrence(Evolution), Size);
    return Start && Step ? Evolution.getAddRecExpr(Start, Step, Recurrence->getLoop(),
                                                   SCEV::FlagAnyWrap)
                         : nullptr;
  }
  if (auto *Sum = dyn_cast<SCEVAddExpr>(Offset)) {
    SmallVector<SCEVUse, 4> Terms;
    for (const SCEV *Term : Sum->operands())
      if (const SCEV *Part = divide(Evolution, Term, Size)) Terms.push_back(Part);
      else return nullptr;
    return Evolution.getAddExpr(Terms);
  }
  if (auto *Product = dyn_cast<SCEVMulExpr>(Offset))
    if (const SCEV *Factor = divide(Evolution, Product->getOperand(0), Size)) {
      SmallVector<SCEVUse, 4> Factors(Product->operands());
      Factors[0] = Factor;
      return Evolution.getMulExpr(Factors);
    }
  const SCEV *Quotient =
      Evolution.getUDivExactExpr(Offset, Evolution.getConstant(Offset->getType(), Size));
  return isa<SCEVUDivExpr>(Quotient) ? nullptr : Quotient;
}
} // namespace ElisaElementOffsets
