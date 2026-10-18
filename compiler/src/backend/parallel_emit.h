#ifndef ELISACORE_PARALLEL_EMIT_H
#define ELISACORE_PARALLEL_EMIT_H

#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
#include <stddef.h>

// Parent side. Returns 0 when the module's partitioned instruction weight is below
// `min_weight` (nothing written; use the serial path), 1 after writing the module's
// bitcode to `bitcode_path`, -1 when that write failed.
// Sets LLVM's -O3-only full-unroll budget (-unroll-threshold-aggressive) once per process.
// LoopUnroll reads it only when OptLevel > 2, so -O1/-O2 pipelines in the same process are
// unaffected. Not thread-safe; the Go side serialises calls with sync.Once.
void elisacoreEnsureO3UnrollThreshold(int threshold);

// Clone and restrict a verified module to one post-optimization partition. The
// caller owns the returned module, in the original context. No original IR is
// mutated. A null result declines partitioning; callers can emit serially.
LLVMModuleRef elisacoreCloneObjectPartition(LLVMModuleRef m, int jobs, int part, const char *unit_namespace);
// Build the stable, module-order owner map once, then borrow it across clones.
// The caller owns and must free the returned array. Clone calls do not retain it.
long long *elisacoreStableObjectOwners(LLVMModuleRef m, int jobs, size_t *count);
LLVMModuleRef elisacoreCloneObjectPartitionWithOwners(
    LLVMModuleRef m, int jobs, int part, const char *unit_namespace,
    const long long *owners, size_t count);
long long elisacoreObjectPartitionWeight(LLVMModuleRef m);

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
