//go:build cgo
#include <llvm/Config/llvm-config.h>
#if LLVM_VERSION_MAJOR >= 23
#include <llvm/TargetParser/Triple.h>
// Remove full zero fills only when a later non-wrapping prefix fill dominates
// every scalar read, and each read index is proved below that prefix length.
// Unknown aliases, escaping allocations and unsupported accesses retain the fill.
#include <llvm/IRReader/IRReader.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/Verifier.h>
#include <llvm/IR/InstIterator.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Dominators.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Analysis/TargetLibraryInfo.h>
#include <map>
using namespace llvm;
static bool coveredPrefixBranchProof(Function &F,Value *Index,Value *Count,Instruction *At){
 DominatorTree DT(F);std::map<Value*,bool> Facts;
 for(BasicBlock &B:F) if(auto *Br=dyn_cast<CondBrInst>(B.getTerminator());Br&&Br->getSuccessor(0)!=Br->getSuccessor(1))
  for(unsigned K=0;K<2;++K) if(DT.dominates(BasicBlockEdge(&B,Br->getSuccessor(K)),At->getParent())) Facts[Br->getCondition()]=K==0;
 for(unsigned Round=0;Round<16;++Round){auto Old=Facts;
  for(auto [V,T]:Old) if(auto *B=dyn_cast<BinaryOperator>(V)){
   Value *A=B->getOperand(0),*C=B->getOperand(1);
   if((B->getOpcode()==Instruction::And && T)||(B->getOpcode()==Instruction::Or && !T)){Facts[A]=T;Facts[C]=T;}
   if((B->getOpcode()==Instruction::And && !T)||(B->getOpcode()==Instruction::Or && T)){
    if(Facts.count(A)&&Facts[A]!=T)Facts[C]=T;
    if(Facts.count(C)&&Facts[C]!=T)Facts[A]=T;
   }
  }
  // Infer only comparisons needed by existing boolean formulas.
  for(auto [V,T]:Old) if(auto *B=dyn_cast<BinaryOperator>(V))
   for(Value *Operand:B->operands()) if(auto *Target=dyn_cast<ICmpInst>(Operand))
    for(auto [Known,Truth]:Old){
     auto Result=isImpliedCondition(Known,Target->getPredicate(),Target->getOperand(0),Target->getOperand(1),F.getParent()->getDataLayout(),Truth);
     if(Result){if(Facts.count(Target)&&Facts[Target]!=*Result)return false;Facts[Target]=*Result;}
    }
  if(Old==Facts)break;
 }
 for(auto [V,T]:Facts)if(auto *C=dyn_cast<ICmpInst>(V)){
  auto P=T?C->getPredicate():C->getInversePredicate();
  if(C->getOperand(0)==Index&&C->getOperand(1)==Count&&P==ICmpInst::ICMP_ULT)return true;
 }
 return false;
}

static unsigned removeCoveredPrefixZeros(Module &M,FunctionAnalysisManager &FAM){
 unsigned Removed=0;
 for(Function &F:M){if(F.isDeclaration()||F.hasOptNone()||F.hasOptSize()||F.hasMinSize()||F.hasFnAttribute(Attribute::SanitizeAddress)||F.hasFnAttribute(Attribute::SanitizeMemory)||F.hasFnAttribute(Attribute::SanitizeThread)||F.hasFnAttribute(Attribute::SanitizeHWAddress))continue;
 auto &DT=FAM.getResult<DominatorTreeAnalysis>(F);auto &SE=FAM.getResult<ScalarEvolutionAnalysis>(F);
 SmallVector<MemSetInst*,8> Initializers;
 for(Instruction &I:instructions(F))if(auto *S=dyn_cast<MemSetInst>(&I))if(auto *Z=dyn_cast<ConstantInt>(S->getValue());Z&&Z->isZero())Initializers.push_back(S);
 for(auto *Init:Initializers){
 auto *A=dyn_cast<AllocaInst>(Init->getRawDest());auto *Len=dyn_cast<ConstantInt>(Init->getLength());
 if(!A||!A->isStaticAlloca()||Init->isVolatile()||Init->hasOperandBundles()||!Len||Len->getBitWidth()!=64||A->getAddressSpace()!=0)continue;
 auto *AT=dyn_cast<ArrayType>(A->getAllocatedType());
 if(!AT||!AT->getElementType()->isIntegerTy(64)||Len->getZExtValue()!=AT->getNumElements()*8)continue;
 MemSetInst *Cover=nullptr;Value *Count=nullptr;
 for(User *U:A->users())if(auto *S=dyn_cast<MemSetInst>(U);S&&S!=Init&&S->getRawDest()==A&&!S->isVolatile()&&!S->hasOperandBundles()&&DT.dominates(Init,S)){
  auto *Shift=dyn_cast<BinaryOperator>(S->getLength());
  if(Shift&&Shift->getOpcode()==Instruction::Shl&&Shift->hasNoUnsignedWrap()&&isa<ConstantInt>(Shift->getOperand(1))&&cast<ConstantInt>(Shift->getOperand(1))->equalsInt(3)){Cover=S;Count=Shift->getOperand(0);}
 }
 if(!Cover||!Count->getType()->isIntegerTy(64))continue;
 bool Safe=true;unsigned Reads=0;
 for(User *U:A->users()){
  if(U==Init||U==Cover||isa<LifetimeIntrinsic>(U))continue;
  auto *G=dyn_cast<GetElementPtrInst>(U);
  if(!G||G->getPointerOperand()!=A||G->getNumIndices()!=1||M.getDataLayout().getTypeAllocSize(G->getSourceElementType())!=8){Safe=false;break;}
  Value *Index=G->idx_begin()->get();
  if(Index->getType()!=Count->getType()){Safe=false;break;}
  for(User *GU:G->users()){
   if(auto *S=dyn_cast<StoreInst>(GU);S&&S->isSimple()&&S->getPointerOperand()==G&&S->getValueOperand()!=G)continue;
   auto *L=dyn_cast<LoadInst>(GU);
   if(!L||!L->isSimple()||L->getPointerOperand()!=G||!L->getType()->isIntegerTy(64)||!DT.dominates(Cover,L)){Safe=false;break;}
   if(++Reads>256){Safe=false;break;}
   auto *End=SE.getAddExpr(SE.getSCEV(Index),SE.getConstant(Index->getType(),1));
   if(!( !SE.getUnsignedRange(SE.getSCEV(Index)).getUnsignedMax().isMaxValue() && SE.isKnownPredicateAt(ICmpInst::ICMP_ULE,End,SE.getSCEV(Count),L))&&!coveredPrefixBranchProof(F,Index,Count,L)){Safe=false;break;}
  }
  if(!Safe)break;
 }
 if(Safe&&Reads){Init->eraseFromParent();++Removed;}
 }
 }
 return Removed;
}

