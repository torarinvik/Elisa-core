//go:build cgo
#include <llvm/Config/llvm-config.h>
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
#if LLVM_VERSION_MAJOR == 23
#include <llvm/ADT/APInt.h>
#include <llvm/ADT/DenseMap.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Intrinsics.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Module.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Analysis/AssumptionCache.h>
#include <llvm/Analysis/CaptureTracking.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/Analysis/CFG.h>
#include <llvm/IR/ConstantRange.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/OptimizationRemarkEmitter.h>
#include <llvm/Analysis/TargetTransformInfo.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/MC/MCSubtargetInfo.h>
#include <llvm/MC/TargetRegistry.h>
#include <llvm/Target/TargetMachine.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/Transforms/Utils/Cloning.h>
#include <llvm/Transforms/Utils/LoopPeel.h>
#include <llvm/Transforms/Utils/LoopSimplify.h>
#include <llvm/Transforms/Utils/LoopUtils.h>
#include <llvm/Transforms/Utils/UnrollLoop.h>
using namespace llvm;
extern "C" unsigned elisacorePeelSmallValidationLoops(LLVMModuleRef moduleRef) {
  if (!moduleRef) return 0;
  Module &module = *unwrap(moduleRef);
  PassBuilder PB;
  LoopAnalysisManager LAM; FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM; ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM); PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM); PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
  unsigned count = 0;
  for (Function &F : module) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasMinSize() || F.hasOptSize()) continue;
    auto &LI = FAM.getResult<LoopAnalysis>(F);
    auto &DT = FAM.getResult<DominatorTreeAnalysis>(F);
    auto &SE = FAM.getResult<ScalarEvolutionAnalysis>(F);
    auto &AC = FAM.getResult<AssumptionAnalysis>(F);
    for (Loop *L : LI) {
      if (!L->getSubLoops().empty() || L->getNumBlocks() != 2 || L->getLoopID()) continue;
      unsigned loads = 0, instructions = 0, exits = 0, cmps = 0;
      bool safe = true;
      for (BasicBlock *BB : L->blocks()) for (Instruction &I : *BB) {
        ++instructions;
        if (auto *load = dyn_cast<LoadInst>(&I)) { if (!load->isSimple()) safe = false; ++loads; }
        else if (I.mayHaveSideEffects() && !isa<BranchInst>(I)) safe = false;
        if (isa<ICmpInst>(I)) ++cmps;
        if (auto *branch = dyn_cast<CondBrInst>(&I))
          for (auto *successor : branch->successors()) if (!L->contains(successor)) ++exits;
      }
      if (!safe || loads != 1 || instructions > 16 || exits != 2 || cmps != 2) continue;
      // Keep short and unbounded scans compact; widen bounded long scans.
      unsigned MaxTrips = SE.getSmallConstantMaxTripCount(L);
      unsigned UnrollCount = MaxTrips >= 64 && MaxTrips <= 4096 ? 8 : 4;
      simplifyLoop(L, &DT, &LI, &SE, &AC, nullptr, false);
      if (!L->isLoopSimplifyForm() || !canPeel(L)) continue;
      formLCSSA(*L, DT, &LI, &SE);
      ValueToValueMapTy values;
      peelLoop(L, 1, false, &LI, &SE, DT, &AC, true, values);
      // Preserve every load and early exit in order. LLVM's unroller handles
      // the runtime remainder without speculating accesses past an invalid item.
      auto &TTI = FAM.getResult<TargetIRAnalysis>(F);
      auto &ORE = FAM.getResult<OptimizationRemarkEmitterAnalysis>(F);
      UnrollLoopOptions options{UnrollCount, true, true, false, true, false, nullptr, 1000, true, false};
      UnrollLoop(L, options, &LI, &SE, &DT, &AC, &TTI, &ORE, true);
      ++count;
      break; // Invalidate function analyses before inspecting another loop.
    }
  }
  return count;
}

static bool hasCSSC(const TargetMachine &TM) {
  if (!TM.getTargetTriple().isAArch64()) return false;
  auto *STI = TM.getTarget().createMCSubtargetInfo(
      TM.getTargetTriple(), TM.getTargetCPU(), TM.getTargetFeatureString());
  if (!STI) return false;
  const bool enabled = STI->checkFeatures("+cssc");
  delete STI;
  return enabled;
}

