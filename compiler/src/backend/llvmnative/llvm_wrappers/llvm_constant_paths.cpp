// Evaluate bounded, deterministic integer paths reached from the entry branch.
// This runs after LLVM has inlined callees and exposed the scalar recurrence.
#include <llvm/ADT/DenseMap.h>
#include <llvm/Analysis/ConstantFolding.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/Transforms/Utils/Local.h>
using namespace llvm;

namespace {
using PathValues = DenseMap<Value *, Constant *>;

static Constant *pathValue(Value *V, const PathValues &Values) {
  if (auto *C = dyn_cast<ConstantInt>(V)) return C;
  return Values.lookup(V);
}

static Constant *evaluatePathInstruction(Instruction &I, const PathValues &Values,
                                         const DataLayout &DL) {
  SmallVector<Constant *, 4> Operands;
  if (auto *Call = dyn_cast<IntrinsicInst>(&I)) {
    if (Call->hasOperandBundles() || Call->isConvergent()) return nullptr;
    switch (Call->getIntrinsicID()) {
    case Intrinsic::sadd_with_overflow: case Intrinsic::ssub_with_overflow:
    case Intrinsic::smul_with_overflow: case Intrinsic::uadd_with_overflow:
    case Intrinsic::usub_with_overflow: case Intrinsic::umul_with_overflow:
    case Intrinsic::uadd_sat: case Intrinsic::usub_sat:
    case Intrinsic::sadd_sat: case Intrinsic::ssub_sat:
      break;
    default:
      return nullptr;
    }
    for (Value *V : Call->args()) {
      auto *C = dyn_cast_or_null<ConstantInt>(pathValue(V, Values));
      if (!C) return nullptr;
      Operands.push_back(C);
    }
    return ConstantFoldIntrinsic(Call->getIntrinsicID(), Operands, Call->getType());
  }

  if (!I.getType()->isIntegerTy() ||
      (!isa<BinaryOperator, ICmpInst, SelectInst, TruncInst, ZExtInst, SExtInst,
            ExtractValueInst, FreezeInst>(I)))
    return nullptr;
  for (Value *V : I.operands()) {
    Constant *C = pathValue(V, Values);
    if (!C) return nullptr;
    Operands.push_back(C);
  }
  if (isa<FreezeInst>(I)) return Operands.front();
  return dyn_cast_or_null<ConstantInt>(
      ConstantFoldInstOperands(&I, Operands, DL, nullptr, false));
}

static ConstantInt *evaluateIntegerPath(BasicBlock *Block, BasicBlock *Previous,
                                        const DataLayout &DL, unsigned &Budget,
                                        PathValues Values = PathValues()) {
  SmallVector<std::pair<PHINode *, Constant *>, 8> Incoming;
  unsigned Steps = 0;
  while (Budget && Steps < 18000) {
    Incoming.clear();
    for (PHINode &Phi : Block->phis()) {
      Constant *C = pathValue(Phi.getIncomingValueForBlock(Previous), Values);
      if (!C) return nullptr;
      Incoming.push_back({&Phi, C});
    }
    // PHIs take their inputs from the previous iteration simultaneously.
    for (auto [Phi, C] : Incoming) Values[Phi] = C;

    for (Instruction &I : *Block) {
      if (!Budget || ++Steps > 18000) return nullptr;
      --Budget;
      if (isa<PHINode>(&I)) continue;
      if (auto *Ret = dyn_cast<ReturnInst>(&I))
        return dyn_cast_or_null<ConstantInt>(pathValue(Ret->getReturnValue(), Values));
      if (I.isTerminator()) break;
      Constant *C = evaluatePathInstruction(I, Values, DL);
      if (!C) return nullptr;
      Values[&I] = C;
    }

    auto *Terminator = Block->getTerminator();
    Previous = Block;
    if (auto *Br = dyn_cast<CondBrInst>(Terminator)) {
      auto *Test = dyn_cast_or_null<ConstantInt>(pathValue(Br->getCondition(), Values));
      if (!Test) return nullptr;
      Block = Br->getSuccessor(Test->isZero() ? 1 : 0);
    } else if (auto *Br = dyn_cast<UncondBrInst>(Terminator)) {
      Block = Br->getSuccessor();
    } else {
      return nullptr;
    }
  }
  return nullptr;
}
// A small unsigned domain makes every execution on the accepted edge
// concrete. Keep the original guard and invalid-input path.
static bool foldSmallIntegerDomain(CondBrInst &Entry, ICmpInst &Test,
                                   ConstantInt &Bound, unsigned &Budget) {
  Function &F = *Entry.getFunction();
  constexpr unsigned MaxDomain = 128;
  constexpr unsigned MaxTableBytes = 768;
  Value *Key = Test.getOperand(0);
  auto *ReturnType = dyn_cast<IntegerType>(F.getReturnType());
  if (F.arg_size() != 1 || !Key->getType()->isIntegerTy(64) ||
      !ReturnType || ReturnType->getBitWidth() > 64 || !Test.isUnsigned())
    return false;

  // LLVM often expresses a nonzero-based interval by adding a constant
  // before the comparison. Invert that addition in the original bit width.
  Value *Input = Key;
  APInt Bias(64, 0);
  if (auto *Add = dyn_cast<BinaryOperator>(Key)) {
    auto *Offset = dyn_cast<ConstantInt>(Add->getOperand(1));
    if (Add->getOpcode() != Instruction::Add || !Offset) return false;
    Input = Add->getOperand(0);
    Bias = Offset->getValue();
  }
  Value *ArgumentValue = Input;
  if (auto *Freeze = dyn_cast<FreezeInst>(Input)) ArgumentValue = Freeze->getOperand(0);
  if (!isa<Argument>(ArgumentValue)) return false;

  unsigned Limit = 0, Accepted = 0;
  APInt Lower(64, 0);
  for (unsigned Edge : {0u, 1u}) {
    auto Predicate = Edge == 0 ? Test.getPredicate() : Test.getInversePredicate();
    auto Range = ConstantRange::makeExactICmpRegion(Predicate, Bound.getValue());
    if (Range.isEmptySet() || Range.isFullSet()) continue;
    APInt Size = Range.getUpper() - Range.getLower();
    if (Size.ult(8) || Size.ugt(MaxDomain)) continue;
    Lower = Range.getLower();
    Limit = Size.getZExtValue();
    Accepted = Edge;
    break;
  }
  if (!Limit) return false;
  BasicBlock *Start = Entry.getSuccessor(Accepted);
  SmallVector<ConstantInt *, 64> Results;
  bool WorthTable = false, Signed = false;
  for (unsigned I = 0; I < Limit; ++I) {
    unsigned Before = Budget;
    APInt KeyValue = Lower + I;
    PathValues Known;
    Known[Input] = ConstantInt::get(F.getContext(), KeyValue - Bias);
    Known[Key] = ConstantInt::get(F.getContext(), KeyValue);
    auto *Result = evaluateIntegerPath(Start, Entry.getParent(),
                                       F.getParent()->getDataLayout(), Budget, std::move(Known));
    if (!Result) return false;
    Results.push_back(Result);
    Signed |= Result->isNegative();
    WorthTable |= Before - Budget >= 32;
  }
  if (!WorthTable) return false;
  unsigned Bits = 1;
  for (ConstantInt *Result : Results)
    Bits = std::max(Bits, Signed ? Result->getValue().getSignificantBits()
                                : Result->getValue().getActiveBits());
  unsigned Width = 8;
  while (Width < Bits) Width *= 2;
  if (Limit * (Width / 8) > MaxTableBytes) return false;

  Module &M = *F.getParent();
  auto *Storage = IntegerType::get(M.getContext(), Width);
  SmallVector<Constant *, 64> Packed;
  for (ConstantInt *Result : Results) {
    const APInt &Value = Result->getValue();
    Packed.push_back(ConstantInt::get(Storage, Signed ? Value.sextOrTrunc(Width)
                                                      : Value.zextOrTrunc(Width)));
  }
  auto *TableType = ArrayType::get(Storage, Packed.size());
  auto *Table = new GlobalVariable(M, TableType, true, GlobalValue::PrivateLinkage,
                                   ConstantArray::get(TableType, Packed), "integer.path.results");
  Table->setUnnamedAddr(GlobalValue::UnnamedAddr::Global);
  IRBuilder<> Guard(&Test);
  Value *Stable = Guard.CreateFreeze(Key);
  Test.setOperand(0, Stable);
  BasicBlock *Lookup = BasicBlock::Create(M.getContext(), "integer.path.lookup", &F);
  IRBuilder<> B(Lookup);
  Value *Index = B.CreateSub(Stable, ConstantInt::get(M.getContext(), Lower));
  Value *Address = B.CreateInBoundsGEP(TableType, Table, {B.getInt64(0), Index});
  B.CreateRet(B.CreateIntCast(B.CreateLoad(Storage, Address), ReturnType, Signed));
  Entry.setSuccessor(Accepted, Lookup);
  Start->removePredecessor(Entry.getParent());
  removeUnreachableBlocks(F);
  return true;
}
} // namespace

