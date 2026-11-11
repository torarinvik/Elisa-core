//go:build cgo

// A guarded signed value can be converted to its narrow unsigned representation
// by masking, rather than by adding the narrow modulus on the negative path.
#include <llvm/Config/llvm-config.h>
#include <llvm-c/Core.h>
#if LLVM_VERSION_MAJOR >= 23
#include <llvm/ADT/DenseMap.h>
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/IR/ConstantRange.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>

using namespace llvm;

namespace {
bool guardBoundsSignedValue(LoadInst &Input, Instruction &At, unsigned Width,
                            DominatorTree &DT) {
  unsigned Bits = Input.getType()->getIntegerBitWidth();
  APInt Low = APInt::getSignedMinValue(Width).sext(Bits);
  APInt High = APInt::getSignedMaxValue(Width).sext(Bits);
  for (User *User : Input.users()) {
    auto *Bias = dyn_cast<BinaryOperator>(User);
    if (!Bias || Bias->getOpcode() != Instruction::Add)
      continue;
    auto *Offset = dyn_cast<ConstantInt>(
        Bias->getOperand(Bias->getOperand(0) == &Input ? 1 : 0));
    if (!Offset)
      continue;
    for (auto *Use : Bias->users()) {
      auto *Compare = dyn_cast<ICmpInst>(Use);
      if (!Compare || Compare->getOperand(0) != Bias)
        continue;
      auto *Limit = dyn_cast<ConstantInt>(Compare->getOperand(1));
      if (!Limit)
        continue;
      for (auto *CompareUse : Compare->users()) {
        auto *Branch = dyn_cast<CondBrInst>(CompareUse);
        if (!Branch || Branch->getSuccessor(0) == Branch->getSuccessor(1))
          continue;
        for (unsigned Edge = 0; Edge != 2; ++Edge) {
          if (!DT.dominates(BasicBlockEdge(Branch->getParent(), Branch->getSuccessor(Edge)),
                            At.getParent()))
            continue;
          auto Predicate = Edge == 0 ? Compare->getPredicate() : Compare->getInversePredicate();
          ConstantRange Range = ConstantRange::makeExactICmpRegion(Predicate, Limit->getValue())
                                    .subtract(Offset->getValue());
          if (!Range.isEmptySet() && Range.getSignedMin().sge(Low) &&
              Range.getSignedMax().sle(High))
            return true;
        }
      }
    }
  }
  return false;
}

struct SignedNormalization {
  BinaryOperator *Sum;
  BinaryOperator *SignMask;
  BinaryOperator *SignShift;
  LoadInst *Input;
  unsigned Width;
};

bool matchSignedNormalization(BinaryOperator &Sum, SignedNormalization &Match) {
  if (Sum.getOpcode() != Instruction::Add || !Sum.getType()->isIntegerTy(64) ||
      Sum.hasMetadataOtherThanDebugLoc())
    return false;
  for (unsigned Op = 0; Op != 2; ++Op) {
    auto *Input = dyn_cast<LoadInst>(Sum.getOperand(Op));
    auto *Mask = dyn_cast<BinaryOperator>(Sum.getOperand(1 - Op));
    if (!Input || !Input->isSimple() || !Mask || Mask->getOpcode() != Instruction::And)
      continue;
    auto *Bit = dyn_cast<ConstantInt>(Mask->getOperand(1));
    auto *Shift = dyn_cast<BinaryOperator>(Mask->getOperand(0));
    if (!Bit || !Bit->getValue().isPowerOf2() || !Shift ||
        Shift->getOpcode() != Instruction::LShr || Shift->getOperand(0) != Input)
      continue;
    unsigned Width = Bit->getValue().logBase2();
    auto *Distance = dyn_cast<ConstantInt>(Shift->getOperand(1));
    if (Width != 32 || !Distance || !Distance->equalsInt(63 - Width))
      continue;
    Match = {&Sum, Mask, Shift, Input, Width};
    return true;
  }
  return false;
}
}

extern "C" unsigned elisacoreFoldSignedRangeMasks(LLVMModuleRef Ref) {
  if (!Ref)
    return 0;
  Module &M = *unwrap(Ref);
  if (!M.getTargetTriple().isAArch64() || M.getNamedMetadata("llvm.dbg.cu"))
    return 0;
  for (StringRef Flag : {"SanitizeAddress", "SanitizeHWAddress", "SanitizeMemory",
                         "SanitizeThread", "SanitizeRealtime"})
    if (M.getModuleFlag(Flag))
      return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.getSubprogram() || F.getInstructionCount() > 2000)
      continue;
    bool Sanitized = false;
    for (Attribute::AttrKind Kind : {Attribute::SanitizeAddress, Attribute::SanitizeHWAddress,
         Attribute::SanitizeMemory, Attribute::SanitizeThread, Attribute::SanitizeMemTag,
         Attribute::SanitizeRealtime, Attribute::SanitizeRealtimeBlocking})
      Sanitized |= F.hasFnAttribute(Kind);
    if (Sanitized)
      continue;
    SmallVector<SignedNormalization, 16> Candidates;
    for (BasicBlock &B : F)
      for (Instruction &I : B) {
        SignedNormalization Match;
        if (auto *Sum = dyn_cast<BinaryOperator>(&I); Sum && matchSignedNormalization(*Sum, Match))
          Candidates.push_back(Match);
      }
    if (Candidates.empty() || Candidates.size() > 64)
      continue;
    DominatorTree DT(F);
    LoopInfo LI(DT);
    SmallVector<SignedNormalization, 16> Proven;
    for (const auto &Match : Candidates)
      if (LI.getLoopFor(Match.Sum->getParent()) &&
          guardBoundsSignedValue(*Match.Input, *Match.Sum, Match.Width, DT))
        Proven.push_back(Match);

    DenseMap<LoadInst *, Value *> Frozen;
    for (const auto &Match : Proven) {
      auto [Entry, Inserted] = Frozen.try_emplace(Match.Input, nullptr);
      if (Inserted) {
        // The range check and the mask must observe the same choice for undef.
        IRBuilder<> LoadBuilder(Match.Input->getNextNode());
        auto *Stable = cast<FreezeInst>(LoadBuilder.CreateFreeze(Match.Input, "signed.range.value"));
        Match.Input->replaceUsesWithIf(Stable, [&](Use &U) { return U.getUser() != Stable; });
        Entry->second = Stable;
      }
      IRBuilder<> Builder(Match.Sum);
      auto *Type = cast<IntegerType>(Match.Sum->getType());
      Value *Masked = Builder.CreateAnd(Entry->second,
          ConstantInt::get(Type, APInt::getLowBitsSet(Type->getBitWidth(), Match.Width)),
          "signed.range.mask");
      Match.Sum->replaceAllUsesWith(Masked);
      Match.Sum->eraseFromParent();
      ++Changed;
    }
    SmallPtrSet<BinaryOperator *, 16> Masks, Shifts;
    for (const auto &Match : Proven) {
      Masks.insert(Match.SignMask);
      Shifts.insert(Match.SignShift);
    }
    for (auto *Mask : Masks)
      if (Mask->use_empty())
        Mask->eraseFromParent();
    for (auto *Shift : Shifts)
      if (Shift->use_empty())
        Shift->eraseFromParent();
  }
  return Changed;
}
#else
extern "C" unsigned elisacoreFoldSignedRangeMasks(LLVMModuleRef) { return 0; }
#endif
