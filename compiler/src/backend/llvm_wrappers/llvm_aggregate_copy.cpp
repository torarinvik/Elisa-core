//go:build cgo

// Stage0 lowers `b = a` for fixed arrays to a first-class load and store. Later
// passes split those per element, which is slow and hides the copy from memcpy
// optimizations, so rewrite an adjacent pair as one memcpy. Same-typed slots
// are identical or disjoint, which memcpy permits.
#include <llvm/Config/llvm-config.h>
#include <llvm-c/Core.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>
using namespace llvm;
extern "C" unsigned elisacoreAggregateCopiesToMemcpy(LLVMModuleRef Ref) {
  if (!Ref) return 0;
  Module &M = *unwrap(Ref);
  const DataLayout &DL = M.getDataLayout();
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone()) continue;
    SmallVector<LoadInst *, 16> Loads;
    for (BasicBlock &BB : F)
      for (Instruction &I : BB)
        if (auto *L = dyn_cast<LoadInst>(&I))
          if (L->getType()->isArrayTy() && L->isSimple() && L->hasOneUse()) Loads.push_back(L);
    for (LoadInst *L : Loads) {
      auto *S = dyn_cast<StoreInst>(L->getNextNode());
      TypeSize Size = DL.getTypeAllocSize(L->getType());
      if (!S || !S->isSimple() || S->getValueOperand() != L || S->getPointerOperand() == L ||
          Size.isScalable() || Size.getFixedValue() < 16) continue;
      IRBuilder<> B(S);
      B.CreateMemCpy(S->getPointerOperand(), S->getAlign(), L->getPointerOperand(), L->getAlign(),
                     Size.getFixedValue());
      S->eraseFromParent();
      L->eraseFromParent();
      ++Changed;
    }
  }
  return Changed;
}
