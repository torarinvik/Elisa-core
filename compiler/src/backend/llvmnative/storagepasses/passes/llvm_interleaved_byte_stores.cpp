// Preserve the original vector when each halfword is stored low byte first.
using namespace llvm;

namespace {
bool isByteShift(Value *high, Value *low) {
  using namespace llvm::PatternMatch;
  return match(high, m_LShr(m_Specific(low), m_SpecificInt(8)));
}

Value *matchInterleavedHalfwords(Value *bytes) {
  // Depending on the vector width, LLVM can truncate before or after interleaving.
  auto *truncation = dyn_cast<TruncInst>(bytes);
  auto *shuffle = dyn_cast<ShuffleVectorInst>(
      truncation ? truncation->getOperand(0) : bytes);
  if (!shuffle) return nullptr;

  auto *laneType = dyn_cast<FixedVectorType>(shuffle->getOperand(0)->getType());
  auto *byteType = dyn_cast<FixedVectorType>(bytes->getType());
  if (!laneType || !byteType || !byteType->getElementType()->isIntegerTy(8))
    return nullptr;
  unsigned lanes = laneType->getNumElements();
  if (byteType->getNumElements() != 2 * lanes) return nullptr;

  for (unsigned lane = 0; lane < lanes; ++lane) {
    if (shuffle->getMaskValue(2 * lane) != static_cast<int>(lane) ||
        shuffle->getMaskValue(2 * lane + 1) != static_cast<int>(lanes + lane))
      return nullptr;
  }

  Value *low = shuffle->getOperand(0);
  Value *high = shuffle->getOperand(1);
  if (!truncation) {
    auto *lowTruncation = dyn_cast<TruncInst>(low);
    auto *highTruncation = dyn_cast<TruncInst>(high);
    if (!lowTruncation || !highTruncation) return nullptr;
    low = lowTruncation->getOperand(0);
    high = highTruncation->getOperand(0);
  }
  auto *halfwords = dyn_cast<FixedVectorType>(low->getType());
  if (!halfwords || !halfwords->getElementType()->isIntegerTy(16) ||
      !isByteShift(high, low))
    return nullptr;
  return low;
}
} // namespace

extern "C" unsigned elisacoreFoldInterleavedByteStores(LLVMModuleRef moduleRef) {
  Module &module = *unwrap(moduleRef);
  if (module.getDataLayoutStr().empty() ||
      !module.getDataLayout().isLittleEndian())
    return 0;

  unsigned changes = 0;
  for (auto &function : module) {
    for (auto &block : function) {
      for (auto &instruction : block) {
        auto *store = dyn_cast<StoreInst>(&instruction);
        if (!store || !store->isSimple()) continue;
        Value *bytes = store->getValueOperand();
        Value *halfwords = matchInterleavedHalfwords(bytes);
        if (!halfwords) continue;

        // Keep the existing store and its alignment, metadata, and address.
        IRBuilder<> builder(store);
        store->setOperand(0, builder.CreateBitCast(halfwords, bytes->getType()));
        RecursivelyDeleteTriviallyDeadInstructions(bytes);
        ++changes;
      }
    }
  }
  return changes;
}
