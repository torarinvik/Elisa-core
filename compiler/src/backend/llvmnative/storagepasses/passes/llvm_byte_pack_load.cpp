// Fold an exact little-endian byte-pack expression into one unaligned i64 load.
// The matcher accepts only eight ordinary i8 loads at consecutive addresses,
// each placed in its own byte lane with zext/shl/or. It is deliberately
// conservative around memory effects and declines debug builds.
//go:build cgo

#include <llvm/ADT/APInt.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/BasicBlock.h>
#include <llvm/IR/DataLayout.h>
#include <llvm/IR/Function.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>
#include <cstdint>
#include <llvm/Transforms/Utils/Local.h>

using namespace llvm;

namespace {

constexpr unsigned ByteCount = 8;
constexpr unsigned WordWidth = 64;
constexpr unsigned MaxMatchNodes = 32;

struct BytePack {
  LoadInst *Loads[ByteCount]{};
  int64_t Offsets[ByteCount]{};
  unsigned Count = 0;
  Value *Base = nullptr;
  BasicBlock *Block = nullptr;
};

bool pointerBaseAndOffset(Value *Ptr, const DataLayout &DL, Value *&Base,
                          int64_t &Offset) {
  if (!Ptr || !Ptr->getType()->isPointerTy())
    return false;
  unsigned Width = DL.getIndexTypeSizeInBits(Ptr->getType());
  if (Width == 0 || Width > 64)
    return false;
  APInt OffsetBits(Width, 0);
  Base = Ptr->stripAndAccumulateConstantOffsets(DL, OffsetBits, true);
  if (!Base || !OffsetBits.isSignedIntN(64))
    return false;
  Offset = OffsetBits.getSExtValue();
  return true;
}

bool matchByteLeaf(Value *V, unsigned &Lane, LoadInst *&Load) {
  Value *ByteValue = V;
  if (auto *Shift = dyn_cast<BinaryOperator>(V)) {
    if (Shift->getOpcode() != Instruction::Shl ||
        !Shift->getType()->isIntegerTy(WordWidth))
      return false;
    auto *Amount = dyn_cast<ConstantInt>(Shift->getOperand(1));
    if (!Amount || Amount->getValue().getActiveBits() > 32)
      return false;
    uint64_t ShiftAmount = Amount->getZExtValue();
    if (ShiftAmount == 0 || ShiftAmount >= WordWidth || ShiftAmount % 8 != 0)
      return false;
    Lane = static_cast<unsigned>(ShiftAmount / 8);
    ByteValue = Shift->getOperand(0);
  } else {
    Lane = 0;
  }

  auto *ZExt = dyn_cast<CastInst>(ByteValue);
  if (!ZExt || ZExt->getOpcode() != Instruction::ZExt ||
      !ZExt->getType()->isIntegerTy(WordWidth) ||
      !ZExt->getSrcTy()->isIntegerTy(8))
    return false;
  Load = dyn_cast<LoadInst>(ZExt->getOperand(0));
  return Load && Load->getType()->isIntegerTy(8) && !Load->isVolatile() &&
         !Load->isAtomic() && !Load->hasMetadataOtherThanDebugLoc();
}

bool collectByteLeaves(Value *Root, BytePack &Pack) {
  SmallVector<Value *, 16> Worklist{Root};
  unsigned Visited = 0;
  while (!Worklist.empty()) {
    Value *V = Worklist.pop_back_val();
    if (++Visited > MaxMatchNodes)
      return false;
    if (auto *Or = dyn_cast<BinaryOperator>(V)) {
      if (Or->getOpcode() == Instruction::Or &&
          Or->getType()->isIntegerTy(WordWidth)) {
        Worklist.push_back(Or->getOperand(0));
        Worklist.push_back(Or->getOperand(1));
        continue;
      }
    }
    unsigned Lane = 0;
    LoadInst *Load = nullptr;
    if (!matchByteLeaf(V, Lane, Load) || Lane >= ByteCount ||
        Pack.Loads[Lane])
      return false;
    if (Pack.Count == 0) {
      Pack.Block = Load->getParent();
    } else if (Load->getParent() != Pack.Block) {
      return false;
    }
    Pack.Loads[Lane] = Load;
    ++Pack.Count;
  }
  return Pack.Count == ByteCount;
}

bool matchBytePack(BinaryOperator *Root, const DataLayout &DL,
                   BytePack &Pack) {
  if (!Root || Root->getOpcode() != Instruction::Or ||
      !Root->getType()->isIntegerTy(WordWidth) ||
      !collectByteLeaves(Root, Pack) || Root->getParent() != Pack.Block)
    return false;

  int64_t StartOffset = 0;
  for (unsigned Lane = 0; Lane < ByteCount; ++Lane) {
    LoadInst *Load = Pack.Loads[Lane];
    if (!Load || !Load->comesBefore(Root))
      return false;
    Value *Base = nullptr;
    int64_t Offset = 0;
    if (!pointerBaseAndOffset(Load->getPointerOperand(), DL, Base, Offset))
      return false;
    if (Lane == 0) {
      Pack.Base = Base;
      StartOffset = Offset;
    }
    if (Base != Pack.Base || StartOffset > INT64_MAX - (ByteCount - 1) ||
        Offset != StartOffset + static_cast<int64_t>(Lane))
      return false;
    Pack.Offsets[Lane] = Offset;
  }

  // The replacement executes at Root, after all original byte loads. Do not
  // cross writes, calls, volatile operations, atomic operations, or fences:
  // each could make the eight original observations differ from one load.
  LoadInst *First = Pack.Loads[0];
  for (unsigned Lane = 1; Lane < ByteCount; ++Lane)
    if (Pack.Loads[Lane]->comesBefore(First))
      First = Pack.Loads[Lane];
  for (Instruction *I = First; I; I = I->getNextNode()) {
    if (auto *Load = dyn_cast<LoadInst>(I)) {
      if (Load->isVolatile() || Load->isAtomic())
        return false;
    }
    if (auto *Store = dyn_cast<StoreInst>(I))
      if (Store->isVolatile() || Store->isAtomic())
        return false;
    if (isa<CallBase>(I) || isa<FenceInst>(I) || I->mayWriteToMemory())
      return false;
    if (I == Root)
      return true;
  }
  return false;
}

} // namespace

