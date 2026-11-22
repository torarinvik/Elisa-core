// Elide zero fills of index-linked scratch arrays. Each array is completely
// written over [0, n) by a counted loop before any read. A value is a link
// when, as an unsigned index, it is below n or at least the array length, so
// a bounds-checked access through it either reads the written prefix or
// traps. Links are constants, n - c, values guarded below n, phis and casts
// of links, and loads from arrays whose every stored value is a link.
// Arrays whose reads all use links lose their initializer.
#include <llvm/IR/PatternMatch.h>
namespace ElisaLinkedPrefix {
using namespace llvm::PatternMatch;
using ElisaCoveredArray::Induction;
using ElisaCoveredArray::induction;
using ElisaCoveredArray::normalExit;

struct Access {
  Instruction *instruction;
  Value *index;
};

struct Candidate {
  AllocaInst *buffer;
  MemSetInst *init;
  Induction fill;
  BasicBlock *exit;
  SmallVector<Access, 8> reads;
  SmallVector<StoreInst *, 8> stores;
};

class Prover {
public:
  Prover(Value *Bound, uint64_t Length, DominatorTree &Tree,
         SmallVectorImpl<Candidate> &Arrays)
      : bound(Bound), length(Length), tree(Tree), arrays(Arrays) {}

  // Greatest fixpoint: surviving arrays read only link indices, and clean
  // arrays store only links, assuming both properties of the current sets.
  void solve(SmallPtrSetImpl<AllocaInst *> &Survivors) {
    for (Candidate &Array : arrays) {
      Survivors.insert(Array.buffer);
      clean.insert(Array.buffer);
    }
    for (bool Changed = true; Changed;) {
      Changed = false;
      for (Candidate &Array : arrays) {
        if (clean.count(Array.buffer) &&
            !llvm::all_of(Array.stores, [&](StoreInst *Write) {
              return isLink(Write->getValueOperand(), Write->getParent(), Survivors);
            })) {
          clean.erase(Array.buffer);
          Changed = true;
        }
        if (Survivors.count(Array.buffer) &&
            !llvm::all_of(Array.reads, [&](Access &Read) {
              return isLink(Read.index, Read.instruction->getParent(), Survivors);
            })) {
          Survivors.erase(Array.buffer);
          Changed = true;
        }
      }
    }
  }

private:
  Value *bound;
  uint64_t length;
  DominatorTree &tree;
  SmallVectorImpl<Candidate> &arrays;
  SmallPtrSet<AllocaInst *, 8> clean;
  DenseMap<LoadInst *, bool> reloads;
  unsigned budget = 1u << 16;

  static bool same(Value *Left, Value *Right, unsigned Depth = 0) {
    if (Left == Right) return true;
    auto *A = dyn_cast<BinaryOperator>(Left);
    auto *B = dyn_cast<BinaryOperator>(Right);
    return Depth < 3 && A && B && A->getOpcode() == B->getOpcode() &&
           same(A->getOperand(0), B->getOperand(0), Depth + 1) &&
           same(A->getOperand(1), B->getOperand(1), Depth + 1);
  }