static unsigned elisacoreFoldConstantIntegerPaths(Module &M) {
  if (!M.getTargetTriple().isAArch64()) return 0;
  unsigned Changed = 0, Budget = 32000;
  for (Function &F : M) {
    if (!Budget) break;
    if (F.size() < 3 || F.size() > 128 || !F.getReturnType()->isIntegerTy() ||
        BDU_SkipFunction(F, M))
      continue;
    auto *Entry = dyn_cast<CondBrInst>(F.getEntryBlock().getTerminator());
    if (!Entry || Entry->getSuccessor(0) == Entry->getSuccessor(1)) continue;
    auto *Test = dyn_cast<ICmpInst>(Entry->getCondition());
    auto *Bound = Test ? dyn_cast<ConstantInt>(Test->getOperand(1)) : nullptr;
    if (!Bound) continue;
    if (!Test->isEquality()) {
      Changed += foldSmallIntegerDomain(*Entry, *Test, *Bound, Budget);
      continue;
    }
    if (!isa<Argument>(Test->getOperand(0))) continue;

    for (unsigned Edge : {0u, 1u}) {
      BasicBlock *Start = Entry->getSuccessor(Edge);
      if (Start->phis().empty()) continue;
      auto *Result = evaluateIntegerPath(Start, Entry->getParent(), M.getDataLayout(), Budget);
      if (!Result) continue;
      auto *Done = BasicBlock::Create(M.getContext(), "constant.path", &F);
      ReturnInst::Create(M.getContext(), Result, Done);
      Entry->setSuccessor(Edge, Done);
      Start->removePredecessor(Entry->getParent());
      removeUnreachableBlocks(F);
      ++Changed;
      break;
    }
  }
  return Changed;
}
