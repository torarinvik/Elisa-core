// Late facts about local arrays and the loops that index them: value
// ranges of array elements, exits to a trap that another exit always beats,
// and zero fills that no read can observe.
#include "llvm_array_value_ranges.h"
#include "llvm_dead_trap_exits.h"
#include "llvm_indexed_prefix.h"

extern "C" unsigned elisacoreAnnotateArrayValueRanges(LLVMModuleRef Module) {
  return Module ? ElisaArrayRanges::annotateArrayValueRanges(*llvm::unwrap(Module)) : 0;
}

extern "C" unsigned elisacoreRemoveDeadTrapExits(LLVMModuleRef Module) {
  return Module ? ElisaDeadTrapExits::removeDeadTrapExits(*llvm::unwrap(Module)) : 0;
}

extern "C" unsigned elisacoreRemoveIndexedPrefixFills(LLVMModuleRef Module) {
  return Module ? ElisaIndexedPrefix::removeIndexedPrefixFills(*llvm::unwrap(Module)) : 0;
}
