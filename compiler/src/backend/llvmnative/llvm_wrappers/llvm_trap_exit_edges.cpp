// Give every trap block a CFG edge to the function's single return block.
// Shrink-wrapping needs one restore point that post-dominates every frame
// use; a bounds-check trap inside a loop is a second exit, so the prologue
// stays in the entry block and early validation returns pay for a frame they
// never use. `llvm.trap` is noreturn, so a branch after it is folded back to
// `unreachable` before instruction selection. An inline `brk #1` is the same
// AArch64 instruction without that attribute, and the edge it keeps is never
// taken at run time.
#include <llvm-c/Target.h>
#include <llvm/IR/InlineAsm.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/MDBuilder.h>
#include <llvm/IR/Module.h>

namespace ElisaTrapExits {
using namespace llvm;

static bool isTrapBlock(BasicBlock &Block) {
  if (!isa<UnreachableInst>(Block.getTerminator())) return false;
  auto *Trap = dyn_cast_or_null<IntrinsicInst>(Block.getTerminator()->getPrevNode());
  return Trap && Trap->getIntrinsicID() == Intrinsic::trap;
}

// Keep the trap edge unlikely: branch probabilities no longer see the
// `unreachable` that marked it cold.
static void markTrapEdgeUnlikely(BasicBlock &Trap) {
  for (BasicBlock *Predecessor : predecessors(&Trap)) {
    auto *Branch = dyn_cast<CondBrInst>(Predecessor->getTerminator());
    if (!Branch || Branch->hasMetadata(LLVMContext::MD_prof) ||
        Branch->getSuccessor(0) == Branch->getSuccessor(1))
      continue;
    bool TrapFirst = Branch->getSuccessor(0) == &Trap;
    MDBuilder Builder(Branch->getContext());
    Branch->setMetadata(LLVMContext::MD_prof,
                        Builder.createBranchWeights(TrapFirst ? 1 : (1u << 20) - 1,
                                                    TrapFirst ? (1u << 20) - 1 : 1));
  }
}

static unsigned connectFunction(Function &Function) {
  if (Function.isDeclaration() || Function.hasOptNone()) return 0;
  BasicBlock *Exit = nullptr;
  SmallVector<BasicBlock *, 8> Traps;
  for (BasicBlock &Block : Function) {
    if (isa<ReturnInst>(Block.getTerminator())) {
      if (Exit) return 0;
      Exit = &Block;
    } else if (isTrapBlock(Block)) {
      Traps.push_back(&Block);
    }
  }
  if (!Exit || Exit == &Function.getEntryBlock() || Traps.empty()) return 0;
  auto *Type = FunctionType::get(Type::getVoidTy(Function.getContext()), false);
  auto *Brk = InlineAsm::get(Type, "brk #0x1", "", /*hasSideEffects=*/true);
  for (BasicBlock *Trap : Traps) {
    markTrapEdgeUnlikely(*Trap);
    auto *Intrinsic = cast<IntrinsicInst>(Trap->getTerminator()->getPrevNode());
    auto *Call = CallInst::Create(Type, Brk, {}, "", Intrinsic->getIterator());
    Call->setDebugLoc(Intrinsic->getDebugLoc());
    Call->addFnAttr(Attribute::Cold);
    Call->addFnAttr(Attribute::NoUnwind);
    Trap->getTerminator()->eraseFromParent();
    Intrinsic->eraseFromParent();
    UncondBrInst::Create(Exit, Trap);
    for (PHINode &Phi : Exit->phis())
      Phi.addIncoming(PoisonValue::get(Phi.getType()), Trap);
  }
  return Traps.size();
}

static unsigned connectTrapExits(Module &Module) {
  if (!Module.getTargetTriple().isAArch64()) return 0;
  // The object streamer parses inline asm; the driver may not have
  // registered the parser. Registration is idempotent.
  LLVMInitializeAArch64AsmParser();
  unsigned Changed = 0;
  for (Function &Function : Module) Changed += connectFunction(Function);
  return Changed;
}
} // namespace ElisaTrapExits

extern "C" unsigned elisacoreConnectTrapExits(LLVMModuleRef Module) {
  return Module ? ElisaTrapExits::connectTrapExits(*llvm::unwrap(Module)) : 0;
}
