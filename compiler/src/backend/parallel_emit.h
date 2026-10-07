#ifndef ELISACORE_PARALLEL_EMIT_H
#define ELISACORE_PARALLEL_EMIT_H

#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>

// Parent side. Returns 0 when the module's partitioned instruction weight is below
// `min_weight` (nothing written; use the serial path), 1 after writing the module's
// bitcode to `bitcode_path`, -1 when that write failed.
int elisacorePartitionPrepare(LLVMModuleRef m, int jobs, long long min_weight, const char *bitcode_path);

// Worker side: emit partition `part` of `jobs` of the bitcode at `bitcode_path` to
// `out_path`. A non-empty `pipeline` is run on the partition first (parallel
// optimization); `import_weight` > 0 then keeps available_externally copies of foreign
// functions of at most that many instructions so they stay inlinable. Returns 1 on
// success, 0 with a malloc'd message in *error_out on failure.
int elisacorePartitionWorker(const char *bitcode_path, int jobs, int part, const char *pipeline,
                             long long import_weight, const char *triple, const char *cpu,
                             const char *features, const char *out_path, char **error_out);

#endif