static unsigned prepareCoveredPrefixZeros(Module &M, FunctionAnalysisManager &FAM, PassBuilder &PB) {
 if (M.getNamedMetadata("llvm.dbg.cu") || !(M.getTargetTriple().isMacOSX() || M.getTargetTriple().isOSLinux()) || M.getTargetTriple().getArch()!=Triple::aarch64) return 0;
 SmallVector<Function *,8> Candidates;
 for(Function &F:M){
  if(F.isDeclaration()||F.hasOptNone()||F.hasOptSize()||F.hasMinSize()||F.hasFnAttribute(Attribute::SanitizeAddress)||F.hasFnAttribute(Attribute::SanitizeMemory)||F.hasFnAttribute(Attribute::SanitizeThread)||F.hasFnAttribute(Attribute::SanitizeHWAddress))continue;
  unsigned Instructions=0;bool Candidate=false;
  for(Instruction &I:instructions(F)){
   if(++Instructions>50000){Candidate=false;break;}
   Value *Dest=nullptr,*Length=nullptr,*Fill=nullptr;
   if(auto *S=dyn_cast<MemSetInst>(&I)){
    if(S->isVolatile()||S->hasOperandBundles())continue;
    Dest=S->getRawDest();Length=S->getLength();Fill=S->getValue();
   }else if(auto *C=dyn_cast<CallInst>(&I)){
    auto *Callee=C->getCalledFunction();LibFunc LF;
    auto &TLI=FAM.getResult<TargetLibraryAnalysis>(F);
    if(!Callee||!Callee->isDeclaration()||Callee->hasFnAttribute(Attribute::NoBuiltin)||C->isNoBuiltin()||C->hasOperandBundles()||C->hasMetadataOtherThanDebugLoc()||C->getCallingConv()!=CallingConv::C||C->arg_size()!=3||!TLI.getLibFunc(*C,LF)||LF!=LibFunc_memset||!TLI.has(LF))continue;
    Dest=C->getArgOperand(0);Fill=C->getArgOperand(1);Length=C->getArgOperand(2);
   }else continue;
   auto *A=dyn_cast<AllocaInst>(Dest);auto *N=dyn_cast<ConstantInt>(Length);auto *V=dyn_cast<ConstantInt>(Fill);
   auto *AT=A?dyn_cast<ArrayType>(A->getAllocatedType()):nullptr;
   if(AT&&AT->getElementType()->isIntegerTy(64)&&AT->getNumElements()>=8&&AT->getNumElements()<=512&&N&&N->getBitWidth()==64&&V&&V->isZero()&&N->getZExtValue()==AT->getNumElements()*8)Candidate=true;
  }
  if(Candidate)Candidates.push_back(&F);
 }
 if(Candidates.empty()||Candidates.size()>32)return 0;
 auto Cleanup=PB.buildFunctionSimplificationPipeline(OptimizationLevel::O1,ThinOrFullLTOPhase::None);
 FunctionPassManager Idiom;
 if(auto E=PB.parsePassPipeline(Idiom,"loop(loop-idiom),instcombine,simplifycfg")){consumeError(std::move(E));return 0;}
 for(Function *F:Candidates){Cleanup.run(*F,FAM);Idiom.run(*F,FAM);}
 return removeCoveredPrefixZeros(M,FAM);
}

#endif
