#ifndef ELISACORE_PARALLEL_EMIT_H
#define ELISACORE_PARALLEL_EMIT_H

#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>

// Emits `m` as `jobs` partition objects at `paths`. Returns 0 when the module's
// partitioned instruction weight is below `min_weight` (nothing written; use the serial
// path), 1 on success, -1 on failure with a malloc'd message in *error_out.
int elisacoreParallelEmit(LLVMModuleRef m, LLVMTargetMachineRef tm, int jobs, long long min_weight,
                          const char *const *paths, char **error_out);

#endif
