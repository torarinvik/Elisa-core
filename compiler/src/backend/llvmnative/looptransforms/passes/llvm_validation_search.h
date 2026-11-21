// Keep a scalar validation loop when it leads into a nested search of the
// same input. A runtime remainder adds setup on short inputs, while the
// nested comparisons dominate sustained work on inputs with long matches.
static bool precedesNestedInputSearch(Loop &validation, LoopInfo &loops,
                                     DominatorTree &dom, LoadInst &validated) {
  BasicBlock *entry = validation.getLoopPredecessor();
  auto *input = dyn_cast<Argument>(getUnderlyingObject(validated.getPointerOperand()));
  if (!entry || !input || !entry->getParent()->onlyReadsMemory()) return false;
  for (Loop *search : loops.getLoopsInPreorder()) {
    if (!search->getParentLoop() || search->getNumBlocks() != 2 ||
        !search->getSubLoops().empty() || !dom.dominates(entry, search->getHeader()))
      continue;
    for (BasicBlock *block : search->blocks()) {
      auto *branch = dyn_cast<CondBrInst>(block->getTerminator());
      if (!branch || search->contains(branch->getSuccessor(0)) ==
                         search->contains(branch->getSuccessor(1)))
        continue;
      auto *comparison = dyn_cast<ICmpInst>(branch->getCondition());
      if (!comparison || !comparison->isEquality()) continue;
      auto *left = dyn_cast<LoadInst>(comparison->getOperand(0));
      auto *right = dyn_cast<LoadInst>(comparison->getOperand(1));
      if (left && right && left != right && left->isSimple() && right->isSimple() &&
          search->contains(left) && search->contains(right) &&
          getUnderlyingObject(left->getPointerOperand()) == input &&
          getUnderlyingObject(right->getPointerOperand()) == input)
        return true;
    }
  }
  return false;
}
