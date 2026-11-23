// Expand a memcpy or memmove whose length is not a constant but has a small
// proven upper bound into fixed-size loads and stores. The backend calls the
// C library for every variable-length copy; a row of at most a few dozen
// bytes then pays a call, the library's size dispatch and the callee-saved
// spills around the call. A length in [C, 2C] is copied as two C-byte
// chunks, one at the start and one ending at the last byte, which overlap
// when the length is below 2C. Both chunks are loaded before either is
// stored, so the expansion is also correct for an overlapping memmove.
// The length's bound comes from the comparisons on every path to the copy
// (ElisaPathRanges), through constant scalings, and from the size of an
// alloca the copy reads or writes; only the lengths its known bits allow
// get a chunk size.
#include <llvm/Analysis/AssumptionCache.h>
#include <llvm/Analysis/TargetLibraryInfo.h>
#include <llvm/IR/IntrinsicInst.h>
#include "llvm_dead_trap_exits.h"
#include "llvm_path_ranges.h"
namespace ElisaBoundedCopies {
using namespace llvm;

constexpr uint64_t LargestExpandedLength = 32;
constexpr unsigned ChunkSizes[] = {16, 8, 4, 2, 1};

struct BoundedCopy {
  MemTransferInst *copy;
  SmallVector<uint64_t, 8> lengths; // every length the copy can have, ascending
};

// [Low << K, High << K] when the shift loses no bits of High.
static ConstantRange scaled(const ConstantRange &Range, const APInt &Factor, bool Shift) {
  unsigned Width = Range.getBitWidth();
  if (Range.isEmptySet() || (Shift && Factor.uge(Width))) return ConstantRange::getFull(Width);
  bool Overflow = false;
  APInt High = Shift ? Range.getUnsignedMax().ushl_ov(Factor.getZExtValue(), Overflow)
                     : Range.getUnsignedMax().umul_ov(Factor.zextOrTrunc(Width), Overflow);
  if (Overflow || High.isMaxValue()) return ConstantRange::getFull(Width);
  APInt Low = Shift ? Range.getUnsignedMin().shl(Factor.getZExtValue())
                    : Range.getUnsignedMin() * Factor.zextOrTrunc(Width);
  return ConstantRange::getNonEmpty(Low, High + 1);
}

// The unsigned values Length can have at Block.
static ConstantRange lengthRange(Value *Length, BasicBlock *Block,
                                 ElisaPathRanges::PathRanges &Paths, unsigned Depth = 0) {
  using namespace PatternMatch;
  unsigned Width = Length->getType()->getIntegerBitWidth();
  if (auto *Constant = dyn_cast<ConstantInt>(Length)) return ConstantRange(Constant->getValue());
  ConstantRange Range = Paths.at(Length, Block);
  Value *Inner = nullptr;
  const APInt *Factor = nullptr;
  if (Depth >= 4) return Range;
  if (match(Length, m_NUWShl(m_Value(Inner), m_APInt(Factor))))
    Range = Range.intersectWith(scaled(lengthRange(Inner, Block, Paths, Depth + 1), *Factor, true),
                                ConstantRange::Unsigned);
  else if (match(Length, m_NUWMul(m_Value(Inner), m_APInt(Factor))) ||
           match(Length, m_NUWMul(m_APInt(Factor), m_Value(Inner))))
    Range = Range.intersectWith(scaled(lengthRange(Inner, Block, Paths, Depth + 1), *Factor, false),
                                ConstantRange::Unsigned);
  else if (match(Length, m_ZExt(m_Value(Inner))))
    Range = Range.intersectWith(lengthRange(Inner, Block, Paths, Depth + 1).zeroExtend(Width),
                                ConstantRange::Unsigned);
  return Range;
}

// The bytes left in the alloca Pointer points into, or the largest length.
static uint64_t allocaRoom(Value *Pointer, const DataLayout &Layout) {
  int64_t Offset = 0;
  auto *Base = dyn_cast<AllocaInst>(GetPointerBaseWithConstantOffset(Pointer, Offset, Layout));
  if (!Base || Offset < 0) return UINT64_MAX;
  std::optional<TypeSize> Size = Base->getAllocationSize(Layout);
  if (!Size || Size->isScalable() || uint64_t(Offset) > Size->getFixedValue()) return UINT64_MAX;
  return Size->getFixedValue() - uint64_t(Offset);
}

static bool candidate(MemTransferInst &Copy) {
  auto ID = Copy.getIntrinsicID();
  return (ID == Intrinsic::memcpy || ID == Intrinsic::memmove) && !Copy.isVolatile() &&
         !isa<ConstantInt>(Copy.getLength()) && Copy.getDestAddressSpace() == 0 &&
         Copy.getSourceAddressSpace() == 0 && !Copy.hasOperandBundles();
}

// Every length in the copy's range that its known bits allow, when at
// most LargestExpandedLength.
static bool bound(BoundedCopy &Bounded, ElisaPathRanges::PathRanges &Paths, const DataLayout &Layout) {
  MemTransferInst &Copy = *Bounded.copy;
  Value *Length = Copy.getLength();
  ConstantRange Range = lengthRange(Length, Copy.getParent(), Paths);
  uint64_t Room = std::min(allocaRoom(Copy.getRawDest(), Layout), allocaRoom(Copy.getRawSource(), Layout));
  uint64_t High = std::min(Range.getUnsignedMax().getLimitedValue(), Room);
  if (Range.isEmptySet() || High > LargestExpandedLength) return false;
  KnownBits Known = computeKnownBits(Length, Layout);
  for (uint64_t N = Range.getUnsignedMin().getLimitedValue(); N <= High; ++N) {
    APInt Value(Known.getBitWidth(), N);
    if ((Value & Known.Zero).isZero() && (Value & Known.One) == Known.One) Bounded.lengths.push_back(N);
  }
  return Bounded.lengths.size() > 1;
}

// Copy the lengths in [Chunk, 2 Chunk] as two Chunk-byte pieces, or one
// piece when Chunk is the only such length.
static void emitChunks(IRBuilder<> &Builder, MemTransferInst &Copy, unsigned Chunk, bool Single) {
  Type *Piece = Builder.getIntNTy(Chunk * 8);
  Value *Length = Copy.getLength();
  Align DestAlign = commonAlignment(Copy.getDestAlign().valueOrOne(), Chunk);
  Align SourceAlign = commonAlignment(Copy.getSourceAlign().valueOrOne(), Chunk);
  Value *Head = Builder.CreateAlignedLoad(Piece, Copy.getRawSource(), SourceAlign);
  if (Single) {
    Builder.CreateAlignedStore(Head, Copy.getRawDest(), DestAlign);
    return;
  }
  Value *TailOffset = Builder.CreateSub(Length, ConstantInt::get(Length->getType(), Chunk));
  Value *TailSource = Builder.CreateGEP(Builder.getInt8Ty(), Copy.getRawSource(), TailOffset);
  Value *Tail = Builder.CreateAlignedLoad(Piece, TailSource, Align(1));
  Builder.CreateAlignedStore(Head, Copy.getRawDest(), DestAlign);
  Value *TailDest = Builder.CreateGEP(Builder.getInt8Ty(), Copy.getRawDest(), TailOffset);
  Builder.CreateAlignedStore(Tail, TailDest, Align(1));
}

// Whether a length is copied as Chunk-byte pieces: [Chunk, 2 Chunk), and
// up to the largest expanded length for the largest chunk.
static bool inTier(uint64_t Length, unsigned Chunk) {
  return Length >= Chunk && (Chunk == ChunkSizes[0] ? Length <= LargestExpandedLength : Length < 2 * Chunk);
}

// Dispatch on the length from the largest chunk size down; a length below
// every reachable chunk size (zero) copies nothing.
static void expand(BoundedCopy &Bounded) {
  MemTransferInst &Copy = *Bounded.copy;
  BasicBlock *Test = Copy.getParent();
  BasicBlock *After = Test->splitBasicBlock(&Copy, "bounded.copy.done");
  Test->getTerminator()->eraseFromParent();
  Function &F = *Test->getParent();
  Value *Length = Copy.getLength();
  SmallVector<uint64_t, 8> &Lengths = Bounded.lengths;
  for (unsigned Chunk : ChunkSizes) {
    auto InTier = [&](uint64_t N) { return inTier(N, Chunk); };
    if (!llvm::any_of(Lengths, InTier)) continue;
    bool Single = llvm::all_of(Lengths, [&](uint64_t N) { return !InTier(N) || N == Chunk; });
    BasicBlock *Body = BasicBlock::Create(F.getContext(), "bounded.copy." + Twine(Chunk), &F, After);
    IRBuilder<> Builder(Body);
    Builder.SetCurrentDebugLocation(Copy.getDebugLoc());
    emitChunks(Builder, Copy, Chunk, Single);
    Builder.CreateBr(After);
    llvm::erase_if(Lengths, InTier);
    Builder.SetInsertPoint(Test);
    if (Lengths.empty()) {
      Builder.CreateBr(Body);
      Test = nullptr;
      break;
    }
    BasicBlock *Next = BasicBlock::Create(F.getContext(), "bounded.copy.below." + Twine(Chunk), &F, Body);
    Builder.CreateCondBr(Builder.CreateICmpUGE(Length, ConstantInt::get(Length->getType(), Chunk)), Body, Next);
    Test = Next;
  }
  if (Test) IRBuilder<>(Test).CreateBr(After);
  Copy.eraseFromParent();
}

static unsigned expandFunction(Function &F) {
  if (F.isDeclaration() || F.hasOptNone() || F.hasMinSize()) return 0;
  SmallVector<BoundedCopy, 4> Copies;
  for (Instruction &I : instructions(F))
    if (auto *Copy = dyn_cast<MemTransferInst>(&I); Copy && candidate(*Copy))
      Copies.push_back({Copy, {}});
  if (Copies.empty()) return 0;
  const DataLayout &Layout = F.getParent()->getDataLayout();
  DominatorTree Tree(F);
  LoopInfo Loops(Tree);
  TargetLibraryInfoImpl LibraryInfo(F.getParent()->getTargetTriple());
  TargetLibraryInfo Library(LibraryInfo);
  AssumptionCache Assumptions(F);
  ScalarEvolution Evolution(F, Library, Assumptions, Tree, Loops);
  ElisaPathRanges::PathRanges Paths(Evolution, Tree, Loops);
  SmallVector<BoundedCopy, 4> Bounded;
  for (BoundedCopy &Copy : Copies)
    if (bound(Copy, Paths, Layout)) Bounded.push_back(std::move(Copy));
  for (BoundedCopy &Copy : Bounded) expand(Copy);
  return Bounded.size();
}

static unsigned expandBoundedCopies(Module &M) {
  unsigned Changed = 0;
  for (Function &F : M) Changed += expandFunction(F);
  return Changed;
}
} // namespace ElisaBoundedCopies

// Runs just before the trap exit edges are connected (llvm_target.go).
extern "C" unsigned elisacoreExpandBoundedCopies(LLVMModuleRef Module) {
  return Module ? ElisaBoundedCopies::expandBoundedCopies(*llvm::unwrap(Module)) : 0;
}