static bool matchSymmetricRangeCheck(ICmpInst &Cmp, BinaryOperator *&Add,
                                     Value *&Input, APInt &Bound) {
  if (Cmp.getPredicate() != ICmpInst::ICMP_ULT ||
      Cmp.hasMetadataOtherThanDebugLoc()) return false;
  Add = dyn_cast<BinaryOperator>(Cmp.getOperand(0));
  auto *Limit = dyn_cast<ConstantInt>(Cmp.getOperand(1));
  if (!Add || Add->getOpcode() != Instruction::Add ||
      Add->hasNoSignedWrap() || Add->hasNoUnsignedWrap() ||
      Add->hasMetadataOtherThanDebugLoc() || !Limit) return false;
  auto *Ty = dyn_cast<IntegerType>(Add->getType());
  if (!Ty || Ty->getBitWidth() != 64 || Limit->getType() != Ty) return false;

  auto *Offset0 = dyn_cast<ConstantInt>(Add->getOperand(0));
  auto *Offset1 = dyn_cast<ConstantInt>(Add->getOperand(1));
  const ConstantInt *Offset = Offset0 ? Offset0 : Offset1;
  if (!Offset || !Offset->getValue().isNegative()) return false;
  Input = Offset0 ? Add->getOperand(1) : Add->getOperand(0);
  if (Input->getType() != Ty) return false;

  const unsigned Width = Ty->getBitWidth();
  Bound = -Offset->getValue() - APInt(Width, 1);
  const APInt MaxBound = APInt::getSignedMaxValue(Width).lshr(1);
  if (Bound.isNegative() || Bound.ugt(MaxBound)) return false;
  const APInt ExpectedLimit = -(Bound.shl(1) + APInt(Width, 1));
  return Limit->getValue() == ExpectedLimit;
}

// Map the exact unsigned range-check idiom for [-B, B] to llvm.abs plus an
// unsigned comparison when the selected AArch64 CPU has CSSC. INT_MIN remains
// the same wrapped bit pattern and is still rejected by the unsigned compare.
extern "C" unsigned elisacoreCsscAbsoluteRangeChecks(
    LLVMModuleRef moduleRef, LLVMTargetMachineRef targetRef) {
  if (!moduleRef || !targetRef) return 0;
  auto &TM = *reinterpret_cast<TargetMachine *>(targetRef);
  if (!hasCSSC(TM)) return 0;
  Module &M = *unwrap(moduleRef);
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.hasFnAttribute("target-cpu") || F.hasFnAttribute("target-features")) continue;
    DominatorTree DT(F); LoopInfo LI(DT); DenseMap<Loop *, unsigned> ScanCounts;
    SmallVector<ICmpInst *, 8> Candidates;
    for (BasicBlock &BB : F)
      for (Instruction &I : BB)
        if (auto *Cmp = dyn_cast<ICmpInst>(&I)) Candidates.push_back(Cmp);
    for (ICmpInst *Cmp : Candidates) {
      // Wide unrolled validation scans favor the original range arithmetic on
      // measured AArch64 targets. Keep the absolute-value fold for other loops.
      if (Loop *L = LI.getLoopFor(Cmp->getParent())) {
        auto It = ScanCounts.find(L);
        if (It == ScanCounts.end()) {
          unsigned RangeChecks = 0;
          for (BasicBlock *B : L->blocks()) for (Instruction &I : *B)
            if (auto *C = dyn_cast<ICmpInst>(&I)) {
              BinaryOperator *Add = nullptr; Value *Input = nullptr; APInt Bound(64, 0);
              if (matchSymmetricRangeCheck(*C, Add, Input, Bound)) ++RangeChecks;
            }
          It = ScanCounts.try_emplace(L, RangeChecks).first;
        }
        if (It->second >= 4) continue;
      }
      BinaryOperator *Add = nullptr;
      Value *Input = nullptr;
      APInt Bound(64, 0);
      if (!matchSymmetricRangeCheck(*Cmp, Add, Input, Bound)) continue;
      IRBuilder<> Builder(Cmp);
      Builder.SetCurrentDebugLocation(Cmp->getDebugLoc());
      auto *AbsFn = Intrinsic::getOrInsertDeclaration(&M, Intrinsic::abs,
                                                      {Input->getType()});
      Value *Abs = Builder.CreateCall(AbsFn,
          {Input, ConstantInt::getFalse(M.getContext())}, "range.abs");
      Value *Invalid = Builder.CreateICmpUGT(Abs,
          ConstantInt::get(cast<IntegerType>(Input->getType()), Bound), "range.invalid");
      Cmp->replaceAllUsesWith(Invalid);
      Cmp->eraseFromParent();
      if (Add->use_empty()) Add->eraseFromParent();
      ++Changed;
    }
  }
  return Changed;
}


