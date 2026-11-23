// Remove stores into local arrays that nothing reads before the function
// returns. A store is dead when every object its address may point into
// is one of the function's allocas and no instruction that may read memory
// can execute after it: the object dies at the return, so the value is
// never observed, even through an address that escaped (a read of it after
// the return, or racing with the store, is undefined). LLVM's dead-store
// elimination finds the object through GEPs only; after full unrolling a backtracking search ends in a
// shared `visited[position] = 0` whose address is a phi over every cell,
// and keeping that store keeps one address per cell live and spilled.
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/CFG.h>
#include <llvm/IR/InstIterator.h>
#include <llvm/Transforms/Utils/Local.h>
namespace ElisaExitDeadStores {
using namespace llvm;

// Lifetime markers read nothing a store could have written.
static bool reads(Instruction &I) {
  return I.mayReadFromMemory() && !I.isLifetimeStartOrEnd();
}

// Blocks from whose start some instruction that may read memory can run.
static DenseSet<BasicBlock *> readingBlocks(Function &F) {
  DenseSet<BasicBlock *> Reading;
  SmallVector<BasicBlock *, 16> Work;
  for (BasicBlock &Block : F)
    if (llvm::any_of(Block, reads)) {
      Reading.insert(&Block);
      Work.push_back(&Block);
    }
  while (!Work.empty())
    for (BasicBlock *Predecessor : predecessors(Work.pop_back_val()))
      if (Reading.insert(Predecessor).second) Work.push_back(Predecessor);
  return Reading;
}

// No instruction after Store, in its block or any block reachable from it,
// may read memory.
static bool lastWrite(StoreInst &Store, const DenseSet<BasicBlock *> &Reading) {
  for (Instruction *Next = Store.getNextNode(); Next; Next = Next->getNextNode())
    if (reads(*Next)) return false;
  return llvm::none_of(successors(Store.getParent()),
                       [&](BasicBlock *Successor) { return Reading.contains(Successor); });
}

// Every object Pointer may address is one of the function's allocas.
static bool localObjects(Value *Pointer) {
  SmallVector<const Value *, 8> Objects;
  getUnderlyingObjects(Pointer, Objects, nullptr, 0);
  return !Objects.empty() &&
         llvm::all_of(Objects, [](const Value *Object) { return isa<AllocaInst>(Object); });
}

static unsigned removeExitDeadStores(Function &F) {
  DenseSet<BasicBlock *> Reading = readingBlocks(F);
  SmallVector<StoreInst *, 8> Dead;
  for (Instruction &I : instructions(F))
    if (auto *Store = dyn_cast<StoreInst>(&I);
        Store && Store->isSimple() && lastWrite(*Store, Reading) &&
        localObjects(Store->getPointerOperand()))
      Dead.push_back(Store);
  for (StoreInst *Store : Dead) {
    Value *Pointer = Store->getPointerOperand();
    Store->eraseFromParent();
    RecursivelyDeleteTriviallyDeadInstructions(Pointer);
  }
  return Dead.size();
}

static unsigned removeExitDeadStores(Module &Module) {
  unsigned Removed = 0;
  for (Function &F : Module)
    if (!F.isDeclaration() && !F.hasOptNone()) Removed += removeExitDeadStores(F);
  return Removed;
}
} // namespace ElisaExitDeadStores
