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
#include <llvm/IR/Constants.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Dominators.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Target/TargetMachine.h>
#include <llvm/Transforms/Utils/Cloning.h>
#include <llvm/Transforms/Utils/BasicBlockUtils.h>
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
// Specialize one literal integer argument of a private recursive function.
// Recursive calls whose argument changes continue to use the original body.
static unsigned specializeRecursiveLiteralCalls(Module &M) {
  SmallVector<Function *, 8> Work;
  for (Function &F : M) {
    if (!F.hasLocalLinkage() || F.isDeclaration() || F.isVarArg() ||
        F.hasAddressTaken() || F.hasPersonalityFn() || F.hasOptNone() ||
        F.hasOptSize() || F.hasMinSize() || F.hasFnAttribute(Attribute::NoInline) ||
        F.hasFnAttribute(Attribute::Convergent) ||
        F.hasFnAttribute(Attribute::NoDuplicate) ||
        F.hasFnAttribute(Attribute::ReturnsTwice) ||
        F.hasFnAttribute("elisa.literal-specialized") ||
        F.hasFnAttribute(Attribute::SanitizeAddress) ||
        F.hasFnAttribute(Attribute::SanitizeMemory) ||
        F.hasFnAttribute(Attribute::SanitizeThread) ||
        F.hasFnAttribute(Attribute::SanitizeHWAddress) ||
        F.getInstructionCount() < 40 || F.getInstructionCount() > 256)
      continue;
    unsigned Recursive = 0;
    bool Safe = true;
    for (Instruction &I : instructions(F)) {
      if (isa<InvokeInst>(I) || isa<CallBrInst>(I) || isa<IndirectBrInst>(I)) {
        Safe = false;
        break;
      }
      if (auto *C = dyn_cast<CallBase>(&I))
        if (C->isInlineAsm() || C->isConvergent() || C->cannotDuplicate() ||
            C->hasFnAttr(Attribute::ReturnsTwice) || C->hasOperandBundles()) {
          Safe = false;
          break;
        }
    }
    if (!Safe) continue;
    for (User *U : F.users()) {
      auto *Call = dyn_cast<CallInst>(U);
      if (!Call || Call->getCalledFunction() != &F || Call->isMustTailCall() ||
          Call->isConvergent() || Call->hasOperandBundles()) { Safe = false; break; }
      if (Call->getFunction() == &F) ++Recursive;
    }
    if (Safe && Recursive && Recursive <= 4) Work.push_back(&F);
  }
  if (Work.size() > 16) return 0;
  unsigned Changed = 0, Growth = 0;
  for (Function *F : Work) {
    if (Growth + F->getInstructionCount() > 2048) continue;
    for (unsigned Index = 0; Index < F->arg_size(); ++Index) {
      Argument *Arg = F->getArg(Index);
      if (!Arg->getType()->isIntegerTy() || Arg->getNumUses() < 2) continue;
      SmallVector<CallInst *, 4> Roots;
      ConstantInt *Literal = nullptr;
      bool Multiple = false;
      for (User *U : F->users()) {
        auto *Call = cast<CallInst>(U);
        if (Call->getFunction() == F) continue;
        auto *C = dyn_cast<ConstantInt>(Call->getArgOperand(Index));
        if (!C) continue;
        if (Literal && C != Literal) { Multiple = true; break; }
        Literal = C; Roots.push_back(Call);
      }
      if (!Literal || Multiple || Roots.empty() || Roots.size() > 16) continue;
      ValueToValueMapTy Map;
      Function *Clone = CloneFunction(F, Map);
      Clone->setName(F->getName() + ".literal_specialized");
      Clone->addFnAttr("elisa.literal-specialized");
      Clone->getArg(Index)->replaceAllUsesWith(Literal);
      for (Instruction &I : instructions(Clone))
        if (auto *Call = dyn_cast<CallInst>(&I);
            Call && (Call->getCalledFunction() == Clone || Call->getCalledFunction() == F)) {
          auto *C = dyn_cast<ConstantInt>(Call->getArgOperand(Index));
          Call->setCalledFunction(C == Literal ? Clone : F);
        }
      for (CallInst *Call : Roots) Call->setCalledFunction(Clone);
      F->addFnAttr("elisa.literal-specialized");
      Growth += F->getInstructionCount();
      ++Changed;
      break;
    }
  }
  return Changed;
}

