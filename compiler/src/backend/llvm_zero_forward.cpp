//go:build cgo

// Forward provably zero loads from private stack storage after LLVM optimization.
#include "llvm/Analysis/CaptureTracking.h"
#include "llvm/IR/DebugInfo.h"
#include "llvm/IR/Instruction.h"
#include "llvm/IR/Instructions.h"
#include "llvm/IR/IntrinsicInst.h"
#include "llvm/IR/LLVMContext.h"
#include "llvm/IR/Module.h"
#include "llvm/IR/Operator.h"
#include "llvm/IR/Verifier.h"
#include "llvm/Support/CBindingWrapping.h"
#include "llvm/Support/SourceMgr.h"
#include "llvm/Support/raw_ostream.h"
#include <cstdint>
#include <chrono>
#include <cstdlib>
#include <limits>
#include <optional>
#include <unordered_map>

using namespace llvm;

struct Location {
  AllocaInst *base;
  uint64_t offset;
};

// Exhaustion is a miss: correctness never depends on completing analysis.
struct FunctionBudget {
  uint64_t scanSteps = 32768;
  unsigned captureQueries = 32;
};

struct ModuleBudget {
  uint64_t scanSteps = 1048576;
  unsigned captureQueries = 1024;
};

// Deliberately narrow: an alloca plus inbounds byte GEPs with nonnegative,
// individually bounded constant offsets. No casts, variable indices, negative
// indices or arithmetic whose pointer-width wrap could hide poison.
static std::optional<Location> location(Value *pointer, FunctionBudget &budget,
                                        ModuleBudget &moduleBudget) {
  uint64_t offset = 0;
  unsigned traversed = 0;
  while (auto *gep = dyn_cast<GEPOperator>(pointer)) {
    if (++traversed > 1024 || budget.scanSteps == 0 || moduleBudget.scanSteps == 0)
      return std::nullopt;
    --budget.scanSteps;
    --moduleBudget.scanSteps;
    if (!gep->isInBounds() || !gep->getSourceElementType()->isIntegerTy(8) ||
        gep->getNumIndices() != 1)
      return std::nullopt;
    auto *index = dyn_cast<ConstantInt>(gep->getOperand(1));
    if (!index || index->isNegative() ||
        index->getValue().getActiveBits() > 63)
      return std::nullopt;
    uint64_t part = index->getZExtValue();
    if (part > uint64_t(std::numeric_limits<int64_t>::max()) - offset)
      return std::nullopt;
    offset += part;
    pointer = gep->getPointerOperand();
  }
  auto *alloca = dyn_cast<AllocaInst>(pointer);
  if (!alloca || !alloca->isStaticAlloca() || alloca->getAddressSpace() != 0 ||
      alloca->isUsedWithInAlloca() || alloca->isSwiftError())
    return std::nullopt;
  return Location{alloca, offset};
}

static std::optional<uint64_t> objectSize(AllocaInst *alloca,
                                          const DataLayout &layout) {
  auto size = alloca->getAllocationSize(layout);
  if (!size || size->isScalable())
    return std::nullopt;
  auto bytes = size->getFixedValue();
  // Inbounds GEP offsets are signed in the target's pointer index width.
  // Limit the whole object so even a wide source index cannot silently wrap
  // when LLVM truncates it for a narrower target address space.
  unsigned bits = layout.getIndexSizeInBits(alloca->getAddressSpace());
  if (bits < 2 || bits > 64)
    return std::nullopt;
  uint64_t maxOffset = bits == 64 ? uint64_t(std::numeric_limits<int64_t>::max())
                                  : (uint64_t(1) << (bits - 1)) - 1;
  if (bytes == 0 || bytes > maxOffset)
    return std::nullopt;
  return bytes;
}

static bool within(uint64_t offset, uint64_t length, uint64_t size) {
  return offset <= size && length <= size - offset;
}

static bool hasDebug(const Function &function) {
  if (function.getSubprogram())
    return true;
  for (const auto &block : function)
    for (const auto &inst : block)
      if (inst.getDebugLoc() || inst.hasDbgRecords() ||
          isa<DbgInfoIntrinsic>(inst))
        return true;
  return false;
}

