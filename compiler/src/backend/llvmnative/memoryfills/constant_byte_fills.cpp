//go:build cgo
// Expand bounded constant-byte fills without writing outside their range.
#include <cstdint>
#include <llvm-c/Core.h>
#include <llvm/Config/llvm-config.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/AsmParser/Parser.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Linker/Linker.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/Transforms/Utils/Cloning.h>

using namespace llvm;

#include "llvm_word_pattern_fill.h"

extern "C" unsigned elisacoreExpandSmallZeroFills(LLVMModuleRef ref) {
  if (!ref) return 0;
  Module &M = *unwrap(ref);
  if (M.getTargetTriple().getArch() != Triple::aarch64 || !M.getDataLayout().isLittleEndian() || M.getNamedValue("__elisacore_smallzero_fill")) return 0;
  // Inline medium, aligned fixed-size fills on Apple AArch64. LLVM otherwise
  // lowers these to a library call. Keep the size bounded to control code growth.
  unsigned fixedChanged = inlineStackWordPatternFills(M);
  if (M.getTargetTriple().isMacOSX()) {
    SmallVector<MemSetInst *, 8> fixed;
    for (Function &F : M) {
      if (F.hasOptNone() || F.hasOptSize() || F.hasMinSize()) continue;
      SmallVector<MemSetInst *, 2> candidates;
      for (BasicBlock &B : F) for (Instruction &I : B) {
        auto *S = dyn_cast<MemSetInst>(&I);
        if (!S || S->getIntrinsicID() != Intrinsic::memset || S->isVolatile() ||
            S->hasOperandBundles() || S->getDestAddressSpace() != 0 ||
            S->getDestAlign().valueOrOne().value() < 8) continue;
        auto *size = dyn_cast<ConstantInt>(S->getLength());
        auto *value = dyn_cast<ConstantInt>(S->getValue());
        if (!size || !value || !value->isZero() || size->getBitWidth() != 64) continue;
        uint64_t bytes = size->getZExtValue();
        if (bytes < 1024 || bytes > 2048 || bytes % 128 != 0) continue;
        candidates.push_back(S);
      }
      if (candidates.size() == 1) fixed.push_back(candidates.front());
    }
    for (MemSetInst *S : fixed) {
      IRBuilder<> builder(S);
      auto *replacement = builder.CreateMemSetInline(S->getDest(), S->getDestAlign(),
                                                       S->getValue(), S->getLength());
      replacement->setDebugLoc(S->getDebugLoc());
      replacement->copyMetadata(*S);
      S->eraseFromParent();
      ++fixedChanged;
    }
  }
  SmallVector<MemSetInst *, 16> work;
  SmallVector<MemSetInst *, 17> nonzeroFills;
  for (Function &F : M) {
    if (F.hasOptNone() || F.hasOptSize() || F.hasMinSize()) continue;
    for (BasicBlock &B : F) for (Instruction &I : B) {
      auto *S = dyn_cast<MemSetInst>(&I);
      if (S && !S->hasOperandBundles() && !S->getMetadata("elisa.smallzero.fallback") && !S->isVolatile() && S->getDestAddressSpace() == 0 &&
          S->getLength()->getType()->isIntegerTy(64) && !isa<ConstantInt>(S->getLength())) {
        auto *V = dyn_cast<ConstantInt>(S->getValue());
        if (!V) continue;
        if (!V->isZero()) {
          // Each inline expansion adds several branches. Bound code growth
          // for the additional nonzero-fill sites.
          if (nonzeroFills.size() <= 16 &&
              !S->getMetadata(LLVMContext::MD_nontemporal) &&
              !F.hasFnAttribute(Attribute::SanitizeAddress) &&
              !F.hasFnAttribute(Attribute::SanitizeHWAddress) &&
              !F.hasFnAttribute(Attribute::SanitizeMemory) &&
              !F.hasFnAttribute(Attribute::SanitizeThread))
            nonzeroFills.push_back(S);
        } else {
          work.push_back(S);
        }
      }
    }
  }
  if (nonzeroFills.size() <= 16) work.append(nonzeroFills);
  if (work.empty()) return fixedChanged;
  SMDiagnostic error;
  // The assembly parser needs local names to resolve the helper's operands.
  // Native object contexts discard names; restore that policy immediately.
  bool discardNames = M.getContext().shouldDiscardValueNames();
  M.getContext().setDiscardValueNames(false);
  auto helper = parseAssemblyString(R"ELISA(define void @__elisacore_smallzero_fill(ptr %p, i64 %n, i64 %pattern) alwaysinline {
entry:
 %byte = trunc i64 %pattern to i8
 %word = trunc i64 %pattern to i16
 %dword = trunc i64 %pattern to i32
 %half = zext i64 %pattern to i128
 %high = shl i128 %half, 64
 %vector = or i128 %high, %half
 %empty = icmp eq i64 %n, 0
 br i1 %empty, label %exit, label %choose
choose:
 %small = icmp ule i64 %n, 64
 br i1 %small, label %size8, label %large
large:
 call void @llvm.memset.p0.i64(ptr %p, i8 %byte, i64 %n, i1 false), !elisa.smallzero.fallback !0
 br label %exit
size8:
 %eight = icmp uge i64 %n, 8
 br i1 %eight, label %size16, label %size4
size16:
 %sixteen = icmp uge i64 %n, 16
 br i1 %sixteen, label %size32, label %fill8
fill8:
 %off8 = sub i64 %n, 8
 %end8 = getelementptr i8, ptr %p, i64 %off8
 store i64 %pattern, ptr %p, align 1
 store i64 %pattern, ptr %end8, align 1
 br label %exit
size32:
 %big = icmp ugt i64 %n, 32
 br i1 %big, label %fill64, label %fill16
fill16:
 %off16 = sub i64 %n, 16
 %end16 = getelementptr i8, ptr %p, i64 %off16
 store i128 %vector, ptr %p, align 1
 store i128 %vector, ptr %end16, align 1
 br label %exit
fill64:
 %p16 = getelementptr i8, ptr %p, i64 16
 %off32 = sub i64 %n, 32
 %end32 = getelementptr i8, ptr %p, i64 %off32
 %offlast = sub i64 %n, 16
 %last = getelementptr i8, ptr %p, i64 %offlast
 store i128 %vector, ptr %p, align 1
 store i128 %vector, ptr %p16, align 1
 store i128 %vector, ptr %end32, align 1
 store i128 %vector, ptr %last, align 1
 br label %exit
size4:
 %four = icmp uge i64 %n, 4
 br i1 %four, label %fill4, label %size2
fill4:
 %off4 = sub i64 %n, 4
 %end4 = getelementptr i8, ptr %p, i64 %off4
 store i32 %dword, ptr %p, align 1
 store i32 %dword, ptr %end4, align 1
 br label %exit
size2:
 %two = icmp uge i64 %n, 2
 br i1 %two, label %fill2, label %fill1
fill2:
 %off2 = sub i64 %n, 2
 %end2 = getelementptr i8, ptr %p, i64 %off2
 store i16 %word, ptr %p, align 1
 store i16 %word, ptr %end2, align 1
 br label %exit
fill1:
 store i8 %byte, ptr %p, align 1
 br label %exit
exit:
 ret void
}

declare void @llvm.memset.p0.i64(ptr, i8, i64, i1 immarg)
!0 = !{}
)ELISA", error, M.getContext());
  M.getContext().setDiscardValueNames(discardNames);
  if (!helper) return fixedChanged;
  helper->setDataLayout(M.getDataLayout());
  helper->setTargetTriple(M.getTargetTriple());
  if (Linker::linkModules(M, std::move(helper))) return fixedChanged;
  Function *F = M.getFunction("__elisacore_smallzero_fill");
  if (!F) return fixedChanged;
  F->setLinkage(GlobalValue::InternalLinkage);
  unsigned changed = 0;
  for (MemSetInst *S : work) {
    IRBuilder<> builder(S);
    uint64_t byte = cast<ConstantInt>(S->getValue())->getZExtValue();
    Value *pattern = builder.getInt64(byte * 0x0101010101010101ULL);
    auto *call = builder.CreateCall(F, {S->getDest(), S->getLength(), pattern});
    call->setDebugLoc(S->getDebugLoc());
    InlineFunctionInfo info;
    if (InlineFunction(*call, info).isSuccess()) {
      S->eraseFromParent();
      ++changed;
    } else call->eraseFromParent();
  }
  if (F->use_empty()) F->eraseFromParent();
  return changed + fixedChanged;
}
