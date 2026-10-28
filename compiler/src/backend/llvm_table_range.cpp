//go:build cgo

// Infer ranges only for direct element loads from local immutable integer arrays.
#include <llvm/IR/Constants.h>
#include <llvm/IR/DebugInfo.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/PassManager.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/IR/Operator.h>
#include <llvm/Support/CBindingWrapping.h>
#include <optional>
#include <unordered_map>

using namespace llvm;
namespace {
struct Bounds { APInt low, high; };

std::optional<Bounds> arrayBounds(GlobalVariable *global, const DataLayout &layout,
                                  unsigned &remaining) {
  if (!global->hasLocalLinkage() || !global->isConstant() ||
      global->isExternallyInitialized() || !global->hasInitializer()) return std::nullopt;
  auto *array = dyn_cast<ArrayType>(global->getValueType());
  if (!array || !array->getElementType()->isIntegerTy()) return std::nullopt;
  auto *element = cast<IntegerType>(array->getElementType());
  unsigned bits = element->getBitWidth();
  // Equal allocation/store/bit size rules out element padding and partial reads.
  if (bits == 0 || bits > 64 || bits % 8 != 0 ||
      layout.getTypeAllocSize(element).isScalable() ||
      layout.getTypeStoreSize(element).isScalable() ||
      layout.getTypeAllocSize(element).getFixedValue() != bits / 8 ||
      layout.getTypeStoreSize(element).getFixedValue() != bits / 8)
    return std::nullopt;
  uint64_t count = array->getNumElements();
  if (!count || count > 4096 || count > remaining) return std::nullopt;
  remaining -= unsigned(count);
  Constant *initializer = global->getInitializer();
  APInt low = APInt::getMaxValue(bits), high(bits, 0);
  for (unsigned i = 0; i < count; ++i) {
    auto *value = dyn_cast_or_null<ConstantInt>(initializer->getAggregateElement(i));
    if (!value || value->getBitWidth() != bits) return std::nullopt;
    const APInt &v = value->getValue();
    if (v.ult(low)) low = v;
    if (v.ugt(high)) high = v;
  }
  // A non-wrapping half-open interval avoids both full-set and wrap ambiguity.
  if (high.isMaxValue()) return std::nullopt;
  return Bounds{low, high + 1};
}

// Follow only inbounds GEPs whose byte displacement is always a multiple of
// the element size. All paths and scan work have hard, fail-closed limits.
GlobalVariable *tableBase(Value *pointer, unsigned elementBytes, unsigned &work) {
  if (elementBytes == 0 || (elementBytes & (elementBytes - 1))) return nullptr;
  unsigned depth = 0;
  while (auto *gep = dyn_cast<GEPOperator>(pointer)) {
    if (++depth > 32 || work == 0 || !gep->isInBounds()) return nullptr;
    --work;
    Type *source = gep->getSourceElementType();
    if (gep->getNumIndices() == 1) {
      if (auto *array = dyn_cast<ArrayType>(source)) {
        if (!array->getElementType()->isIntegerTy(8) || array->getNumElements() != elementBytes)
          return nullptr;
      } else if (source->isIntegerTy(8)) {
        auto *index = dyn_cast<ConstantInt>(gep->getOperand(1));
        if (!index || index->getValue().urem(elementBytes) != 0) return nullptr;
      } else return nullptr;
    } else if (gep->getNumIndices() == 2) {
      auto *array = dyn_cast<ArrayType>(source);
      auto *zero = dyn_cast<ConstantInt>(gep->getOperand(1));
      if (!array || !zero || !zero->isZero() ||
          !array->getElementType()->isIntegerTy(elementBytes * 8)) return nullptr;
      auto *global = dyn_cast<GlobalVariable>(gep->getPointerOperand());
      if (!global || global->getValueType() != source) return nullptr;
    } else return nullptr;
    pointer = gep->getPointerOperand();
  }
  auto *global = dyn_cast<GlobalVariable>(pointer);
  if (!global) return nullptr;
  auto *array = dyn_cast<ArrayType>(global->getValueType());
  return array && array->getElementType()->isIntegerTy(elementBytes * 8) ? global : nullptr;
}

// Inspect debug info, loop markers and function size in a single bounded scan.
// Budget exhaustion never authorizes a partial function transformation.
bool inspectFunction(const Function &function, unsigned &moduleWork,
                     unsigned maxInstructions, unsigned &instructions) {
  if (function.getSubprogram()) return false;
  instructions = 0;
  for (const BasicBlock &block : function) {
    for (const Instruction &inst : block) {
      if (!moduleWork || instructions == maxInstructions) return false;
      --moduleWork;
      ++instructions;
      if (inst.getDebugLoc() || inst.hasDbgRecords() || isa<DbgInfoIntrinsic>(inst) ||
          (inst.isTerminator() && inst.getMetadata(LLVMContext::MD_loop)))
        return false;
    }
  }
  // The second traversal that discovers loads also consumes the module budget.
  if (instructions > moduleWork) return false;
  moduleWork -= instructions;
  return true;
}
}

