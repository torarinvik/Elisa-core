// Shrink the zero fill of a local array that is used only through one view
// of its first L elements (a scratch buffer handed to a helper as
// `scratch[0:length]`) to those L elements. Elements past L would need
// their zeros only if some access through the view reached them, so every
// access is proved to stay inside it: the bytes a load, store or memory
// intrinsic touches through the view's data pointer lie below its length,
// by ElisaIndexBounds' proofs in the function that makes the access. A
// view may be passed on whole to a defined function whose parameter passes
// the same check; a parameter being checked is assumed to pass, an
// induction over the depth of calls. Callees are checked on a copy that
// SROA and instcombine have cleaned (the view in SSA form, each access
// still under its bounds check, as before O3 vectorizes and hoists the
// checks); the copies are deleted afterwards. The vectorizer's overlap checks may
// compare its address; any other use of the pointer (a cast back to a
// pointer, a store of it, a call with the bare pointer) keeps the fill.
#include <llvm/IR/Argument.h>
#include "llvm_element_offsets.h"
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/Transforms/InstCombine/InstCombine.h>
#include <llvm/Transforms/Scalar/EarlyCSE.h>
#include <llvm/Transforms/Scalar/SROA.h>
#include <llvm/Transforms/Scalar/SimplifyCFG.h>
#include <llvm/Transforms/Utils/Cloning.h>
namespace ElisaViewBounds {
using namespace llvm;

class Checker {
public:
  Checker(FunctionAnalysisManager &Analyses, uint64_t ElementSize)
      : analyses(Analyses), elementSize(ElementSize) {}
  ~Checker() {
    for (auto &[Original, Copy] : copies) {
      analyses.clear(*Copy, Copy->getName());
      Copy->eraseFromParent();
    }
  }

  // Every access through View's data pointer stays below View's length.
  bool bounded(Value &View, Function &F) {
    Value *Length = lengthOf(View, F);
    return Length && bounded(View, *Length, F);
  }

  // A value, in Call's function, below which the callee accesses the view
  // passed at Position and that Accept takes: that view's length, else an
  // integer argument the callee's indices stay below (a count passed beside
  // a whole buffer, `sort(items[0:capacity], count)`). Null when none is.
  Value *callBound(CallBase &Call, unsigned Position, Function &Caller, function_ref<bool(Value *)> Accept) {
    Function *Callee = Call.getCalledFunction();
    if (!Callee || Callee->isDeclaration() || Callee->getFunctionType() != Call.getFunctionType()) return nullptr;
    Function &Copy = cleaned(*Callee);
    if (Value *Length = lengthOf(*Call.getArgOperand(Position), Caller);
        Length && Accept(Length) && argumentBounded(Copy, Position, Position))
      return Length;
    for (unsigned Count = 0; Count < Call.arg_size(); ++Count)
      if (Value *Bound = Call.getArgOperand(Count);
          Count != Position && Bound->getType()->isIntegerTy(64) && Accept(Bound) &&
          argumentBounded(Copy, Position, Count))
        return Bound;
    return nullptr;
  }

private:
  FunctionAnalysisManager &analyses;
  uint64_t elementSize;
  // Claims being checked: a view parameter bounded by a parameter (itself
  // for its own length).
  SmallDenseSet<std::pair<Argument *, Argument *>, 8> checking;
  DenseMap<Function *, Function *> copies;
  unsigned budget = 64;

  // Every access through View's data pointer stays below Length.
  bool bounded(Value &View, Value &Length, Function &F) {
    for (User *Use : View.users()) {
      if (auto *Extract = dyn_cast<ExtractValueInst>(Use)) {
        if (Extract->getNumIndices() != 1) return false;
        if (Extract->getIndices()[0] == 1) continue;
        if (!dataBounded(*Extract, Length, F)) return false;
      } else if (auto *Call = dyn_cast<CallBase>(Use)) {
        for (unsigned Position = 0; Position < Call->arg_size(); ++Position)
          if (Call->getArgOperand(Position) == &View &&
              !callBound(*Call, Position, F, [&](Value *Bound) { return atMost(*Bound, Length, *Call, F); }))
            return false;
      } else {
        return false;
      }
    }
    return true;
  }

  bool atMost(Value &Bound, Value &Length, Instruction &At, Function &F) {
    if (&Bound == &Length) return true;
    auto &Evolution = analyses.getResult<ScalarEvolutionAnalysis>(F);
    if (!Evolution.isSCEVable(Bound.getType()) || Bound.getType() != Length.getType()) return false;
    ElisaIndexBounds::Prover Proof(Evolution, analyses.getResult<DominatorTreeAnalysis>(F),
                                   analyses.getResult<LoopAnalysis>(F));
    return Proof.proveAtMost(Evolution.getSCEV(&Bound), Evolution.getSCEV(&Length), &At);
  }

