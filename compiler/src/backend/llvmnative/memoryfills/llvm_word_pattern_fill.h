// Keep small constant word-pattern fills in the generated function rather
// than calling the Darwin pattern-fill runtime. Use only fixed stack storage.
static unsigned inlineStackWordPatternFills(Module &M) {
#if LLVM_VERSION_MAJOR >= 23
  if (!M.getTargetTriple().isMacOSX() ||
      M.getNamedValue("__elisacore_fill_stack_words"))
    return 0;
  SmallVector<MemSetPatternInst *, 8> Work;
  for (Function &F : M) {
    if (F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.hasFnAttribute(Attribute::SanitizeAddress) ||
        F.hasFnAttribute(Attribute::SanitizeHWAddress) ||
        F.hasFnAttribute(Attribute::SanitizeMemory) ||
        F.hasFnAttribute(Attribute::SanitizeThread))
      continue;
    SmallVector<MemSetPatternInst *, 2> Candidates;
    for (BasicBlock &B : F)
      for (Instruction &I : B) {
        auto *Fill = dyn_cast<MemSetPatternInst>(&I);
        if (!Fill || Fill->isVolatile() || Fill->hasOperandBundles() ||
            Fill->getDestAlign().valueOrOne().value() < 8 ||
            !isa<ConstantInt>(Fill->getValue()) ||
            !Fill->getValue()->getType()->isIntegerTy(64) ||
            !Fill->getLength()->getType()->isIntegerTy(64))
          continue;
        auto *Array = dyn_cast<AllocaInst>(Fill->getRawDest());
        if (!Array || !Array->isStaticAlloca() || Array->isArrayAllocation() ||
            Array->getAddressSpace() != 0 || Array->isUsedWithInAlloca() ||
            Array->isSwiftError())
          continue;
        auto *Type = dyn_cast<ArrayType>(Array->getAllocatedType());
        if (!Type || !Type->getElementType()->isIntegerTy(64) ||
            Type->getNumElements() == 0 || Type->getNumElements() > 64)
          continue;
        Candidates.push_back(Fill);
      }
    if (Candidates.size() == 1) Work.push_back(Candidates.front());
  }
  if (Work.empty()) return 0;

  SMDiagnostic Error;
  bool DiscardNames = M.getContext().shouldDiscardValueNames();
  M.getContext().setDiscardValueNames(false);
  auto Helper = parseAssemblyString(R"ELISA(define void @__elisacore_fill_stack_words(ptr %base, i64 %n, i64 %word) alwaysinline {
entry:
  %lane = insertelement <2 x i64> poison, i64 %word, i32 0
  %pattern = shufflevector <2 x i64> %lane, <2 x i64> poison, <2 x i32> zeroinitializer
  %groups = lshr i64 %n, 2
  %more = icmp ne i64 %groups, 0
  br i1 %more, label %bulk, label %tail
bulk:
  %i = phi i64 [ 0, %entry ], [ %next, %bulk ]
  %offset = shl i64 %i, 2
  %p = getelementptr i64, ptr %base, i64 %offset
  %p2 = getelementptr i64, ptr %p, i64 2
  store <2 x i64> %pattern, ptr %p, align 8
  store <2 x i64> %pattern, ptr %p2, align 8
  %next = add i64 %i, 1
  %repeat = icmp ult i64 %next, %groups
  br i1 %repeat, label %bulk, label %tail
tail:
  %left = and i64 %n, 3
  %start = and i64 %n, -4
  %p3 = getelementptr i64, ptr %base, i64 %start
  %has_two = icmp uge i64 %left, 2
  br i1 %has_two, label %pair, label %single
pair:
  store <2 x i64> %pattern, ptr %p3, align 8
  br label %single
single:
  %odd = trunc i64 %n to i1
  br i1 %odd, label %last, label %exit
last:
  %last_index = sub i64 %n, 1
  %last_ptr = getelementptr i64, ptr %base, i64 %last_index
  store i64 %word, ptr %last_ptr, align 8
  br label %exit
exit:
  ret void
}
)ELISA", Error, M.getContext());
  M.getContext().setDiscardValueNames(DiscardNames);
  if (!Helper) return 0;
  Helper->setDataLayout(M.getDataLayout());
  Helper->setTargetTriple(M.getTargetTriple());
  if (Linker::linkModules(M, std::move(Helper))) return 0;
  Function *Routine = M.getFunction("__elisacore_fill_stack_words");
  if (!Routine) return 0;
  Routine->setLinkage(GlobalValue::InternalLinkage);
  unsigned Changed = 0;
  for (MemSetPatternInst *Fill : Work) {
    IRBuilder<> Builder(Fill);
    auto *Call = Builder.CreateCall(Routine, {Fill->getRawDest(), Fill->getLength(), Fill->getValue()});
    Call->setDebugLoc(Fill->getDebugLoc());
    InlineFunctionInfo Info;
    if (InlineFunction(*Call, Info).isSuccess()) {
      Fill->eraseFromParent();
      ++Changed;
    } else {
      Call->eraseFromParent();
    }
  }
  if (Routine->use_empty()) Routine->eraseFromParent();
  return Changed;
#else
  return 0;
#endif
}
