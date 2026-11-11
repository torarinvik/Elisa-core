// The vectorizer normally suppresses runtime unrolling of these loops. A
// second vector iteration reduces loop overhead in small nested recurrences
// with four adjacent two-lane loads and stores.
static void unrollNestedVectorRecurrence(Loop &L) {
  BasicBlock *Body = L.getHeader();
  if (L.getNumBlocks() != 1 || L.getLoopLatch() != Body ||
      Body->size() > 42 || !L.getLoopPreheader())
    return;
  MDNode *ID = L.getLoopID();
  if (!ID || ID->getNumOperands() != 3) return;
  bool Vectorized = false;
  bool RuntimeDisabled = false;
  for (unsigned I = 1; I < ID->getNumOperands(); ++I) {
    auto *Property = dyn_cast_or_null<MDNode>(ID->getOperand(I));
    auto *Name = Property && Property->getNumOperands()
                     ? dyn_cast_or_null<MDString>(Property->getOperand(0))
                     : nullptr;
    if (!Name) return;
    Vectorized |= Name->getString() == "llvm.loop.isvectorized";
    RuntimeDisabled |= Name->getString() == "llvm.loop.unroll.runtime.disable";
  }
  if (!Vectorized || !RuntimeDisabled) return;
  Type *Pair = FixedVectorType::get(Type::getInt64Ty(Body->getContext()), 2);
  bool HasRecurrence = false;
  for (PHINode &Phi : Body->phis())
    if (Phi.getType() == Pair)
      if (auto *Shuffle = dyn_cast<ShuffleVectorInst>(Phi.getIncomingValueForBlock(Body)))
        HasRecurrence |= Shuffle->getParent() == Body &&
                         isa<LoadInst>(Shuffle->getOperand(0));
  if (!HasRecurrence) return;
  unsigned Loads = 0, Stores = 0;
  for (Instruction &I : *Body) {
    if (auto *Load = dyn_cast<LoadInst>(&I)) {
      if (!Load->isSimple() || Load->getType() != Pair) return;
      ++Loads;
    } else if (auto *Store = dyn_cast<StoreInst>(&I)) {
      if (!Store->isSimple() || Store->getValueOperand()->getType() != Pair) return;
      ++Stores;
    } else if (I.mayHaveSideEffects()) {
      return;
    }
  }
  if (Loads != 4 || Stores != 4) return;
  LLVMContext &Context = Body->getContext();
  Metadata *Count[] = {
      MDString::get(Context, "llvm.loop.unroll.count"),
      ConstantAsMetadata::get(ConstantInt::get(Type::getInt32Ty(Context), 2))};
  for (unsigned I = 1; I < ID->getNumOperands(); ++I) {
    auto *Property = cast<MDNode>(ID->getOperand(I));
    auto *Name = cast<MDString>(Property->getOperand(0));
    if (Name->getString() == "llvm.loop.isvectorized") {
      MDNode *Updated = MDNode::getDistinct(Context, {nullptr, Property, MDNode::get(Context, Count)});
      Updated->replaceOperandWith(0, Updated);
      L.setLoopID(Updated);
      return;
    }
  }
}
