//go:build cgo

#include <llvm-c/Core.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/Function.h>
#include <llvm/IR/Module.h>
#include <algorithm>
#include <vector>
#include <llvm/Transforms/Utils/Local.h>
#include <cstdint>
#include <cstdlib>
#include <llvm/Config/llvm-config.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Instructions.h>
#include <llvm/Support/CBindingWrapping.h>
#include <optional>
#include <llvm/ADT/DenseSet.h>
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm-c/Error.h>
#include <llvm-c/TargetMachine.h>
#include <llvm-c/Transforms/PassBuilder.h>
#include <llvm/Analysis/Utils/Local.h>
#include <functional>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Analysis/AssumptionCache.h>
#include <llvm/Analysis/OptimizationRemarkEmitter.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/ADT/APInt.h>
#include <llvm/IR/BasicBlock.h>
#include <llvm/IR/DataLayout.h>
#include <llvm/TargetParser/Triple.h>
#include <llvm/IRReader/IRReader.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/IR/Verifier.h>
#include <llvm/IR/InstIterator.h>
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
#include <llvm/Analysis/TargetLibraryInfo.h>
#include <map>
#include <llvm/IR/Metadata.h>
#include <llvm/Support/raw_ostream.h>
#include <limits>
#include <llvm/IR/Constants.h>
#include <llvm/IR/CFG.h>
#include <llvm/Analysis/CFG.h>
#include <llvm/IR/DerivedTypes.h>
#include <llvm/IR/ValueHandle.h>
#include <llvm/Transforms/Utils/SimplifyLibCalls.h>
#include <llvm/ADT/PostOrderIterator.h>
#include <deque>
#include <set>
#include <string>
#include <llvm/IR/DebugInfo.h>
#include <llvm/IR/Operator.h>
#include <unordered_map>
#include <llvm/Analysis/CaptureTracking.h>
#include <llvm/IR/Instruction.h>
#include <llvm/IR/LLVMContext.h>
#include <chrono>

namespace wrap_llvm_aggregate_copy {
#include "passes/llvm_aggregate_copy.cpp"
}  // namespace wrap_llvm_aggregate_copy
namespace wrap_llvm_array_copy_elision {
#include "passes/llvm_array_copy_elision.cpp"
}  // namespace wrap_llvm_array_copy_elision
namespace wrap_llvm_bool_array_pack {
#include "passes/llvm_bool_array_pack.cpp"
}  // namespace wrap_llvm_bool_array_pack
namespace wrap_llvm_byte_pack_load {
#include "passes/llvm_byte_pack_load.cpp"
}  // namespace wrap_llvm_byte_pack_load
namespace wrap_llvm_covered_prefix {
#include "passes/llvm_covered_prefix.cpp"
}  // namespace wrap_llvm_covered_prefix
namespace wrap_llvm_scratch_prefix {
#include "passes/llvm_scratch_prefix.cpp"
}  // namespace wrap_llvm_scratch_prefix
namespace wrap_llvm_zero_forward {
#include "passes/llvm_zero_forward.cpp"
}  // namespace wrap_llvm_zero_forward
