// A bounded abstract interpreter for private i64 push/pop scratch arrays.
// State means [0,prefix) is initialized, plus the boundary element when `extra`.
// SSA offsets are modulo-64 equalities relative to prefix. Stores on defined
// executions are within the allocation; appending therefore cannot wrap.
#include <llvm/IR/Module.h>
#include <deque>
#include <map>
#include <set>
#include <tuple>
#include <llvm/Analysis/CFG.h>
#include <llvm/IR/Dominators.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/ADT/PostOrderIterator.h>
using namespace llvm;
namespace {
struct StackPrefixState {
 unsigned block=0,lo=0,hi=0;bool active=false,extra=false;
 std::map<Value*,int> offsets;
 bool operator<(const StackPrefixState &R)const{return std::tie(block,lo,hi,active,extra,offsets)<std::tie(R.block,R.lo,R.hi,R.active,R.extra,R.offsets);}
};
static std::optional<int> stackOffset(Value *V,const StackPrefixState &S){
 if(auto *C=dyn_cast<ConstantInt>(V);C&&C->getBitWidth()==64&&S.lo==S.hi){
  APInt D=C->getValue()-S.lo;if(D.isSignedIntN(16))return int(D.getSExtValue());
 }
 auto I=S.offsets.find(V);if(I!=S.offsets.end())return I->second;return std::nullopt;
}
static void stackShift(StackPrefixState &S,int D){
 S.lo+=D;S.hi+=D;
 for(auto I=S.offsets.begin();I!=S.offsets.end();){I->second-=D;if(I->second < -1024||I->second>1024)I=S.offsets.erase(I);else ++I;}
}
static Value *stackIndex(Value *P,AllocaInst *A){
 auto *I64=Type::getInt64Ty(A->getContext());
 if(P==A)return ConstantInt::get(I64,0);
 auto *G=dyn_cast<GetElementPtrInst>(P);if(!G||G->getPointerOperand()!=A)return nullptr;
 if(G->getNumIndices()==1&&A->getModule()->getDataLayout().getTypeAllocSize(G->getSourceElementType())==8){Value *I=G->idx_begin()->get();return I->getType()==I64?I:nullptr;}
 if(G->getNumIndices()==2&&G->getSourceElementType()==A->getAllocatedType()){
  auto I=G->idx_begin();auto *Z=dyn_cast<ConstantInt>(I->get());++I;
  if(Z&&Z->isZero()&&I->get()->getType()==I64)return I->get();
 }
 return nullptr;
}
static bool stackPrefixProof(AllocaInst *A,MemSetInst *Init,unsigned Capacity){
 Function &F=*A->getFunction();DominatorTree DT(F);LoopInfo LI(DT);ReversePostOrderTraversal<Function*> RPO(&F);
 if(containsIrreducibleCFG<BasicBlock*>(RPO,LI))return false;
 unsigned Reads=0; SmallPtrSet<Value*,32> Relevant; SmallVector<Value*,32> Pending;
 for(User *U:A->users()){
  if(U==Init)continue;
  if(auto *L=dyn_cast<LifetimeIntrinsic>(U)){if(!DT.dominates(L,Init)&&L->getIntrinsicID()==Intrinsic::lifetime_start)return false;continue;}
  SmallVector<User*,8> Uses;
  if(auto *G=dyn_cast<GetElementPtrInst>(U)){
   if(G->getPointerOperand()!=A||G->isUsedByMetadata())return false;
   for(User *GU:G->users())Uses.push_back(GU);
  }else Uses.push_back(U);
  for(User *V:Uses){
   if(V==Init)continue;
   if(auto *L=dyn_cast<LoadInst>(V)){
    if(!L->isSimple()||!L->getType()->isIntegerTy(64)||!stackIndex(L->getPointerOperand(),A)||!DT.dominates(Init,L))return false;
    ++Reads; Pending.push_back(stackIndex(L->getPointerOperand(),A));
   }else if(auto *S=dyn_cast<StoreInst>(V)){
    if(!S->isSimple()||!S->getValueOperand()->getType()->isIntegerTy(64)||!stackIndex(S->getPointerOperand(),A)||!DT.dominates(Init,S))return false;
    Pending.push_back(stackIndex(S->getPointerOperand(),A));
   }else return false;
  }
 }
 if(!Reads||Reads>256)return false;
 while(!Pending.empty()){Value *V=Pending.pop_back_val();if(!Relevant.insert(V).second)continue;if(auto *P=dyn_cast<PHINode>(V)){for(Value *X:P->incoming_values())Pending.push_back(X);}else if(auto *O=dyn_cast<BinaryOperator>(V)){Pending.push_back(O->getOperand(0));Pending.push_back(O->getOperand(1));}}
 SmallVector<BasicBlock*,64> Blocks;for(BasicBlock &B:F)Blocks.push_back(&B);
 auto Id=[&](BasicBlock *B){return unsigned(std::find(Blocks.begin(),Blocks.end(),B)-Blocks.begin());};
 std::deque<StackPrefixState> Work;std::set<StackPrefixState> Seen;StackPrefixState Start;Start.block=Id(&F.getEntryBlock());Work.push_back(Start);Seen.insert(Start);
 unsigned Visits=0,Units=0;
 while(!Work.empty()){
  auto S=Work.front();Work.pop_front();if(++Visits>4096)return false;
  BasicBlock *B=Blocks[S.block];bool impossible=false;
  for(Instruction &I:*B){
   if(++Units>2000000)return false;
   if(&I==Init){if(S.active)return false;S.active=true;S.lo=S.hi=0;S.extra=false;S.offsets.clear();S.offsets[ConstantInt::get(Type::getInt64Ty(F.getContext()),0)]=0;continue;}
   if(!S.active)continue;
   if(auto *O=dyn_cast<BinaryOperator>(&I);O&&O->getType()->isIntegerTy(64)&&O->getOpcode()==Instruction::Add){
    auto *C=dyn_cast<ConstantInt>(O->getOperand(1));auto V=stackOffset(O->getOperand(0),S);
    if(!C){C=dyn_cast<ConstantInt>(O->getOperand(0));V=stackOffset(O->getOperand(1),S);}
    S.offsets.erase(O);
    if(C&&C->getValue().isSignedIntN(16)&&V){int D=int(C->getSExtValue()),Off=*V+D;
     if(Relevant.count(O)&&*V==0&&D==-1&&S.lo>0){stackShift(S,-1);S.extra=true;Off=0;}
     else if(Relevant.count(O)&&*V==0&&D==1&&S.extra){S.hi=std::min(S.hi,Capacity-1);if(S.lo>S.hi){impossible=true;break;}stackShift(S,1);S.extra=false;Off=0;}
     if(Relevant.count(O)&&Off>=-1024&&Off<=1024)S.offsets[O]=Off;
    }
   }
   if(auto *L=dyn_cast<LoadInst>(&I);L&&getUnderlyingObject(L->getPointerOperand())==A){
    auto O=stackOffset(stackIndex(L->getPointerOperand(),A),S);
    if(!O||!((*O<0&&unsigned(-*O)<=S.lo)||(*O==0&&S.extra)))return false;
   }
   if(auto *T=dyn_cast<StoreInst>(&I);T&&getUnderlyingObject(T->getPointerOperand())==A){
    auto O=stackOffset(stackIndex(T->getPointerOperand(),A),S);if(!O)return false;
    if(*O==1&&S.extra){S.hi=std::min(S.hi,Capacity-1);if(S.lo>S.hi){impossible=true;break;}stackShift(S,1);S.extra=false;O=0;}
    if(*O==0){S.hi=std::min(S.hi,Capacity-1);if(S.lo>S.hi){impossible=true;break;}S.extra=true;}
    else if(!(*O<0&&unsigned(-*O)<=S.lo))return false;
   }
  }
  if(impossible)continue;
  auto *T=B->getTerminator();auto *CB=dyn_cast<CondBrInst>(T);auto *UB=dyn_cast<UncondBrInst>(T);
  if(!CB&&!UB&&!isa<ReturnInst>(T)&&!isa<UnreachableInst>(T))return false;
  unsigned N=CB?2:UB?1:0;
  for(unsigned K=0;K<N;++K){
   BasicBlock *Dest=CB?CB->getSuccessor(K):UB->getSuccessor(0);auto Next=S;Next.block=Id(Dest);
   if(CB)if(auto *C=dyn_cast<ICmpInst>(CB->getCondition())){
    auto *Zero=dyn_cast<ConstantInt>(C->getOperand(1));auto Off=stackOffset(C->getOperand(0),Next);
    if(Zero&&Zero->isZero()&&Off&&Next.lo==Next.hi&&(C->getPredicate()==ICmpInst::ICMP_EQ||C->getPredicate()==ICmpInst::ICMP_NE)){bool Equal=(int64_t(Next.lo)+*Off)==0;bool Truth=C->getPredicate()==ICmpInst::ICMP_EQ?Equal:!Equal;if(Truth!=(K==0))continue;}
    if(Zero&&Zero->isZero()&&Off&&*Off==0){
     auto P=K==0?C->getPredicate():C->getInversePredicate();
     if(P==ICmpInst::ICMP_EQ)Next.hi=0;
     if(P==ICmpInst::ICMP_NE||P==ICmpInst::ICMP_UGT||P==ICmpInst::ICMP_SGT)Next.lo=std::max(Next.lo,1u);
    }
   }
   if(Next.lo>Next.hi)continue;
   SmallVector<std::pair<PHINode*,std::optional<int>>,8> Phis;
   for(PHINode &P:Dest->phis()){int IX=P.getBasicBlockIndex(B);if(IX<0)return false;Phis.push_back({&P,stackOffset(P.getIncomingValue(IX),Next)});}
   for(auto [P,O]:Phis)Next.offsets.erase(P);
   if(DT.dominates(Dest,B)){
    if (!Next.offsets.count(ConstantInt::get(Type::getInt64Ty(F.getContext()),0))) { Next.lo=Next.lo?1:0;Next.hi=Next.extra?Capacity-1:Capacity; }
    for(auto I=Next.offsets.begin();I!=Next.offsets.end();){auto *Def=dyn_cast<Instruction>(I->first);if(Def&&DT.dominates(Dest,Def->getParent()))I=Next.offsets.erase(I);else ++I;}
   }
   for(auto [P,O]:Phis)if(O&&Relevant.count(P))Next.offsets[P]=*O;
   if(Next.offsets.size()>1024||Seen.size()>4096)return false;
   if(Seen.insert(Next).second)Work.push_back(std::move(Next));
  }
 }
 return true;
}
static unsigned removeStackPrefixZeros(Module &M){
 unsigned Removed=0;
 if (M.getDataLayout().getPointerSizeInBits(0) != 64 ||
     M.getDataLayout().getIndexSizeInBits(0) != 64) return 0;
 for(Function &F:M){if(F.isDeclaration()||F.hasOptNone()||F.hasOptSize()||F.hasMinSize()||F.hasFnAttribute(Attribute::SanitizeAddress)||F.hasFnAttribute(Attribute::SanitizeMemory)||F.hasFnAttribute(Attribute::SanitizeThread)||F.hasFnAttribute(Attribute::SanitizeHWAddress)||F.getInstructionCount()>50000)continue;
 SmallVector<MemSetInst*,8> Inits;
 for(Instruction &I:instructions(F))if(auto *S=dyn_cast<MemSetInst>(&I))Inits.push_back(S);
 if (Inits.size() > 32) continue;
 for(auto *S:Inits){auto *A=dyn_cast<AllocaInst>(getUnderlyingObject(S->getRawDest()));auto *AT=A?dyn_cast<ArrayType>(A->getAllocatedType()):nullptr;
  auto *V=dyn_cast<ConstantInt>(S->getValue());auto *N=dyn_cast<ConstantInt>(S->getLength());
  if(!AT||!AT->getElementType()->isIntegerTy(64)||AT->getNumElements()<8||AT->getNumElements()>512||!A->isStaticAlloca()||!isa<ConstantInt>(A->getArraySize())||!cast<ConstantInt>(A->getArraySize())->isOne()||A->getAddressSpace()!=0||A->isUsedByMetadata()||!V||!V->isZero()||!N||S->isVolatile()||S->hasOperandBundles()||S->hasMetadataOtherThanDebugLoc())continue;
  int64_t Offset=0;auto *Base=GetPointerBaseWithConstantOffset(S->getRawDest(),Offset,M.getDataLayout());
  if(Base!=A||Offset<0||uint64_t(Offset)>AT->getNumElements()*8||N->getLimitedValue()>AT->getNumElements()*8-Offset)continue;
  if(stackPrefixProof(A,S,AT->getNumElements())){S->eraseFromParent();++Removed;}
 }
 }
 return Removed;
}
}
