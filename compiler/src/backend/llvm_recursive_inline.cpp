// go:build cgo
//  Expand small private binary recursion from stable snapshots, with fixed
//  budgets.
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
#include <llvm/Config/llvm-config.h>
#if LLVM_VERSION_MAJOR >= 23
#include <llvm/Analysis/InlineCost.h>
#include <llvm/IR/InstIterator.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Target/TargetMachine.h>
#include <llvm/Transforms/Utils/Cloning.h>
using namespace llvm;
static bool recursiveInlineCandidate(Function &F) {
  if (!F.hasLocalLinkage() || F.isDeclaration() || F.isVarArg() ||
      F.hasAddressTaken() || F.hasPersonalityFn() || F.hasOptNone() ||
      F.hasOptSize() || F.hasMinSize() ||
      F.hasFnAttribute(Attribute::NoInline) ||
      F.hasFnAttribute(Attribute::Convergent) ||
      !F.hasFnAttribute(Attribute::NoUnwind) ||
      F.hasFnAttribute("elisa.recursive-expanded") ||
      F.hasFnAttribute(Attribute::SanitizeAddress) ||
      F.hasFnAttribute(Attribute::SanitizeMemory) ||
      F.hasFnAttribute(Attribute::SanitizeThread) ||
      F.hasFnAttribute(Attribute::SanitizeHWAddress) ||
      F.getInstructionCount() < 16 || F.getInstructionCount() > 128)
    return false;
  auto *Entry = dyn_cast<CondBrInst>(F.getEntryBlock().getTerminator());
  auto *Cmp = Entry ? dyn_cast<ICmpInst>(Entry->getCondition()) : nullptr;
  if (!Cmp || !Cmp->isEquality() || !isa<Argument>(Cmp->getOperand(0)) ||
      !isa<ConstantInt>(Cmp->getOperand(1)))
    return false;
  unsigned Calls = 0;
  for (Instruction &I : instructions(F)) {
    if (isa<AllocaInst>(I) || isa<InvokeInst>(I) || isa<CallBrInst>(I) ||
        isa<IndirectBrInst>(I))
      return false;
    if (auto *C = dyn_cast<CallBase>(&I)) {
      if (C->isConvergent() || C->hasOperandBundles() || C->isMustTailCall())
        return false;
      if (C->getCalledFunction() == &F)
        ++Calls;
      else if (!isa<IntrinsicInst>(C))
        return false;
    }
  }
  return Calls == 2;
}
extern "C" unsigned
elisacoreInlineSmallRecursiveFunctions(LLVMModuleRef Ref,
                                       LLVMTargetMachineRef TMRef) {
  if (!Ref || !TMRef)
    return 0;
  Module &M = *unwrap(Ref);
  auto *TM = reinterpret_cast<TargetMachine *>(TMRef);
  if (!M.getTargetTriple().isMacOSX() ||
      M.getTargetTriple().getArch() != Triple::aarch64 ||
      M.getNamedMetadata("llvm.dbg.cu"))
    return 0;
  SmallVector<Function *, 8> Candidates;
  for (Function &F : M)
    if (recursiveInlineCandidate(F))
      Candidates.push_back(&F);
  if (Candidates.empty() || Candidates.size() > 16)
    return 0;
  unsigned Changed = 0, Growth = 0;
  for (Function *F : Candidates) {
    for (unsigned Depth = 0; Depth < 2; ++Depth) {
      SmallVector<CallBase *, 8> Calls;
      for (Instruction &I : instructions(F))
        if (auto *C = dyn_cast<CallBase>(&I); C && C->getCalledFunction() == F)
          Calls.push_back(C);
      unsigned Cost = F->getInstructionCount() * Calls.size();
      if (Calls.size() > 4 || F->getInstructionCount() + Cost > 2048 ||
          Growth + Cost > 16384)
        break;
      ValueToValueMapTy Map;
      Function *Snapshot = CloneFunction(F, Map);
      Snapshot->setName(F->getName() + ".recursive_snapshot");
      for (Instruction &I : instructions(Snapshot))
        if (auto *C = dyn_cast<CallBase>(&I);
            C && C->getCalledFunction() == Snapshot)
          C->setCalledFunction(F);
      if (!isInlineViable(*Snapshot).isSuccess()) {
        Snapshot->eraseFromParent();
        break;
      }
      for (CallBase *C : Calls) {
        C->setCalledFunction(Snapshot);
        InlineFunctionInfo IFI;
        if (InlineFunction(*C, IFI).isSuccess())
          ++Changed;
        else
          C->setCalledFunction(F);
      }
      Growth += Cost;
      if (Snapshot->use_empty())
        Snapshot->eraseFromParent();
    }
    F->addFnAttr("elisa.recursive-expanded");
  }
  if (!Changed)
    return 0;
  PassBuilder PB(TM);
  LoopAnalysisManager LAM;
  FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM;
  ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM);
  PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM);
  PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
  auto Cleanup = PB.buildPerModuleDefaultPipeline(OptimizationLevel::O3);
  Cleanup.run(M, MAM);
  return 1;
}
#else
extern "C" unsigned
elisacoreInlineSmallRecursiveFunctions(LLVMModuleRef, LLVMTargetMachineRef) {
  return 0;
}
#endif