// Version one loop-invariant call when a recursive helper has a direct
// zero-argument return. The cloned zero path is inlined, so normal LLVM
// cleanup can discard work that is unobservable to that caller. Freeze the
// guarded operand and use the same value in the unspecialized call.
static unsigned versionRecursiveZeroCall(Module &M) {
  for (Function &F : M) {
    if (!F.hasLocalLinkage() || F.isDeclaration() || F.isVarArg() ||
        F.hasAddressTaken() || F.hasPersonalityFn() || F.hasOptNone() ||
        F.hasOptSize() || F.hasMinSize() || F.hasFnAttribute(Attribute::NoInline) ||
        F.hasFnAttribute(Attribute::Convergent) ||
        F.hasFnAttribute(Attribute::NoDuplicate) ||
        F.hasFnAttribute(Attribute::ReturnsTwice) ||
        F.hasFnAttribute(Attribute::SanitizeAddress) ||
        F.hasFnAttribute(Attribute::SanitizeMemory) ||
        F.hasFnAttribute(Attribute::SanitizeThread) ||
        F.hasFnAttribute(Attribute::SanitizeHWAddress) ||
        !F.hasFnAttribute("elisa.literal-specialized") ||
        F.hasFnAttribute("elisa.zero-versioned") ||
        F.getInstructionCount() > 256) continue;
    for (BasicBlock &BB : F) {
      auto *Br = dyn_cast<CondBrInst>(BB.getTerminator());
      auto *Cmp = Br ? dyn_cast<ICmpInst>(Br->getCondition()) : nullptr;
      if (!Cmp || Cmp->getPredicate() != ICmpInst::ICMP_EQ) continue;
      auto *Arg = dyn_cast<Argument>(Cmp->getOperand(0));
      auto *Zero = dyn_cast<ConstantInt>(Cmp->getOperand(1));
      if (!Arg || Arg->getParent() != &F || !Zero || !Zero->isZero()) continue;
      BasicBlock *Return = Br->getSuccessor(0);
      auto *Ret = dyn_cast<ReturnInst>(Return->getTerminator());
      auto *Phi = Ret ? dyn_cast_or_null<PHINode>(Ret->getReturnValue()) : nullptr;
      int Incoming = Phi ? Phi->getBasicBlockIndex(&BB) : -1;
      auto *Result = Incoming >= 0 ? dyn_cast<ConstantInt>(Phi->getIncomingValue(Incoming)) : nullptr;
      if (!Result || !Result->isZero()) continue;
      SmallVector<CallInst *, 4> Roots;
      for (User *U : F.users()) {
        auto *Call = dyn_cast<CallInst>(U);
        if (!Call || Call->getCalledFunction() != &F || Call->getFunction() == &F ||
            Call->isMustTailCall() || Call->isConvergent() || Call->cannotDuplicate() ||
            Call->hasFnAttr(Attribute::ReturnsTwice) || Call->hasOperandBundles() ||
            !Call->getType()->isIntegerTy() || isa<Constant>(Call->getArgOperand(Arg->getArgNo()))) continue;
        Function *Caller = Call->getFunction();
        if (Caller->hasPersonalityFn() || Caller->hasOptNone() || Caller->hasOptSize() || Caller->hasMinSize() ||
            Caller->hasFnAttribute(Attribute::SanitizeAddress) ||
            Caller->hasFnAttribute(Attribute::SanitizeMemory) ||
            Caller->hasFnAttribute(Attribute::SanitizeThread) ||
            Caller->hasFnAttribute(Attribute::SanitizeHWAddress)) continue;
        DominatorTree DT(*Caller);
        LoopInfo LI(DT);
        Loop *L = LI.getLoopFor(Call->getParent());
        if (L && L->isLoopInvariant(Call->getArgOperand(Arg->getArgNo()))) Roots.push_back(Call);
      }
      if (Roots.size() != 1) continue;
      CallInst *Root = Roots.front();
      ValueToValueMapTy Map;
      Function *Clone = CloneFunction(&F, Map);
      Clone->setName(F.getName() + ".zero_version");
      Clone->getArg(Arg->getArgNo())->replaceAllUsesWith(Zero);
      for (Instruction &I : instructions(Clone))
        if (auto *Call = dyn_cast<CallInst>(&I); Call && Call->getCalledFunction() == Clone)
          Call->setCalledFunction(&F);
      if (!isInlineViable(*Clone).isSuccess()) {
        Clone->eraseFromParent();
        continue;
      }
      IRBuilder<> B(Root);
      Value *Stable = B.CreateFreeze(Root->getArgOperand(Arg->getArgNo()), "zero.version.value");
      Value *Condition = B.CreateICmpEQ(Stable, Zero);
      Root->setArgOperand(Arg->getArgNo(), Stable);
      Instruction *ThenTerm = nullptr, *ElseTerm = nullptr;
      SplitBlockAndInsertIfThenElse(Condition, Root, &ThenTerm, &ElseTerm);
      auto *Fast = cast<CallInst>(Root->clone());
      Fast->setCalledFunction(Clone);
      Fast->setArgOperand(Arg->getArgNo(), Zero);
      Fast->setTailCallKind(CallInst::TCK_None);
      Fast->insertBefore(ThenTerm->getIterator());
      auto *Slow = cast<CallInst>(Root->clone());
      Slow->setTailCallKind(CallInst::TCK_None);
      Slow->insertBefore(ElseTerm->getIterator());
      auto *ResultPhi = PHINode::Create(Root->getType(), 2, "zero.version.result", Root->getIterator());
      ResultPhi->addIncoming(Fast, ThenTerm->getParent());
      ResultPhi->addIncoming(Slow, ElseTerm->getParent());
      Root->replaceAllUsesWith(ResultPhi);
      Root->eraseFromParent();
      InlineFunctionInfo IFI;
      (void)InlineFunction(*Fast, IFI);
      if (Clone->use_empty()) Clone->eraseFromParent();
      F.addFnAttr("elisa.zero-versioned");
      return 1;
    }
  }
  return 0;
}
extern "C" unsigned
elisacoreInlineSmallRecursiveFunctions(LLVMModuleRef Ref,
                                       LLVMTargetMachineRef TMRef) {
  if (!Ref || !TMRef)
    return 0;
  Module &M = *unwrap(Ref);
  auto *TM = reinterpret_cast<TargetMachine *>(TMRef);
  if (!(M.getTargetTriple().isMacOSX() || M.getTargetTriple().isOSLinux()) ||
      M.getTargetTriple().getArch() != Triple::aarch64 ||
      M.getNamedMetadata("llvm.dbg.cu"))
    return 0;
  SmallVector<Function *, 8> Candidates;
  for (Function &F : M)
    if (recursiveInlineCandidate(F))
      Candidates.push_back(&F);
  if (Candidates.size() > 16) return 0;
  unsigned Changed = specializeRecursiveLiteralCalls(M), Growth = 0;
  if (Candidates.empty() && !Changed) return 0;
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
  if (versionRecursiveZeroCall(M)) {
    ModulePassManager SecondCleanup = PB.buildPerModuleDefaultPipeline(OptimizationLevel::O3);
    SecondCleanup.run(M, MAM);
  }
  return 1;
}
#else
extern "C" unsigned
elisacoreInlineSmallRecursiveFunctions(LLVMModuleRef, LLVMTargetMachineRef) {
  return 0;
}
#endif