// Expand one level of a small, directly recursive local traversal. LLVM's
// inliner preserves checks and memory effects; the budget bounds code growth.
extern "C" unsigned elisacoreInlineSmallRecursion(LLVMModuleRef ref) {
  if (!ref) return 0;
  Module &M = *unwrap(ref);
  if ((!M.getTargetTriple().isMacOSX() && !M.getTargetTriple().isOSLinux()) ||
      M.getTargetTriple().getArch() != Triple::aarch64 ||
      M.getNamedMetadata("llvm.dbg.cu")) return 0;
  unsigned changed = 0;
  for (Function &F : M) {
    if (!F.hasLocalLinkage() || F.isDeclaration() || F.isVarArg() || F.size() > 20 ||
        F.hasOptNone() || F.hasOptSize() || F.hasMinSize() || F.hasPersonalityFn() ||
        F.hasGC() || F.getSubprogram() || F.hasFnAttribute(Attribute::NoInline) ||
        F.hasFnAttribute(Attribute::Naked) || F.hasFnAttribute(Attribute::Convergent) ||
        F.hasFnAttribute(Attribute::NoDuplicate) || F.hasFnAttribute("elisa.recursion.expanded") ||
        !F.getReturnType()->isVoidTy()) continue;
    bool safe = true, hasCaller = false;
    for (User *U : F.users()) {
      auto *CB = dyn_cast<CallBase>(U);
      if (!CB || CB->getCalledOperand() != &F) { safe = false; break; }
      if (CB->getFunction() != &F) hasCaller = true;
    }
    unsigned instructions = 0, loads = 0, stores = 0, recursive = 0;
    CallBase *site = nullptr;
    for (BasicBlock &B : F) for (Instruction &I : B) {
      ++instructions;
      if (isa<AllocaInst>(I) || isa<InvokeInst>(I) || isa<CallBrInst>(I)) safe = false;
      if (auto *L = dyn_cast<LoadInst>(&I)) { ++loads; if (!L->isSimple()) safe = false; }
      if (auto *S = dyn_cast<StoreInst>(&I)) { ++stores; if (!S->isSimple()) safe = false; }
      if (auto *CB = dyn_cast<CallBase>(&I)) {
        if (CB->hasOperandBundles() || CB->isConvergent() || CB->cannotDuplicate() ||
            CB->isNoInline()) safe = false;
        if (CB->getCalledFunction() == &F) { ++recursive; site = CB; }
        else if (CB->getIntrinsicID() != Intrinsic::trap) safe = false;
      }
    }
    if (!safe || !hasCaller || instructions > 80 || recursive != 1 || !loads || !stores) continue;
    InlineFunctionInfo IFI;
    if (InlineFunction(*site, IFI).isSuccess()) {
      F.addFnAttr("elisa.recursion.expanded");
      ++changed;
    }
  }
  return changed;
}

