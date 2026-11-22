// go:build cgo
//  Thread an edge past a side-effect-free branch block whose condition is
//  already decided on that edge. A latch `i.next == n` reached from a block
//  guarded by `i.next < n` then jumps straight back to the loop header.
#include <llvm/Config/llvm-config.h>
#if LLVM_VERSION_MAJOR >= 23
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>
namespace ElisaImpliedExit {
using namespace llvm;

// Every value the block defines is consumed by its own branch or by a phi
// edge leaving the block, so bypassing it needs no SSA repair.
static bool bypassable(BasicBlock &Block, ICmpInst &Condition) {
  if (Condition.getParent() != &Block || !Condition.hasOneUse()) return false;
  for (Value *Operand : Condition.operands())
    if (auto *Definition = dyn_cast<Instruction>(Operand);
        Definition && Definition->getParent() == &Block)
      return false;
  for (Instruction &Instruction : Block) {
    if (&Instruction == &Condition || Instruction.isTerminator()) continue;
    if (!isa<PHINode>(Instruction)) return false;
    for (Use &Use : Instruction.uses()) {
      auto *Phi = dyn_cast<PHINode>(Use.getUser());
      if (!Phi || Phi->getParent() == &Block ||
          Phi->getIncomingBlock(Use) != &Block)
        return false;
    }
  }
  return true;
}

// The branch value implied on entry to Entry by a short chain of single
// predecessors, if any guard decides it.
static std::optional<bool> impliedOnEntry(BasicBlock *Entry, ICmpInst &Condition,
                                          const DataLayout &Layout) {
  for (unsigned Depth = 0; Depth < 4; ++Depth) {
    BasicBlock *Predecessor = Entry->getSinglePredecessor();
    if (!Predecessor || Predecessor == Entry) return std::nullopt;
    auto *Branch = dyn_cast<CondBrInst>(Predecessor->getTerminator());
    if (Branch && Branch->getSuccessor(0) != Branch->getSuccessor(1))
      if (auto Known = isImpliedCondition(Branch->getCondition(), &Condition, Layout,
                                          Branch->getSuccessor(0) == Entry))
        return Known;
    Entry = Predecessor;
  }
  return std::nullopt;
}

static unsigned threadFunction(Function &Function, const DataLayout &Layout) {
  unsigned Changed = 0;
  for (BasicBlock &Block : Function) {
    auto *Branch = dyn_cast<CondBrInst>(Block.getTerminator());
    auto *Condition = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
    if (!Condition || Branch->getSuccessor(0) == Branch->getSuccessor(1) ||
        Block.size() > 8 || !bypassable(Block, *Condition))
      continue;
    SmallVector<BasicBlock *, 4> Predecessors(predecessors(&Block));
    for (BasicBlock *Predecessor : Predecessors) {
      auto *Jump = dyn_cast<UncondBrInst>(Predecessor->getTerminator());
      if (!Jump || Predecessor == &Block) continue;
      auto Known = impliedOnEntry(Predecessor, *Condition, Layout);
      if (!Known) continue;
      BasicBlock *Target = Branch->getSuccessor(*Known ? 0 : 1);
      if (Target == &Block) continue;
      for (PHINode &Phi : Target->phis()) {
        Value *Incoming = Phi.getIncomingValueForBlock(&Block);
        if (auto *Local = dyn_cast<PHINode>(Incoming); Local && Local->getParent() == &Block)
          Incoming = Local->getIncomingValueForBlock(Predecessor);
        Phi.addIncoming(Incoming, Predecessor);
      }
      Block.removePredecessor(Predecessor, /*KeepOneInputPHIs=*/true);
      Jump->setSuccessor(0, Target);
      ++Changed;
    }
  }
  return Changed;
}
} // namespace ElisaImpliedExit

static unsigned threadImpliedLoopExits(llvm::Module &Module) {
  unsigned Changed = 0;
  for (llvm::Function &Function : Module)
    if (!Function.isDeclaration() && !Function.hasOptNone() &&
        !Function.hasOptSize() && !Function.hasMinSize())
      Changed += ElisaImpliedExit::threadFunction(Function, Module.getDataLayout());
  return Changed;
}
#endif
