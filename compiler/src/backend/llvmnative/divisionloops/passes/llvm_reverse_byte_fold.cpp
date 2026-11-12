// Fold a reverse polynomial reduction over a privately stored byte prefix.
namespace {
struct ReverseByteFold {
  BasicBlock *Body, *Entry, *Exit;
  LoadInst *Read;
  BinaryOperator *Result;
  PHINode *Length;
  ConstantInt *Radix;
  AllocaInst *Array;
};

static bool matchReverseByteFold(BasicBlock &Body, ReverseByteFold &Fold) {
  if (Body.size() != 10 || Body.hasAddressTaken()) return false;
  auto *Back = dyn_cast<CondBrInst>(Body.getTerminator());
  auto *Test = Back ? dyn_cast<ICmpInst>(Back->getCondition()) : nullptr;
  if (!Test || Test->getParent() != &Body || Test->getPredicate() != ICmpInst::ICMP_EQ ||
      Back->getSuccessor(1) != &Body || Back->getSuccessor(0) == &Body ||
      !scalarLoopHasConstant(Test->getOperand(1), 0))
    return false;
  auto *Step = dyn_cast<BinaryOperator>(Test->getOperand(0));
  auto *Index = Step ? dyn_cast<PHINode>(Step->getOperand(0)) : nullptr;
  if (!Index || Index->getParent() != &Body || Step->getParent() != &Body ||
      Step->getOpcode() != Instruction::Add || !scalarLoopHasConstant(Step->getOperand(1), -1ULL) ||
      !Index->getType()->isIntegerTy(64) || Index->getNumIncomingValues() != 2 ||
      Index->getBasicBlockIndex(&Body) < 0 || Index->getIncomingValueForBlock(&Body) != Step)
    return false;
  auto *Entry = Index->getIncomingBlock(Index->getIncomingBlock(0) == &Body ? 1 : 0);
  auto *Length = dyn_cast<PHINode>(Index->getIncomingValueForBlock(Entry));
  auto *Enter = dyn_cast<UncondBrInst>(Entry->getTerminator());
  if (!Length || Length->getParent() != Entry || !Enter || Enter->getSuccessor() != &Body)
    return false;

  auto *Hash = cast<PHINode>(&Body.front());
  if (Hash == Index) Hash = dyn_cast<PHINode>(Hash->getNextNode());
  if (!Hash || Hash->getNumIncomingValues() != 2 ||
      Hash->getBasicBlockIndex(&Body) < 0 ||
      !scalarLoopHasConstant(Hash->getIncomingValueForBlock(Entry), 0))
    return false;
  auto *Add = dyn_cast<BinaryOperator>(Hash->getIncomingValueForBlock(&Body));
  auto *Mul = Add ? dyn_cast<BinaryOperator>(Add->getOperand(0)) : nullptr;
  auto *Extend = Add ? dyn_cast<ZExtInst>(Add->getOperand(1)) : nullptr;
  auto *Read = Extend ? dyn_cast<LoadInst>(Extend->getOperand(0)) : nullptr;
  auto *GEP = Read ? dyn_cast<GetElementPtrInst>(Read->getPointerOperand()) : nullptr;
  auto *Radix = Mul ? dyn_cast<ConstantInt>(Mul->getOperand(1)) : nullptr;
  auto *Array = GEP ? dyn_cast<AllocaInst>(GEP->getPointerOperand()) : nullptr;
  if (!Array || !Array->isStaticAlloca() || Array->isUsedWithInAlloca() || Array->isSwiftError() ||
      !Radix || Mul->getOpcode() != Instruction::Mul || Mul->getOperand(0) != Hash ||
      Add->getOpcode() != Instruction::Add || !Read->isSimple() ||
      !Read->getType()->isIntegerTy(8) || !Hash->getType()->isIntegerTy(64) ||
      !GEP->getSourceElementType()->isIntegerTy(8) || GEP->getNumIndices() != 1 ||
      GEP->getOperand(1) != Step)
    return false;
  for (Instruction *I : std::initializer_list<Instruction *>{Add, Mul, Extend, Read, GEP})
    if (I->getParent() != &Body) return false;
  for (Instruction &I : Body)
    if (&I != Add)
      for (User *U : I.users())
        if (auto *Use = dyn_cast<Instruction>(U); !Use || Use->getParent() != &Body)
          return false;
  Fold = {&Body, Entry, Back->getSuccessor(0), Read, Add, Length, Radix, Array};
  return true;
}

static bool findReverseByteStores(const ReverseByteFold &Fold, const DataLayout &DL,
                                  MutableArrayRef<StoreInst *> Stores, MemSetInst *&Fill) {
  SmallVector<Value *, 16> Pointers{Fold.Array};
  for (User *U : Fold.Array->users())
    if (auto *GEP = dyn_cast<GetElementPtrInst>(U)) {
      if (GEP->getPointerOperand() != Fold.Array) return false;
      Pointers.push_back(GEP);
    }
  for (Value *Pointer : Pointers)
    for (User *U : Pointer->users()) {
      if (U == Fold.Read || (Pointer == Fold.Array && is_contained(Pointers, U)))
        continue;
      if (auto *Store = dyn_cast<StoreInst>(U)) {
        int64_t Offset = 0;
        if (!Store->isSimple() || Store->getPointerOperand() != Pointer ||
            !Store->getValueOperand()->getType()->isIntegerTy(8) ||
            GetPointerBaseWithConstantOffset(Pointer, Offset, DL) != Fold.Array ||
            Offset < 0 || static_cast<uint64_t>(Offset) >= Stores.size() || Stores[Offset])
          return false;
        Stores[Offset] = Store;
      } else if (auto *Set = dyn_cast<MemSetInst>(U);
                 Set && !Fill && !Set->isVolatile() && !Set->hasOperandBundles() &&
                 Set->getRawDest() == Pointer) {
        Fill = Set;
      } else {
        return false;
      }
    }

  for (unsigned I = 0; I < Stores.size(); ++I) {
    if (!Stores[I]) return false;
    auto *Block = Stores[I]->getParent();
    int Incoming = Fold.Length->getBasicBlockIndex(Block);
    auto *Length = Incoming < 0 ? nullptr
                                : dyn_cast<ConstantInt>(Fold.Length->getIncomingValue(Incoming));
    if (!Length || !Length->equalsInt(I + 1) ||
        (I != 0 && Block->getSinglePredecessor() != Stores[I - 1]->getParent()))
      return false;
  }
  return !Fill || (Fill->getParent() == Stores[0]->getParent() && Fill->comesBefore(Stores[0]));
}

static bool foldReverseByteReduction(BasicBlock &Body, const DataLayout &DL) {
  ReverseByteFold Fold;
  if (!matchReverseByteFold(Body, Fold) || DL.getIndexSizeInBits(Fold.Array->getAddressSpace()) != 64)
    return false;
  unsigned Count = Fold.Length->getNumIncomingValues();
  if (Count < 4 || Count > 12) return false;
  SmallVector<StoreInst *, 12> Stores(Count);
  MemSetInst *Fill = nullptr;
  if (!findReverseByteStores(Fold, DL, Stores, Fill)) return false;

  // Horner evaluation from the end equals the sum of each byte times
  // its index power, modulo the accumulator width.
  auto *Sum = PHINode::Create(Fold.Result->getType(), Count, "byte.prefix.sum",
                              Fold.Entry->getFirstNonPHIIt());
  Value *Prefix = ConstantInt::get(Fold.Result->getType(), 0);
  APInt Power(64, 1);
  for (StoreInst *Store : Stores) {
    IRBuilder<> B(Store);
    Value *Byte = B.CreateZExt(Store->getValueOperand(), Fold.Result->getType());
    Prefix = B.CreateAdd(Prefix, B.CreateMul(Byte, ConstantInt::get(B.getContext(), Power)));
    Sum->addIncoming(Prefix, Store->getParent());
    Power *= Fold.Radix->getValue();
    Store->eraseFromParent();
  }
  if (Fill) Fill->eraseFromParent();
  Fold.Result->replaceAllUsesWith(Sum);
  for (Instruction &I : Body) I.dropAllReferences();
  while (!Body.empty()) Body.back().eraseFromParent();
  BranchInst::Create(Fold.Exit, &Body);
  return true;
}
} // namespace

static unsigned elisacoreFoldReverseByteReductions(Module &M) {
  if (!M.getTargetTriple().isAArch64() || M.size() > 512) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.size() > 32 || BDU_SkipFunction(F, M)) continue;
    for (BasicBlock &Body : F)
      Changed += foldReverseByteReduction(Body, M.getDataLayout());
  }
  return Changed;
}