  // Visit the conditions of branch edges that dominate Context.
  // Comparisons known to have a given truth when Condition has value Truth,
  // looking through and/or, selects and short-circuit phis of i1 values.
  // Via collects the single possible predecessor of each short-circuit phi:
  // facts holding at the end of that block also hold on the edge.
  static void factsOf(Value *Condition, bool Truth,
                      SmallVectorImpl<std::pair<ICmpInst *, bool>> &Facts,
                      SmallVectorImpl<BasicBlock *> &Via, unsigned Depth = 0) {
    if (Depth == 8) return;
    if (auto *Compare = dyn_cast<ICmpInst>(Condition)) {
      Facts.push_back({Compare, Truth});
      return;
    }
    Value *Left = nullptr, *Right = nullptr;
    if ((Truth && match(Condition, m_LogicalAnd(m_Value(Left), m_Value(Right)))) ||
        (!Truth && match(Condition, m_LogicalOr(m_Value(Left), m_Value(Right))))) {
      factsOf(Left, Truth, Facts, Via, Depth + 1);
      factsOf(Right, Truth, Facts, Via, Depth + 1);
      return;
    }
    auto *Phi = dyn_cast<PHINode>(Condition);
    if (!Phi || !Phi->getType()->isIntegerTy(1)) return;
    Value *Possible = nullptr;
    BasicBlock *From = nullptr;
    for (unsigned Index = 0; Index < Phi->getNumIncomingValues(); ++Index) {
      Value *Incoming = Phi->getIncomingValue(Index);
      if (auto *Constant = dyn_cast<ConstantInt>(Incoming)) {
        if (Constant->isOne() == Truth) return; // a path that proves nothing
        continue;                              // a path that cannot take this edge
      }
      if (Possible) return;
      Possible = Incoming;
      From = Phi->getIncomingBlock(Index);
    }
    if (!Possible) return;
    Via.push_back(From);
    factsOf(Possible, Truth, Facts, Via, Depth + 1);
  }

  // Visit the comparisons decided by branch edges that dominate Context.
  template <typename Visit>
  bool anyFact(BasicBlock *Context, Visit Fact, unsigned Depth = 0) {
    if (Depth == 4) return false;
    for (DomTreeNode *Node = tree.getNode(Context); Node; Node = Node->getIDom()) {
      BasicBlock *Dominator = Node->getBlock();
      for (BasicBlock *Predecessor : predecessors(Dominator)) {
        auto *Branch = dyn_cast<CondBrInst>(Predecessor->getTerminator());
        if (!Branch || Branch->getSuccessor(0) == Branch->getSuccessor(1)) continue;
        for (unsigned Edge = 0; Edge < 2; ++Edge) {
          if (Branch->getSuccessor(Edge) != Dominator ||
              !tree.dominates(BasicBlockEdge(Predecessor, Dominator), Context))
            continue;
          SmallVector<std::pair<ICmpInst *, bool>, 4> Facts;
          SmallVector<BasicBlock *, 2> Via;
          factsOf(Branch->getCondition(), Edge == 0, Facts, Via);
          for (BasicBlock *Before : Via)
            if (anyFact(Before, Fact, Depth + 1)) return true;
          for (auto [Compare, Truth] : Facts) {
            auto Predicate = Truth ? Compare->getPredicate() : Compare->getInversePredicate();
            Value *Left = Compare->getOperand(0), *Right = Compare->getOperand(1);
            if (ICmpInst::isGT(Predicate) || ICmpInst::isGE(Predicate)) {
              std::swap(Left, Right);
              Predicate = ICmpInst::getSwappedPredicate(Predicate);
            }
            if (Fact(Predicate, Left, Right)) return true;
          }
        }
      }
    }
    return false;
  }

  static bool sameAddress(Value *Left, Value *Right) {
    if (Left == Right) return true;
    auto *A = dyn_cast<GetElementPtrInst>(Left);
    auto *B = dyn_cast<GetElementPtrInst>(Right);
    return A && B && A->getSourceElementType() == B->getSourceElementType() &&
           A->getPointerOperand() == B->getPointerOperand() &&
           A->getNumIndices() == B->getNumIndices() &&
           std::equal(A->idx_begin(), A->idx_end(), B->idx_begin());
  }

  // A reload of the bound's address with no possible write between them.
  bool isBound(Value *Candidate) {
    if (Candidate == bound) return true;
    auto *Reload = dyn_cast<LoadInst>(Candidate);
    auto *Original = dyn_cast<LoadInst>(bound);
    if (!Reload || !Original || !Reload->isSimple() || !Original->isSimple() ||
        Reload->getType() != Original->getType() ||
        !sameAddress(Reload->getPointerOperand(), Original->getPointerOperand()))
      return false;
    auto Cached = reloads.find(Reload);
    if (Cached != reloads.end()) return Cached->second;
    LoadInst *First = Reload, *Last = Original;
    if (tree.dominates(Original, Reload)) std::swap(First, Last);
    bool Same = tree.dominates(First, Last) && !writeBetween(*First, *Last);
    reloads[Reload] = Same;
    return Same;
  }

