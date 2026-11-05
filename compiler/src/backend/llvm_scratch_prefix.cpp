//go:build cgo
// Remove a private byte-array zero fill only after proving that
// all stores append to a contiguous initialized prefix and every later load is
// within that prefix and within the allocation.
//
// Proof domain: at each CFG point, `prefixAliases` is the finite set of SSA
// values proven equal to the count of bytes explicitly written from offset 0;
// `belowCapacity` records aliases proven < buffer size; `readBelowPrefix` records
// indices proven < prefix; `readInBounds` records indices proven < buffer size.
// Edge transfer maps these facts through PHIs by comparing each incoming value
// with the predecessor fact sets. A candidate store is accepted only at an exact
// prefix alias, after a true branch proving that alias < capacity; the store must
// be followed immediately by add(alias, 1), which becomes the new prefix alias.
// Unknown branches fork both ways. Candidate loads require both a prefix-range
// fact and a fixed-buffer-bounds fact. Any gap/overwrite, unmodeled alias, other
// call, unsupported CFG, state-cap overflow, or unrecognized relation rejects.
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/Analysis/CFG.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/IR/Dominators.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/Constants.h>
#include <llvm/IR/DerivedTypes.h>
#include <llvm/IR/InstIterator.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Metadata.h>
#include <llvm/IR/ValueHandle.h>
#include <llvm/Config/llvm-config.h>
#include <llvm/Analysis/AssumptionCache.h>
#include <llvm/Analysis/OptimizationRemarkEmitter.h>
#include <llvm/Analysis/TargetLibraryInfo.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Transforms/Utils/SimplifyLibCalls.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/CFG.h>
#include <llvm/ADT/PostOrderIterator.h>
#include <algorithm>
#include <cstdint>
#include <deque>
#include <map>
#include <optional>
#include <set>
#include <string>
#include <vector>
using namespace llvm;

using ValueSet = std::set<Value *>;
struct PrefixState {
  unsigned block = 0;
  bool initialized = false;
  bool appendPending = false;
  Value *pendingIndex = nullptr;
  ValueSet prefixAliases;
  ValueSet belowCapacity;
  ValueSet readBelowPrefix;
  ValueSet readInBounds;
  bool operator<(const PrefixState &R) const {
    if (block != R.block) return block < R.block;
    if (initialized != R.initialized) return initialized < R.initialized;
    if (appendPending != R.appendPending) return appendPending < R.appendPending;
    if (pendingIndex != R.pendingIndex) return std::less<Value *>{}(pendingIndex, R.pendingIndex);
    if (prefixAliases != R.prefixAliases) return prefixAliases < R.prefixAliases;
    if (belowCapacity != R.belowCapacity) return belowCapacity < R.belowCapacity;
    if (readBelowPrefix != R.readBelowPrefix) return readBelowPrefix < R.readBelowPrefix;
    return readInBounds < R.readInBounds;
  }
};
static std::optional<int64_t> const64(Value *V) {
  auto *C = dyn_cast<ConstantInt>(V);
  if (!C || !C->getType()->isIntegerTy(64)) return std::nullopt;
  return C->getValue().getSExtValue();
}
static bool isZeroConstant(Value *V) {
  auto *C = dyn_cast<ConstantInt>(V);
  return C && C->isZero();
}
static bool indexedBytePointer(Value *P, AllocaInst *A, unsigned size,
                               Value *&index) {
  auto *G = dyn_cast<GetElementPtrInst>(P);
  auto *AT = dyn_cast<ArrayType>(A->getAllocatedType());
  if (!G || !AT || G->getPointerOperand() != A || G->getNumIndices() != 2 ||
      G->getSourceElementType() != A->getAllocatedType() ||
      G->getAddressSpace() != A->getAddressSpace() ||
      !AT->getElementType()->isIntegerTy(8) ||
      G->getResultElementType() != AT->getElementType()) return false;
  auto I = G->idx_begin();
  auto *zero = dyn_cast<ConstantInt>(I->get()); ++I;
  if (!zero || !zero->isZero() || !I->get()->getType()->isIntegerTy(64)) return false;
  index = I->get();
  // Range is established by path facts, so dynamic indices are accepted here.
  return size == AT->getNumElements();
}
static bool isCanonicalMemset(CallBase *CB, AllocaInst *A, uint64_t size) {
  auto *MS = dyn_cast_or_null<MemSetInst>(CB);
  if (!MS || MS->isVolatile() || MS->hasOperandBundles() ||
      MS->getRawDest() != A ||
      !isZeroConstant(MS->getValue())) return false;
  SmallVector<std::pair<unsigned, MDNode *>, 8> MDs;
  MS->getAllMetadata(MDs);
  for (const auto &MD : MDs)
    if (MD.first != LLVMContext::MD_dbg) return false;
  auto *AT = dyn_cast<ArrayType>(A->getAllocatedType());
  auto len = const64(MS->getLength());
  return AT && len && *len >= 0 && uint64_t(*len) == size;
}
static bool appendIncrement(Instruction &I, Value *index, Value *&next) {
  auto *B = dyn_cast<BinaryOperator>(&I);
  if (!B || B->getOpcode() != Instruction::Add || B->hasNoSignedWrap() || B->hasNoUnsignedWrap())
    return false;
  Value *other = nullptr;
  if (B->getOperand(0) == index) other = B->getOperand(1);
  else if (B->getOperand(1) == index) other = B->getOperand(0);
  if (!other) return false;
  auto C = const64(other);
  if (!C || *C != 1) return false;
  next = B;
  return true;
}

