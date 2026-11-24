// Late facts about local arrays and the loops that index them: value
// ranges of array elements, exits to a trap that another exit always beats,
// zero fills that no read can observe or that a view bounds, and bounds
// checks that always pass.
#include "llvm_array_value_ranges.h"
#include "llvm_dead_trap_exits.h"
#include "llvm_path_ranges.h"
#include "llvm_index_bounds.h"
#include "llvm_view_bounded_fills.h"
#include "llvm_indexed_prefix.h"
#include "llvm_proven_checks.h"
#include "llvm_validated_reads.h"
#include "llvm_bounded_trip_ranges.h"

extern "C" unsigned elisacoreAnnotateArrayValueRanges(LLVMModuleRef Module) {
  return Module ? ElisaArrayRanges::annotateArrayValueRanges(*llvm::unwrap(Module)) : 0;
}

extern "C" unsigned elisacoreRemoveDeadTrapExits(LLVMModuleRef Module) {
  return Module ? ElisaDeadTrapExits::removeDeadTrapExits(*llvm::unwrap(Module)) : 0;
}

// Also shrinks zero fills of arrays whose only use is a view every callee
// keeps inside its length, and bounds reads of input elements an earlier
// loop validated.
extern "C" unsigned elisacoreRemoveIndexedPrefixFills(LLVMModuleRef Module) {
  if (!Module) return 0;
  unsigned Removed = ElisaIndexedPrefix::removeIndexedPrefixFills(*llvm::unwrap(Module));
  Removed += ElisaValidatedReads::annotateValidatedReads(*llvm::unwrap(Module));
  return Removed + ElisaViewBounds::shrinkViewFills(*llvm::unwrap(Module));
}

// Also folds overflow checks that loops of a small known trip count cannot
// trigger.
extern "C" unsigned elisacoreRemoveProvenChecks(LLVMModuleRef Module) {
  if (!Module) return 0;
  unsigned Folded = ElisaBoundedTripRanges::foldBoundedTripChecks(*llvm::unwrap(Module));
  return Folded + ElisaProvenChecks::removeProvenChecks(*llvm::unwrap(Module));
}
