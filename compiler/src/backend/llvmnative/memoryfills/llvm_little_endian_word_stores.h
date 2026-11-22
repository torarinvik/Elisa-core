// Merge a run of narrow stores that together write the little-endian bytes of
// one word. Instruction combining rewrites `lshr (sext x), 32` into
// `ashr x, 31`, which hides the upper bytes of a sign-extended serialization
// from the target's store merging, so `store_i64_le(x.i64())` became one
// 32-bit store and four byte stores instead of one 64-bit store.
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Instructions.h>
#include <optional>

namespace ElisaWordStores {
using namespace llvm;

// Byte Index of Source, or its sign byte when Index is -1, or zero when
// Source is null.
struct ByteRef {
  Value *Source = nullptr;
  int Index = 0;
  bool operator==(const ByteRef &Other) const {
    return Source == Other.Source && Index == Other.Index;
  }
};

static unsigned byteWidth(Value *Candidate) {
  return Candidate->getType()->getIntegerBitWidth() / 8;
}

static std::optional<ByteRef> byteOf(Value *Candidate, unsigned Byte, unsigned Depth = 0) {
  auto *Type = dyn_cast<IntegerType>(Candidate->getType());
  if (!Type || Type->getBitWidth() % 8 || Byte >= Type->getBitWidth() / 8 || Depth > 6)
    return std::nullopt;
  if (auto *Constant = dyn_cast<ConstantInt>(Candidate);
      Constant && !Constant->getValue().extractBitsAsZExtValue(8, 8 * Byte))
    return ByteRef{};
  auto *Operation = dyn_cast<Instruction>(Candidate);
  if (!Operation) return ByteRef{Candidate, static_cast<int>(Byte)};
  auto *Amount = Operation->getNumOperands() == 2
                     ? dyn_cast<ConstantInt>(Operation->getOperand(1)) : nullptr;
  Value *Operand = Operation->getNumOperands() ? Operation->getOperand(0) : nullptr;
  switch (Operation->getOpcode()) {
  case Instruction::Trunc:
    return byteOf(Operand, Byte, Depth + 1);
  case Instruction::SExt:
  case Instruction::ZExt:
    if (Operand->getType()->getIntegerBitWidth() % 8) break;
    if (Byte < byteWidth(Operand)) return byteOf(Operand, Byte, Depth + 1);
    if (Operation->getOpcode() == Instruction::ZExt) return ByteRef{};
    return ByteRef{Operand, -1};
  case Instruction::LShr:
  case Instruction::AShr: {
    if (!Amount || Amount->getZExtValue() % 8) {
      // `ashr x, bits - 1` is the sign byte everywhere.
      if (Operation->getOpcode() == Instruction::AShr && Amount &&
          Amount->getZExtValue() + 1 == Type->getBitWidth())
        return ByteRef{Operand, -1};
      break;
    }
    unsigned Source = Byte + Amount->getZExtValue() / 8;
    if (Source < byteWidth(Operand)) return byteOf(Operand, Source, Depth + 1);
    if (Operation->getOpcode() == Instruction::LShr) return ByteRef{};
    return ByteRef{Operand, -1};
  }
  default:
    break;
  }
  return ByteRef{Candidate, static_cast<int>(Byte)};
}

// A value whose eight bytes are Bytes, built from their common source.
static Value *wordOf(ArrayRef<ByteRef> Bytes, IRBuilder<> &Builder) {
  Value *Source = Bytes[0].Source;
  if (!Source || Bytes[0].Index != 0) return nullptr;
  unsigned Width = byteWidth(Source);
  bool Signed = false, Zero = false;
  for (unsigned Byte = 0; Byte < Bytes.size(); ++Byte) {
    const ByteRef &Ref = Bytes[Byte];
    if (Byte < Width) {
      if (!(Ref == ByteRef{Source, static_cast<int>(Byte)})) return nullptr;
    } else if (Ref == ByteRef{Source, -1}) {
      Signed = true;
    } else if (!Ref.Source) {
      Zero = true;
    } else {
      return nullptr;
    }
  }
  if (Signed && Zero) return nullptr;
  Type *Word = Builder.getIntNTy(8 * Bytes.size());
  if (Width == Bytes.size()) return Source;
  if (Width > Bytes.size()) return Builder.CreateTrunc(Source, Word);
  return Signed ? Builder.CreateSExt(Source, Word) : Builder.CreateZExt(Source, Word);
}

struct Piece {
  StoreInst *Store;
  int64_t Offset;
  unsigned Bytes;
};

// Merge one run of stores to the same base with nothing else touching
// memory between them.
static unsigned mergeRun(SmallVectorImpl<Piece> &Run) {
  unsigned Changed = 0;
  for (size_t First = 0; First < Run.size(); ++First) {
    Piece &Start = Run[First];
    if (!Start.Store) continue;
    SmallVector<size_t, 8> Members;
    SmallVector<ByteRef, 8> Bytes(8);
    SmallVector<bool, 8> Written(8, false);
    bool Valid = true;
    for (size_t Other = 0; Other < Run.size() && Valid; ++Other) {
      Piece &Candidate = Run[Other];
      if (!Candidate.Store) continue;
      int64_t Begin = Candidate.Offset - Start.Offset;
      int64_t End = Begin + Candidate.Bytes;
      if (End <= 0 || Begin >= 8) continue;
      // A partial overlap, or a store before the group that a later group
      // member would be moved over, cannot be merged.
      if (Begin < 0 || End > 8) { Valid = false; break; }
      for (unsigned Byte = 0; Byte < Candidate.Bytes; ++Byte) {
        auto Ref = byteOf(Candidate.Store->getValueOperand(), Byte);
        if (!Ref || Written[Begin + Byte]) { Valid = false; break; }
        Bytes[Begin + Byte] = *Ref;
        Written[Begin + Byte] = true;
      }
      Members.push_back(Other);
    }
    if (!Valid || Members.size() < 2 || llvm::count(Written, false)) continue;
    StoreInst *Last = Run[Members.back()].Store;
    IRBuilder<> Builder(Last);
    Value *Word = wordOf(Bytes, Builder);
    if (!Word) continue;
    Align Alignment = commonAlignment(Start.Store->getAlign(), 0);
    Builder.CreateAlignedStore(Word, Start.Store->getPointerOperand(), Alignment);
    for (size_t Member : Members) {
      Run[Member].Store->eraseFromParent();
      Run[Member].Store = nullptr;
    }
    ++Changed;
  }
  return Changed;
}

static unsigned mergeBlock(BasicBlock &Block, const DataLayout &Layout) {
  unsigned Changed = 0;
  SmallVector<Piece, 8> Run;
  Value *Base = nullptr;
  auto Flush = [&] {
    if (Run.size() > 1) Changed += mergeRun(Run);
    Run.clear();
    Base = nullptr;
  };
  for (Instruction &Instruction : llvm::make_early_inc_range(Block)) {
    auto *Store = dyn_cast<StoreInst>(&Instruction);
    auto *Type = Store ? dyn_cast<IntegerType>(Store->getValueOperand()->getType()) : nullptr;
    if (!Store || !Store->isSimple() || !Type || Type->getBitWidth() % 8 ||
        Type->getBitWidth() > 64 || Store->getPointerAddressSpace() != 0) {
      if (Instruction.mayReadOrWriteMemory() || Instruction.mayHaveSideEffects()) Flush();
      continue;
    }
    int64_t Offset = 0;
    Value *Pointer = GetPointerBaseWithConstantOffset(Store->getPointerOperand(), Offset, Layout);
    if (Base && Pointer != Base) Flush();
    Base = Pointer;
    Run.push_back({Store, Offset, Type->getBitWidth() / 8});
  }
  Flush();
  return Changed;
}

static unsigned mergeLittleEndianWordStores(Module &Module) {
  const DataLayout &Layout = Module.getDataLayout();
  if (!Layout.isLittleEndian()) return 0;
  unsigned Changed = 0;
  for (Function &Function : Module) {
    if (Function.isDeclaration() || Function.hasOptNone() || Function.hasOptSize() ||
        Function.hasMinSize() || Function.hasFnAttribute(Attribute::SanitizeAddress) ||
        Function.hasFnAttribute(Attribute::SanitizeHWAddress) ||
        Function.hasFnAttribute(Attribute::SanitizeMemory) ||
        Function.hasFnAttribute(Attribute::SanitizeThread))
      continue;
    for (BasicBlock &Block : Function) Changed += mergeBlock(Block, Layout);
  }
  return Changed;
}
} // namespace ElisaWordStores
