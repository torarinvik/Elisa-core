//go:build cgo

#include <llvm-c/Core.h>
#include <llvm/ADT/DenseMap.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/Module.h>
#include <llvm/Transforms/Utils/Cloning.h>
#include <llvm/Config/llvm-config.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Instructions.h>
#include <llvm/Support/CBindingWrapping.h>
#include <optional>
#include <llvm/ADT/SmallPtrSet.h>
#include <llvm/Analysis/ValueTracking.h>
#include <llvm/IR/Dominators.h>
#include <llvm/IR/IntrinsicInst.h>
#include <llvm-c/Error.h>
#include <llvm-c/TargetMachine.h>
#include <llvm-c/Transforms/PassBuilder.h>
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
#include <llvm/TargetParser/Triple.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/IR/Verifier.h>
#include <llvm/Analysis/TargetLibraryInfo.h>
#include <llvm-c/Target.h>
#include <mutex>
#include <llvm/IR/InlineAsm.h>
#include <llvm/Support/raw_ostream.h>
#include <llvm/Target/TargetMachine.h>
#include <limits>
#include <llvm/IR/Intrinsics.h>
#include <llvm/IR/PassManager.h>
#include <llvm/IR/Constants.h>
#include <llvm/IR/CFG.h>
#include <llvm/Support/FileSystem.h>
#include <llvm/Analysis/CFG.h>
#include <llvm/IR/DerivedTypes.h>
#include <llvm/AsmParser/Parser.h>
#include <llvm/Linker/Linker.h>
#include <llvm/Transforms/Utils/ValueMapper.h>
#include <llvm/IR/DebugInfo.h>
#include <llvm/IR/Operator.h>
#include <unordered_map>
#include <llvm/Analysis/CaptureTracking.h>
#include <llvm/MC/MCSubtargetInfo.h>
#include <llvm/MC/TargetRegistry.h>
#include <llvm/Transforms/Utils/LoopPeel.h>

namespace wrap_llvm_bounded_unswitch {
#include "passes/llvm_bounded_unswitch.cpp"
}  // namespace wrap_llvm_bounded_unswitch
namespace wrap_llvm_checked_mul {
#include "passes/llvm_checked_mul.cpp"
}  // namespace wrap_llvm_checked_mul
namespace wrap_llvm_even_range {
#include "passes/llvm_even_range.cpp"
}  // namespace wrap_llvm_even_range
namespace wrap_llvm_index_bounds {
#include "passes/llvm_index_bounds.cpp"
}  // namespace wrap_llvm_index_bounds
namespace wrap_llvm_ordered_vector {
#include "passes/llvm_ordered_vector.cpp"
}  // namespace wrap_llvm_ordered_vector
namespace wrap_llvm_remainder_reduction {
#include "passes/llvm_remainder_reduction.cpp"
}  // namespace wrap_llvm_remainder_reduction
namespace wrap_llvm_remainder_tail {
#include "passes/llvm_remainder_tail.cpp"
}  // namespace wrap_llvm_remainder_tail
namespace wrap_llvm_short_trip {
#include "passes/llvm_short_trip.cpp"
}  // namespace wrap_llvm_short_trip
namespace wrap_llvm_guarded_reductions {
#include "passes/llvm_guarded_reductions.cpp"
}  // namespace wrap_llvm_guarded_reductions
namespace wrap_llvm_table_range {
#include "passes/llvm_table_range.cpp"
}  // namespace wrap_llvm_table_range
namespace wrap_llvm_table_unroll {
#include "passes/llvm_table_unroll.cpp"
}  // namespace wrap_llvm_table_unroll
namespace wrap_llvm_validation_peel {
#include "passes/llvm_validation_peel.cpp"
}  // namespace wrap_llvm_validation_peel
