// Shared LLVM wrappers. Loop and storage transforms build in separate packages.
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
#include <llvm/Passes/PassBuilder.h>
#include <llvm/Transforms/Utils/BasicBlockUtils.h>
#include <llvm/IR/BasicBlock.h>
#include <llvm/IR/InstIterator.h>
#include <llvm/Analysis/TargetLibraryInfo.h>
#include <mutex>
#include <llvm/Bitcode/BitcodeWriter.h>
#include <llvm/IR/Attributes.h>
#include <llvm/IR/Metadata.h>
#include <llvm/Support/raw_ostream.h>
#include <llvm/Target/TargetMachine.h>
#include <llvm/Analysis/InlineCost.h>
#include <llvm/IR/Constants.h>
#include <llvm/IR/MDBuilder.h>
#include <llvm/CodeGen/MachineFunction.h>
#include <llvm/CodeGen/MachineScheduler.h>
#include <llvm/Support/CommandLine.h>
#include <llvm-c/Support.h>
#include <llvm/Support/Casting.h>
#include <llvm/Transforms/Utils/ValueMapper.h>
#include <llvm/IR/DebugProgramInstruction.h>
#include <llvm/Support/KnownFPClass.h>
namespace wrap_incremental_clone {
#include "llvm_wrappers/incremental_clone.cpp"
}  // namespace wrap_incremental_clone
namespace wrap_llvm_optimized_cache_writer {
#include "llvm_wrappers/llvm_optimized_cache_writer.cpp"
}  // namespace wrap_llvm_optimized_cache_writer
namespace wrap_llvm_recursive_inline {
#include "llvm_wrappers/llvm_recursive_inline.cpp"
}  // namespace wrap_llvm_recursive_inline
namespace wrap_llvm_selective_scheduler {
#include "llvm_wrappers/llvm_selective_scheduler.cpp"
}  // namespace wrap_llvm_selective_scheduler
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
namespace wrap_llvm_validation_layout {
#include "llvm_wrappers/llvm_validation_layout.cpp"
}  // namespace wrap_llvm_validation_layout
