// Parallel post-optimization code generation for `-emit obj`.
//
// Port of the stage1 design (Elisa-compiler/src/backend/codegen_parallel_emit.elisa).
// The optimized, verified module is partitioned by function: defined functions are
// split into N contiguous runs of roughly equal instruction count. Stage1 forks one
// child per run. Stage0 is a Go process and cannot fork, and LLVM's pass pipeline is not
// safe to run in several threads of one process (concurrent default<O3> in separate
// LLVMContexts crashed in SimplifyCFG), so the module is written once to a bitcode file
// and N worker PROCESSES (this executable re-run in worker mode, see
// llvm_parallel_emit.go) each parse it into a fresh context, build their own target
// machine (same triple, CPU, features, codegen level, reloc and code model as the serial
// path), restrict the module to their partition and write `<out>.part<k>.o`. The Go
// caller merges the parts with `ld -r` (honoring $ELISA_LD).
//
// Linkage is rewritten identically in every partition so cross-partition references
// resolve: a function or mutable/external global with internal or private linkage
// becomes a HIDDEN external symbol renamed `elisa.part.<name>` (or
// `elisa.part.anon.<ordinal>` when unnamed), defined only by its owner. Mutable and
// external globals are owned by partition 0; read-only private constants stay private
// and are duplicated. linkonce/weak definitions stay in every partition. An alias whose
// aliasee became a declaration has its uses redirected to the aliasee and turns private.
//
// The partitioning is a pure function of the module, so output is deterministic.
//
// Opt-in parallel optimization (ELISACORE_PARALLEL_OPT=1): the module is partitioned
// BEFORE the default<O*> pipeline and each worker optimizes its own partition, so
// cross-partition inlining is lost. To limit that, a non-owner keeps an
// available_externally copy of every partitioned function of at most `import_weight`
// instructions (ThinLTO-style import) instead of a bare declaration: it can still be
// inlined there, and the pipeline drops the copy before codegen.

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <llvm-c/BitReader.h>
#include <llvm-c/BitWriter.h>
#include <llvm-c/Core.h>
#include <llvm-c/DebugInfo.h>
#include <llvm-c/TargetMachine.h>
#include <llvm-c/Transforms/PassBuilder.h>

#include "parallel_emit.h"

static int part_linkage_is_local(LLVMLinkage l) {
	return l == LLVMInternalLinkage || l == LLVMPrivateLinkage;
}

static int part_linkage_is_partitioned(LLVMLinkage l) {
	return l == LLVMExternalLinkage || part_linkage_is_local(l);
}

static long long part_function_weight(LLVMValueRef f) {
	long long weight = 0;
	for (LLVMBasicBlockRef b = LLVMGetFirstBasicBlock(f); b; b = LLVMGetNextBasicBlock(b)) {
		for (LLVMValueRef i = LLVMGetFirstInstruction(b); i; i = LLVMGetNextInstruction(i)) {
			weight++;
		}
	}
	return weight;
}

static int part_function_owned(LLVMValueRef f) {
	if (LLVMIsDeclaration(f)) {
		return 0;
	}
	return part_linkage_is_partitioned(LLVMGetLinkage(f));
}

static int part_global_owned(LLVMValueRef g) {
	if (LLVMIsDeclaration(g)) {
		return 0;
	}
	LLVMLinkage l = LLVMGetLinkage(g);
	if (!part_linkage_is_partitioned(l)) {
		return 0;
	}
	if (l == LLVMExternalLinkage) {
		return 1;
	}
	return !LLVMIsGlobalConstant(g);
}

static void part_externalize(LLVMValueRef v, long long ordinal) {
	if (!part_linkage_is_local(LLVMGetLinkage(v))) {
		return;
	}
	size_t len = 0;
	const char *name = LLVMGetValueName2(v, &len);
	size_t cap = len + 64;
	char *renamed = malloc(cap);
	size_t n;
	if (len == 0) {
		n = (size_t)snprintf(renamed, cap, "elisa.part.anon.%lld", ordinal);
	} else {
		memcpy(renamed, "elisa.part.", 11);
		memcpy(renamed + 11, name, len);
		n = 11 + len;
		renamed[n] = 0;
	}
	LLVMSetValueName2(v, renamed, n);
	free(renamed);
	LLVMSetLinkage(v, LLVMExternalLinkage);
	LLVMSetVisibility(v, LLVMHiddenVisibility);
}