// Predicate a conditional OR update of a private stack array. A 0/1 switch
// retains its invalid-input edge. Extra accesses are
// permitted only when SCEV proves their full byte range and the array cannot
// escape. Selects and freezes preserve defined results without introducing
// poison through a value that the zero case originally did not evaluate.
extern "C" unsigned elisacorePredicatePrivateBitUpdates(LLVMModuleRef ref) {
  if (!ref) return 0;
  Module &M = *unwrap(ref);
  if (M.getTargetTriple().getArch() != Triple::aarch64 || !M.getDataLayout().isLittleEndian() ||
      (!M.getTargetTriple().isMacOSX() && !M.getTargetTriple().isOSLinux()) ||
      M.getNamedMetadata("llvm.dbg.cu")) return 0;
  PassBuilder PB;
  LoopAnalysisManager LAM; FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM; ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM); PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM); PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
  const DataLayout &DL = M.getDataLayout();
  unsigned changed = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.getSubprogram() || F.hasFnAttribute(Attribute::SanitizeMemory) ||
        F.hasFnAttribute(Attribute::SanitizeAddress) || F.hasFnAttribute(Attribute::SanitizeThread) ||
        F.hasFnAttribute(Attribute::SanitizeHWAddress) || F.hasFnAttribute(Attribute::SanitizeMemTag) ||
        F.getInstructionCount() > 1000) continue;
    for (BasicBlock &B : F) {
      Instruction *decision = B.getTerminator();
      auto *SW = dyn_cast<SwitchInst>(decision);
      auto *CB = dyn_cast<CondBrInst>(decision);
      BasicBlock *zero = nullptr, *one = nullptr;
      Value *set = nullptr;
      if (SW) {
        if (SW->getNumCases() != 2 || !SW->getCondition()->getType()->isIntegerTy()) continue;
        for (auto C : SW->cases()) {
          if (C.getCaseValue()->isZero()) zero = C.getCaseSuccessor();
          if (C.getCaseValue()->isOne()) one = C.getCaseSuccessor();
        }
        if (zero == SW->getDefaultDest() || one == SW->getDefaultDest()) continue;
      } else if (CB) {
        one = CB->getSuccessor(0); zero = CB->getSuccessor(1); set = CB->getCondition();
      } else continue;
      if (!zero || !one || zero == one || one->getSinglePredecessor() != &B ||
          !zero->phis().empty()) continue;
      auto *end = dyn_cast<UncondBrInst>(one->getTerminator());
      if (!end || end->getSuccessor(0) != zero) continue;
      StoreInst *store = nullptr;
      unsigned stores = 0;
      for (Instruction &I : *one) if (auto *S = dyn_cast<StoreInst>(&I)) { store = S; ++stores; }
      if (stores != 1 || !store->isSimple()) continue;
      auto *update = dyn_cast<BinaryOperator>(store->getValueOperand());
      auto *pointer = dyn_cast<GetElementPtrInst>(store->getPointerOperand());
      if (!update || update->getParent() != one || update->getOpcode() != Instruction::Or || !update->hasOneUse() ||
          !pointer || pointer->getParent() != one || pointer->getNumIndices() != 1) continue;
      auto *load = dyn_cast<LoadInst>(update->getOperand(0));
      Value *mask = update->getOperand(1);
      if (!load) { load = dyn_cast<LoadInst>(mask); mask = update->getOperand(0); }
      if (!load || !load->isSimple() || load->getParent() != one || !load->hasOneUse() ||
          load->getPointerOperand() != pointer || !load->getType()->isIntegerTy()) continue;
      unsigned bits = load->getType()->getIntegerBitWidth();
      if (bits != 8 && bits != 16 && bits != 32 && bits != 64) continue;
      bool simple = true;
      for (Instruction &I : *one)
        if (&I != pointer && &I != load && &I != update && &I != store && &I != end) simple = false;
      if (!simple) continue;
      auto &SE = FAM.getResult<ScalarEvolutionAnalysis>(F);
      auto &DT = FAM.getResult<DominatorTreeAnalysis>(F);
      auto dominates = [&](Value *V) {
        auto *I = dyn_cast<Instruction>(V); return !I || DT.dominates(I, decision);
      };
      Value *index = pointer->getOperand(1);
      if (!index->getType()->isIntegerTy() || index->getType()->getIntegerBitWidth() > 64 ||
          !dominates(index) || !dominates(mask)) continue;
      auto *array = dyn_cast<AllocaInst>(pointer->getPointerOperand());
      if (!array || !array->isStaticAlloca() || array->getAddressSpace() != 0 ||
          !cast<ConstantInt>(array->getArraySize())->isOne() || !DT.dominates(array, decision) ||
          PointerMayBeCaptured(array, true)) continue;
      TypeSize allocation = DL.getTypeAllocSize(array->getAllocatedType());
      TypeSize stride = DL.getTypeAllocSize(pointer->getSourceElementType());
      TypeSize access = DL.getTypeStoreSize(load->getType());
      if (allocation.isScalable() || stride.isScalable() || access.isScalable()) continue;
      uint64_t bytes = allocation.getFixedValue(), step = stride.getFixedValue(), width = access.getFixedValue();
      if (!width || !step || bytes > 256 || bytes < width ||
          array->getAlign() < load->getAlign() || array->getAlign() < store->getAlign() ||
          step % load->getAlign().value() || step % store->getAlign().value()) continue;
      const ConstantRange &range = SE.getUnsignedRange(SE.getSCEV(index));
      if (range.getUnsignedMax().ugt((bytes - width) / step)) continue;
      // Lifetime markers must not make the newly accessed zero path dead.
      for (BasicBlock &BB : F) for (Instruction &I : BB) if (auto *II = dyn_cast<IntrinsicInst>(&I)) {
        if (II->getIntrinsicID() == Intrinsic::lifetime_end && isPotentiallyReachable(II, decision)) simple = false;
        if (II->getIntrinsicID() == Intrinsic::lifetime_start && !DT.dominates(II, decision)) simple = false;
      }
      if (!simple) continue;
      IRBuilder<> head(decision);
      Instruction *branch;
      if (SW) {
        Value *valid = head.CreateICmpULE(SW->getCondition(), ConstantInt::get(SW->getCondition()->getType(), 1));
        set = head.CreateICmpEQ(SW->getCondition(), ConstantInt::get(SW->getCondition()->getType(), 1));
        branch = head.CreateCondBr(valid, one, SW->getDefaultDest());
      } else branch = head.CreateBr(one);
      branch->setDebugLoc(decision->getDebugLoc());
      zero->removePredecessor(&B);
      decision->eraseFromParent();
      IRBuilder<> body(update);
      Value *selected = body.CreateSelect(set, mask, ConstantInt::get(load->getType(), 0));
      Value *result = body.CreateOr(body.CreateFreeze(load), selected);
      store->setOperand(0, result);
      update->eraseFromParent();
      // Metadata valid only on the original one path must not constrain zero.
      for (Instruction *I : {static_cast<Instruction *>(load), static_cast<Instruction *>(store)}) {
        SmallVector<std::pair<unsigned, MDNode *>, 8> metadata;
        I->getAllMetadataOtherThanDebugLoc(metadata);
        for (auto item : metadata) I->setMetadata(item.first, nullptr);
      }
      ++changed;
      break; // Analyses describe the original CFG; one update per function.
    }
  }
  return changed;
}