extern "C" unsigned
elisacoreFoldLittleEndianBytePacks(LLVMModuleRef ModuleRef) {
  if (!ModuleRef)
    return 0;
  Module *M = unwrap(ModuleRef);
  const DataLayout &DL = M->getDataLayout();
  if (DL.isDefault() || !DL.isLittleEndian())
    return 0;

  unsigned Rewritten = 0;
  for (Function &F : *M) {
    if (F.isDeclaration())
      continue;
    for (BasicBlock &BB : F) {
      SmallVector<BinaryOperator *, 8> Roots;
      for (Instruction &I : BB) {
        auto *Root = dyn_cast<BinaryOperator>(&I);
        BytePack Pack;
        if (matchBytePack(Root, DL, Pack))
          Roots.push_back(Root);
      }
      // Match the whole block before deleting dead expression trees. Mutating
      // an intrusive instruction list while searching can skip later packs.
      for (BinaryOperator *Root : Roots) {
        if (!Root->getParent())
          continue;
        BytePack Pack;
        if (!matchBytePack(Root, DL, Pack))
          continue;

        IRBuilder<> Builder(Root);
        auto *WideLoad = Builder.CreateLoad(
            Type::getInt64Ty(M->getContext()), Pack.Loads[0]->getPointerOperand(),
            "elisa.bytepack.load");
        WideLoad->setAlignment(Align(1));
        WideLoad->setDebugLoc(Root->getDebugLoc());
        Root->replaceAllUsesWith(WideLoad);
        RecursivelyDeleteTriviallyDeadInstructions(Root);
        ++Rewritten;
      }
    }
  }
  return Rewritten;
}
