// Bound the values a local integer array can hold. When a non-escaping
// array is accessed only through element-aligned loads and stores, starts
// from a zero fill, and every store writes either a value of known range or
// a value loaded from the array itself, then every load returns a value in
// the union of those ranges: a fill or an outside store puts one there, and a
// copied element was already there. The loads get `!range` metadata, which
// lets later folding remove bounds checks on indices read from the array
// (permutation and order arrays indexing a second array are the common case).
#include <llvm/Analysis/LazyValueInfo.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/IR/GetElementPtrTypeIterator.h>
#include <llvm/IR/MDBuilder.h>
namespace ElisaArrayRanges {
using namespace llvm;

struct Accesses {
  SmallVector<LoadInst *, 16> Loads;
  SmallVector<StoreInst *, 16> Stores;
  SmallVector<MemTransferInst *, 2> Copies;
  SmallVector<Instruction *, 1> Fills;
  bool Filled = false;
};

// Every index of Address steps by whole elements of ElementSize bytes.
static bool elementAligned(GetElementPtrInst &Address, uint64_t ElementSize,
                           const DataLayout &Layout) {
  for (gep_type_iterator Step = gep_type_begin(Address), End = gep_type_end(Address);
       Step != End; ++Step) {
    if (Step.isStruct()) return false;
    uint64_t Stride = Step.getSequentialElementStride(Layout).getFixedValue();
    auto *Constant = dyn_cast<ConstantInt>(Step.getOperand());
    if (Stride % ElementSize != 0 &&
        !(Constant && (Constant->getSExtValue() * int64_t(Stride)) % int64_t(ElementSize) == 0))
      return false;
  }
  return true;
}

// Collect the accesses of Pointer (Array or an element-aligned address in
// it); false when some use could read or write it in another way.
static bool collect(Value *Pointer, Type *Element, uint64_t ElementSize,
                    const DataLayout &Layout, Accesses &Out, unsigned Depth = 0) {
  if (Depth > 4) return false;
  for (User *Use : Pointer->users()) {
    if (auto *Load = dyn_cast<LoadInst>(Use)) {
      if (!Load->isSimple() || Load->getType() != Element) return false;
      Out.Loads.push_back(Load);
    } else if (auto *Store = dyn_cast<StoreInst>(Use)) {
      if (!Store->isSimple() || Store->getPointerOperand() != Pointer) return false;
      Type *Stored = Store->getValueOperand()->getType();
      if (Stored == Element) {
        Out.Stores.push_back(Store);
      } else if (isa<ConstantAggregateZero>(Store->getValueOperand()) && Depth == 0) {
        Out.Filled = true;
        Out.Fills.push_back(Store);
      } else {
        return false;
      }
    } else if (auto *Address = dyn_cast<GetElementPtrInst>(Use)) {
      if (!elementAligned(*Address, ElementSize, Layout) ||
          !collect(Address, Element, ElementSize, Layout, Out, Depth + 1))
        return false;
    } else if (auto *Fill = dyn_cast<MemSetInst>(Use)) {
      auto *Byte = dyn_cast<ConstantInt>(Fill->getValue());
      if (Fill->getRawDest() != Pointer || Fill->isVolatile() || !Byte || !Byte->isZero())
        return false;
      Out.Filled = true;
      Out.Fills.push_back(Fill);
    } else if (auto *Copy = dyn_cast<MemTransferInst>(Use)) {
      // Copies out of the array read it; they cannot change it.
      if (Copy->getRawDest() == Pointer || Copy->isVolatile()) return false;
      Out.Copies.push_back(Copy);
    } else if (!isa<LifetimeIntrinsic>(Use)) {
      return false;
    }
  }
  return true;
}

class Bounds {
public:
  Bounds(Accesses &Uses, ScalarEvolution &Evolution, LazyValueInfo &Values)
      : uses(Uses), evolution(Evolution), values(Values) {
    for (LoadInst *Load : Uses.Loads) own.insert(Load);
  }

