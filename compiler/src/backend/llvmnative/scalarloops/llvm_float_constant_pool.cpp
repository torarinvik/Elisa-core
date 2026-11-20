// Pool expensive scalar constants in small, acyclic AArch64 functions.
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
#include <llvm/ADT/SCCIterator.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/Constants.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Module.h>
#include <llvm/MC/MCSubtargetInfo.h>
#include <llvm/MC/TargetRegistry.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Target/TargetMachine.h>
#include <llvm/Transforms/Utils/BasicBlockUtils.h>

#include <algorithm>
#include <cstdint>
#include <memory>

namespace {
using namespace llvm;

struct PoolUse {
  Instruction *instruction;
  unsigned operand;
  unsigned slot;
};

bool needsMultipleImmediateInstructions(const ConstantFP &constant) {
  if (!constant.getType()->isDoubleTy() || !constant.getValueAPF().isNormal())
    return false;
  const uint64_t bits = constant.getValueAPF().bitcastToAPInt().getZExtValue();
  unsigned nonzero = 0;
  unsigned nonones = 0;
  for (unsigned shift = 0; shift < 64; shift += 16) {
    const uint64_t chunk = (bits >> shift) & 0xffff;
    nonzero += chunk != 0;
    nonones += chunk != 0xffff;
  }
  return std::min(nonzero, nonones) >= 3;
}

bool poolFunctionConstants(Function &function) {
  if (function.isDeclaration() || function.size() > 16 ||
      function.hasOptNone() ||
      function.hasFnAttribute("target-cpu") ||
      function.hasFnAttribute("target-features"))
    return false;
  const unsigned instructionCount = function.getInstructionCount();
  if (instructionCount > 100)
    return false;

  SmallVector<Constant *, 8> constants;
  SmallVector<PoolUse, 8> uses;
  for (BasicBlock &block : function) {
    for (Instruction &instruction : block) {
      const bool tinyResult = instructionCount <= 12 && (isa<SelectInst>(instruction) || isa<ReturnInst>(instruction));
      const auto *phi = dyn_cast<PHINode>(&instruction);
      const bool returnPhi = instructionCount <= 12 && phi &&
          phi->getNumIncomingValues() == 2 && phi->hasOneUse() &&
          isa<ReturnInst>(*phi->user_begin()) &&
          phi->getIncomingBlock(0) != phi->getIncomingBlock(1);

      if (!returnPhi && !isa<FCmpInst>(instruction) && !tinyResult &&
          instruction.getOpcode() != Instruction::FMul)
        continue;
      for (unsigned operand = 0; operand < instruction.getNumOperands(); ++operand) {
        auto *constant = dyn_cast<ConstantFP>(instruction.getOperand(operand));
        if (!constant || !needsMultipleImmediateInstructions(*constant))
          continue;
        if (returnPhi) {
          const auto *branch = dyn_cast<BranchInst>(phi->getIncomingBlock(operand)->getTerminator());
          if (!branch)
            return false;
        }
        auto existing = std::find(constants.begin(), constants.end(), constant);
        unsigned slot = static_cast<unsigned>(existing - constants.begin());
        if (existing == constants.end()) {
          if (constants.size() == 8)
            return false;
          constants.push_back(constant);
        }
        if (uses.size() == 12)
          return false;
        uses.push_back({&instruction, operand, slot});

      }
    }
  }
  const bool small = function.size() <= 12 && instructionCount <= 64;
  const bool distinct = constants.size() == uses.size();
  // In tiny functions the load sequence can replace a long immediate even
  // when only one operand uses it. Larger functions need more opportunities.
  if (constants.empty() ||
      (distinct ? (!small || (constants.size() < 4 && instructionCount > 12))
               : (constants.size() < 3 || uses.size() < 6)))
    return false;

  for (auto component = scc_begin(&function); !component.isAtEnd(); ++component)
    if (component.hasCycle())
      return false;

  Module &module = *function.getParent();
  Type *element = Type::getDoubleTy(module.getContext());
  auto *type = ArrayType::get(element, constants.size());
  auto *table = new GlobalVariable(module, type, true, GlobalValue::PrivateLinkage,
                                   ConstantArray::get(type, constants), "elisa.float.constants");
  table->setUnnamedAddr(GlobalValue::UnnamedAddr::Global);
  table->setAlignment(Align(8));
  for (const PoolUse &use : uses) {
    Instruction *insertionPoint = use.instruction;
    if (auto *phi = dyn_cast<PHINode>(use.instruction)) {
      BasicBlock *predecessor = phi->getIncomingBlock(use.operand);
      // The literal must stay on its incoming path so a cold return adds
      // no work to the path computing the normal result.
      if (predecessor->getTerminator()->getNumSuccessors() > 1)
        predecessor = SplitEdge(predecessor, phi->getParent());
      insertionPoint = predecessor->getTerminator();
    }
    IRBuilder<> builder(insertionPoint);
    Value *address = builder.CreateInBoundsGEP(type, table,
                                               {builder.getInt32(0), builder.getInt32(use.slot)});
    Value *value = builder.CreateAlignedLoad(element, address, Align(8));
    use.instruction->setOperand(use.operand, value);
  }
  return true;
}
} // namespace

extern "C" unsigned elisacorePoolFloatingConstants(LLVMModuleRef module,
                                                   LLVMTargetMachineRef machine) {
  if (!module || !machine)
    return 0;
  auto &target = *reinterpret_cast<llvm::TargetMachine *>(machine);
  if (!target.getTargetTriple().isAArch64())
    return 0;
  const std::unique_ptr<llvm::MCSubtargetInfo> subtarget(
      target.getTarget().createMCSubtargetInfo(
          target.getTargetTriple(), target.getTargetCPU(), target.getTargetFeatureString()));
  if (!subtarget || !subtarget->checkFeatures("+fuse-literals"))
    return 0;
  unsigned changed = 0;
  for (llvm::Function &function : *llvm::unwrap(module))
    changed += poolFunctionConstants(function);
  return changed;
}