  // A copy of F in SSA form, made once.
  Function &cleaned(Function &F) {
    if (Function *Copy = copies.lookup(&F)) return *Copy;
    ValueToValueMapTy Values;
    Function *Copy = CloneFunction(&F, Values);
    Copy->setLinkage(GlobalValue::PrivateLinkage);
    FunctionPassManager Cleanup;
    Cleanup.addPass(SROAPass(SROAOptions::ModifyCFG));
    Cleanup.addPass(EarlyCSEPass());
    Cleanup.addPass(InstCombinePass());
    Cleanup.addPass(SimplifyCFGPass());
    Cleanup.run(*Copy, analyses);
    // Every extraction of a parameter's field is the same value; one at the
    // entry lets a bounds check inside a loop and the length share it.
    for (Argument &Parameter : Copy->args()) {
      SmallDenseMap<unsigned, ExtractValueInst *, 2> Fields;
      for (User *Use : make_early_inc_range(Parameter.users()))
        if (auto *Field = dyn_cast<ExtractValueInst>(Use); Field && Field->getNumIndices() == 1) {
          auto [Kept, First] = Fields.try_emplace(Field->getIndices()[0], Field);
          if (First) {
            Field->moveBefore(Copy->getEntryBlock().getFirstInsertionPt());
          } else {
            Field->replaceAllUsesWith(Kept->second);
            Field->eraseFromParent();
          }
        }
    }
    analyses.invalidate(*Copy, PreservedAnalyses::none());
    copies[&F] = Copy;
    return *Copy;
  }

  // Callee's view parameter Index stays below parameter Limit (its own
  // length when Limit is Index); a claim being checked is assumed.
  bool argumentBounded(Function &Callee, unsigned Index, unsigned Limit) {
    Argument *Parameter = Callee.getArg(Index), *Bound = Callee.getArg(Limit);
    if (checking.contains({Parameter, Bound})) return true;
    if (budget == 0) return false;
    --budget;
    Value *Length = Limit == Index ? lengthOf(*Parameter, Callee) : Bound;
    if (!Length) return false;
    checking.insert({Parameter, Bound});
    bool Holds = bounded(*Parameter, *Length, Callee);
    checking.erase({Parameter, Bound});
    return Holds;
  }

  // The view's length: the inserted value of the view being built, or an
  // extraction of it (made at the function's entry for a parameter).
  static Value *lengthOf(Value &View, Function &F) {
    if (auto *Insert = dyn_cast<InsertValueInst>(&View))
      return Insert->getNumIndices() == 1 && Insert->getIndices()[0] == 1
                 ? plainLength(Insert->getInsertedValueOperand()) : nullptr;
    for (User *Use : View.users())
      if (auto *Extract = dyn_cast<ExtractValueInst>(Use);
          Extract && Extract->getNumIndices() == 1 && Extract->getIndices()[0] == 1)
        if (isa<Argument>(View) && Extract->getParent() == &F.getEntryBlock()) return Extract;
    if (!isa<Argument>(View)) return nullptr;
    return ExtractValueInst::Create(&View, {1}, "view.len", F.getEntryBlock().getFirstInsertionPt());
  }

  // A slice from zero (`items[0:count]`) builds its length as `hi - 0`,
  // with hi chosen by a test that always holds (`hi >= 0`): see through both.
  static Value *plainLength(Value *Length) {
    using namespace PatternMatch;
    for (unsigned Step = 0; Step < 4; ++Step) {
      Value *Whole = nullptr;
      auto *Choice = dyn_cast<SelectInst>(Length);
      auto *Test = Choice ? dyn_cast<ICmpInst>(Choice->getCondition()) : nullptr;
      if (match(Length, m_Sub(m_Value(Whole), m_Zero()))) Length = Whole;
      else if (Test && Test->getPredicate() == ICmpInst::ICMP_UGE && match(Test->getOperand(1), m_Zero()))
        Length = Choice->getTrueValue();
      else break;
    }
    return Length;
  }

