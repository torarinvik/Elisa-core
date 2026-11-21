#pragma once

#include <llvm/ADT/SmallBitVector.h>
#include <llvm/Analysis/CFG.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/InstIterator.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm/IR/Module.h>
#include <llvm/Passes/PassBuilder.h>
namespace ElisaCopiedScratch {
using namespace llvm;

static std::optional<uint64_t> evaluate(const SCEV *expr, ScalarEvolution &se,
                                        Value &argument, uint64_t value,
                                        const Loop *loop, const SCEV *iteration) {
  ValueToSCEVMapTy bindings;
  bindings[&argument] = se.getConstant(argument.getType(), value);
  if (auto *recurrence = dyn_cast<SCEVAddRecExpr>(expr)) {
    if (recurrence->getLoop() != loop || !iteration) return std::nullopt;
    expr = recurrence->evaluateAtIteration(iteration, se);
  }
  auto *constant = dyn_cast<SCEVConstant>(SCEVParameterRewriter::rewrite(expr, se, bindings));
  if (!constant || constant->getAPInt().getActiveBits() > 64) return std::nullopt;
  return constant->getAPInt().getZExtValue();
}

static std::optional<bool> condition(Value *value, ScalarEvolution &se,
                                      Value &argument, uint64_t input) {
  auto *comparison = dyn_cast<ICmpInst>(value);
  if (!comparison) return std::nullopt;
  auto scalar = [&](Value *operand) -> std::optional<uint64_t> {
    if (auto *binary = dyn_cast<BinaryOperator>(operand);
        binary && binary->getOpcode() == Instruction::And) {
      auto left = evaluate(se.getSCEV(binary->getOperand(0)), se, argument, input, nullptr, nullptr);
      auto right = evaluate(se.getSCEV(binary->getOperand(1)), se, argument, input, nullptr, nullptr);
      if (left && right) return *left & *right;
    }
    return evaluate(se.getSCEV(operand), se, argument, input, nullptr, nullptr);
  };
  auto left = scalar(comparison->getOperand(0)), right = scalar(comparison->getOperand(1));
  if (!left || !right) return std::nullopt;
  unsigned bits = comparison->getOperand(0)->getType()->getIntegerBitWidth();
  return ICmpInst::compare(APInt(bits, *left), APInt(bits, *right), comparison->getPredicate());
}

// Evaluate the candidate without changing IR until coverage is established.
static unsigned eliminateInFunction(Function &function) {
  Module &module = *function.getParent();
  SmallPtrSet<MemSetInst *, 4> removable;
  PassBuilder builder;
  FunctionAnalysisManager analyses;
  builder.registerFunctionAnalyses(analyses);
  for (Instruction &instruction : instructions(function)) {
    auto *copy = dyn_cast<MemCpyInst>(&instruction);
    if (!copy || copy->isVolatile() || copy->hasOperandBundles() ||
        copy->hasMetadataOtherThanDebugLoc())
      continue;
    auto *buffer = dyn_cast<AllocaInst>(copy->getRawSource());
    if (!buffer || !buffer->isStaticAlloca() || buffer->getAddressSpace() != 0 ||
        buffer->isUsedByMetadata() || !copy->getLength()->getType()->isIntegerTy(64))
      continue;
    auto &loops = analyses.getResult<LoopAnalysis>(function);
    auto &dom = analyses.getResult<DominatorTreeAnalysis>(function);
    auto &evolution = analyses.getResult<ScalarEvolutionAnalysis>(function);
    SmallVector<Value *, 16> pointers{buffer};
    SmallPtrSet<Value *, 16> visited;
    SmallVector<StoreInst *, 8> allStores;
    SmallVector<LifetimeIntrinsic *, 4> lifetimes;
    MemSetInst *initializer = nullptr;
    bool supported = true;
    while (!pointers.empty() && supported) {
      auto *pointer = pointers.pop_back_val();
      if (!visited.insert(pointer).second) continue;
      if (pointer->isUsedByMetadata() || visited.size() > 32) {
        supported = false;
        break;
      }
      for (User *user : pointer->users()) {
        if (user == copy) {
          if (copy->getRawDest() == pointer) supported = false;
        } else if (auto *address = dyn_cast<GetElementPtrInst>(user)) {
          if (address->getPointerOperand() != pointer) supported = false;
          else pointers.push_back(address);
        } else if (auto *store = dyn_cast<StoreInst>(user)) {
          if (!store->isSimple() || store->getPointerOperand() != pointer ||
              store->getValueOperand() == pointer) supported = false;
          else if (!store->getValueOperand()->getType()->isIntegerTy() ||
                   store->getValueOperand()->getType()->getIntegerBitWidth() % 8 != 0)
            supported = false;
          else allStores.push_back(store);
        } else if (auto *fill = dyn_cast<MemSetInst>(user)) {
          auto *zero = dyn_cast<ConstantInt>(fill->getValue());
          if (initializer || fill->isVolatile() || fill->getRawDest() != pointer ||
              fill->hasOperandBundles() || fill->hasMetadataOtherThanDebugLoc() ||
              !zero || !zero->isZero() || !isa<ConstantInt>(fill->getLength()))
            supported = false;
          else initializer = fill;
        } else if (auto *intrinsic = dyn_cast<IntrinsicInst>(user)) {
          auto *lifetime = dyn_cast<LifetimeIntrinsic>(intrinsic);
          if (!lifetime) supported = false;
          else lifetimes.push_back(lifetime);
        } else {
          supported = false;
        }
      }
    }
    LifetimeIntrinsic *start = nullptr;
    for (auto *lifetime : lifetimes) {
      if (lifetime->getIntrinsicID() == Intrinsic::lifetime_start) {
        if (start || !dom.dominates(lifetime, copy)) supported = false;
        start = lifetime;
      } else if (isPotentiallyReachable(lifetime, copy, nullptr, &dom, nullptr)) {
        supported = false;
      }
    }
    if (start)
      for (auto *store : allStores)
        if (!dom.dominates(start, store)) supported = false;
    if (!initializer || !dom.dominates(initializer, copy) ||
        loops.getLoopFor(initializer->getParent()))
      supported = false;
    if (supported)
      for (auto *store : allStores)
        if (!dom.dominates(initializer, store)) supported = false;
    if (!supported || allStores.size() > 16) continue;
    for (Loop *loop : loops) {
      SmallVector<Loop::Edge, 4> exits;
      loop->getExitEdges(exits);
      if (!loop->getSubLoops().empty() || exits.size() > 8) continue;
      Instruction *successfulExit = nullptr;
      BasicBlock *successfulTarget = nullptr;
      bool multipleCopyExits = false;
      for (auto [from, to] : exits) {
        if (!isPotentiallyReachable(to, copy->getParent(), nullptr, &dom, &loops))
          continue;
        if (successfulExit) {
          multipleCopyExits = true;
          break;
        }
        successfulExit = from->getTerminator();
        successfulTarget = to;
      }
      if (multipleCopyExits || !successfulExit || !loop->getLoopPreheader() ||
          !dom.dominates(loop->getLoopPreheader(), copy->getParent()))
        continue;
      auto *iterations = evolution.getExitCount(loop, successfulExit->getParent());
      if (isa<SCEVCouldNotCompute>(iterations)) continue;
      if (!loop->getLoopLatch() || loop->contains(copy->getParent()) ||
          !dom.dominates(BasicBlockEdge(successfulExit->getParent(), successfulTarget),
                         copy->getParent()) ||
          !dom.dominates(BasicBlockEdge(loop->getLoopPreheader(), loop->getHeader()),
                         copy->getParent()))
        continue;
      auto *array = dyn_cast<ArrayType>(buffer->getAllocatedType());
      if (!array || !array->getElementType()->isIntegerTy(8) ||
          array->getNumElements() > 512 ||
          !isa<ConstantInt>(buffer->getArraySize()) ||
          !cast<ConstantInt>(buffer->getArraySize())->isOne()) continue;
      const unsigned capacity = array->getNumElements();
      SmallVector<Value *, 8> inputs;
      for (Argument &argument : function.args()) inputs.push_back(&argument);
      for (Instruction &op : instructions(function))
        if (isa<FreezeInst>(op) && dom.dominates(&op, loop->getLoopPreheader()->getTerminator()))
          inputs.push_back(&op);
      for (Value *inputValue : inputs) {
        Value &argument = *inputValue;
        if (!argument.getType()->isIntegerTy(64) ||
            !isGuaranteedNotToBeUndefOrPoison(&argument, nullptr, copy, &dom))
          continue;
        auto range = evolution.getUnsignedRange(
            evolution.applyLoopGuards(evolution.getSCEV(&argument), loop));
        if (range.isFullSet() || range.isEmptySet() || range.isWrappedSet()) continue;
        const uint64_t lower = range.getLower().getZExtValue();
        const uint64_t upper = range.getUpper().getZExtValue();
        if (upper - lower > 64) continue;
        bool allCovered = true;
        for (uint64_t input = lower; input < upper && allCovered; ++input) {
          auto eval = [&](const SCEV *expr, const SCEV *iteration = nullptr) {
            return evaluate(expr, evolution, argument, input, loop, iteration);
          };
          auto trip = eval(iterations), bytes = eval(evolution.getSCEV(copy->getLength()));
          if (!trip || !bytes || *trip >= 64 || *bytes > capacity) {
            allCovered = false;
            break;
          }
          auto mark = [&](SmallBitVector &coverage, StoreInst *store, const SCEV *iteration) {
            const auto *pointer = evolution.getSCEV(store->getPointerOperand());
            if (evolution.getPointerBase(pointer) != evolution.getSCEV(buffer)) return;
            auto start = eval(evolution.removePointerBase(pointer), iteration);
            auto size = module.getDataLayout().getTypeStoreSize(store->getValueOperand()->getType());
            if (start && !size.isScalable() && *start <= capacity &&
                size.getFixedValue() <= capacity - *start)
              coverage.set(*start, *start + size.getFixedValue());
          };
          SmallBitVector initialized(capacity);
          for (StoreInst *store : allStores) {
            if (dom.dominates(store, loop->getLoopPreheader()->getTerminator()))
              mark(initialized, store, nullptr);
            else if (loop->contains(store->getParent()) &&
                     dom.dominates(store, successfulExit) &&
                     dom.dominates(store, loop->getLoopLatch()->getTerminator()))
              for (uint64_t step = 0; step <= *trip; ++step)
                mark(initialized, store, evolution.getConstant(argument.getType(), step));
          }
          const auto *last = evolution.getConstant(argument.getType(), *trip);
          SmallVector<std::pair<BasicBlock *, SmallBitVector>, 8> pending;
          pending.emplace_back(successfulTarget, initialized);
          unsigned work = 0;
          while (!pending.empty() && allCovered) {
            auto [block, coverage] = pending.pop_back_val();
            if (++work > 128 || loop->contains(block)) {
              allCovered = false;
              break;
            }
            for (Instruction &op : *block) {
              if (&op == copy) {
                for (unsigned byte = 0; byte < *bytes; ++byte)
                  if (!coverage.test(byte)) allCovered = false;
                break;
              }
              if (auto *store = dyn_cast<StoreInst>(&op);
                  store && is_contained(allStores, store))
                mark(coverage, store, last);
            }
            if (block == copy->getParent()) continue;
            auto *conditional = dyn_cast<CondBrInst>(block->getTerminator());
            std::optional<bool> take;
            if (conditional) take = condition(conditional->getCondition(), evolution, argument, input);
            for (unsigned edge = 0; edge < block->getTerminator()->getNumSuccessors(); ++edge) {
              if (take && (*take != (edge == 0))) continue;
              auto *target = block->getTerminator()->getSuccessor(edge);
              if (isPotentiallyReachable(target, copy->getParent(), nullptr, &dom, nullptr))
                pending.emplace_back(target, coverage);
            }
          }
        }
        if (allCovered) {
          removable.insert(initializer);
          break;
        }
      }
    }
  }
  for (auto *initializer : removable) initializer->eraseFromParent();
  return removable.size();
}

// Refine an integer argument to one defined value only when the resulting
// function admits an initializer removal. A failed trial restores every use.
static unsigned tryFreezeLength(Function &function) {
  bool hasCopy = false;
  bool hasFill = false;
  for (Instruction &op : instructions(function)) {
    if (auto *copy = dyn_cast<MemCpyInst>(&op))
      hasCopy |= isa<AllocaInst>(copy->getRawSource());
    if (auto *fill = dyn_cast<MemSetInst>(&op))
      hasFill |= isa<ConstantInt>(fill->getLength());
  }
  if (!hasCopy || !hasFill) return 0;
  for (Argument &argument : function.args()) {
    if (!argument.getType()->isIntegerTy(64) || argument.hasAttribute(Attribute::NoUndef) ||
        argument.use_empty() || argument.isUsedByMetadata())
      continue;
    auto *freeze = new FreezeInst(&argument, "scratch.length",
                                  function.getEntryBlock().getFirstInsertionPt());
    argument.replaceUsesWithIf(freeze, [freeze](Use &use) { return use.getUser() != freeze; });
    if (unsigned removed = eliminateInFunction(function)) return removed;
    freeze->replaceAllUsesWith(&argument);
    freeze->eraseFromParent();
  }
  return 0;
}

// A private initializer can be removed only when the sole reader copies a
// prefix that is covered by explicit integer stores on every feasible path.
static unsigned eliminate(Module &module) {
  if (module.getNamedMetadata("llvm.dbg.cu") ||
      module.getTargetTriple().getArch() != Triple::aarch64 ||
      !module.getTargetTriple().isOSLinux() ||
      module.getDataLayout().getIndexSizeInBits(0) != 64)
    return 0;
  unsigned removed = 0;
  for (Function &function : module) {
    if (function.isDeclaration() || function.hasOptNone() ||
        function.hasOptSize() || function.hasMinSize() ||
        function.getInstructionCount() > 1000 ||
        function.hasFnAttribute(Attribute::SanitizeAddress) ||
        function.hasFnAttribute(Attribute::SanitizeHWAddress) ||
        function.hasFnAttribute(Attribute::SanitizeMemory) ||
        function.hasFnAttribute(Attribute::SanitizeThread))
      continue;
    unsigned direct = eliminateInFunction(function);
    removed += direct ? direct : tryFreezeLength(function);
  }
  return removed;
}
} // namespace ElisaCopiedScratch