static void part_strip_body(LLVMValueRef f) {
	for (LLVMBasicBlockRef b = LLVMGetFirstBasicBlock(f); b; b = LLVMGetNextBasicBlock(b)) {
		for (LLVMValueRef i = LLVMGetFirstInstruction(b); i; i = LLVMGetNextInstruction(i)) {
			LLVMTypeRef t = LLVMTypeOf(i);
			LLVMTypeKind k = LLVMGetTypeKind(t);
			if (k != LLVMVoidTypeKind && k != LLVMTokenTypeKind) {
				LLVMReplaceAllUsesWith(i, LLVMGetUndef(t));
			}
		}
	}
	LLVMBasicBlockRef b;
	while ((b = LLVMGetFirstBasicBlock(f)) != NULL) {
		LLVMValueRef i;
		while ((i = LLVMGetFirstInstruction(b)) != NULL) {
			LLVMInstructionEraseFromParent(i);
		}
		LLVMDeleteBasicBlock(b);
	}
	LLVMSetSubprogram(f, NULL);
	if (LLVMHasPersonalityFn(f)) {
		LLVMSetPersonalityFn(f, NULL);
	}
}

// Owner of every function in module order (-1: not partitioned). Returns total weight.
static long long part_function_owners(LLVMModuleRef m, int jobs, long long **owners_out, size_t *count_out) {
	size_t count = 0;
	for (LLVMValueRef f = LLVMGetFirstFunction(m); f; f = LLVMGetNextFunction(f)) {
		count++;
	}
	long long *weights = calloc(count ? count : 1, sizeof(long long));
	long long total = 0;
	size_t idx = 0;
	for (LLVMValueRef f = LLVMGetFirstFunction(m); f; f = LLVMGetNextFunction(f), idx++) {
		long long w = part_function_owned(f) ? part_function_weight(f) + 1 : -1;
		weights[idx] = w;
		if (w > 0) {
			total += w;
		}
	}
	long long prefix = 0;
	for (idx = 0; idx < count; idx++) {
		long long w = weights[idx];
		if (w < 0) {
			continue;
		}
		long long owner = total > 0 ? (prefix * jobs) / total : 0;
		if (owner >= jobs) {
			owner = jobs - 1;
		}
		weights[idx] = owner;
		prefix += w;
	}
	// weights[] now holds owners (non-partitioned entries kept as -1).
	*owners_out = weights;
	*count_out = count;
	return total;
}

static int part_restrict_module(LLVMModuleRef m, const long long *owners, size_t count, int part, long long import_weight) {
	long long ordinal = 0;
	for (LLVMValueRef g = LLVMGetFirstGlobal(m); g; g = LLVMGetNextGlobal(g), ordinal++) {
		if (part_global_owned(g)) {
			part_externalize(g, ordinal);
			if (part != 0) {
				LLVMSetInitializer(g, NULL);
				LLVMSetLinkage(g, LLVMExternalLinkage);
			}
		}
	}
	size_t idx = 0;
	for (LLVMValueRef f = LLVMGetFirstFunction(m); f; f = LLVMGetNextFunction(f), idx++, ordinal++) {
		if (idx >= count) {
			return 0;
		}
		long long owner = owners[idx];
		if (owner >= 0) {
			part_externalize(f, ordinal);
			if (owner != part) {
				if (import_weight > 0 && part_function_weight(f) <= import_weight) {
					LLVMSetLinkage(f, LLVMAvailableExternallyLinkage);
				} else {
					part_strip_body(f);
				}
			}
		}
	}
	if (idx != count) {
		return 0;
	}
	for (LLVMValueRef a = LLVMGetFirstGlobalAlias(m); a; a = LLVMGetNextGlobalAlias(a)) {
		LLVMValueRef aliasee = LLVMAliasGetAliasee(a);
		if (LLVMIsDeclaration(aliasee)) {
			LLVMReplaceAllUsesWith(a, aliasee);
			LLVMSetLinkage(a, LLVMPrivateLinkage);
		}
	}
	return 1;
}

