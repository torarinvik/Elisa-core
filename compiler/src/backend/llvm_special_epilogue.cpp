// go:build cgo

#include <llvm-c/Core.h>
#include <llvm/Config/llvm-config.h>
#if LLVM_VERSION_MAJOR == 23
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Support/Casting.h>
#include <llvm/Transforms/Utils/Cloning.h>
#include <llvm/Transforms/Utils/ValueMapper.h>
using namespace llvm;
namespace ValidationEpilogue {
// A runtime-unrolled, early-exit scan can leave a chain of per-element
// remainder tests. When the remainder is eight, execute clones of exactly
// the first eight original load-and-guard blocks without those tests. The
// original chain remains the fallback for every other remainder.
struct Candidate {
  Value *Rem;
  BasicBlock *Exit;
  SmallVector<BasicBlock *, 8> Steps;
  SmallVector<unsigned, 8> Continue;
};
static bool plainBlock(BasicBlock *B, unsigned expectedLoads) {
  unsigned loads = 0;
  for (Instruction &I : *B) {
    if (isa<PHINode>(I) || isa<DbgInfoIntrinsic>(I) || isa<CallBase>(I))
      return false;
    if (auto *L = dyn_cast<LoadInst>(&I)) {
      if (!L->isSimple())
        return false;
      ++loads;
    } else if (!I.isTerminator() && I.mayHaveSideEffects())
      return false;
  }
  return loads == expectedLoads;
}
static bool match(BasicBlock &Pre, Candidate &C) {
  auto *J = dyn_cast<UncondBrInst>(Pre.getTerminator());
  if (!J)
    return false;
  Value *Rem = nullptr;
  for (Instruction &I : Pre)
    if (auto *A = dyn_cast<AssumeInst>(&I)) {
      auto *Cmp = dyn_cast<ICmpInst>(A->getArgOperand(0));
      if (!Cmp || Cmp->getPredicate() != ICmpInst::ICMP_NE)
        continue;
      auto *Z = dyn_cast<ConstantInt>(Cmp->getOperand(1));
      auto *And = dyn_cast<BinaryOperator>(Cmp->getOperand(0));
      if (!Z || !Z->isZero() || !And || And->getOpcode() != Instruction::And)
        continue;
      auto *Mask = dyn_cast<ConstantInt>(And->getOperand(1));
      if (!Mask || !Mask->equalsInt(15))
        continue;
      if (Rem && Rem != And)
        return false;
      Rem = And;
    }
  if (!Rem || !Rem->getType()->isIntegerTy())
    return false;
  BasicBlock *Cur = J->getSuccessor(0), *Failure = nullptr, *Done = nullptr;
  SmallPtrSet<BasicBlock *, 32> Seen;
  for (unsigned k = 1; k <= 8; k++) {
    if (!Seen.insert(Cur).second || !plainBlock(Cur, 1) ||
        Cur->getSinglePredecessor() !=
            (k == 1 ? &Pre
                    : C.Steps.back()->getTerminator()->getSuccessor(
                          C.Continue.back())))
      return false;
    auto *Guard = dyn_cast<CondBrInst>(Cur->getTerminator());
    if (!Guard)
      return false;
    BasicBlock *Check = nullptr;
    unsigned next = 0;
    for (unsigned si = 0; si < 2; si++) {
      BasicBlock *Try = Guard->getSuccessor(si);
      auto *Br = dyn_cast<CondBrInst>(Try->getTerminator());
      if (!Br || !plainBlock(Try, 0))
        continue;
      auto *Cmp = dyn_cast<ICmpInst>(Br->getCondition());
      if (!Cmp || Cmp->getPredicate() != ICmpInst::ICMP_EQ)
        continue;
      auto *K = dyn_cast<ConstantInt>(Cmp->getOperand(1));
      if (Cmp->getOperand(0) != Rem || !K || !K->equalsInt(k) ||
          !Cmp->hasOneUse())
        continue;
      if (Check)
        return false;
      Check = Try;
      next = si;
    }
    if (!Check || Check->getSinglePredecessor() != Cur)
      return false;
    for (Instruction &I : *Check)
      if (!I.isTerminator() &&
          &I != cast<CondBrInst>(Check->getTerminator())->getCondition())
        return false;
    auto *CheckBr = cast<CondBrInst>(Check->getTerminator());
    BasicBlock *E = CheckBr->getSuccessor(0), *Next = CheckBr->getSuccessor(1),
               *Err = Guard->getSuccessor(1 - next);
    if ((Failure && Failure != Err) || (Done && Done != E))
      return false;
    Failure = Err;
    Done = E;
    if (!Failure->phis().empty() || !Done->phis().empty())
      return false;
    for (Instruction &I : *Cur)
      for (User *U : I.users()) {
        auto *UI = dyn_cast<Instruction>(U);
        if (!UI || UI->getParent() != Cur)
          return false;
      }
    C.Steps.push_back(Cur);
    C.Continue.push_back(next);
    Cur = Next;
  }
  C.Rem = Rem;
  C.Exit = Done;
  return true;
}
static bool specialize(Function &F) {
  if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
      F.hasFnAttribute(Attribute::SanitizeAddress) ||
      F.hasFnAttribute(Attribute::SanitizeMemory) ||
      F.hasFnAttribute(Attribute::SanitizeThread) ||
      F.hasFnAttribute(Attribute::SanitizeHWAddress))
    return false;
  for (BasicBlock &Pre : F) {
    Candidate C{};
    if (!match(Pre, C))
      continue;
    ValueToValueMapTy Map;
    SmallVector<BasicBlock *, 8> Clones;
    for (auto *Step : C.Steps) {
      auto *N = CloneBasicBlock(Step, Map, ".fast8", &F);
      Map[Step] = N;
      Clones.push_back(N);
    }
    for (auto *N : Clones)
      for (Instruction &I : *N)
        RemapInstruction(&I, Map,
                         RF_NoModuleLevelChanges | RF_IgnoreMissingLocals);
    for (unsigned i = 0; i < 8; i++) {
      auto *Br = cast<CondBrInst>(Clones[i]->getTerminator());
      Br->setSuccessor(C.Continue[i], i == 7 ? C.Exit : Clones[i + 1]);
    }
    Pre.getTerminator()->eraseFromParent();
    IRBuilder<> B(&Pre);
    Value *Fast = B.CreateICmpEQ(C.Rem, ConstantInt::get(C.Rem->getType(), 8));
    B.CreateCondBr(Fast, Clones[0], C.Steps[0]);
    return true;
  }
  return false;
}
} // namespace ValidationEpilogue

extern "C" unsigned elisacoreSpecializeValidationEpilogues(LLVMModuleRef Ref) {
  if (!Ref)
    return 0;
  Module &M = *unwrap(Ref);
  const auto &T = M.getTargetTriple();
  if (!T.isAArch64() || (!T.isOSLinux() && !T.isOSDarwin()) ||
      M.getNamedMetadata("llvm.dbg.cu") || M.getModuleFlag("ProfileSummary"))
    return 0;
  unsigned Changed = 0;
  for (Function &F : M)
    Changed += ValidationEpilogue::specialize(F);
  return Changed;
}
#else
extern "C" unsigned elisacoreSpecializeValidationEpilogues(LLVMModuleRef) {
  return 0;
}
#endif