// Return paths with no reads cannot observe a private stack initialization.
static bool privateInitReturnBlock(BasicBlock *B) {
  if (!isa<ReturnInst>(B->getTerminator())) return false;
  for (Instruction &I : *B) {
    if (isa<PHINode>(I) || isa<ReturnInst>(I)) continue;
    auto *II = dyn_cast<IntrinsicInst>(&I);
    if (II && II->getIntrinsicID() == Intrinsic::lifetime_end) continue;
    return false;
  }
  return true;
}
extern "C" unsigned elisacoreSinkPrivateInitializations(LLVMModuleRef ref) {
  if (!ref) return 0;
  Module &M = *unwrap(ref);
  const DataLayout &DL = M.getDataLayout();
  if (M.getTargetTriple().getArch() != Triple::aarch64 ||
      (!M.getTargetTriple().isMacOSX() && !M.getTargetTriple().isOSLinux()) ||
      DL.getPointerSizeInBits(0) != 64 || M.getNamedMetadata("llvm.dbg.cu")) return 0;
  unsigned changed = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.getSubprogram() || F.hasFnAttribute(Attribute::SanitizeAddress) ||
        F.hasFnAttribute(Attribute::SanitizeMemory) || F.hasFnAttribute(Attribute::SanitizeThread) ||
        F.hasFnAttribute(Attribute::SanitizeHWAddress) || F.hasFnAttribute(Attribute::SanitizeMemTag)) continue;
    SmallVector<MemSetInst *, 8> work;
    for (BasicBlock &B : F) for (Instruction &I : B)
      if (auto *S = dyn_cast<MemSetInst>(&I)) work.push_back(S);
    for (MemSetInst *S : work) {
      auto *length = dyn_cast<ConstantInt>(S->getLength());
      auto *value = dyn_cast<ConstantInt>(S->getValue());
      if (!length || !value || !value->isZero() || length->getValue().ult(256) ||
          length->getValue().ugt(1 << 20) || S->isVolatile() || S->hasOperandBundles() ||
          S->hasMetadataOtherThanDebugLoc() || S->getDestAddressSpace() != 0) continue;
      BasicBlock *B = S->getParent();
      auto *branch = dyn_cast<CondBrInst>(B->getTerminator());
      if (!branch) continue;
      // Do not cross a read of the initialized data, another memory operation,
      // a call, or a potentially observable side effect.
      bool pure = true;
      for (Instruction *I = S->getNextNode(); I && I != branch; I = I->getNextNode())
        if (I->mayReadOrWriteMemory() || I->mayHaveSideEffects() || isa<CallBase>(I)) pure = false;
      if (!pure) continue;
      bool first = privateInitReturnBlock(branch->getSuccessor(0));
      bool second = privateInitReturnBlock(branch->getSuccessor(1));
      if (first == second) continue;
      int64_t offset = 0;
      auto *array = dyn_cast<AllocaInst>(GetPointerBaseWithConstantOffset(S->getRawDest(), offset, DL));
      if (!array || !array->isStaticAlloca() || array->getAddressSpace() != 0 ||
          array->isUsedByMetadata() || offset < 0 ||
          !cast<ConstantInt>(array->getArraySize())->isOne() || PointerMayBeCaptured(array, true, 256)) continue;
      TypeSize size = DL.getTypeAllocSize(array->getAllocatedType());
      if (size.isScalable() || uint64_t(offset) > size.getFixedValue() ||
          length->getZExtValue() > size.getFixedValue() - uint64_t(offset)) continue;
      unsigned continuing = first ? 1 : 0;
      BasicBlock *destination = branch->getSuccessor(continuing);
      auto *edge = BasicBlock::Create(M.getContext(), "stack.init.nonempty", &F, destination);
      IRBuilder<> builder(edge);
      builder.CreateBr(destination)->setDebugLoc(S->getDebugLoc());
      branch->setSuccessor(continuing, edge);
      destination->replacePhiUsesWith(B, edge);
      S->moveBefore(*edge, edge->begin());
      ++changed;
    }
  }
  return changed;
}

