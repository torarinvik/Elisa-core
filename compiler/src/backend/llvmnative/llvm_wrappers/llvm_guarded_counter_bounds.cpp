// A counter seeded in [0, cap] stays there if its only update is +1,
// and that update is reached only when the counter differs from cap.
#include <llvm/ADT/SmallVector.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/IR/Constants.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>

using namespace llvm;
namespace {
bool guardedCounterEdge(ICmpInst &Compare, bool Truth, BasicBlock *At,
                        DominatorTree &DT) {
  for (User *Use : Compare.users()) {
    auto *Branch = dyn_cast<CondBrInst>(Use);
    if (!Branch || Branch->getCondition() != &Compare ||
        Branch->getSuccessor(0) == Branch->getSuccessor(1))
      continue;
    if (DT.dominates(BasicBlockEdge(Branch->getParent(),
                                    Branch->getSuccessor(Truth ? 0 : 1)), At))
      return true;
  }
  return false;
}

bool guardedCounterUpdate(Value *ValueAtLatch, PHINode &Counter, Loop &L,
                          ICmpInst &CapTest, bool AtCap, DominatorTree &DT,
                          unsigned &Budget) {
  if (!Budget) return false;
  --Budget;
  if (ValueAtLatch == &Counter) return true;
  auto *I = dyn_cast<Instruction>(ValueAtLatch);
  if (!I || !L.contains(I) || !DT.dominates(&Counter, I)) return false;
  if (auto *Add = dyn_cast<BinaryOperator>(I)) {
    auto *Step = dyn_cast<ConstantInt>(Add->getOperand(1));
    return Add->getOpcode() == Instruction::Add && Add->getOperand(0) == &Counter &&
           Step && Step->isOne() &&
           guardedCounterEdge(CapTest, !AtCap, Add->getParent(), DT);
  }
  if (auto *Merge = dyn_cast<PHINode>(I)) {
    for (Value *Incoming : Merge->incoming_values())
      if (!guardedCounterUpdate(Incoming, Counter, L, CapTest, AtCap, DT, Budget))
        return false;
    return true;
  }
  if (auto *Select = dyn_cast<SelectInst>(I))
    return guardedCounterUpdate(Select->getTrueValue(), Counter, L, CapTest, AtCap, DT, Budget) &&
           guardedCounterUpdate(Select->getFalseValue(), Counter, L, CapTest, AtCap, DT, Budget);
  return false;
}

bool guardedCounterHasCap(PHINode &Counter, LoopInfo &LI, DominatorTree &DT) {
  Loop *L = LI.getLoopFor(Counter.getParent());
  if (!L || L->getHeader() != Counter.getParent() || !L->getSubLoops().empty() ||
      Counter.getNumIncomingValues() != 2 || !L->getLoopPredecessor() || !L->getLoopLatch())
    return false;
  auto *Seed = dyn_cast_or_null<ConstantInt>(Counter.getIncomingValueForBlock(L->getLoopPredecessor()));
  if (!Seed || Seed->isNegative()) return false;
  Value *Back = Counter.getIncomingValueForBlock(L->getLoopLatch());
  for (User *Use : Counter.users()) {
    auto *CapTest = dyn_cast<ICmpInst>(Use);
    if (!CapTest || !CapTest->isEquality() || CapTest->getOperand(0) != &Counter ||
        !L->contains(CapTest))
      continue;
    auto *Cap = dyn_cast<ConstantInt>(CapTest->getOperand(1));
    if (!Cap || Cap->isNegative() || Seed->getValue().ugt(Cap->getValue())) continue;
    unsigned Budget = 32;
    if (guardedCounterUpdate(Back, Counter, *L, *CapTest,
                             CapTest->getPredicate() == ICmpInst::ICMP_EQ, DT, Budget))
      return true;
  }
  return false;
}

unsigned elisacoreFoldGuardedCounterBounds(Module &M) {
  if (!M.getTargetTriple().isAArch64() || M.getNamedMetadata("llvm.dbg.cu")) return 0;
  unsigned Changed = 0;
  for (Function &F : M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() ||
        F.size() > 128 || F.getInstructionCount() > 2000 ||
        F.hasFnAttribute(Attribute::SanitizeAddress) || F.hasFnAttribute(Attribute::SanitizeMemory) ||
        F.hasFnAttribute(Attribute::SanitizeThread) || F.hasFnAttribute(Attribute::SanitizeHWAddress))
      continue;
    SmallVector<ICmpInst *, 8> Checks;
    for (BasicBlock &B : F) {
      auto *Branch = dyn_cast<CondBrInst>(B.getTerminator());
      auto *Check = Branch ? dyn_cast<ICmpInst>(Branch->getCondition()) : nullptr;
      if (Check && Check->hasOneUse() && Check->getPredicate() == ICmpInst::ICMP_ULT &&
          !Check->hasMetadataOtherThanDebugLoc() && isa<PHINode>(Check->getOperand(0)))
        Checks.push_back(Check);
    }
    if (Checks.empty() || Checks.size() > 32) continue;
    DominatorTree DT(F);
    LoopInfo LI(DT);
    SmallVector<ICmpInst *, 8> Proven;
    for (ICmpInst *Check : Checks) {
      auto &Counter = *cast<PHINode>(Check->getOperand(0));
      auto *Bound = dyn_cast<ConstantInt>(Check->getOperand(1));
      if (!Bound || !Bound->getValue().isStrictlyPositive() || !DT.dominates(&Counter, Check))
        continue;
      for (User *Use : Counter.users()) {
        auto *Guard = dyn_cast<ICmpInst>(Use);
        if (!Guard || Guard->getOperand(0) != &Counter || Guard->getOperand(1) != Bound)
          continue;
        auto Pred = Guard->getPredicate();
        if ((Pred == ICmpInst::ICMP_SLT || Pred == ICmpInst::ICMP_SGE) &&
            guardedCounterEdge(*Guard, Pred == ICmpInst::ICMP_SLT, Check->getParent(), DT) &&
            guardedCounterHasCap(Counter, LI, DT)) {
          Proven.push_back(Check);
          break;
        }
      }
    }
    for (ICmpInst *Check : Proven) {
      Check->replaceAllUsesWith(ConstantInt::getTrue(M.getContext()));
      ++Changed;
    }
  }
  return Changed;
}
}
