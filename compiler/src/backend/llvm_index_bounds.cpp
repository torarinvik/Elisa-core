//go:build cgo

// Fold paired signed-index validations using a poison-safe bound.
#include <llvm/Config/llvm-config.h>
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
#include <llvm-c/Error.h>
#include <llvm-c/Transforms/PassBuilder.h>
#if LLVM_VERSION_MAJOR >= 23
#include <llvm/Analysis/LoopInfo.h>
#include <llvm-c/Core.h>
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/Analysis/LazyValueInfo.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/ConstantRange.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Support/CBindingWrapping.h>
using namespace llvm;
namespace ValidatedIndexBounds {
static bool leaves(Value *V, SmallVectorImpl<ICmpInst *> &Out, unsigned Depth=0) {
  if (Depth>6 || Out.size()>4) return false;
  if (auto *C=dyn_cast<ICmpInst>(V)) { Out.push_back(C); return true; }
  if (auto *S=dyn_cast<SelectInst>(V)) {
    if (auto *T=dyn_cast<ConstantInt>(S->getTrueValue()))
      if (T->isOne() && S->getType()->isIntegerTy(1))
        return leaves(S->getCondition(),Out,Depth+1) && leaves(S->getFalseValue(),Out,Depth+1);
  }
  if (auto *B=dyn_cast<BinaryOperator>(V))
    if (B->getOpcode()==Instruction::Or && B->getType()->isIntegerTy(1))
      return leaves(B->getOperand(0),Out,Depth+1) && leaves(B->getOperand(1),Out,Depth+1);
  return false;
}
static Value *negative(ICmpInst *C) {
  auto *Z=dyn_cast<ConstantInt>(C->getOperand(1));
  return C->getPredicate()==ICmpInst::ICMP_SLT && Z && Z->isZero() ? C->getOperand(0):nullptr;
}
static bool dominates(Value *V,Instruction *At,DominatorTree &DT) {
  auto *I=dyn_cast<Instruction>(V);return !I || DT.dominates(I,At);
}
static bool matched(ArrayRef<ICmpInst *> C,Value *&A,Value *&B,Value *&N) {
  if (C.size()!=4) return false;
  for (auto *I:C) if (I->hasMetadataOtherThanDebugLoc()) return false;
  A=negative(C[0]);B=negative(C[1]);
  if (!A || !B || A==B || !A->getType()->isIntegerTy() || A->getType()!=B->getType()) return false;
  if (A->getType()->getIntegerBitWidth()<2 || A->getType()->getIntegerBitWidth()>128) return false;
  N=C[2]->getOperand(1);
  if (N->getType()!=A->getType() || C[3]->getOperand(1)!=N) return false;
  for (unsigned I=2;I<4;++I)
    if (C[I]->getPredicate()!=ICmpInst::ICMP_SGE && C[I]->getPredicate()!=ICmpInst::ICMP_UGE) return false;
  return (C[2]->getOperand(0)==A && C[3]->getOperand(0)==B) ||
         (C[2]->getOperand(0)==B && C[3]->getOperand(0)==A);
}
static bool mergeable(BasicBlock *First,BasicBlock *Second,BasicBlock *Exit,BasicBlock *Continue) {
  if (First==Second || First==Exit || First==Continue || Second==Exit || Second==Continue || Exit==Continue ||
      Second->isEHPad() || Exit->isEHPad() || Continue->isEHPad() ||
      Second->getSinglePredecessor()!=First || !Continue->phis().empty()) return false;
  // Redirected invalid cases formerly used the second predecessor's PHI
  // value. Keeping an unreachable old edge does NOT preserve that selection.
  for (PHINode &P:Exit->phis()) {
    int I=P.getBasicBlockIndex(First),J=P.getBasicBlockIndex(Second);
    if (I<0 || J<0 || P.getIncomingValue(I)!=P.getIncomingValue(J)) return false;
  }
  for (Instruction &I:*Second) {
    if (I.isTerminator() || isa<DbgInfoIntrinsic>(I)) continue;
    bool Pure=isa<ICmpInst>(I)||isa<SelectInst>(I);
    if (auto *BO=dyn_cast<BinaryOperator>(&I)) Pure|=BO->getOpcode()==Instruction::Or && BO->getType()->isIntegerTy(1);
    if (!Pure || I.hasMetadataOtherThanDebugLoc()) return false;
    for (User *U:I.users()) {
      auto *Use=dyn_cast<Instruction>(U);
      if (!Use || Use->getParent()!=Second) return false;
    }
  }
  return true;
}
}
extern "C" unsigned elisacoreValidatedIndexBounds(LLVMModuleRef Ref) {
  using namespace ValidatedIndexBounds;
  if (!Ref) return 0;Module &M=*unwrap(Ref);
  if (M.getNamedMetadata("llvm.dbg.cu")) return 0;
  PassBuilder PB;LoopAnalysisManager LAM;FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM;ModuleAnalysisManager MAM;
  PB.registerModuleAnalyses(MAM);PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM);PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM,FAM,CGAM,MAM);
  unsigned Changed=0;
  for (Function &F:M) {
    if (F.isDeclaration() || F.hasOptNone() || F.hasOptSize() || F.hasMinSize() || F.hasFnAttribute(Attribute::SanitizeAddress) || F.hasFnAttribute(Attribute::SanitizeMemory) || F.hasFnAttribute(Attribute::SanitizeThread) || F.hasFnAttribute(Attribute::SanitizeHWAddress) || F.getInstructionCount()>2500) continue;
    for (BasicBlock &BB:F) {
      auto *Br=dyn_cast<CondBrInst>(BB.getTerminator());
      if (!Br || Br->hasMetadataOtherThanDebugLoc()) continue;
      SmallVector<ICmpInst *,4> C;
      if (!leaves(Br->getCondition(),C)) continue;
      BasicBlock *Second=nullptr,*Continue=nullptr;
      if (C.size()==2) {
        Second=Br->getSuccessor(1);auto *Br2=dyn_cast<CondBrInst>(Second->getTerminator());
        if (!Br2 || Br2->hasMetadataOtherThanDebugLoc() || Br2->getSuccessor(0)!=Br->getSuccessor(0)) continue;
        Continue=Br2->getSuccessor(1);
        if (!mergeable(&BB,Second,Br->getSuccessor(0),Continue) || !leaves(Br2->getCondition(),C)) continue;
      }
      Value *A=nullptr,*B=nullptr,*N=nullptr;
      if (!matched(C,A,B,N)) continue;
      auto &DT=FAM.getResult<DominatorTreeAnalysis>(F);
      if (!dominates(A,Br,DT)||!dominates(B,Br,DT)||!dominates(N,Br,DT)) continue;
      auto &LVI=FAM.getResult<LazyValueAnalysis>(F);
      auto R=LVI.getConstantRange(N,Br,false);
      if (R.isEmptySet()||R.isFullSet()||R.isWrappedSet()||R.getSignedMin().isNegative()) continue;
      if (R.isSingleElement()&&!isGuaranteedNotToBeUndefOrPoison(N,nullptr,Br,&DT)) continue;
      // Keep small guarded scans aligned after shrinking their entry checks.
      // The Linux backend has a different layout tradeoff; its default wins.
      const Triple &Target=M.getTargetTriple();
      if (Target.isOSDarwin() && Target.getArch()==Triple::aarch64) {
        auto &LI=FAM.getResult<LoopAnalysis>(F);
        for (Loop *L:LI.getLoopsInPreorder()) {
          if (!L->getSubLoops().empty() || L->getNumBlocks()>3) continue;
          unsigned Size=0;bool Leaf=true;
          for (BasicBlock *Block:L->blocks()) for (Instruction &I:*Block) {
            ++Size;
            if (auto *CB=dyn_cast<CallBase>(&I))
              Leaf &= isa<DbgInfoIntrinsic>(CB);
          }
          if (!Leaf || Size>32 || !L->getLoopLatch()) continue;
          // Preserve explicit source/optimizer loop settings, including any
          // existing alignment. Construct a self-referencing loop ID.
          SmallVector<Metadata *,8> Options{nullptr};bool HasAlign=false;
          if (MDNode *ID=L->getLoopID()) for (unsigned I=1;I<ID->getNumOperands();++I) {
            Metadata *Option=ID->getOperand(I).get();Options.push_back(Option);
            auto *Node=dyn_cast_or_null<MDNode>(Option);
            if (Node && Node->getNumOperands())
              if (auto *Name=dyn_cast_or_null<MDString>(Node->getOperand(0)))
                HasAlign |= Name->getString()=="llvm.loop.align";
          }
          if (HasAlign) continue;
          LLVMContext &C=M.getContext();
          Options.push_back(MDNode::get(C,{MDString::get(C,"llvm.loop.align"),
              ConstantAsMetadata::get(ConstantInt::get(Type::getInt32Ty(C),16))}));
          MDNode *ID=MDNode::getDistinct(C,Options);ID->replaceOperandWith(0,ID);
          L->setLoopID(ID);
        }
      }
      IRBuilder<> Builder(Br);
      // Concrete nonnegative bounds are unchanged. For a poison bound the
      // mask ensures negative indices still reject, as they did before using
      // the bound. With two nonnegative indices the old bound use was UB.
      auto *Ty=cast<IntegerType>(N->getType());
      Value *Safe=Builder.CreateFreeze(N,"index.bound.defined");
      Safe=Builder.CreateAnd(Safe,ConstantInt::get(Ty,APInt::getSignedMaxValue(Ty->getBitWidth())),"index.bound.nonnegative");
      Value *First=Builder.CreateICmpUGE(A,Safe);
      Value *Next=Builder.CreateICmpUGE(B,Safe);
      Br->setCondition(Builder.CreateSelect(First,Builder.getTrue(),Next,"index.invalid"));
      if (Second) Br->setSuccessor(1,Continue);
      ++Changed;break; // All function analyses are discarded before reuse.
    }
  }
  return Changed;
}

#else
extern "C" unsigned elisacoreValidatedIndexBounds(LLVMModuleRef) { return 0; }
#endif
extern "C" unsigned elisacoreOptimizeIndexBounds(LLVMModuleRef M, LLVMTargetMachineRef T) {
  if (!elisacoreValidatedIndexBounds(M)) return 0;
  auto Options=LLVMCreatePassBuilderOptions();
  auto Error=LLVMRunPasses(M,"function(instcombine<max-iterations=8;no-verify-fixpoint>,simplifycfg)",T,Options);
  LLVMDisposePassBuilderOptions(Options);
  if(Error){LLVMConsumeError(Error);return 2;}return 1;
}