extern "C" unsigned elisacoreInferConstantTableRanges(LLVMModuleRef moduleRef) {
  if (!moduleRef) return 0;
  Module *module = unwrap(moduleRef);
  if (module->getDataLayoutStr().empty()) return 0;
  const DataLayout &layout = module->getDataLayout();
  std::unordered_map<GlobalVariable *, std::optional<Bounds>> bounds;
  unsigned remaining = 65536, changed = 0, globalQueries = 1024, gepWork = 65536;
  SmallVector<Function *, 8> changedFunctions;
  unsigned cleanupWork = 100000, cleanupFunctions = 32;
  unsigned discoveryWork = 262144, functionWork = 4096;
  for (Function &function : *module) {
    if (!functionWork-- || !discoveryWork || !cleanupFunctions) break;
    if (function.isDeclaration()) continue;
    unsigned instructions = 0;
    if (!inspectFunction(function, discoveryWork, 32768, instructions) ||
        instructions > cleanupWork) continue;
    bool changedFunction = false;
    for (BasicBlock &block : function) for (Instruction &instruction : block) {
      auto *load = dyn_cast<LoadInst>(&instruction);
      if (!load || load->isVolatile() || load->isAtomic() || load->getMetadata(LLVMContext::MD_range)) continue;
      auto *type = dyn_cast<IntegerType>(load->getType());
      if (!type || type->getBitWidth() == 0 || type->getBitWidth() > 64 ||
          type->getBitWidth() % 8 != 0) continue;
      auto *global = tableBase(load->getPointerOperand(), type->getBitWidth() / 8, gepWork);
      if (!global) continue;
      auto entry = bounds.find(global);
      if (entry == bounds.end()) {
        if (!globalQueries) continue;
        --globalQueries;
        entry = bounds.emplace(global, arrayBounds(global, layout, remaining)).first;
      }
      if (!entry->second) continue;
      auto &range = *entry->second;
      Metadata *metadata[] = {
          ConstantAsMetadata::get(ConstantInt::get(module->getContext(), range.low)),
          ConstantAsMetadata::get(ConstantInt::get(module->getContext(), range.high))};
      load->setMetadata(LLVMContext::MD_range, MDNode::get(module->getContext(), metadata));
      ++changed;
      changedFunction = true;
    }
    if (changedFunction) {
      cleanupWork -= instructions;
      --cleanupFunctions;
      changedFunctions.push_back(&function);
    }
  }
  if (!changedFunctions.empty()) {
    LoopAnalysisManager LAM;
    FunctionAnalysisManager FAM;
    CGSCCAnalysisManager CGAM;
    ModuleAnalysisManager MAM;
    PassBuilder PB;
    PB.registerModuleAnalyses(MAM);
    PB.registerCGSCCAnalyses(CGAM);
    PB.registerFunctionAnalyses(FAM);
    PB.registerLoopAnalyses(LAM);
    PB.crossRegisterProxies(LAM, FAM, CGAM, MAM);
    FunctionPassManager FPM;
    if (auto error = PB.parsePassPipeline(FPM, "instcombine,simplifycfg")) {
      consumeError(std::move(error));
      return changed;
    }
    for (Function *function : changedFunctions) FPM.run(*function, FAM);
  }
  return changed;
}
