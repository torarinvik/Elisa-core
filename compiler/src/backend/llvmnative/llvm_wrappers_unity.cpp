// Unity translation unit for the LLVM C++ wrappers in llvm_wrappers/.
//
// Each wrapper is compiled inside its own namespace so file-scope helpers and
// `using namespace` directives cannot collide, while extern "C" entry points keep
// their C names. LLVM headers are included once here so they are parsed once
// instead of once per wrapper. To add a wrapper: move it into llvm_wrappers/, add
// its LLVM includes to the list below, and add a namespace block for it.
#include <llvm-c/Core.h>
#include <llvm/ADT/DenseMap.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/Function.h>
#include <llvm/IR/GlobalVariable.h>
#include <llvm/IR/InstrTypes.h>
#include <llvm/IR/Module.h>
#include <llvm/Bitcode/BitcodeReader.h>
#include <llvm/Support/Error.h>
#include <llvm/Support/MemoryBuffer.h>
#include <cstring>
#include <algorithm>
#include <vector>
#include <llvm/Transforms/Utils/Cloning.h>
#include <llvm/Transforms/Utils/Local.h>
#include <llvm/Transforms/IPO/StripDeadPrototypes.h>
#include <cstdint>
#include <cstdlib>
#include <iterator>
#include <utility>
#include <llvm/Config/llvm-config.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/PatternMatch.h>

