// Remove checks that branch to a trap when the comparison on the other edge
// always holds. The proofs are ElisaIndexBounds': guards on dominating
// edges, ranges along every path, recurrences, and induction over loop
// headers, which covers the cursors of heap sifts and two-pointer scans
// that scalar evolution alone cannot bound. A proof may use another
// check's passing edge: it executed earlier, so removing both is an
// induction over execution order. All proofs finish before any branch
// changes, so no proof reads a branch an earlier removal already folded.
#include <llvm/IR/Constants.h>
namespace ElisaProvenChecks {
using namespace llvm;

// Whether the comparison deciding Branch holds whenever Branch takes its
// Safe successor's side, i.e. the other side is never taken.
static bool proved(ElisaIndexBounds::Prover &Proof, ScalarEvolution &Evolution,
                   CondBrInst &Branch, unsigned Safe) {
  auto *Compare = dyn_cast<ICmpInst>(Branch.getCondition());
  if (!Compare || !Evolution.isSCEVable(Compare->getOperand(0)->getType())) return false;
  auto Predicate = Safe == 0 ? Compare->getPredicate() : Compare->getInversePredicate();
  const SCEV *Left = Evolution.getSCEV(Compare->getOperand(0));
  const SCEV *Right = Evolution.getSCEV(Compare->getOperand(1));
  if (Predicate == ICmpInst::ICMP_UGT || Predicate == ICmpInst::ICMP_UGE) {
    std::swap(Left, Right);
    Predicate = ICmpInst::getSwappedPredicate(Predicate);
  }
  switch (Predicate) {
  case ICmpInst::ICMP_ULT:
    return Proof.proveBelow(Left, Right, &Branch);
  case ICmpInst::ICMP_ULE:
    return Proof.proveAtMost(Left, Right, &Branch);
  case ICmpInst::ICMP_NE:
    return Proof.proveBelow(Left, Right, &Branch) || Proof.proveBelow(Right, Left, &Branch);
  default:
    return false;
  }
}

static unsigned removeProvenChecks(Function &F, FunctionAnalysisManager &Analyses) {
  SmallVector<std::pair<CondBrInst *, unsigned>, 16> Checks;
  for (BasicBlock &Block : F)
    if (auto *Branch = dyn_cast<CondBrInst>(Block.getTerminator()))
      for (unsigned Side = 0; Side < 2; ++Side)
        if (ElisaDeadTrapExits::isTrap(*Branch->getSuccessor(Side)) &&
            !ElisaDeadTrapExits::isTrap(*Branch->getSuccessor(1 - Side)))
          Checks.push_back({Branch, 1 - Side});
  if (Checks.empty()) return 0;
  auto &Evolution = Analyses.getResult<ScalarEvolutionAnalysis>(F);
  ElisaIndexBounds::Prover Proof(Evolution, Analyses.getResult<DominatorTreeAnalysis>(F),
                                 Analyses.getResult<LoopAnalysis>(F));
  SmallVector<std::pair<CondBrInst *, unsigned>, 16> Proven;
  for (auto [Branch, Safe] : Checks)
    if (proved(Proof, Evolution, *Branch, Safe)) Proven.push_back({Branch, Safe});
  for (auto [Branch, Safe] : Proven)
    Branch->setCondition(ConstantInt::getBool(F.getContext(), Safe == 0));
  if (!Proven.empty()) Analyses.invalidate(F, PreservedAnalyses::none());
  return Proven.size();
}

static unsigned removeProvenChecks(Module &Module) {
  PassBuilder Builder;
  FunctionAnalysisManager Analyses;
  Builder.registerFunctionAnalyses(Analyses);
  unsigned Removed = 0;
  for (Function &F : Module)
    if (!F.isDeclaration() && !F.hasOptNone()) Removed += removeProvenChecks(F, Analyses);
  return Removed;
}
} // namespace ElisaProvenChecks
