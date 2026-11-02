//go:build cgo
// Version a counted loop for one iteration only when LLVM proves that doing so
// removes substantial fixed stack initialization and leaves a tiny function.
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Transforms/Utils/Cloning.h>
#include <llvm/Target/TargetMachine.h>
using namespace llvm;
namespace {
static uint64_t zeroBytes(Function &F) {
  uint64_t total = 0;
  for (BasicBlock &B : F) for (Instruction &I : B) {
    auto *S = dyn_cast<MemSetInst>(&I);
    if (!S || S->isVolatile() || S->hasOperandBundles()) continue;
    auto *V = dyn_cast<ConstantInt>(S->getValue());
    auto *N = dyn_cast<ConstantInt>(S->getLength());
    auto *A = dyn_cast<AllocaInst>(getUnderlyingObject(S->getDest()));
    if (V && V->isZero() && N && N->getValue().ule(8192) && A && A->isStaticAlloca())
      total += N->getZExtValue();
  }
  return total;
}
static Argument *tripArgument(Function &F, LoopInfo &LI) {
  if (LI.getTopLevelLoops().size() != 1) return nullptr;
  Loop *L = *LI.begin();
  if (!L->getSubLoops().empty() || !L->getLoopPreheader() || !L->getLoopLatch()) return nullptr;
  auto *B = dyn_cast<BranchInst>(L->getLoopLatch()->getTerminator());
  if (!B || !B->isConditional()) return nullptr;
  auto *C = dyn_cast<ICmpInst>(B->getCondition());
  if (!C || !C->isEquality()) return nullptr;
  bool backOnEqual = B->getSuccessor(0) == L->getHeader();
  bool backOnNotEqual = B->getSuccessor(1) == L->getHeader();
  if ((!backOnEqual && !backOnNotEqual) ||
      (backOnEqual != (C->getPredicate() == ICmpInst::ICMP_NE))) return nullptr;
  for (unsigned side = 0; side != 2; ++side) {
    auto *N = dyn_cast<Argument>(C->getOperand(side));
    auto *Add = dyn_cast<BinaryOperator>(C->getOperand(1-side));
    if (!N || !N->getType()->isIntegerTy() ||
        (N->getType()->getIntegerBitWidth() != 32 && N->getType()->getIntegerBitWidth() != 64) ||
        !Add || Add->getOpcode() != Instruction::Add) continue;
    for (unsigned operand = 0; operand != 2; ++operand) {
      auto *P = dyn_cast<PHINode>(Add->getOperand(operand));
      auto *One = dyn_cast<ConstantInt>(Add->getOperand(1-operand));
      if (!P || !One || !One->isOne() || P->getParent() != L->getHeader() ||
          P->getNumIncomingValues() != 2 ||
          P->getIncomingValueForBlock(L->getLoopLatch()) != Add) continue;
      auto *Zero = dyn_cast<ConstantInt>(P->getIncomingValueForBlock(L->getLoopPreheader()));
      if (Zero && Zero->isZero()) return N;
    }
  }
  return nullptr;
}
static bool eligible(Function &F) {
  if (F.isDeclaration() || F.isVarArg() || F.getCallingConv() != CallingConv::C ||
      F.hasGC() || F.getEntryCount().has_value() || F.hasOptNone() || F.hasOptSize() ||
      F.hasMinSize() || F.hasFnAttribute(Attribute::Naked) ||
      F.hasFnAttribute(Attribute::Convergent) || F.hasFnAttribute(Attribute::NoDuplicate) || F.getSubprogram() || F.hasPersonalityFn() ||
      F.hasFnAttribute("elisa.short-trip-versioned") || !F.doesNotThrow() ||
      !F.getReturnType()->isIntegerTy() || F.arg_size() > 4 || F.getInstructionCount() > 350)
    return false;
  for (BasicBlock &B : F) {
    if (B.hasAddressTaken()) return false;
    for (Instruction &I : B) {
      if (auto *A = dyn_cast<AllocaInst>(&I)) if (!A->isStaticAlloca()) return false;
      if (auto *C = dyn_cast<CallBase>(&I)) {
        auto *Intrinsic = dyn_cast<IntrinsicInst>(C);
        if (!Intrinsic || C->isConvergent() || C->cannotDuplicate() || C->hasOperandBundles()) return false;
        switch (Intrinsic->getIntrinsicID()) {
        case Intrinsic::memset: case Intrinsic::lifetime_start:
        case Intrinsic::lifetime_end: case Intrinsic::trap: case Intrinsic::assume: break;
        default: return false;
        }
      }
    }
  }
  uint64_t bytes = zeroBytes(F);
  return bytes >= 1024 && bytes <= 8192;
}
}
extern "C" unsigned elisacoreSpecializeShortTrips(LLVMModuleRef ref, LLVMTargetMachineRef target) {
  if (!ref) return 0;
  Module &M = *unwrap(ref);
  if (M.getTargetTriple().getArch() != Triple::aarch64 || M.getNamedMetadata("llvm.dbg.cu")) return 0;
  PassBuilder PB(reinterpret_cast<TargetMachine *>(target));
  LoopAnalysisManager LAM; FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM; ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM); PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM); PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
  SmallVector<Function *, 16> functions;
  for (Function &F : M) if (eligible(F)) functions.push_back(&F);
  unsigned changed = 0;
  for (Function *F : functions) {
    DominatorTree DT(*F); LoopInfo LI(DT);
    Argument *N = tripArgument(*F, LI);
    if (!N) continue;
    ValueToValueMapTy map;
    map[N] = ConstantInt::get(N->getType(), 1);
    Function *One = CloneFunction(F, map);
    One->setName(F->getName() + ".one_iteration");
    One->setLinkage(GlobalValue::InternalLinkage);
    One->setComdat(nullptr);
    FunctionPassManager simplify;
    if (auto error = PB.parsePassPipeline(simplify,
        "sroa,instcombine<max-iterations=8>,simplifycfg,early-cse,loop-simplify,lcssa,loop-unroll,sroa,early-cse,gvn,instcombine<max-iterations=8>,simplifycfg,dse,adce,simplifycfg")) {
      consumeError(std::move(error)); One->eraseFromParent(); return 2;
    }
    simplify.run(*One, FAM);
    if (One->getInstructionCount() > 64 || zeroBytes(*One) + 512 > zeroBytes(*F)) {
      FAM.clear(*One, One->getName()); One->eraseFromParent(); continue;
    }
    BasicBlock *Original = &F->getEntryBlock();
    BasicBlock *Entry = BasicBlock::Create(M.getContext(), "short_trip.dispatch", F, Original);
    BasicBlock *Small = BasicBlock::Create(M.getContext(), "short_trip.one", F, Original);
    // Keep every original static allocation in the entry block. Otherwise the
    // new predecessor would turn it into a dynamic stack allocation.
    SmallVector<AllocaInst *, 8> allocas;
    for (Instruction &I : *Original) if (auto *A = dyn_cast<AllocaInst>(&I)) allocas.push_back(A);
    for (AllocaInst *A : allocas) A->moveBefore(*Entry, Entry->end());
    IRBuilder<> builder(Entry);
    builder.CreateCondBr(builder.CreateICmpEQ(N, ConstantInt::get(N->getType(), 1)), Small, Original);
    builder.SetInsertPoint(Small);
    SmallVector<Value *, 4> args;
    for (Argument &A : F->args()) if (&A != N) args.push_back(&A);
    auto *Call = builder.CreateCall(One, args);
    Call->setCallingConv(One->getCallingConv());
    Call->setAttributes(One->getAttributes());
    builder.CreateRet(Call);
    // The specialized body is small by construction; leave no extra call on
    // the fast path. LLVM's inliner preserves argument and return attributes.
    InlineFunctionInfo IFI;
    if (InlineFunction(*Call, IFI).isSuccess() && One->use_empty()) {
      FAM.clear(*One, One->getName()); One->eraseFromParent();
    }
    F->addFnAttr("elisa.short-trip-versioned");
    ++changed;
  }
  return changed ? 1 : 0;
}