// Same options as elisacoreRunOptimizationPipeline in llvm_target.go.
static int part_optimize(LLVMModuleRef m, LLVMTargetMachineRef tm, const char *pipeline, char **error) {
	LLVMPassBuilderOptionsRef options = LLVMCreatePassBuilderOptions();
	LLVMPassBuilderOptionsSetLoopInterleaving(options, 1);
	LLVMPassBuilderOptionsSetLoopVectorization(options, 1);
	LLVMPassBuilderOptionsSetSLPVectorization(options, 1);
	LLVMPassBuilderOptionsSetLoopUnrolling(options, 1);
	LLVMErrorRef err = LLVMRunPasses(m, pipeline, tm, options);
	LLVMDisposePassBuilderOptions(options);
	if (err == NULL) {
		return 1;
	}
	char *msg = LLVMGetErrorMessage(err);
	*error = strdup(msg);
	LLVMDisposeErrorMessage(msg);
	return 0;
}

// Parent side: decide whether to partition and serialize the module for the workers.
int elisacorePartitionPrepare(LLVMModuleRef m, int jobs, long long min_weight, const char *bitcode_path) {
	long long *owners = NULL;
	size_t count = 0;
	long long total = part_function_owners(m, jobs, &owners, &count);
	free(owners);
	if (total < min_weight) {
		return 0;
	}
	return LLVMWriteBitcodeToFile(m, bitcode_path) == 0 ? 1 : -1;
}

static int part_fail(char **error_out, const char *text) {
	*error_out = strdup(text ? text : "unknown partition worker error");
	return 0;
}

// Worker side (a separate process): parse, restrict to `part`, optionally optimize, emit.
int elisacorePartitionWorker(const char *bitcode_path, int jobs, int part, const char *pipeline,
                             long long import_weight, const char *triple, const char *cpu,
                             const char *features, const char *out_path, char **error_out) {
	*error_out = NULL;
	char *msg = NULL;
	LLVMMemoryBufferRef buf = NULL;
	if (LLVMCreateMemoryBufferWithContentsOfFile(bitcode_path, &buf, &msg) != 0) {
		int r = part_fail(error_out, msg);
		if (msg) LLVMDisposeMessage(msg);
		return r;
	}
	LLVMContextRef ctx = LLVMContextCreate();
	LLVMModuleRef m = NULL;
	if (LLVMParseBitcodeInContext2(ctx, buf, &m) != 0 || m == NULL) {
		LLVMDisposeMemoryBuffer(buf);
		return part_fail(error_out, "failed to parse partition bitcode");
	}
	LLVMDisposeMemoryBuffer(buf);
	// The owners are recomputed from the identical module, so every worker agrees.
	long long *owners = NULL;
	size_t count = 0;
	part_function_owners(m, jobs, &owners, &count);
	int ok = part_restrict_module(m, owners, count, part, import_weight);
	free(owners);
	if (!ok) {
		return part_fail(error_out, "partition function list does not match the source module");
	}
	LLVMTargetRef target;
	if (LLVMGetTargetFromTriple(triple, &target, &msg) != 0) {
		int r = part_fail(error_out, msg);
		if (msg) LLVMDisposeMessage(msg);
		return r;
	}
	LLVMTargetMachineRef tm = LLVMCreateTargetMachine(target, triple, cpu, features,
		LLVMCodeGenLevelDefault, LLVMRelocDefault, LLVMCodeModelDefault);
	if (tm == NULL) {
		return part_fail(error_out, "failed to create partition target machine");
	}
	if (pipeline != NULL && pipeline[0] != 0 && !part_optimize(m, tm, pipeline, error_out)) {
		return 0;
	}
	if (LLVMTargetMachineEmitToFile(tm, m, (char *)out_path, LLVMObjectFile, &msg) != 0) {
		int r = part_fail(error_out, msg ? msg : "partition object emission failed");
		if (msg) LLVMDisposeMessage(msg);
		return r;
	}
	// The process exits right after; skip tearing down the module and context.
	return 1;
}