// Unroll short nested scans with conditional stores. LLVM retains all exits and
// bounds checks and builds a runtime remainder where necessary.
extern "C" unsigned elisacoreUnrollNestedScans(LLVMModuleRef ref) {
  if (!ref) return 0;
  Module &M = *unwrap(ref);
  if (!M.getTargetTriple().isOSLinux() || M.getTargetTriple().getArch() != Triple::aarch64 ||
      M.getNamedMetadata("llvm.dbg.cu")) return 0;
  PassBuilder PB;
  LoopAnalysisManager LAM; FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM; ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM); PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM); PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
  unsigned changed = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.hasPersonalityFn() || F.hasGC() || F.getSubprogram() ||
        F.hasFnAttribute(Attribute::Naked) || F.hasFnAttribute(Attribute::Convergent) ||
        F.hasFnAttribute(Attribute::NoDuplicate) || F.getEntryCount().has_value()) continue;
    auto &LI = FAM.getResult<LoopAnalysis>(F);
    auto &SE = FAM.getResult<ScalarEvolutionAnalysis>(F);
    auto &DT = FAM.getResult<DominatorTreeAnalysis>(F);
    auto &AC = FAM.getResult<AssumptionAnalysis>(F);
    auto &TTI = FAM.getResult<TargetIRAnalysis>(F);
    auto &ORE = FAM.getResult<OptimizationRemarkEmitterAnalysis>(F);
    bool done = false;
    for (Loop *root : LI) {
      if (done) break;
      for (Loop *L : root->getLoopsInPreorder()) {
        if (!L->getParentLoop() || !L->getSubLoops().empty() || L->getLoopID() ||
            L->getNumBlocks() < 5 || L->getNumBlocks() > 8) continue;
        BasicBlock *latch = L->getLoopLatch();
        if (!latch) continue;
        auto *branch = dyn_cast<CondBrInst>(latch->getTerminator());
        auto *cmp = branch ? dyn_cast<ICmpInst>(branch->getCondition()) : nullptr;
        if (!cmp || !cmp->isEquality()) continue;
        bool induction = false;
        for (Value *v : cmp->operands())
          if (auto *add = dyn_cast<BinaryOperator>(v))
            if (add->getOpcode() == Instruction::Add)
              for (unsigned i = 0; i < 2; ++i)
                if (auto *phi = dyn_cast<PHINode>(add->getOperand(i)))
                  if (phi->getParent() == L->getHeader())
                    if (auto *one = dyn_cast<ConstantInt>(add->getOperand(1 - i)))
                      if (one->isOne()) induction = true;
        if (!induction) continue;
        unsigned loads = 0, stores = 0, instructions = 0, conditions = 0;
        bool safe = true;
        for (BasicBlock *BB : L->blocks()) {
          if (BB->hasAddressTaken() || BB->isEHPad()) safe = false;
          for (Instruction &I : *BB) {
            ++instructions;
            if (auto *load = dyn_cast<LoadInst>(&I)) { ++loads; safe &= load->isSimple(); }
            else if (auto *store = dyn_cast<StoreInst>(&I)) { ++stores; safe &= store->isSimple(); }
            else if (isa<CondBrInst>(I)) ++conditions;
            else if (isa<CallBase>(I) || isa<AllocaInst>(I) || isa<IndirectBrInst>(I) ||
                     I.getType()->isTokenTy()) safe = false;
          }
        }
        if (!safe || loads < 2 || stores < 1 || conditions < 3 || instructions > 65) continue;
        simplifyLoop(L, &DT, &LI, &SE, &AC, nullptr, false);
        if (!L->isLoopSimplifyForm()) continue;
        formLCSSA(*L, DT, &LI, &SE);
        UnrollLoopOptions options{2, true, true, false, true, false, nullptr, 1000, true, false};
        if (UnrollLoop(L, options, &LI, &SE, &DT, &AC, &TTI, &ORE, true) != LoopUnrollResult::Unmodified)
          ++changed;
        done = true; // Recompute analyses before considering another loop in this function.
        break;
      }
    }
  }
  return changed;
}