  // Whether a memory write may execute after First and before Last, using
  // exact block reachability (no exploration limit).
  static bool writeBetween(Instruction &First, Instruction &Last) {
    auto Reach = [](BasicBlock *Start, bool Forward) {
      SmallPtrSet<BasicBlock *, 32> Seen;
      SmallVector<BasicBlock *, 32> Work;
      auto Push = [&](BasicBlock *Block) {
        if (Seen.insert(Block).second) Work.push_back(Block);
      };
      if (Forward) for (BasicBlock *Next : successors(Start)) Push(Next);
      else for (BasicBlock *Previous : predecessors(Start)) Push(Previous);
      while (!Work.empty()) {
        BasicBlock *Block = Work.pop_back_val();
        if (Forward) for (BasicBlock *Next : successors(Block)) Push(Next);
        else for (BasicBlock *Previous : predecessors(Block)) Push(Previous);
      }
      return Seen;
    };
    auto Writes = [](Instruction &Instruction) {
      return Instruction.mayWriteToMemory() && !isa<LifetimeIntrinsic>(Instruction);
    };
    auto After = Reach(First.getParent(), true);
    auto Before = Reach(Last.getParent(), false);
    for (BasicBlock *Block : After)
      if (Before.count(Block) && llvm::any_of(*Block, Writes)) return true;
    // Straight-line parts of the endpoint blocks.
    bool SameBlock = First.getParent() == Last.getParent();
    for (Instruction *Step = First.getNextNode(); Step && Step != &Last; Step = Step->getNextNode())
      if (Writes(*Step)) return true;
    if (!SameBlock)
      for (Instruction &Step : *Last.getParent()) {
        if (&Step == &Last) break;
        if (Writes(Step)) return true;
      }
    return false;
  }

  // n > Value holds at Context.
  // n > Value holds at Context.
  bool boundExceeds(uint64_t Value, BasicBlock *Context) {
    return anyFact(Context, [&](ICmpInst::Predicate Predicate, llvm::Value *Left,
                                llvm::Value *Right) {
      auto *Constant = dyn_cast<ConstantInt>(Left);
      if (!isBound(Right)) {
        auto *Zero = dyn_cast<ConstantInt>(Right);
        return Value == 0 && isBound(Left) && Zero && Zero->isZero() &&
               Predicate == ICmpInst::ICMP_NE;
      }
      bool Nonnegative = Constant && !Constant->isNegative();
      if (Predicate == ICmpInst::ICMP_ULT)
        return Value == 0 || (Constant && Constant->getZExtValue() >= Value);
      if (Predicate == ICmpInst::ICMP_SLT)
        return Nonnegative && Constant->getZExtValue() >= Value;
      return (Predicate == ICmpInst::ICMP_ULE ||
              (Predicate == ICmpInst::ICMP_SLE && Nonnegative)) &&
             Constant && Constant->getZExtValue() > Value;
    });
  }

  bool guardedBelowBound(Value *Candidate, BasicBlock *Context) {
    return anyFact(Context, [&](ICmpInst::Predicate Predicate, Value *Left,
                                Value *Right) {
      return Predicate == ICmpInst::ICMP_ULT && isBound(Right) && same(Left, Candidate);
    });
  }

  bool isLink(Value *Candidate, BasicBlock *Context,
              SmallPtrSetImpl<AllocaInst *> &Survivors) {
    SmallPtrSet<Value *, 16> Visiting;
    return isLink(Candidate, Context, Survivors, Visiting);
  }