static bool verifyScratchPrefix(Function &, AllocaInst *, CallBase *, unsigned &,
                                std::string &, uint64_t &);

#if LLVM_VERSION_MAJOR >= 23
static bool candidateByteArray(AllocaInst *A) {
  auto *AT = A ? dyn_cast<ArrayType>(A->getAllocatedType()) : nullptr;
  auto *Count = A ? dyn_cast<ConstantInt>(A->getArraySize()) : nullptr;
  return A && A->isStaticAlloca() && Count && Count->isOne() && AT &&
         AT->getElementType()->isIntegerTy(8) && AT->getNumElements() >= 256 &&
         AT->getNumElements() <= 8192;
}

static bool canonicalizeTrustedMemset(CallInst *CI, AllocaInst *A,
                                     TargetLibraryInfo &TLI,
                                     LibCallSimplifier &LCS) {
  if (!CI || !A || CI->arg_size() != 3 || CI->isNoBuiltin() ||
      CI->getCallingConv() != CallingConv::C || CI->hasOperandBundles() ||
      CI->hasMetadataOtherThanDebugLoc() || CI->getCalledFunction() == nullptr)
    return false;
  Function *Callee = CI->getCalledFunction();
  LibFunc LF;
  if (!Callee->isDeclaration() || Callee->hasFnAttribute(Attribute::NoBuiltin) ||
      !TLI.getLibFunc(*CI, LF) || LF != LibFunc_memset || !TLI.has(LF))
    return false;
  auto *AT = dyn_cast<ArrayType>(A->getAllocatedType());
  auto *Dest = dyn_cast<AllocaInst>(CI->getArgOperand(0));
  auto *Fill = dyn_cast<ConstantInt>(CI->getArgOperand(1));
  auto *Length = dyn_cast<ConstantInt>(CI->getArgOperand(2));
  if (!AT || Dest != A || !Fill || !Fill->isZero() || !Length ||
      Length->getValue() != APInt(Length->getBitWidth(), AT->getNumElements()))
    return false;
  WeakTrackingVH Handle(CI);
  IRBuilder<> B(CI);
  Value *Replacement = LCS.optimizeCall(CI, B);
  if (!Replacement || !Handle || Replacement == CI) return false;
  CI->replaceAllUsesWith(Replacement);
  CI->eraseFromParent();
  return true;
}