static bool shouldRevisitNestedVectorLoops(Module &M) {
  if (M.getNamedMetadata("llvm.dbg.cu") || !M.getTargetTriple().isAArch64())
    return false;
  unsigned Functions = 0, Instructions = 0;
  for (Function &F : M) {
    if (F.isDeclaration()) continue;
    if (++Functions > 4 || (Instructions += F.getInstructionCount()) > 2000 ||
        F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.hasFnAttribute(Attribute::SanitizeAddress) ||
        F.hasFnAttribute(Attribute::SanitizeMemory) ||
        F.hasFnAttribute(Attribute::SanitizeThread) ||
        F.hasFnAttribute(Attribute::SanitizeHWAddress)) return false;
  }
  for (Function &F : M) {
    if (F.isDeclaration()) continue;
    DominatorTree DT(F); LoopInfo LI(DT);
    for (Loop *L : LI.getLoopsInPreorder()) {
      if (!L->getParentLoop()) continue;
      for (BasicBlock *B : L->blocks())
        for (Instruction &I : *B)
          if (auto *Load = dyn_cast<LoadInst>(&I))
            if (Load->getType()->isVectorTy()) return true;
    }
  }
  return false;
}

extern "C" unsigned elisacoreShouldRevisitNestedVectors(LLVMModuleRef R) { return R && shouldRevisitNestedVectorLoops(*unwrap(R)); }

#else
extern "C" unsigned elisacoreShouldRevisitNestedVectors(LLVMModuleRef) { return 0; }
extern "C" unsigned elisacorePredicatePrivateBitUpdates(LLVMModuleRef) { return 0; }
extern "C" unsigned elisacoreSinkPrivateInitializations(LLVMModuleRef) { return 0; }
extern "C" unsigned elisacoreInlineSmallRecursion(LLVMModuleRef) { return 0; }
extern "C" unsigned elisacoreUnrollNestedScans(LLVMModuleRef) { return 0; }
extern "C" unsigned elisacorePeelSmallValidationLoops(LLVMModuleRef) { return 0; }
extern "C" unsigned elisacoreCsscAbsoluteRangeChecks(LLVMModuleRef, LLVMTargetMachineRef) { return 0; }
#endif
