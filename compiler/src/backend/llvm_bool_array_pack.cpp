//go:build cgo
// Keep small, nonescaping Boolean arrays in an integer register. Byte addresses
// are not observable outside the accepted load/store/lifetime use graph.
#include <llvm-c/Core.h>
#include <llvm-c/Error.h>
#include <llvm-c/TargetMachine.h>
#include <llvm-c/Transforms/PassBuilder.h>
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/Analysis/Utils/Local.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>
#include <functional>
using namespace llvm;
namespace {
struct BoolArrayPlan {
  AllocaInst *array;
  unsigned length;
  SmallVector<GetElementPtrInst *, 16> geps;
  SmallVector<Instruction *, 32> accesses;
};
static bool planArray(AllocaInst &A, const DataLayout &DL, BoolArrayPlan &P) {
  auto *T = dyn_cast<ArrayType>(A.getAllocatedType());
  auto *count = dyn_cast<ConstantInt>(A.getArraySize());
  if (!A.isStaticAlloca() || A.getAddressSpace() != 0 || !count || !count->isOne() ||
      !T || !T->getElementType()->isIntegerTy(1) || T->getNumElements() < 2 ||
      T->getNumElements() > 128 || A.isUsedByMetadata()) return false;
  if (DL.getTypeAllocSize(T).getFixedValue() != T->getNumElements()) return false;
  P.array = &A;
  P.length = T->getNumElements();
  SmallVector<Value *, 16> work{&A};
  SmallPtrSet<Value *, 32> seen;
  while (!work.empty()) {
    Value *pointer = work.pop_back_val();
    if (!seen.insert(pointer).second) continue;
    if (pointer->isUsedByMetadata()) return false;
    for (User *U : pointer->users()) {
      auto *I = dyn_cast<Instruction>(U);
      if (!I || I->hasMetadataOtherThanDebugLoc()) return false;
      if (auto *G = dyn_cast<GetElementPtrInst>(I)) {
        if (G->getPointerOperand() != pointer) return false;
        // Plain GEPs may leave the allocation and return, including through
        // wrapping arithmetic. Only the final load/store must be within this
        // alloca on a defined execution; no address escapes or is observed.
        // offsetOf reproduces the full wrapping computation without assuming
        // intermediate GEPs are inbounds.
        P.geps.push_back(G);
        work.push_back(G);
      } else if (auto *L = dyn_cast<LoadInst>(I)) {
        if (L->getPointerOperand() != pointer || !L->isSimple() ||
            !L->getType()->isIntegerTy(1)) return false;
        P.accesses.push_back(L);
      } else if (auto *S = dyn_cast<StoreInst>(I)) {
        if (S->getPointerOperand() != pointer || !S->isSimple()) return false;
        if (!S->getValueOperand()->getType()->isIntegerTy(1)) {
          auto *C = dyn_cast<ConstantInt>(S->getValueOperand());
          if (pointer != &A || !C || !C->isZero() ||
              C->getBitWidth() != P.length * 8) return false;
        }
        P.accesses.push_back(S);
      } else if (auto *S = dyn_cast<MemSetInst>(I)) {
        auto *V = dyn_cast<ConstantInt>(S->getValue());
        auto *N = dyn_cast<ConstantInt>(S->getLength());
        int64_t offset = 0;
        if (S->getRawDest() != pointer || S->isVolatile() || S->hasOperandBundles() ||
            !V || !V->isZero() || !N ||
            GetPointerBaseWithConstantOffset(pointer, offset, DL) != &A ||
            offset < 0 || uint64_t(offset) > P.length ||
            N->getLimitedValue() > P.length - uint64_t(offset)) return false;
        P.accesses.push_back(S);
      } else if (auto *II = dyn_cast<IntrinsicInst>(I)) {
        if (pointer != &A || II->hasOperandBundles() ||
            (II->getIntrinsicID() != Intrinsic::lifetime_start &&
             II->getIntrinsicID() != Intrinsic::lifetime_end)) return false;
        P.accesses.push_back(II);
      } else return false;
    }
  }
  return !P.accesses.empty();
}
static void packArray(BoolArrayPlan &P, const DataLayout &DL) {
  AllocaInst *A = P.array;
  IRBuilder<> entry(A);
  auto *word = IntegerType::get(A->getContext(), P.length > 64 ? 128 : 64);
  auto *indexType = Type::getInt64Ty(A->getContext());
  auto *slot = entry.CreateAlloca(word, nullptr, A->getName() + ".bits");
  slot->setAlignment(Align(8));
  // Choosing zero for initially undefined bits is a refinement. It also keeps
  // a partial initialization from propagating undef into unrelated elements.
  entry.CreateStore(ConstantInt::get(word, 0), slot);
  // Pointer offsets wrap at the target index width, including for chained
  // GEPs. Widen only the final offset used by the packed-word shift.
  DenseMap<Value *, Value *> offsets;
  offsets[A] = ConstantInt::get(indexType, 0);
  std::function<Value *(Value *)> offsetOf = [&](Value *pointer) -> Value * {
    auto found = offsets.find(pointer);
    if (found != offsets.end()) return found->second;
    auto *G = cast<GetElementPtrInst>(pointer);
    Value *base = offsetOf(G->getPointerOperand());
    IRBuilder<> B(G);
    Value *delta = emitGEPOffset(&B, DL, G, true);
    return offsets[pointer] = B.CreateAdd(base, delta, "bool.index");
  };
  for (Instruction *I : P.accesses) {
    IRBuilder<> B(I);
    B.SetCurrentDebugLocation(I->getDebugLoc());
    if (auto *L = dyn_cast<LoadInst>(I)) {
      Value *bits = B.CreateLoad(word, slot);
      Value *bit = B.CreateLShr(bits, B.CreateZExt(offsetOf(L->getPointerOperand()), word));
      L->replaceAllUsesWith(B.CreateTrunc(bit, L->getType()));
    } else if (auto *S = dyn_cast<StoreInst>(I)) {
      if (!S->getValueOperand()->getType()->isIntegerTy(1)) {
        B.CreateStore(ConstantInt::get(word, 0), slot);
      } else {
        Value *index = B.CreateZExt(offsetOf(S->getPointerOperand()), word);
        Value *bits = B.CreateLoad(word, slot);
        Value *mask = B.CreateShl(ConstantInt::get(word, 1), index);
        Value *cleared = B.CreateAnd(bits, B.CreateNot(mask));
        // A poison stored in one byte must not poison the other array elements.
        // Freeze chooses a permissible value for that element alone.
        Value *value = B.CreateFreeze(S->getValueOperand());
        Value *inserted = B.CreateShl(B.CreateZExt(value, word), index);
        B.CreateStore(B.CreateOr(cleared, inserted), slot);
      }
    } else if (auto *S = dyn_cast<MemSetInst>(I)) {
      int64_t start = 0;
      GetPointerBaseWithConstantOffset(S->getRawDest(), start, DL);
      unsigned count = cast<ConstantInt>(S->getLength())->getZExtValue();
      APInt mask = APInt::getBitsSet(word->getBitWidth(), start, start + count);
      Value *bits = B.CreateLoad(word, slot);
      B.CreateStore(B.CreateAnd(bits, ConstantInt::get(word, ~mask)), slot);
    }
    // The original lifetime markers describe an unobservable stack allocation;
    // its replacement is promoted to SSA by the cleanup below.
    I->eraseFromParent();
  }
  // Pointer expressions form a DAG; remove leaves until only the root remains.
  bool progress = true;
  while (progress) {
    progress = false;
    for (auto *&G : P.geps) if (G && G->use_empty()) {
      G->eraseFromParent(); G = nullptr; progress = true;
    }
  }
  if (A->use_empty()) A->eraseFromParent();
}
} // namespace
extern "C" unsigned elisacorePackPrivateBoolArrays(LLVMModuleRef ref, LLVMTargetMachineRef target) {
  if (!ref) return 0;
  Module &M = *unwrap(ref);
  if (M.getNamedMetadata("llvm.dbg.cu")) return 0;
  const DataLayout &DL = M.getDataLayout();
  if (M.getTargetTriple().getArch() != Triple::aarch64 ||
      DL.getPointerSizeInBits(0) != 64 || DL.getIndexSizeInBits(0) != 64 || !DL.isLittleEndian()) return 0;
  bool changed = false;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() || F.getSubprogram()) continue;
    SmallVector<AllocaInst *, 16> arrays;
    for (Instruction &I : F.getEntryBlock()) if (auto *A = dyn_cast<AllocaInst>(&I)) arrays.push_back(A);
    for (AllocaInst *A : arrays) {
      BoolArrayPlan P;
      if (!planArray(*A, DL, P)) continue;
      packArray(P, DL);
      changed = true;
    }
  }
  if (!changed) return 0;
  auto options = LLVMCreatePassBuilderOptions();
  auto error = LLVMRunPasses(ref, "function(sroa,instcombine<max-iterations=8>,simplifycfg)", target, options);
  LLVMDisposePassBuilderOptions(options);
  if (error) { LLVMConsumeError(error); return 2; }
  return 1;
}
