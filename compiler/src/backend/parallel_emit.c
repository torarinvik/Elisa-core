// Parallel post-optimization code generation for `-emit obj`.
//
// Port of the stage1 design (Elisa-compiler/src/backend/codegen_parallel_emit.elisa).
// The optimized, verified module is partitioned by function: defined functions are
// split into N contiguous runs of roughly equal instruction count. Stage1 forks one
// child per run; stage0 is a Go process and cannot fork, so instead the module is
// serialized once to an in-memory bitcode buffer and N pthreads each parse their own
// copy into their OWN LLVMContext, build their OWN target machine (same triple, CPU,
// features, codegen level, reloc and code model as the serial path), restrict the copy
// to their partition and write `<out>.part<k>.o`. The Go caller merges the parts with
// `ld -r` (honoring $ELISA_LD).
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

#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <llvm-c/BitReader.h>
#include <llvm-c/BitWriter.h>
#include <llvm-c/Core.h>
#include <llvm-c/DebugInfo.h>
#include <llvm-c/TargetMachine.h>

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

static int part_restrict_module(LLVMModuleRef m, const long long *owners, size_t count, int part) {
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
				part_strip_body(f);
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

typedef struct {
	const char *bitcode;
	size_t bitcode_len;
	const long long *owners;
	size_t owner_count;
	int part;
	const char *triple;
	const char *cpu;
	const char *features;
	const char *path;
	char *error;
} part_worker;

static void *part_worker_run(void *arg) {
	part_worker *w = arg;
	char *msg = NULL;
	LLVMContextRef ctx = LLVMContextCreate();
	LLVMMemoryBufferRef buf = LLVMCreateMemoryBufferWithMemoryRange(w->bitcode, w->bitcode_len, "elisa.part", 0);
	LLVMModuleRef m = NULL;
	if (LLVMParseBitcodeInContext2(ctx, buf, &m) != 0 || m == NULL) {
		w->error = strdup("failed to parse partition bitcode");
		LLVMDisposeMemoryBuffer(buf);
		LLVMContextDispose(ctx);
		return NULL;
	}
	LLVMDisposeMemoryBuffer(buf);
	if (!part_restrict_module(m, w->owners, w->owner_count, w->part)) {
		w->error = strdup("partition function list does not match the source module");
		LLVMDisposeModule(m);
		LLVMContextDispose(ctx);
		return NULL;
	}
	LLVMTargetRef target;
	if (LLVMGetTargetFromTriple(w->triple, &target, &msg) != 0) {
		w->error = strdup(msg ? msg : "failed to resolve partition target");
		if (msg) LLVMDisposeMessage(msg);
		LLVMDisposeModule(m);
		LLVMContextDispose(ctx);
		return NULL;
	}
	LLVMTargetMachineRef tm = LLVMCreateTargetMachine(target, w->triple, w->cpu, w->features,
		LLVMCodeGenLevelDefault, LLVMRelocDefault, LLVMCodeModelDefault);
	if (tm == NULL) {
		w->error = strdup("failed to create partition target machine");
	} else {
		if (LLVMTargetMachineEmitToFile(tm, m, (char *)w->path, LLVMObjectFile, &msg) != 0) {
			w->error = strdup(msg ? msg : "partition object emission failed");
			if (msg) LLVMDisposeMessage(msg);
		}
		LLVMDisposeTargetMachine(tm);
	}
	LLVMDisposeModule(m);
	LLVMContextDispose(ctx);
	return NULL;
}

int elisacoreParallelEmit(LLVMModuleRef m, LLVMTargetMachineRef tm, int jobs, long long min_weight,
                          const char *const *paths, char **error_out) {
	*error_out = NULL;
	long long *owners = NULL;
	size_t count = 0;
	long long total = part_function_owners(m, jobs, &owners, &count);
	if (total < min_weight) {
		free(owners);
		return 0;
	}
	LLVMMemoryBufferRef bc = LLVMWriteBitcodeToMemoryBuffer(m);
	if (bc == NULL) {
		free(owners);
		*error_out = strdup("failed to serialize module bitcode");
		return -1;
	}
	char *triple = LLVMGetTargetMachineTriple(tm);
	char *cpu = LLVMGetTargetMachineCPU(tm);
	char *features = LLVMGetTargetMachineFeatureString(tm);
	part_worker *workers = calloc((size_t)jobs, sizeof(part_worker));
	pthread_t *threads = calloc((size_t)jobs, sizeof(pthread_t));
	int *started = calloc((size_t)jobs, sizeof(int));
	pthread_attr_t attr;
	pthread_attr_init(&attr);
	// Codegen of deep functions recurses; give workers a generous stack.
	pthread_attr_setstacksize(&attr, 512u * 1024u * 1024u);
	for (int k = 0; k < jobs; k++) {
		part_worker *w = &workers[k];
		w->bitcode = LLVMGetBufferStart(bc);
		w->bitcode_len = LLVMGetBufferSize(bc);
		w->owners = owners;
		w->owner_count = count;
		w->part = k;
		w->triple = triple;
		w->cpu = cpu;
		w->features = features;
		w->path = paths[k];
		if (pthread_create(&threads[k], &attr, part_worker_run, w) == 0) {
			started[k] = 1;
		} else {
			part_worker_run(w);
		}
	}
	pthread_attr_destroy(&attr);
	int ok = 1;
	for (int k = 0; k < jobs; k++) {
		if (started[k]) {
			pthread_join(threads[k], NULL);
		}
		if (workers[k].error != NULL) {
			if (ok) {
				*error_out = workers[k].error;
				ok = 0;
			} else {
				free(workers[k].error);
			}
		}
	}
	free(started);
	free(threads);
	free(workers);
	LLVMDisposeMessage(triple);
	LLVMDisposeMessage(cpu);
	LLVMDisposeMessage(features);
	LLVMDisposeMemoryBuffer(bc);
	free(owners);
	return ok ? 1 : -1;
}