  bool isLink(Value *Candidate, BasicBlock *Context,
              SmallPtrSetImpl<AllocaInst *> &Survivors,
              SmallPtrSetImpl<Value *> &Visiting) {
    if (budget == 0) return false;
    --budget;
    if (auto *Constant = dyn_cast<ConstantInt>(Candidate))
      return Constant->getValue().uge(length) ||
             boundExceeds(Constant->getZExtValue(), Context);
    if (auto *Frozen = dyn_cast<FreezeInst>(Candidate))
      return isLink(Frozen->getOperand(0), Context, Survivors, Visiting);
    // Truncating a value below n keeps it below n (x mod 2^k <= x); a
    // narrow result sign extended past 2^(k-1) becomes an out-of-range index.
    if (auto *Narrow = dyn_cast<TruncInst>(Candidate))
      return guardedBelowBound(Narrow->getOperand(0), Context);
    // A narrow link stays a link when sign or zero extended.
    if (isa<SExtInst, ZExtInst>(Candidate))
      return isLink(cast<Instruction>(Candidate)->getOperand(0), Context, Survivors,
                    Visiting);
    if (auto *Difference = dyn_cast<BinaryOperator>(Candidate)) {
      auto *Step = dyn_cast<ConstantInt>(Difference->getOperand(1));
      // n - c is below n when n >= c and wraps past the length otherwise.
      if (Difference->getOpcode() == Instruction::Sub && isBound(Difference->getOperand(0)) &&
          Step && !Step->isZero() && Step->getValue().ule(length))
        return true;
    }
    if (guardedBelowBound(Candidate, Context)) return true;
    if (auto *Phi = dyn_cast<PHINode>(Candidate)) {
      if (!Visiting.insert(Phi).second) return true;
      for (unsigned Index = 0; Index < Phi->getNumIncomingValues(); ++Index)
        if (!isLink(Phi->getIncomingValue(Index), Phi->getIncomingBlock(Index),
                    Survivors, Visiting))
          return false;
      return true;
    }
    auto *Read = dyn_cast<LoadInst>(Candidate);
    auto *Address = Read ? dyn_cast<GetElementPtrInst>(Read->getPointerOperand()) : nullptr;
    auto *Source = Address ? dyn_cast<AllocaInst>(Address->getPointerOperand()) : nullptr;
    return Source && Survivors.count(Source) && clean.count(Source);
  }
};
} // namespace ElisaLinkedPrefix

