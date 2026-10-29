//go:build cgo
//  Optional O3 profitability pass. It adds no semantic facts: LLVM owns
//  legality.
#include <llvm/Config/llvm-config.h>
#if LLVM_VERSION_MAJOR == 23
#include <llvm-c/TargetMachine.h>
#include <llvm/ADT/DenseMap.h>
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/Constants.h>
#include <llvm/IR/ConstantRange.h>
#include <llvm/IR/DebugInfo.h>
#include <llvm/IR/DerivedTypes.h>
#include <llvm/IR/InlineAsm.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/Operator.h>
#include <llvm/IR/PassManager.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Target/TargetMachine.h>
#include <llvm/Transforms/Utils/Cloning.h>
using namespace llvm;
namespace {
constexpr unsigned MaxModule = 8192, MaxGraph = 200000, MaxChecks = 64,
                   MaxTrials = 4;
// Bound cloning work by accounting for globals, named metadata, constants,
// operand fan-out, names and embedded byte payloads in addition to IR
// instructions. Unknown or exceptionally large graphs fail closed before
// CloneModule.
struct ModuleBudget {
  size_t nodes = 0, bytes = 0;
  SmallPtrSet<const Constant *, 32> constants;
  SmallPtrSet<const Metadata *, 32> metadata;
  SmallPtrSet<const Type *, 32> types;
  bool charge(size_t n) {
    if (n > 2097152 - bytes)
      return false;
    bytes += n;
    return true;
  }
  bool node() { return ++nodes <= 32768; }
  bool type(const Type *ty, unsigned depth = 0) {
    if (depth > 64) return false;
    if (!ty || !types.insert(ty).second) return true;
    if (!node() || isa<TargetExtType>(ty) || ty->getNumContainedTypes() > 256) return false;
    if (auto *i = dyn_cast<IntegerType>(ty)) if (i->getBitWidth() > 65536) return false;
    if (auto *a = dyn_cast<ArrayType>(ty)) if (a->getNumElements() > 65536) return false;
    if (auto *v = dyn_cast<VectorType>(ty)) if (v->getElementCount().getKnownMinValue() > 65536) return false;
    if (auto *s = dyn_cast<StructType>(ty)) if (s->hasName() && !charge(s->getName().size())) return false;
    for (const Type *child : ty->subtypes()) if (!type(child, depth + 1)) return false;
    return true;
  }
  bool constant(const Constant *c, unsigned depth = 0) {
    if (depth > 64)
      return false;
    if (!c || !constants.insert(c).second)
      return true;
    if (!node() || !type(c->getType()) || c->getNumOperands() > 256)
      return false;
    if (auto *gep = dyn_cast<GEPOperator>(c))
      if (!type(gep->getSourceElementType())) return false;
    if (auto *ci = dyn_cast<ConstantInt>(c))
      if (!charge((size_t(ci->getBitWidth()) + 7) / 8))
        return false;
    if (auto *d = dyn_cast<ConstantDataSequential>(c))
      if (!charge(d->getRawDataValues().size()))
        return false;
    for (const Use &u : c->operands())
      if (auto *child = dyn_cast<Constant>(u.get()))
        if (!constant(child, depth + 1))
          return false;
    return true;
  }
  bool md(const Metadata *m, unsigned depth = 0) {
    if (depth > 64)
      return false;
    if (!m || !metadata.insert(m).second)
      return true;
    if (!node())
      return false;
    if (auto *s = dyn_cast<MDString>(m))
      return charge(s->getString().size());
    if (auto *n = dyn_cast<MDNode>(m)) {
      // Specialized debug metadata can carry private variable-length payloads.
      // Only tuples have completely inspectable operands here.
      if (!isa<MDTuple>(n)) return false;
      if (n->getNumOperands() > 256)
        return false;
      for (auto &op : n->operands())
        if (!md(op.get(), depth + 1))
          return false;
    }
    if (auto *v = dyn_cast<ConstantAsMetadata>(m))
      return constant(v->getValue());
    return true;
  }
};
bool boundedAttrs(AttributeSet attrs, ModuleBudget &b) {
  unsigned count = 0;
  auto chargeRangeEndpoints = [&b](const ConstantRange &range) {
    uint32_t width = range.getBitWidth();
    if (width == 0 || width > 65536)
      return false;
    if (width <= 64)
      return true; // APInt stores these bits inline in the range object.
    size_t endpointBytes = ((size_t(width) + 63) / 64) * sizeof(uint64_t);
    return b.charge(endpointBytes) && b.charge(endpointBytes);
  };
  for (Attribute attr : attrs) {
    if (++count > 256 || !b.node())
      return false;
    if (attr.isTypeAttribute() && !b.type(attr.getValueAsType())) return false;
    if (attr.isStringAttribute() && (!b.charge(attr.getKindAsString().size()) ||
                                     !b.charge(attr.getValueAsString().size())))
      return false;
    if (attr.isConstantRangeAttribute()) {
      if (!b.charge(sizeof(ConstantRange)) ||
          !chargeRangeEndpoints(attr.getValueAsConstantRange()))
        return false;
    }
    if (attr.isConstantRangeListAttribute()) {
      auto ranges = attr.getValueAsConstantRangeList();
      if (ranges.size() > 256)
        return false;
      if (ranges.size() > 2097152 / sizeof(ConstantRange) ||
          !b.charge(ranges.size() * sizeof(ConstantRange)))
        return false;
      for (const ConstantRange &range : ranges)
        if (!chargeRangeEndpoints(range))
          return false;
    }
  }
  return true;
}
bool boundedModule(const Module &M) {
  ModuleBudget b;
  unsigned globals = 0, functions = 0;
  if (!b.charge(M.getModuleIdentifier().size()) ||
      !b.charge(M.getSourceFileName().size()) ||
      !b.charge(M.getDataLayoutStr().size()) ||
      !b.charge(M.getTargetTriple().str().size()) ||
      !b.charge(M.getModuleInlineAsm().size()))
    return false;
  if (!M.alias_empty() || !M.ifunc_empty())
    return false;
  for (const GlobalVariable &g : M.globals()) {
    if (++globals > 1024 || !b.charge(g.getName().size()) || !b.charge(g.getPartition().size()) || !b.node() ||
        g.hasSection() || g.hasComdat())
      return false;
    if (!b.type(g.getValueType())) return false;
    if (g.hasInitializer() && !b.constant(g.getInitializer()))
      return false;
    SmallVector<std::pair<unsigned, MDNode *>, 4> md;
    g.getAllMetadata(md);
    for (auto &e : md)
      if (!b.md(e.second))
        return false;
  }
  for (const Function &f : M) {
    if (++functions > 1024 || !b.charge(f.getName().size()) || !b.charge(f.getPartition().size()) ||
        f.arg_size() > 256 || !b.node() || f.hasSection() || f.hasGC() ||
        f.hasComdat() || f.hasPersonalityFn() || f.hasPrefixData() ||
        f.hasPrologueData())
      return false;
    if (!b.type(f.getFunctionType())) return false;
    SmallVector<std::pair<unsigned, MDNode *>, 4> functionMD;
    f.getAllMetadata(functionMD);
    for (auto &e : functionMD) if (!b.md(e.second)) return false;
    for (const Argument &arg : f.args()) if (!b.charge(arg.getName().size()) || !b.node()) return false;
    AttributeList attrs = f.getAttributes();
    if (!boundedAttrs(attrs.getFnAttrs(), b) ||
        !boundedAttrs(attrs.getRetAttrs(), b))
      return false;
    for (unsigned a = 0; a < f.arg_size(); ++a)
      if (!boundedAttrs(attrs.getParamAttrs(a), b))
        return false;
    for (const BasicBlock &block : f) {
      if (!b.charge(block.getName().size()))
        return false;
      for (const Instruction &i : block) {
        if (!b.node() || !b.charge(i.getName().size()) || !b.type(i.getType()) ||
            i.getNumOperands() > 256)
          return false;
        if (auto *shuffle = dyn_cast<ShuffleVectorInst>(&i)) {
          // Shuffle masks live outside the ordinary operand list. getShuffleMask
          // returns a borrowed view, avoiding allocation before the size check.
          size_t count = shuffle->getShuffleMask().size();
          if (count > 2097152 / sizeof(int) || !b.charge(count * sizeof(int))) return false;
          if (!b.constant(shuffle->getShuffleMaskForBitcode())) return false;
        }
        if (auto *alloca = dyn_cast<AllocaInst>(&i)) if (!b.type(alloca->getAllocatedType())) return false;
        if (auto *gep = dyn_cast<GetElementPtrInst>(&i)) if (!b.type(gep->getSourceElementType())) return false;
        if (auto *call = dyn_cast<CallBase>(&i)) {
          if (!b.type(call->getFunctionType()) || call->getNumOperandBundles() > 256) return false;
          for (unsigned k=0; k<call->getNumOperandBundles(); ++k) if (!b.charge(call->getOperandBundleAt(k).getTagName().size())) return false;
          AttributeList callAttrs=call->getAttributes();
          if(!boundedAttrs(callAttrs.getFnAttrs(),b) || !boundedAttrs(callAttrs.getRetAttrs(),b)) return false;
          for(unsigned a=0;a<call->arg_size();++a) if(!boundedAttrs(callAttrs.getParamAttrs(a),b)) return false;
        }
        for (const Use &u : i.operands()) {
          if (!b.type(u->getType())) return false;
          if (isa<MetadataAsValue>(u.get()) || isa<InlineAsm>(u.get()))
            return false;
          if (auto *c = dyn_cast<Constant>(u.get()))
            if (!b.constant(c))
              return false;
        }
        SmallVector<std::pair<unsigned, MDNode *>, 4> md;
        i.getAllMetadata(md);
        for (auto &e : md)
          if (!b.md(e.second))
            return false;
      }
    }
  }
  for (const NamedMDNode &named : M.named_metadata()) {
    if (!b.node() || !b.charge(named.getName().size()) || named.getNumOperands() > 256)
      return false;
    for (const MDNode *n : named.operands())
      if (!b.md(n))
        return false;
  }
  return true;
}
unsigned countInst(const Function &F) {
  unsigned n = 0;
  for (auto &B : F)
    for (auto &I : B) {
      (void)I;
      if (++n > 16384)
        return n;
    }
  return n;
}
const GlobalVariable *tableRoot(const Value *V) {
  for (unsigned depth = 0; depth < 8; ++depth) {
    if (auto *G = dyn_cast<GlobalVariable>(V))
      return G->hasLocalLinkage() && G->isConstant() && !G->isDeclaration() &&
                     !G->isExternallyInitialized()
                 ? G
                 : nullptr;
    auto *G = dyn_cast<GetElementPtrInst>(V);
    if (!G)
      return nullptr;
    V = G->getPointerOperand();
  }
  return nullptr;
}
bool depends(const Value *V, const Value *counter, unsigned depth,
             unsigned &work) {
  if (!work)
    return false;
  --work;
  if (V == counter)
    return true;
  if (depth >= 12)
    return false;
  auto *I = dyn_cast<Instruction>(V);
  if (!I || I->getNumOperands() > 4)
    return false;
  for (const Use &U : I->operands())
    if (depends(U.get(), counter, depth + 1, work))
      return true;
  return false;
}
bool eligible(Function &F) {
  if (F.isDeclaration() || F.getSubprogram() ||
      F.hasFnAttribute(Attribute::OptimizeNone))
    return false;
  unsigned n = 0, loads = 0;
  for (auto &B : F)
    for (auto &I : B) {
      if (++n > 8192 || I.getDebugLoc() || I.hasDbgRecords() ||
          isa<DbgInfoIntrinsic>(I) || isa<FenceInst>(I) ||
          isa<AtomicRMWInst>(I) || isa<AtomicCmpXchgInst>(I))
        return false;
      if (auto *L = dyn_cast<LoadInst>(&I)) {
        if (!L->isSimple())
          return false;
        if (tableRoot(L->getPointerOperand()))
          ++loads;
      }
      if (auto *S = dyn_cast<StoreInst>(&I))
        if (!S->isSimple())
          return false;
      SmallVector<std::pair<unsigned, MDNode *>, 4> MD;
      I.getAllMetadata(MD);
      for (auto &entry : MD) {
        auto *L = dyn_cast<LoadInst>(&I);
        // Stage0's certified table-range pass attaches !range. Preserve it and
        // reject every other metadata kind, including loop and performance
        // hints.
        if (entry.first != LLVMContext::MD_range || !L ||
            !tableRoot(L->getPointerOperand()))
          return false;
      }
    }
  return loads >= 4;
}
bool acyclic(Function &F, BasicBlock *latch, BasicBlock *header, bool ignore,
             unsigned &work) {
  SmallVector<BasicBlock *, 256> blocks;
  DenseMap<BasicBlock *, unsigned> index;
  for (auto &B : F) {
    if (!work || blocks.size() >= 256)
      return false;
    --work;
    index[&B] = blocks.size();
    blocks.push_back(&B);
  }
  SmallVector<unsigned, 256> degree(blocks.size(), 0);
  SmallVector<bool, 256> removed(blocks.size(), false);
  for (auto *B : blocks) {
    auto *T = B->getTerminator();
    if (!T || T->getNumSuccessors() > 2)
      return false;
    for (unsigned j = 0; j < T->getNumSuccessors(); ++j) {
      if (!work)
        return false;
      --work;
      auto *D = T->getSuccessor(j);
      if (ignore && B == latch && D == header)
        continue;
      auto it = index.find(D);
      if (it == index.end())
        return false;
      ++degree[it->second];
    }
  }
  size_t remain = blocks.size();
  for (size_t round = 0; round < blocks.size(); ++round) {
    bool progress = false;
    for (size_t i = 0; i < blocks.size(); ++i) {
      if (!work)
        return false;
      --work;
      if (removed[i] || degree[i])
        continue;
      progress = true;
      removed[i] = true;
      --remain;
      auto *T = blocks[i]->getTerminator();
      for (unsigned j = 0; j < T->getNumSuccessors(); ++j) {
        if (!work)
          return false;
        --work;
        auto *D = T->getSuccessor(j);
        if (ignore && blocks[i] == latch && D == header)
          continue;
        unsigned k = index.lookup(D);
        if (!degree[k])
          return false;
        --degree[k];
      }
    }
    if (!remain)
      return true;
    if (!progress)
      return false;
  }
  return remain == 0;
}
unsigned growth(BasicBlock *header, BasicBlock *latch, BranchInst *branch) {
  if (!branch->isConditional() || branch->getSuccessor(0) == header ||
      branch->getSuccessor(1) != header)
    return 0;
  auto *cmp = dyn_cast<ICmpInst>(branch->getCondition());
  if (!cmp || cmp->getPredicate() != ICmpInst::ICMP_EQ)
    return 0;
  auto *next = dyn_cast<BinaryOperator>(cmp->getOperand(0));
  auto *trip = dyn_cast<ConstantInt>(cmp->getOperand(1));
  if (!next || next->getOpcode() != Instruction::Add || !trip ||
      trip->getBitWidth() > 64 || trip->getZExtValue() < 2 ||
      trip->getZExtValue() > 16 || next->getParent() != latch)
    return 0;
  auto *counter = dyn_cast<PHINode>(next->getOperand(0));
  auto *step = dyn_cast<ConstantInt>(next->getOperand(1));
  if (!counter || counter->getParent() != header ||
      counter->getNumIncomingValues() != 2 || !step || !step->isOne())
    return 0;
  int idx = counter->getBasicBlockIndex(latch);
  if (idx < 0 || counter->getIncomingValue(idx) != next)
    return 0;
  auto *start = dyn_cast<ConstantInt>(counter->getIncomingValue(1 - idx));
  if (!start || !start->isZero())
    return 0;
  unsigned instructions = 0, loads = 0;
  BasicBlock *block = header;
  for (unsigned path = 0; path < 128; ++path) {
    for (auto &I : *block) {
      if (++instructions > 400)
        return 0;
      if (auto *C = dyn_cast<CallBase>(&I)) {
        auto *Fn = C->getCalledFunction();
        if (!Fn || Fn->getIntrinsicID() != Intrinsic::fshl ||
            Fn->getName() != "llvm.fshl.i64")
          return 0;
      }
      if (auto *L = dyn_cast<LoadInst>(&I)) {
        unsigned w = 256;
        if (L->isSimple() && tableRoot(L->getPointerOperand()) &&
            depends(L->getPointerOperand(), counter, 0, w))
          ++loads;
      }
    }
    if (block == latch) {
      auto n = instructions * trip->getZExtValue();
      return loads >= 4 && n <= 5000 ? unsigned(n) : 0;
    }
    auto *br = dyn_cast<BranchInst>(block->getTerminator());
    if (!br)
      return 0;
    if (br->isUnconditional())
      block = br->getSuccessor(0);
    else if (auto *cond = dyn_cast<ConstantInt>(br->getCondition()))
      block = br->getSuccessor(cond->isZero() ? 1 : 0);
    else
      return 0;
  }
  return 0;
}
void tag(BranchInst *B) {
  LLVMContext &C = B->getContext();
  Metadata *hint[] = {MDString::get(C, "llvm.loop.unroll.full")};
  MDNode *H = MDNode::get(C, hint);
  Metadata *values[] = {nullptr, H};
  MDNode *loop = MDNode::getDistinct(C, values);
  loop->replaceOperandWith(0, loop);
  B->setMetadata(LLVMContext::MD_loop, loop);
}
bool run(Function &F, TargetMachine *TM) {
  LoopAnalysisManager LAM;
  FunctionAnalysisManager FAM;
  CGSCCAnalysisManager CGAM;
  ModuleAnalysisManager MAM;
  PassBuilder PB(TM);
  PB.registerModuleAnalyses(MAM);
  PB.registerCGSCCAnalyses(CGAM);
  PB.registerFunctionAnalyses(FAM);
  PB.registerLoopAnalyses(LAM);
  PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
  FunctionPassManager FPM;
  if (auto err = PB.parsePassPipeline(
          FPM, "simplifycfg,loop-unroll,instcombine,simplifycfg")) {
    consumeError(std::move(err));
    return false;
  }
  FPM.run(F, FAM);
  return true;
}
} // namespace
extern "C" int elisacoreTableUnrollSupported() { return 1; }
extern "C" int elisacoreUnrollConstantTableLoops(LLVMModuleRef Ref,
                                                 LLVMTargetMachineRef Target) {
  auto *TM = reinterpret_cast<TargetMachine *>(Target);
  if (!Ref)
    return 0;
  Module &M = *unwrap(Ref);
  if (!boundedModule(M))
    return 0;
  if (M.getFunction("llvm.dbg.value") || M.getFunction("llvm.dbg.declare") ||
      M.getFunction("llvm.dbg.assign"))
    return 0;
  unsigned total = 0;
  for (auto &F : M) {
    total += countInst(F);
    if (total > MaxModule)
      return 0;
  }
  unsigned checks = 0, attempts = 0, graph = MaxGraph, growthTotal = 0,
           changed = 0;
  for (auto &F : M) {
    if (!eligible(F))
      continue;
    for (auto &B : F) {
      auto *br = dyn_cast_or_null<BranchInst>(B.getTerminator());
      if (!br || !br->isConditional())
        continue;
      if (checks++ >= MaxChecks)
        return changed ? 1 : 0;
      BasicBlock *header = br->getSuccessor(1);
      unsigned estimate = growth(header, &B, br);
      if (!estimate || attempts >= MaxTrials || growthTotal + estimate > 8192 ||
          !acyclic(F, &B, header, true, graph))
        continue;
      ++attempts;
      ValueToValueMapTy map;
      auto clone = CloneModule(M, map);
      auto *trial = dyn_cast_or_null<Function>(map.lookup(&F));
      auto *trialBr = dyn_cast_or_null<BranchInst>(map.lookup(br));
      if (!trial || !trialBr)
        continue;
      unsigned before = countInst(F);
      tag(trialBr);
      if (!run(*trial, TM))
        continue;
      unsigned trialGraph = MaxGraph;
      auto *first = &trial->getEntryBlock();
      if (!acyclic(*trial, first, first, false, trialGraph) ||
          countInst(*trial) > before + estimate ||
          countInst(*trial) > before + 8192 - growthTotal)
        continue;
      tag(br);
      if (!run(F, TM))
        return 2;
      unsigned actualGraph = MaxGraph;
      first = &F.getEntryBlock();
      if (!acyclic(F, first, first, false, actualGraph) ||
          countInst(F) > before + estimate ||
          countInst(F) > before + 8192 - growthTotal)
        return 2;
      ++changed;
      growthTotal += estimate;
      break;
    }
  }
  return changed ? 1 : 0;
}
#else
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
extern "C" int elisacoreTableUnrollSupported() { return 0; }
extern "C" int elisacoreUnrollConstantTableLoops(LLVMModuleRef,
                                                 LLVMTargetMachineRef) {
  return 0;
}
#endif