  // The union of the ranges of values the array can hold, or the full set.
  ConstantRange hull() {
    unsigned Width = uses.Loads.front()->getType()->getIntegerBitWidth();
    ConstantRange Result(APInt(Width, 0));
    for (StoreInst *Store : uses.Stores) {
      Result = Result.unionWith(rangeOf(Store->getValueOperand(), Store),
                                ConstantRange::Unsigned);
      if (Result.isFullSet()) break;
    }
    return Result;
  }

private:
  Accesses &uses;
  ScalarEvolution &evolution;
  LazyValueInfo &values;
  SmallPtrSet<Value *, 16> own, visiting;

  // Values flowing from the array's own loads add nothing new.
  ConstantRange rangeOf(Value *Stored, Instruction *At, unsigned Depth = 0) {
    unsigned Width = Stored->getType()->getIntegerBitWidth();
    if (own.count(Stored) || visiting.count(Stored)) return ConstantRange::getEmpty(Width);
    if (Depth > 6) return ConstantRange::getFull(Width);
    SmallVector<Value *, 4> Choices;
    if (auto *Merge = dyn_cast<PHINode>(Stored)) Choices.append(Merge->incoming_values().begin(),
                                                                Merge->incoming_values().end());
    if (auto *Choice = dyn_cast<SelectInst>(Stored))
      Choices.append({Choice->getTrueValue(), Choice->getFalseValue()});
    if (!Choices.empty() && llvm::any_of(Choices, [&](Value *V) { return own.count(V); })) {
      visiting.insert(Stored);
      ConstantRange Result = ConstantRange::getEmpty(Width);
      for (Value *Choice : Choices)
        Result = Result.unionWith(rangeOf(Choice, At, Depth + 1), ConstantRange::Unsigned);
      visiting.erase(Stored);
      return Result;
    }
    ConstantRange Known = values.getConstantRange(Stored, At, false);
    if (evolution.isSCEVable(Stored->getType()))
      Known = Known.intersectWith(evolution.getUnsignedRange(evolution.getSCEV(Stored)),
                                  ConstantRange::Unsigned);
    return Known;
  }
};

static bool annotate(Accesses &Uses, const ConstantRange &Range) {
  if (Range.isFullSet() || Range.isEmptySet() || Range.isWrappedSet()) return false;
  bool Changed = false;
  for (LoadInst *Load : Uses.Loads) {
    if (Load->getMetadata(LLVMContext::MD_range)) continue;
    MDBuilder Builder(Load->getContext());
    Load->setMetadata(LLVMContext::MD_range,
                      Builder.createRange(Range.getLower(), Range.getUpper()));
    Changed = true;
  }
  return Changed;
}

static unsigned annotateArrayValueRanges(Module &Module) {
  PassBuilder Builder;
  FunctionAnalysisManager Analyses;
  Builder.registerFunctionAnalyses(Analyses);
  unsigned Changed = 0;
  for (Function &F : Module) {
    if (F.isDeclaration()) continue;
    const DataLayout &Layout = Module.getDataLayout();
    ScalarEvolution *Evolution = nullptr;
    LazyValueInfo *Values = nullptr;
    for (Instruction &Instruction : F.getEntryBlock()) {
      auto *Array = dyn_cast<AllocaInst>(&Instruction);
      auto *Type = Array ? dyn_cast<ArrayType>(Array->getAllocatedType()) : nullptr;
      if (!Type || !Type->getElementType()->isIntegerTy() || !Array->isStaticAlloca())
        continue;
      Accesses Uses;
      if (!collect(Array, Type->getElementType(),
                   Layout.getTypeAllocSize(Type->getElementType()).getFixedValue(), Layout,
                   Uses) ||
          !Uses.Filled || Uses.Loads.empty())
        continue;
      if (!Evolution) {
        Evolution = &Analyses.getResult<ScalarEvolutionAnalysis>(F);
        Values = &Analyses.getResult<LazyValueAnalysis>(F);
      }
      Changed += annotate(Uses, Bounds(Uses, *Evolution, *Values).hull());
    }
  }
  return Changed;
}
} // namespace ElisaArrayRanges