namespace ElisaLinkedPrefix {
static Type *linkedElement(MemSetInst &Init, const DataLayout &Layout) {
  auto *Buffer = dyn_cast<AllocaInst>(Init.getRawDest());
  auto *Array = Buffer ? dyn_cast<ArrayType>(Buffer->getAllocatedType()) : nullptr;
  auto *Length = dyn_cast<ConstantInt>(Init.getLength());
  auto *Fill = dyn_cast<ConstantInt>(Init.getValue());
  if (!Array || !Buffer->isStaticAlloca() || Buffer->isArrayAllocation() ||
      Init.isVolatile() || !Length || !Fill || !Fill->isZero() ||
      (!Array->getElementType()->isIntegerTy(32) &&
       !Array->getElementType()->isIntegerTy(64)) ||
      Array->getNumElements() < 16 || Array->getNumElements() > 4096 ||
      !Length->equalsInt(Layout.getTypeAllocSize(Array).getFixedValue()))
    return nullptr;
  return Array->getElementType();
}

// Element accesses only, and a complete counted fill before every read.
static bool describe(MemSetInst &Init, Type *Element, LoopInfo &Loops,
                     DominatorTree &Tree, const DataLayout &Layout, Candidate &Out) {
  auto &Buffer = *cast<AllocaInst>(Init.getRawDest());
  if (Loops.getLoopFor(Init.getParent()) || Buffer.isUsedByMetadata()) return false;
  Out.buffer = &Buffer;
  Out.init = &Init;
  for (User *Use : Buffer.users()) {
    if (Use == &Init) continue;
    auto *Address = dyn_cast<GetElementPtrInst>(Use);
    Value *Index = Address && Address->getPointerOperand() == &Buffer
        ? ElisaCountedPrefix::elementIndex(*Address, Layout, Element) : nullptr;
    if (!Index || !Index->getType()->isIntegerTy(64)) return false;
    for (User *AccessUse : Address->users()) {
      auto *Read = dyn_cast<LoadInst>(AccessUse);
      auto *Write = dyn_cast<StoreInst>(AccessUse);
      if (Read && Read->isSimple() && Read->getType() == Element &&
          Read->getPointerOperand() == Address)
        Out.reads.push_back({Read, Index});
      else if (Write && Write->isSimple() && Write->getPointerOperand() == Address &&
               Write->getValueOperand()->getType() == Element)
        Out.stores.push_back(Write);
      else
        return false;
    }
  }
  SmallVector<StoreInst *, 4> Writers;
  for (StoreInst *Write : Out.stores) {
    Value *Index = ElisaCountedPrefix::elementIndex(
        *cast<GetElementPtrInst>(Write->getPointerOperand()), Layout, Element);
    if (Writers.empty() && !induction(Index, Loops, Out.fill)) continue;
    if (Writers.empty() || Index == ElisaCountedPrefix::elementIndex(
            *cast<GetElementPtrInst>(Writers.front()->getPointerOperand()), Layout, Element))
      Writers.push_back(Write);
  }
  if (Writers.empty() || Writers.size() > 4 || Out.reads.empty() ||
      Out.fill.loop->getParentLoop() || !Out.fill.loop->getSubLoops().empty() ||
      !ElisaCountedPrefix::writersCoverIteration(Writers, Out.fill, Tree))
    return false;
  for (StoreInst *Writer : Writers)
    if (!Tree.dominates(&Init, Writer) ||
        !Tree.dominates(BasicBlockEdge(Out.fill.loop->getHeader(), Out.fill.body),
                        Writer->getParent()))
      return false;
  Out.exit = normalExit(Out.fill.loop);
  if (!Out.exit || ElisaCountedPrefix::canRecompute(Out.fill.bound, Out.exit, Tree, Loops) ||
      isPotentiallyReachable(Out.exit, Init.getParent(), nullptr, &Tree, &Loops))
    return false;
  BasicBlockEdge Completion(Out.fill.loop->getHeader(), Out.exit);
  return llvm::all_of(Out.reads, [&](Access &Read) {
    BasicBlock *Block = Read.instruction->getParent();
    return !Out.fill.loop->contains(Block) && Tree.dominates(Completion, Block);
  });
}

static unsigned removeLinkedPrefixInitializers(Function &Function, LoopInfo &Loops,
                                               DominatorTree &Tree,
                                               const DataLayout &Layout) {
  SmallVector<Candidate, 4> Arrays;
  for (Instruction &Instruction : instructions(Function)) {
    auto *Init = dyn_cast<MemSetInst>(&Instruction);
    Type *Element = Init ? linkedElement(*Init, Layout) : nullptr;
    Candidate Entry;
    if (Element && Arrays.size() < 8 &&
        describe(*Init, Element, Loops, Tree, Layout, Entry))
      Arrays.push_back(std::move(Entry));
  }
  unsigned Removed = 0;
  SmallPtrSet<Value *, 4> Bounds;
  for (Candidate &Entry : Arrays) Bounds.insert(Entry.fill.bound);
  for (Value *Bound : Bounds) {
    SmallVector<Candidate, 4> Group;
    for (Candidate &Entry : Arrays)
      if (Entry.fill.bound == Bound) Group.push_back(Entry);
    uint64_t Length = cast<ArrayType>(Group.front().buffer->getAllocatedType())->getNumElements();
    if (llvm::any_of(Group, [&](Candidate &Entry) {
          return cast<ArrayType>(Entry.buffer->getAllocatedType())->getNumElements() != Length;
        }))
      continue;
    SmallPtrSet<AllocaInst *, 8> Survivors;
    Prover(Bound, Length, Tree, Group).solve(Survivors);
    for (Candidate &Entry : Group)
      if (Survivors.count(Entry.buffer)) {
        Entry.init->eraseFromParent();
        ++Removed;
      }
  }
  return Removed;
}
} // namespace ElisaLinkedPrefix