#include "llvm_covered_prefix.cpp"

extern "C" unsigned elisacoreElideScratchPrefixInitializers(LLVMModuleRef Ref) {
  if (!Ref) return 0;
  Module &M = *unwrap(Ref);
  if (M.getNamedMetadata("llvm.dbg.cu")) return 0;
  PassBuilder PB;
  LoopAnalysisManager LAM;
  FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM;
  ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM);
  PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM);
  PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
  unsigned Removed = prepareCoveredPrefixZeros(M, FAM, PB), Analyses = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() ||
        F.hasFnAttribute(Attribute::SanitizeAddress) ||
        F.hasFnAttribute(Attribute::SanitizeHWAddress) ||
        F.hasFnAttribute(Attribute::SanitizeMemory) ||
        F.hasFnAttribute(Attribute::SanitizeThread))
      continue;
    SmallVector<AllocaInst *, 8> Arrays;
    unsigned InstructionCount = 0;
    for (Instruction &I : instructions(F)) {
      if (++InstructionCount > 50000) { Arrays.clear(); break; }
      if (auto *A = dyn_cast<AllocaInst>(&I); candidateByteArray(A))
        Arrays.push_back(A);
    }
    if (Arrays.empty() || Arrays.size() > 16) continue;
    auto &TLI = FAM.getResult<TargetLibraryAnalysis>(F);
    auto &DT = FAM.getResult<DominatorTreeAnalysis>(F);
    auto &AC = FAM.getResult<AssumptionAnalysis>(F);
    auto &ORE = FAM.getResult<OptimizationRemarkEmitterAnalysis>(F);
    LibCallSimplifier LCS(M.getDataLayout(), &TLI, &DT, nullptr, &AC, ORE,
                          nullptr, nullptr);
    uint64_t FunctionWorkUnits = 0;
    for (AllocaInst *A : Arrays) {
      if (Analyses >= 256) return Removed;
      ++Analyses;
      SmallVector<CallInst *, 2> ExternalInitializers;
      for (User *U : A->users())
        if (auto *CI = dyn_cast<CallInst>(U)) ExternalInitializers.push_back(CI);
      for (CallInst *CI : ExternalInitializers)
        canonicalizeTrustedMemset(CI, A, TLI, LCS);
      CallBase *Init = nullptr;
      unsigned InitCount = 0;
      for (User *U : A->users())
        if (auto *CB = dyn_cast<CallBase>(U))
          if (isCanonicalMemset(CB, A,
                                cast<ArrayType>(A->getAllocatedType())->getNumElements())) {
            Init = CB;
            ++InitCount;
          }
      if (!Init || InitCount != 1) continue;
      unsigned States = 0;
      std::string Reason;
      if (verifyScratchPrefix(F, A, Init, States, Reason, FunctionWorkUnits)) {
        Init->eraseFromParent();
        ++Removed;
      }
    }
  }
  return Removed;
}
extern "C" unsigned elisacoreScratchPrefixSupported(void) { return 1; }
#else
extern "C" unsigned elisacoreElideScratchPrefixInitializers(LLVMModuleRef) {
  return 0;
}
extern "C" unsigned elisacoreScratchPrefixSupported(void) { return 0; }
#endif
static void refineBranchFacts(PrefixState &S, BranchInst *B, unsigned successorIndex,
                              unsigned capacity) {
  if (!B || !B->isConditional()) return;
  auto *IC = dyn_cast<ICmpInst>(B->getCondition());
  if (!IC) return;
  bool trueEdge = successorIndex == 0;
  Value *L = IC->getOperand(0), *R = IC->getOperand(1);
  auto RHS = const64(R), LHS = const64(L);
  // Record index < exact capacity and cursor < exact capacity only on the
  // comparison's true edge. A false UGE edge is equivalent and is also handled.
  bool positive = IC->getPredicate() == ICmpInst::ICMP_ULT && trueEdge;
  bool inverse = IC->getPredicate() == ICmpInst::ICMP_UGE && !trueEdge;
  if ((positive || inverse) && RHS && uint64_t(*RHS) == capacity) {
    S.readInBounds.insert(L);
    if (S.prefixAliases.count(L)) S.belowCapacity.insert(L);
  }
  // Stores may append only under a branch proving the current cursor is below N.
  if ((positive || inverse) && RHS && uint64_t(*RHS) == capacity && S.prefixAliases.count(L))
    S.belowCapacity.insert(L);
  // For later reads, preserve the relational bound index < current prefix.
  bool belowPrefix = (IC->getPredicate() == ICmpInst::ICMP_ULT && trueEdge) ||
                     (IC->getPredicate() == ICmpInst::ICMP_UGE && !trueEdge);
  if (belowPrefix && S.prefixAliases.count(R)) S.readBelowPrefix.insert(L);
  // Also accept a reversed UGT/ULE comparison for the same strict relation.
  bool reversedBelowPrefix = (IC->getPredicate() == ICmpInst::ICMP_UGT && trueEdge) ||
                             (IC->getPredicate() == ICmpInst::ICMP_ULE && !trueEdge);
  if (reversedBelowPrefix && S.prefixAliases.count(L)) S.readBelowPrefix.insert(R);
  (void)LHS;
}
static bool verifyScratchPrefix(Function &F, AllocaInst *A, CallBase *Init,
                                unsigned &statesVisited, std::string &reason,
                                uint64_t &functionWorkUnits) {
  auto *AT = dyn_cast<ArrayType>(A->getAllocatedType());
  if (!AT || AT->getNumElements() == 0 || AT->getNumElements() > 8192 ||
      !AT->getElementType()->isIntegerTy(8) || !A->isStaticAlloca() ||
      A->getAddressSpace() != 0 || A->isUsedByMetadata() || !Init ||
      !isCanonicalMemset(Init, A, AT->getNumElements())) {
    reason = "unsupported buffer/init shape"; return false;
  }
  if (F.hasFnAttribute(Attribute::SanitizeAddress) ||
      F.hasFnAttribute(Attribute::SanitizeHWAddress) ||
      F.hasFnAttribute(Attribute::SanitizeMemory) ||
      F.hasFnAttribute(Attribute::SanitizeThread)) {
    reason = "sanitized function is outside the proof model"; return false;
  }
  unsigned capacity = unsigned(AT->getNumElements());
  DominatorTree DT(F);
  LoopInfo LI(DT);
  ReversePostOrderTraversal<Function *> RPO(&F);
  if (containsIrreducibleCFG<BasicBlock *>(RPO, LI)) {
    reason = "irreducible CFG is outside the supported loop transfer model";
    return false;
  }
  SmallVector<GetElementPtrInst *, 16> geps;
  for (User *U : A->users()) {
    if (U == Init) continue;
    auto *G = dyn_cast<GetElementPtrInst>(U);
    if (!G || G->getPointerOperand() != A || G->isUsedByMetadata()) {
      reason = "buffer alias/escape/debug metadata"; return false;
    }
    geps.push_back(G);
    if (geps.size() > 4096) { reason = "candidate GEP cap exceeded"; return false; }
    for (User *GU : G->users()) {
      if (auto *L = dyn_cast<LoadInst>(GU)) {
        if (L->getPointerOperand() != G || !L->isSimple() ||
            L->getType() != AT->getElementType() || !DT.dominates(Init, L)) {
          reason = "unsupported load/volatile access or init does not dominate"; return false;
        }
      } else if (auto *S = dyn_cast<StoreInst>(GU)) {
        if (S->getPointerOperand() != G || !S->isSimple() ||
            S->getValueOperand()->getType() != AT->getElementType() || !DT.dominates(Init, S)) {
          reason = "unsupported store/volatile access or init does not dominate"; return false;
        }
      } else { reason = "unsupported GEP user"; return false; }
    }
  }
  if (geps.empty()) { reason = "buffer has no indexed access"; return false; }
  for (auto *G : geps) {
    if (G->getNumIndices() != 2 || G->getSourceElementType() != A->getAllocatedType() ||
        G->getResultElementType() != AT->getElementType()) {
      reason = "noncanonical typed array GEP"; return false;
    }
  }
  SmallVector<BasicBlock *, 64> blocks;
  for (BasicBlock &B : F) blocks.push_back(&B);
  auto blockId = [&](BasicBlock *B) {
    return unsigned(std::find(blocks.begin(), blocks.end(), B) - blocks.begin());
  };
  std::map<PrefixState, bool> seen;
  std::deque<PrefixState> work;
  uint64_t totalFacts = 0;
  auto factCount = [](const PrefixState &S) {
    return S.prefixAliases.size() + S.belowCapacity.size() +
           S.readBelowPrefix.size() + S.readInBounds.size();
  };
  PrefixState initial;
  initial.block = blockId(&F.getEntryBlock());
  seen[initial] = true;
  work.push_back(initial);
  statesVisited = 0;
  while (!work.empty()) {
    PrefixState S = work.front(); work.pop_front();
    if (++statesVisited > 4096) { reason = "state/work cap exceeded"; return false; }
    BasicBlock *BB = blocks[S.block];
    for (Instruction &I : *BB) {
      if (++functionWorkUnits > 2000000) {
        reason = "per-function instruction-visit cap exceeded"; return false;
      }
      if (&I == Init) {
        if (S.initialized) { reason = "initializer executes more than once"; return false; }
        S.initialized = true;
        S.prefixAliases.clear(); S.belowCapacity.clear();
        S.readBelowPrefix.clear(); S.readInBounds.clear();
        S.prefixAliases.insert(ConstantInt::get(Type::getInt64Ty(F.getContext()), 0));
        S.belowCapacity.insert(ConstantInt::get(Type::getInt64Ty(F.getContext()), 0));
        continue;
      }
      if (S.appendPending) {
        Value *next = nullptr;
        if (!appendIncrement(I, S.pendingIndex, next)) {
          reason = "append store is not followed by cursor + 1"; return false;
        }
        // Prefix aliases describe the current cursor only. Keeping the old
        // cursor here would permit a later store to overwrite an earlier byte
        // and falsely advance the committed prefix across a gap.
        S.prefixAliases.clear();
        S.prefixAliases.insert(next);
        // A successful append at cursor < capacity leaves the new prefix <= capacity.
        // We know next <= capacity, not next < capacity. In particular the
        // last legal append may make next exactly capacity; another append
        // still needs a fresh strict in-bounds branch.
        S.appendPending = false; S.pendingIndex = nullptr;
        continue;
      }
      if (auto *L = dyn_cast<LoadInst>(&I)) {
        if (getUnderlyingObject(L->getPointerOperand()) == A) {
          Value *idx = nullptr;
          if (!S.initialized || !indexedBytePointer(L->getPointerOperand(), A, capacity, idx) ||
              !S.readBelowPrefix.count(idx) || !S.readInBounds.count(idx)) {
            reason = "load lacks index < prefix and index < capacity proof"; return false;
          }
        }
      }
      if (auto *St = dyn_cast<StoreInst>(&I)) {
        if (getUnderlyingObject(St->getPointerOperand()) == A) {
          Value *idx = nullptr;
          if (!S.initialized || !indexedBytePointer(St->getPointerOperand(), A, capacity, idx) ||
              !S.prefixAliases.count(idx) || !S.belowCapacity.count(idx)) {
            reason = "store is not at a proven in-bounds prefix end (gap/overwrite)"; return false;
          }
          S.appendPending = true;
          S.pendingIndex = idx;
        }
      }
      if (auto *CB = dyn_cast<CallBase>(&I)) {
        if (isa<IntrinsicInst>(CB) && cast<IntrinsicInst>(CB)->getIntrinsicID() == Intrinsic::trap) continue;
        // The candidate alloca has already been proven unescaped. A call with
        // a read-only memory effect cannot mutate its bytes; calls receiving
        // any candidate-derived pointer were rejected by the use audit.
        if (CB->onlyReadsMemory() && !CB->hasOperandBundles()) continue;
        reason = "unmodeled call"; return false;
      }
      if (I.mayReadOrWriteMemory() && !isa<LoadInst>(I) && !isa<StoreInst>(I)) {
        reason = "unmodeled memory instruction"; return false;
      }
    }
    if (S.appendPending) { reason = "append reaches block edge without cursor increment"; return false; }
    SmallVector<BasicBlock *, 2> successors;
    auto *T = BB->getTerminator();
    if (auto *B = dyn_cast<BranchInst>(T)) {
      if (!B->isConditional()) successors.push_back(B->getSuccessor(0));
      else { successors.push_back(B->getSuccessor(0)); successors.push_back(B->getSuccessor(1)); }
    } else if (!isa<ReturnInst>(T) && !isa<UnreachableInst>(T)) {
      reason = "unsupported terminator"; return false;
    }
    for (unsigned e = 0; e < successors.size(); ++e) {
      PrefixState N = S;
      N.block = blockId(successors[e]);
      refineBranchFacts(N, dyn_cast<BranchInst>(T), e, capacity);
      struct PhiFacts { PHINode *phi; bool prefix, capacity, readPrefix, inBounds; };
      SmallVector<PhiFacts, 8> phiFacts;
      for (PHINode &P : successors[e]->phis()) {
        int incoming = P.getBasicBlockIndex(BB);
        if (incoming < 0) { reason = "missing PHI edge"; return false; }
        Value *V = P.getIncomingValue(incoming);
        phiFacts.push_back({&P, N.prefixAliases.count(V) != 0,
                            N.belowCapacity.count(V) != 0,
                            N.readBelowPrefix.count(V) != 0,
                            N.readInBounds.count(V) != 0});
        N.prefixAliases.erase(&P); N.belowCapacity.erase(&P);
        N.readBelowPrefix.erase(&P); N.readInBounds.erase(&P);
      }
      // On a backedge, instruction results from the previous iteration are
      // stale dynamic values even though they have the same static SSA name.
      // Carry only facts explicitly transferred through target PHIs. The
      // target-dominates-source test is a conservative natural-loop backedge
      // detector; it may kill extra facts, but cannot make a proof stronger.
      if (DT.dominates(successors[e], BB)) {
        auto killLoopLocal = [&](ValueSet &facts) {
          for (auto I = facts.begin(); I != facts.end();) {
            auto *Def = dyn_cast<Instruction>(*I);
            if (Def && DT.dominates(successors[e], Def->getParent())) I = facts.erase(I);
            else ++I;
          }
        };
        killLoopLocal(N.prefixAliases); killLoopLocal(N.belowCapacity);
        killLoopLocal(N.readBelowPrefix); killLoopLocal(N.readInBounds);
      }
      for (const PhiFacts &PF : phiFacts) {
        if (PF.prefix) N.prefixAliases.insert(PF.phi);
        if (PF.capacity) N.belowCapacity.insert(PF.phi);
        if (PF.readPrefix) N.readBelowPrefix.insert(PF.phi);
        if (PF.inBounds) N.readInBounds.insert(PF.phi);
      }
      if (factCount(N) > 1024) { reason = "per-state fact cap exceeded"; return false; }
      if (seen.size() >= 4096 && !seen.count(N)) { reason = "abstract state cap exceeded"; return false; }
      if (!seen.count(N)) {
        totalFacts += factCount(N);
        if (totalFacts > 200000) { reason = "aggregate fact-entry cap exceeded"; return false; }
        seen[N] = true; work.push_back(std::move(N));
      }
    }
  }
  return true;
}