  // Loads, stores and memory intrinsics through Data stay inside the
  // Length elements it points to.
  bool dataBounded(Value &Data, Value &Length, Function &F) {
    auto &Evolution = analyses.getResult<ScalarEvolutionAnalysis>(F);
    ElisaIndexBounds::Prover Proof(Evolution, analyses.getResult<DominatorTreeAnalysis>(F),
                                   analyses.getResult<LoopAnalysis>(F));
    if (!Evolution.isSCEVable(Length.getType())) return false;
    const SCEV *Elements = Evolution.getSCEV(&Length), *Base = Evolution.getSCEV(&Data);
    const DataLayout &Layout = F.getDataLayout();
    auto Inside = [&](Value *Pointer, const SCEV *Bytes, Instruction *At) {
      const SCEV *Offset = divide(Evolution, Evolution.getMinusSCEV(Evolution.getSCEV(Pointer), Base));
      const SCEV *Count = divide(Evolution, Bytes);
      if (!Offset || !Count || Offset->getType() != Elements->getType() || Count->getType() != Offset->getType())
        return false;
      return Proof.proveAtMost(Evolution.getAddExpr(Offset, Count), Elements, At) &&
             (Count->isZero() || Proof.proveBelow(Offset, Elements, At));
    };
    SmallVector<Value *, 8> Pointers{&Data};
    SmallPtrSet<Value *, 16> Seen;
    while (!Pointers.empty()) {
      Value *Pointer = Pointers.pop_back_val();
      if (!Seen.insert(Pointer).second) continue;
      for (User *Use : Pointer->users()) {
        auto Fixed = [&](Type *Accessed) {
          return Evolution.getConstant(Elements->getType(), Layout.getTypeStoreSize(Accessed).getFixedValue());
        };
        if (auto *Address = dyn_cast<GetElementPtrInst>(Use); Address && Address->getPointerOperand() == Pointer) {
          Pointers.push_back(Address);
        } else if (auto *Load = dyn_cast<LoadInst>(Use)) {
          if (Load->isVolatile() || !Inside(Pointer, Fixed(Load->getType()), Load)) return false;
        } else if (auto *Store = dyn_cast<StoreInst>(Use)) {
          if (Store->isVolatile() || Store->getValueOperand() == Pointer ||
              !Inside(Pointer, Fixed(Store->getValueOperand()->getType()), Store))
            return false;
        } else if (isa<PtrToAddrInst>(Use) || isa<PtrToIntInst>(Use)) {
          if (!addressOnly(*cast<Instruction>(Use))) return false;
        } else if (auto *Memory = dyn_cast<MemIntrinsic>(Use)) {
          if (Memory->isVolatile() || Memory->getLength()->getType() != Elements->getType() ||
              !Inside(Pointer, Evolution.getSCEV(Memory->getLength()), Memory))
            return false;
        } else {
          return false;
        }
      }
    }
    return true;
  }

  // An address compared or computed with (the vectorizer's overlap
  // checks), never turned back into a pointer, stored, or passed on.
  static bool addressOnly(Instruction &Address) {
    SmallVector<Instruction *, 8> Work{&Address};
    SmallPtrSet<Instruction *, 16> Seen;
    while (!Work.empty()) {
      Instruction *Value = Work.pop_back_val();
      if (!Seen.insert(Value).second || isa<ICmpInst>(Value)) continue;
      if (!isa<BinaryOperator>(Value) && !isa<SelectInst>(Value) && !isa<PHINode>(Value) &&
          !isa<FreezeInst>(Value) && !isa<TruncInst>(Value) && !isa<ZExtInst>(Value) &&
          !isa<SExtInst>(Value) && !isa<PtrToAddrInst>(Value) && !isa<PtrToIntInst>(Value) &&
          !(isa<IntrinsicInst>(Value) && cast<IntrinsicInst>(Value)->getIntrinsicID() == Intrinsic::umin) &&
          !(isa<IntrinsicInst>(Value) && cast<IntrinsicInst>(Value)->getIntrinsicID() == Intrinsic::umax))
        return false;
      for (User *Use : Value->users())
        if (auto *Next = dyn_cast<Instruction>(Use)) Work.push_back(Next);
        else return false;
    }
    return true;
  }