static bool canForward(LoadInst &load, const DataLayout &layout,
                       std::unordered_map<AllocaInst *, bool> &captured,
                       FunctionBudget &budget, ModuleBudget &moduleBudget) {
  auto *type = dyn_cast<IntegerType>(load.getType());
  if (!type || (type->getBitWidth() != 8 && type->getBitWidth() != 16 &&
                type->getBitWidth() != 32 && type->getBitWidth() != 64) ||
      load.isVolatile() || load.isAtomic() || load.getDebugLoc() ||
      load.hasMetadataOtherThanDebugLoc() || load.hasDbgRecords())
    return false;
  auto loc = location(load.getPointerOperand(), budget, moduleBudget);
  if (!loc)
    return false;
  auto size = objectSize(loc->base, layout);
  uint64_t length = type->getBitWidth() / 8;
  if (!size || !within(loc->offset, length, *size) ||
      load.getAlign().value() > loc->base->getAlign().value() ||
      loc->offset % load.getAlign().value() != 0)
    return false;

  auto cursor = load.getIterator();
  auto &block = *load.getParent();
  unsigned scanned = 0;
  while (cursor != block.begin()) {
    if (++scanned > 1024 || budget.scanSteps == 0 || moduleBudget.scanSteps == 0)
      return false;
    --budget.scanSteps;
    --moduleBudget.scanSteps;
    Instruction &inst = *--cursor;
    if (auto *memset = dyn_cast<MemSetInst>(&inst)) {
      auto *fill = dyn_cast<ConstantInt>(memset->getValue());
      auto *count = dyn_cast<ConstantInt>(memset->getLength());
      auto dest = location(memset->getDest(), budget, moduleBudget);
      if (memset->isVolatile() || memset->hasMetadataOtherThanDebugLoc() ||
          !fill || !fill->isZero() || !count ||
          count->getValue().getActiveBits() > 63 || !dest ||
          dest->base != loc->base)
        return false;
      uint64_t bytes = count->getZExtValue();
      if (!within(dest->offset, bytes, *size))
        return false;
      if (loc->offset < dest->offset ||
          !within(loc->offset - dest->offset, length, bytes))
        return false;
      auto entry = captured.find(loc->base);
      if (entry == captured.end()) {
        if (budget.captureQueries == 0 || moduleBudget.captureQueries == 0)
          return false;
        --budget.captureQueries;
        --moduleBudget.captureQueries;
        entry = captured.emplace(loc->base,
                                 PointerMayBeCaptured(loc->base, true, 4096)).first;
      }
      return !entry->second;
    }
    if (auto *store = dyn_cast<StoreInst>(&inst)) {
      if (store->isVolatile() || store->isAtomic())
        return false;
      auto dest = location(store->getPointerOperand(), budget, moduleBudget);
      if (!dest || dest->base != loc->base)
        return false;
      TypeSize storeSize = layout.getTypeStoreSize(store->getValueOperand()->getType());
      if (storeSize.isScalable())
        return false;
      uint64_t bytes = storeSize.getFixedValue();
      if (!within(dest->offset, bytes, *size))
        return false;
      if (dest->offset < loc->offset + length &&
          loc->offset < dest->offset + bytes)
        return false;
      continue;
    }
    if (auto *priorLoad = dyn_cast<LoadInst>(&inst)) {
      if (priorLoad->isVolatile() || priorLoad->isAtomic())
        return false;
      continue;
    }
    if (isa<CallBase>(inst) || isa<FenceInst>(inst) ||
        isa<AtomicRMWInst>(inst) || isa<AtomicCmpXchgInst>(inst) ||
        inst.mayWriteToMemory())
      return false;
  }
  return false;
}

extern "C" unsigned elisacoreForwardZeroedLoads(LLVMModuleRef moduleRef) {
  if (!moduleRef) return 0;
  Module *module = unwrap(moduleRef);
  if (module->getDataLayoutStr().empty()) return 0;
  unsigned count = 0;
  ModuleBudget moduleBudget;
  const DataLayout &layout = module->getDataLayout();
  for (Function &function : *module) {
    if (function.isDeclaration() || hasDebug(function)) continue;
    std::unordered_map<AllocaInst *, bool> captured;
    FunctionBudget budget;
    for (BasicBlock &block : function) {
      bool seenZeroMemset = false;
      for (auto it = block.begin(), end = block.end(); it != end;) {
        Instruction &inst = *it++;
        if (auto *memset = dyn_cast<MemSetInst>(&inst)) {
          auto *fill = dyn_cast<ConstantInt>(memset->getValue());
          if (!memset->isVolatile() && fill && fill->isZero()) seenZeroMemset = true;
        }
        auto *load = dyn_cast<LoadInst>(&inst);
        if (!seenZeroMemset || budget.scanSteps == 0 || moduleBudget.scanSteps == 0 ||
            !load || !canForward(*load, layout, captured, budget, moduleBudget)) continue;
        load->replaceAllUsesWith(ConstantInt::get(load->getType(), 0));
        load->eraseFromParent();
        ++count;
      }
    }
  }
  return count;
}
