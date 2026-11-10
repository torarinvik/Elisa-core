// Prove that nested counted loops initialize every element read from a private
// zero-filled floating-point array. Unknown aliases, irregular loops, skipped
// writes, exceptional exits, and unsupported accesses retain initialization.
namespace ElisaCoveredArray {
struct Induction {
  Loop *loop;
  Value *bound;
  BasicBlock *body;
};
static bool induction(Value *V, LoopInfo &LI, Induction &out) {
  auto *P = dyn_cast<PHINode>(V);
  if (!P || !P->getType()->isIntegerTy(64))
    return false;
  Loop *L = LI.getLoopFor(P->getParent());
  if (!L || L->getHeader() != P->getParent() || !L->isLoopSimplifyForm() ||
      P->getNumIncomingValues() != 2)
    return false;
  auto *Pre = L->getLoopPreheader();
  auto *Latch = L->getLoopLatch();
  if (!Pre || !Latch)
    return false;
  auto *Z = dyn_cast_or_null<ConstantInt>(P->getIncomingValueForBlock(Pre));
  auto *A =
      dyn_cast_or_null<BinaryOperator>(P->getIncomingValueForBlock(Latch));
  if (!Z || !Z->isZero() || !A || A->getOpcode() != Instruction::Add)
    return false;
  auto *One = dyn_cast<ConstantInt>(A->getOperand(1));
  if (A->getOperand(0) != P || !One || !One->isOne())
    return false;
  auto *B = dyn_cast<CondBrInst>(L->getHeader()->getTerminator());
  auto *C = B ? dyn_cast<ICmpInst>(B->getCondition()) : nullptr;
  if (!C || C->getPredicate() != ICmpInst::ICMP_ULT || C->getOperand(0) != P ||
      !L->isLoopInvariant(C->getOperand(1)) ||
      !L->contains(B->getSuccessor(0)) || L->contains(B->getSuccessor(1)))
    return false;
  out = {L, C->getOperand(1), B->getSuccessor(0)};
  return true;
}
static Value *indexOf(Value *Ptr, AllocaInst *A, const DataLayout &DL) {
  auto *G = dyn_cast<GetElementPtrInst>(Ptr);
  if (!G || G->getPointerOperand() != A || G->getNumIndices() != 1 ||
      DL.getTypeAllocSize(G->getSourceElementType()) != 8)
    return nullptr;
  Value *I = G->idx_begin()->get();
  return I->getType()->isIntegerTy(64) ? I : nullptr;
}
static bool sameIndex(Value *W, Value *R, Instruction *Read, LoopInfo &LI,
                      DominatorTree &DT, std::map<Loop *, Value *> &used,
                      unsigned depth = 0) {
  if (depth > 12)
    return false;
  if (W == R) {
    auto *I = dyn_cast<Instruction>(W);
    return !I || !LI.getLoopFor(I->getParent());
  }
  auto *WC = dyn_cast<ConstantInt>(W);
  auto *RC = dyn_cast<ConstantInt>(R);
  if (WC || RC)
    return WC && RC && WC->getValue() == RC->getValue();
  Induction a, b;
  if (induction(W, LI, a) && induction(R, LI, b)) {
    if (a.bound != b.bound ||
        !DT.dominates(BasicBlockEdge(b.loop->getHeader(), b.body),
                      Read->getParent()))
      return false;
    auto [it, inserted] = used.emplace(a.loop, a.bound);
    return inserted || it->second == a.bound;
  }
  auto *WB = dyn_cast<BinaryOperator>(W);
  auto *RB = dyn_cast<BinaryOperator>(R);
  if (!WB || !RB || WB->getOpcode() != RB->getOpcode() ||
      (WB->getOpcode() != Instruction::Add &&
       WB->getOpcode() != Instruction::Mul))
    return false;
  return sameIndex(WB->getOperand(0), RB->getOperand(0), Read, LI, DT, used,
                   depth + 1) &&
         sameIndex(WB->getOperand(1), RB->getOperand(1), Read, LI, DT, used,
                   depth + 1);
}
static bool isDeadEnd(BasicBlock *B) {
  for (Instruction &I : *B) {
    if (isa<UnreachableInst>(I))
      return true;
    if (auto *II = dyn_cast<IntrinsicInst>(&I)) {
      if (II->getIntrinsicID() == Intrinsic::trap)
        continue;
    }
    if (isa<DbgInfoIntrinsic>(I))
      continue;
    return false;
  }
  return false;
}
static BasicBlock *normalExit(Loop *L) {
  BasicBlock *normal = nullptr;
  for (BasicBlock *B : L->blocks())
    for (BasicBlock *E : successors(B)) {
      if (L->contains(E) || isDeadEnd(E))
        continue;
      // Any non-header exit could bypass a suffix of the initialization loop.
      if (B != L->getHeader() || (normal && normal != E))
        return nullptr;
      normal = E;
    }
  return normal;
}
static bool writerCovers(Instruction *Writer, std::map<Loop *, Value *> &used,
                         LoopInfo &LI, DominatorTree &DT, BasicBlock *&Exit) {
  Loop *Inner = LI.getLoopFor(Writer->getParent());
  if (!Inner || !used.count(Inner) ||
      !DT.dominates(Writer->getParent(), Inner->getLoopLatch()))
    return false;
  Loop *child = nullptr;
  unsigned seen = 0;
  for (Loop *L = Inner; L; L = L->getParentLoop()) {
    if (!used.count(L) || !L->isLoopSimplifyForm() || !normalExit(L))
      return false;
    seen++;
    if (child && !DT.dominates(child->getLoopPreheader(), L->getLoopLatch()))
      return false;
    child = L;
  }
  if (seen != used.size() || !child || LI.getLoopFor(child->getLoopPreheader()))
    return false;
  for (auto [L, Bound] : used)
    if (!child->isLoopInvariant(Bound))
      return false;
  Exit = normalExit(child);
  return Exit != nullptr;
}
static bool prove(AllocaInst *A, MemSetInst *Init, LoopInfo &LI,
                  DominatorTree &DT, const DataLayout &DL) {
  if (LI.getLoopFor(Init->getParent()))
    return false;
  SmallVector<Instruction *, 16> reads, writes;
  for (User *U : A->users()) {
    if (U == Init)
      continue;
    auto *G = dyn_cast<GetElementPtrInst>(U);
    if (!G || !indexOf(G, A, DL))
      return false;
    for (User *GU : G->users()) {
      if (auto *L = dyn_cast<LoadInst>(GU)) {
        if (!L->isSimple() || !L->getType()->isDoubleTy() ||
            L->getPointerOperand() != G)
          return false;
        reads.push_back(L);
      } else if (auto *S = dyn_cast<StoreInst>(GU)) {
        if (!S->isSimple() || !S->getValueOperand()->getType()->isDoubleTy() ||
            S->getPointerOperand() != G)
          return false;
        writes.push_back(S);
      } else
        return false;
    }
  }
  if (reads.empty())
    return false;
  for (Instruction *W : writes) {
    if (!DT.dominates(Init, W))
      continue;
    Value *wi = indexOf(cast<StoreInst>(W)->getPointerOperand(), A, DL);
    if (!wi)
      continue;
    bool all = true;
    for (Instruction *R : reads) {
      Value *ri = indexOf(cast<LoadInst>(R)->getPointerOperand(), A, DL);
      std::map<Loop *, Value *> used;
      BasicBlock *exit = nullptr;
      if (!ri || !sameIndex(wi, ri, R, LI, DT, used) ||
          !writerCovers(W, used, LI, DT, exit) ||
          !DT.dominates(exit, R->getParent())) {
        all = false;
        break;
      }
    }
    if (all)
      return true;
  }
  return false;
}
} // namespace ElisaCoveredArray