  const SCEV *divide(ScalarEvolution &Evolution, const SCEV *Bytes) {
    return isa<SCEVCouldNotCompute>(Bytes) ? nullptr : ElisaElementOffsets::divide(Evolution, Bytes, elementSize);
  }
};

// The fill, the view of the first Length elements built from Buffer, and
// nothing else, or null.
struct Candidate {
  MemSetInst *Fill = nullptr;
  InsertValueInst *View = nullptr;
};

static Candidate candidate(AllocaInst &Buffer, const DataLayout &Layout) {
  Candidate Found;
  InsertValueInst *Data = nullptr;
  uint64_t Bytes = Layout.getTypeAllocSize(Buffer.getAllocatedType()).getFixedValue();
  for (User *Use : Buffer.users()) {
    if (auto *Fill = dyn_cast<MemSetInst>(Use)) {
      auto *Byte = dyn_cast<ConstantInt>(Fill->getValue());
      auto *Length = dyn_cast<ConstantInt>(Fill->getLength());
      if (Found.Fill || Fill->isVolatile() || Fill->getRawDest() != &Buffer || !Byte || !Byte->isZero() ||
          !Length || Length->getZExtValue() != Bytes)
        return {};
      Found.Fill = Fill;
    } else if (isa<InsertValueInst>(Use) || isa<GetElementPtrInst>(Use)) {
      // The view's data: the buffer, or its first element's address.
      Value *Start = Use;
      if (auto *First = dyn_cast<GetElementPtrInst>(Use)) {
        if (!First->hasAllZeroIndices() || !First->hasOneUse()) return {};
        Start = *First->user_begin();
      }
      auto *Insert = dyn_cast<InsertValueInst>(Start);
      if (Data || !Insert || Insert->getNumIndices() != 1 || Insert->getIndices()[0] != 0 ||
          Insert->getInsertedValueOperand() != (Start == Use ? static_cast<Value *>(&Buffer) : Use) ||
          !Insert->hasOneUse())
        return {};
      Data = Insert;
    } else if (!isa<LifetimeIntrinsic>(Use)) {
      return {};
    }
  }
  auto *View = Data ? dyn_cast<InsertValueInst>(*Data->user_begin()) : nullptr;
  auto *Type = View ? dyn_cast<StructType>(View->getType()) : nullptr;
  if (!Found.Fill || !View || View->getAggregateOperand() != Data || View->getNumIndices() != 1 ||
      View->getIndices()[0] != 1 || !Type || Type->getNumElements() != 2 ||
      !Type->getElementType(1)->isIntegerTy(64))
    return {};
  Found.View = View;
  return Found;
}

static unsigned shrinkViewFills(Module &Module) {
  // Cleaning callee copies runs function passes that consult module
  // analyses, so every manager is registered and linked.
  PassBuilder Builder;
  LoopAnalysisManager LoopAnalyses;
  FunctionAnalysisManager Analyses;
  CGSCCAnalysisManager CallGraphAnalyses;
  ModuleAnalysisManager ModuleAnalyses;
  Builder.registerModuleAnalyses(ModuleAnalyses);
  Builder.registerCGSCCAnalyses(CallGraphAnalyses);
  Builder.registerFunctionAnalyses(Analyses);
  Builder.registerLoopAnalyses(LoopAnalyses);
  Builder.crossRegisterProxies(LoopAnalyses, Analyses, CallGraphAnalyses, ModuleAnalyses);
  unsigned Shrunk = 0;
  for (Function &F : Module) {
    if (F.isDeclaration() || F.hasOptNone()) continue;
    const DataLayout &Layout = F.getDataLayout();
    SmallVector<std::pair<AllocaInst *, Candidate>, 2> Found;
    for (Instruction &Instruction : F.getEntryBlock())
      if (auto *Buffer = dyn_cast<AllocaInst>(&Instruction);
          Buffer && Buffer->isStaticAlloca() && isa<ArrayType>(Buffer->getAllocatedType()) &&
          Buffer->getAllocatedType()->getArrayElementType()->isIntegerTy())
        if (Candidate Shape = candidate(*Buffer, Layout); Shape.View) Found.push_back({Buffer, Shape});
    for (auto [Buffer, Shape] : Found) {
      auto *Type = cast<ArrayType>(Buffer->getAllocatedType());
      uint64_t Size = Layout.getTypeAllocSize(Type->getElementType()).getFixedValue();
      Value *Length = Shape.View->getInsertedValueOperand();
      auto &Evolution = Analyses.getResult<ScalarEvolutionAnalysis>(F);
      auto &Tree = Analyses.getResult<DominatorTreeAnalysis>(F);
      auto &Loops = Analyses.getResult<LoopAnalysis>(F);
      ElisaIndexBounds::Prover Proof(Evolution, Tree, Loops);
      if (Loops.getLoopFor(Shape.Fill->getParent()) || !Tree.dominates(Shape.Fill, Shape.View) ||
          !Proof.proveAtMost(Evolution.getSCEV(Length),
                             Evolution.getConstant(Length->getType(), Type->getNumElements()), Shape.View) ||
          !Checker(Analyses, Size).bounded(*Shape.View, F))
        continue;
      // Zero the first Length elements where the view is built.
      IRBuilder<> Here(Shape.View);
      Value *Bytes = Here.CreateMul(Length, ConstantInt::get(Length->getType(), Size), "view.fill.bytes",
                                    /*HasNUW=*/true, /*HasNSW=*/true);
      Here.CreateMemSet(Buffer, Here.getInt8(0), Bytes, Shape.Fill->getDestAlign());
      Shape.Fill->eraseFromParent();
      Analyses.invalidate(F, PreservedAnalyses::none());
      ++Shrunk;
    }
  }
  return Shrunk;
}
} // namespace ElisaViewBounds