#include <llvm/IR/Instructions.h>
#include <llvm/Support/CBindingWrapping.h>
#include <optional>
#include <llvm/ADT/DenseSet.h>
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/Analysis/ConstantFolding.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm-c/Error.h>
#include <llvm-c/TargetMachine.h>
#include <llvm-c/Transforms/PassBuilder.h>
#include <llvm/Analysis/Utils/Local.h>
#include <functional>
#include <llvm/IR/ConstantRange.h>
#include <llvm/Analysis/LazyValueInfo.h>
#include <llvm/Analysis/LoopInfo.h>
#include <llvm/Analysis/ScalarEvolution.h>
#include <llvm/Analysis/AssumptionCache.h>
#include <llvm/Analysis/OptimizationRemarkEmitter.h>
#include <llvm/Analysis/TargetTransformInfo.h>
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Transforms/Utils/LoopUtils.h>
#include <llvm/Transforms/Utils/UnrollLoop.h>
#include <llvm/Transforms/InstCombine/InstCombine.h>
#include <llvm/Transforms/Scalar/SimplifyCFG.h>
#include <llvm/Transforms/Utils/BasicBlockUtils.h>
#include <llvm/Transforms/Utils/LCSSA.h>
#include <llvm/Transforms/Utils/LoopSimplify.h>
#include <llvm/ADT/APInt.h>
#include <llvm/IR/BasicBlock.h>
#include <llvm/IR/DataLayout.h>
#include <llvm/Transforms/Utils/Local.h>
#include <llvm/TargetParser/Triple.h>
#include <llvm/IRReader/IRReader.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/IR/Verifier.h>
#include <llvm/IR/InstIterator.h>
#include <llvm/Analysis/ScalarEvolutionExpressions.h>
#include <llvm/Analysis/TargetLibraryInfo.h>
#include <map>
#include <llvm-c/Target.h>
#include <mutex>
#include <llvm/IR/InlineAsm.h>
#include <llvm/Bitcode/BitcodeWriter.h>
#include <llvm/IR/Attributes.h>
#include <llvm/IR/Metadata.h>
#include <llvm/Support/raw_ostream.h>
#include <llvm/Target/TargetMachine.h>
#include <limits>
#include <llvm/IR/Intrinsics.h>
#include <llvm/IR/PassManager.h>
#include <llvm/Analysis/InlineCost.h>
#include <llvm/IR/Constants.h>
#include <llvm/IR/MDBuilder.h>
#include <llvm/IR/CFG.h>
#include <llvm/Support/FileSystem.h>
#include <llvm/Analysis/CFG.h>
#include <llvm/IR/DerivedTypes.h>
#include <llvm/IR/ValueHandle.h>
#include <llvm/Transforms/Utils/SimplifyLibCalls.h>
#include <llvm/ADT/PostOrderIterator.h>
#include <deque>
#include <set>
#include <string>
#include <llvm/CodeGen/MachineFunction.h>
#include <llvm/CodeGen/MachineScheduler.h>
#include <llvm/Support/CommandLine.h>
#include <llvm-c/Support.h>
#include <llvm/AsmParser/Parser.h>
#include <llvm/Linker/Linker.h>
#include <llvm/Support/Casting.h>
#include <llvm/Transforms/Utils/ValueMapper.h>
#include <llvm/IR/DebugProgramInstruction.h>
#include <llvm/Support/KnownFPClass.h>
#include <llvm/IR/DebugInfo.h>
#include <llvm/IR/Operator.h>
#include <unordered_map>
#include <llvm/Analysis/CaptureTracking.h>
#include <llvm/MC/MCSubtargetInfo.h>
#include <llvm/MC/TargetRegistry.h>
#include <llvm/Transforms/Utils/LoopPeel.h>
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
#include <chrono>
namespace wrap_incremental_clone {
#include "llvm_wrappers/incremental_clone.cpp"
}  // namespace wrap_incremental_clone
namespace wrap_llvm_aggregate_copy {
#include "llvm_wrappers/llvm_aggregate_copy.cpp"
}  // namespace wrap_llvm_aggregate_copy
namespace wrap_llvm_array_copy_elision {
#include "llvm_wrappers/llvm_array_copy_elision.cpp"
}  // namespace wrap_llvm_array_copy_elision
namespace wrap_llvm_bool_array_pack {
#include "llvm_wrappers/llvm_bool_array_pack.cpp"
}  // namespace wrap_llvm_bool_array_pack
namespace wrap_llvm_bounded_division_unroll {
// Bounded division and three-state recurrence unrolling.
#include "llvm_wrappers/llvm_bounded_division_unroll.cpp"
}  // namespace wrap_llvm_bounded_division_unroll
namespace wrap_llvm_bounded_unswitch {
#include "llvm_wrappers/llvm_bounded_unswitch.cpp"
}  // namespace wrap_llvm_bounded_unswitch
namespace wrap_llvm_byte_pack_load {
#include "llvm_wrappers/llvm_byte_pack_load.cpp"
}  // namespace wrap_llvm_byte_pack_load
namespace wrap_llvm_checked_mul {
#include "llvm_wrappers/llvm_checked_mul.cpp"
}  // namespace wrap_llvm_checked_mul
namespace wrap_llvm_covered_prefix {
#include "llvm_wrappers/llvm_covered_prefix.cpp"
}  // namespace wrap_llvm_covered_prefix
namespace wrap_llvm_even_range {
#include "llvm_wrappers/llvm_even_range.cpp"
}  // namespace wrap_llvm_even_range
namespace wrap_llvm_index_bounds {
#include "llvm_wrappers/llvm_index_bounds.cpp"
}  // namespace wrap_llvm_index_bounds
namespace wrap_llvm_optimized_cache_writer {
#include "llvm_wrappers/llvm_optimized_cache_writer.cpp"
}  // namespace wrap_llvm_optimized_cache_writer
namespace wrap_llvm_ordered_vector {
#include "llvm_wrappers/llvm_ordered_vector.cpp"
}  // namespace wrap_llvm_ordered_vector
namespace wrap_llvm_recursive_inline {
#include "llvm_wrappers/llvm_recursive_inline.cpp"
}  // namespace wrap_llvm_recursive_inline
namespace wrap_llvm_remainder_reduction {
#include "llvm_wrappers/llvm_remainder_reduction.cpp"
}  // namespace wrap_llvm_remainder_reduction
namespace wrap_llvm_remainder_tail {
#include "llvm_wrappers/llvm_remainder_tail.cpp"
}  // namespace wrap_llvm_remainder_tail
namespace wrap_llvm_scratch_prefix {
#include "llvm_wrappers/llvm_scratch_prefix.cpp"
}  // namespace wrap_llvm_scratch_prefix
namespace wrap_llvm_selective_scheduler {
#include "llvm_wrappers/llvm_selective_scheduler.cpp"
}  // namespace wrap_llvm_selective_scheduler
namespace wrap_llvm_short_trip {
#include "llvm_wrappers/llvm_short_trip.cpp"
}  // namespace wrap_llvm_short_trip
namespace wrap_llvm_small_zero {
#include "llvm_wrappers/llvm_small_zero.cpp"
}  // namespace wrap_llvm_small_zero
namespace wrap_llvm_special_epilogue {
#include "llvm_wrappers/llvm_special_epilogue.cpp"
}  // namespace wrap_llvm_special_epilogue
namespace wrap_llvm_split_half_add {
#include "llvm_wrappers/llvm_split_half_add.cpp"
}  // namespace wrap_llvm_split_half_add
namespace wrap_llvm_masked_split_multiply {
#include "llvm_wrappers/llvm_masked_split_multiply.cpp"
}  // namespace wrap_llvm_masked_split_multiply
namespace wrap_llvm_sqrt_proof {
#include "llvm_wrappers/llvm_sqrt_proof.cpp"
}  // namespace wrap_llvm_sqrt_proof
namespace wrap_llvm_table_range {
#include "llvm_wrappers/llvm_table_range.cpp"
}  // namespace wrap_llvm_table_range
namespace wrap_llvm_table_unroll {
#include "llvm_wrappers/llvm_table_unroll.cpp"
}  // namespace wrap_llvm_table_unroll
namespace wrap_llvm_validation_layout {
#include "llvm_wrappers/llvm_validation_layout.cpp"
}  // namespace wrap_llvm_validation_layout
namespace wrap_llvm_validation_peel {
#include "llvm_wrappers/llvm_validation_peel.cpp"
}  // namespace wrap_llvm_validation_peel
namespace wrap_llvm_zero_forward {
#include "llvm_wrappers/llvm_zero_forward.cpp"
}  // namespace wrap_llvm_zero_forward