static unsigned prepareCoveredArrayInitializers(Module &M,
                                                FunctionAnalysisManager &FAM,
                                                PassBuilder &PB) {
  if (M.getNamedMetadata("llvm.dbg.cu") ||
      M.getTargetTriple().getArch() != Triple::aarch64 ||
      !(M.getTargetTriple().isMacOSX() || M.getTargetTriple().isOSLinux()))
    return 0;
  SmallVector<Function *, 8> candidates;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() ||
        F.hasMinSize() || F.hasFnAttribute(Attribute::SanitizeAddress) ||
        F.hasFnAttribute(Attribute::SanitizeMemory) ||
        F.hasFnAttribute(Attribute::SanitizeThread) ||
        F.hasFnAttribute(Attribute::SanitizeHWAddress))
      continue;
    unsigned instCount = 0;
    bool candidate = false;
    for (Instruction &I : instructions(F)) {
      if (++instCount > 4000) {
        candidate = false;
        break;
      }
      Value *dest = nullptr, *fill = nullptr, *length = nullptr;
      if (auto *S = dyn_cast<MemSetInst>(&I)) {
        if (S->isVolatile() || S->hasOperandBundles())
          continue;
        dest = S->getRawDest();
        fill = S->getValue();
        length = S->getLength();
      } else if (auto *C = dyn_cast<CallInst>(&I)) {
        // Screening only: LLVM's InstCombine must independently recognize and
        // canonicalize the libc call before the proof can remove anything.
        auto *callee = C->getCalledFunction();
        if (!callee || !callee->isDeclaration() ||
            callee->hasFnAttribute(Attribute::NoBuiltin) ||
            callee->getName() != "memset" || C->arg_size() != 3 ||
            C->getCallingConv() != CallingConv::C || C->hasOperandBundles() ||
            C->isNoBuiltin())
          continue;
        dest = C->getArgOperand(0);
        fill = C->getArgOperand(1);
        length = C->getArgOperand(2);
      } else
        continue;
      auto *A = dyn_cast<AllocaInst>(dest);
      auto *T = A ? dyn_cast<ArrayType>(A->getAllocatedType()) : nullptr;
      auto *V = dyn_cast<ConstantInt>(fill);
      auto *N = dyn_cast<ConstantInt>(length);
      if (T && T->getElementType()->isDoubleTy() &&
          T->getNumElements() >= 128 && T->getNumElements() <= 8192 && V &&
          V->isZero() && N && N->getValue().getActiveBits() <= 64 &&
          N->getZExtValue() == M.getDataLayout().getTypeAllocSize(T))
        candidate = true;
    }
    if (candidate)
      candidates.push_back(&F);
  }
  if (candidates.empty() || candidates.size() > 16)
    return 0;
  FunctionPassManager FPM;
  if (auto E = PB.parsePassPipeline(
          FPM, "mem2reg,instcombine,gvn,simplifycfg,loop-simplify,lcssa")) {
    consumeError(std::move(E));
    return 0;
  }
  unsigned removed = 0;
  for (Function *F : candidates) {
    FPM.run(*F, FAM);
    auto &DT = FAM.getResult<DominatorTreeAnalysis>(*F);
    auto &LI = FAM.getResult<LoopAnalysis>(*F);
    bool changed = false;
    SmallVector<MemSetInst *, 8> sets;
    for (Instruction &I : instructions(*F))
      if (auto *S = dyn_cast<MemSetInst>(&I))
        sets.push_back(S);
    for (auto *S : sets) {
      auto *A = dyn_cast<AllocaInst>(S->getRawDest());
      auto *T = A ? dyn_cast<ArrayType>(A->getAllocatedType()) : nullptr;
      auto *V = dyn_cast<ConstantInt>(S->getValue());
      auto *N = dyn_cast<ConstantInt>(S->getLength());
      if (!A || !T || !T->getElementType()->isDoubleTy() ||
          !A->isStaticAlloca() || T->getNumElements() < 128 ||
          T->getNumElements() > 8192 || !V || !V->isZero() || !N ||
          N->getValue().getActiveBits() > 64 ||
          N->getZExtValue() != M.getDataLayout().getTypeAllocSize(T) ||
          S->isVolatile() || S->hasOperandBundles())
        continue;
      if (ElisaCoveredArray::prove(A, S, LI, DT, M.getDataLayout())) {
        S->eraseFromParent();
        ++removed;
        changed = true;
      }
    }
    if (changed)
      FAM.invalidate(*F, PreservedAnalyses::none());
  }
  if (removed)
    M.getOrInsertNamedMetadata("elisa.covered-array-revisit")
        ->addOperand(MDNode::get(M.getContext(), {}));
  return removed;
}
