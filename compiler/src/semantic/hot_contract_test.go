package semantic

import (
	"strings"
	"testing"
)

// The @hot fast contract (docs/70): a hot function over preallocated data, doing only
// indexed arithmetic, carries no banned effect and analyzes cleanly — the fast path needs
// no ceremony.
func TestAnalyzeHotContractAcceptsAllocationFreeKernel(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "hot_clean.elisa", `@hot
def dot(xs: darray[i64]&, ys: darray[i64]&, n: usize) -> i64:
    acc: mutable i64 = 0
    for i in 0..<n:
        acc <- acc + xs[i] * ys[i]
    return acc
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean @hot kernel to analyze without errors, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Region allocation is the frictionless default, so plain @hot ALLOWS it — only @hot(noalloc)
// is the strict zero-allocation kernel. A @hot function that grows a darray analyzes cleanly.
func TestAnalyzeHotContractAllowsAllocation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "hot_alloc_ok.elisa", `@hot
def grow() -> i64:
    can Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(7)
        return xs[0]
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected plain @hot to permit region allocation, got:\n%s", strings.Join(errs, "\n"))
	}
}

// @hot(noalloc) is the strict variant — allocation on the hot path is rejected so the kernel
// can't silently grow a region.
func TestAnalyzeHotContractNoAllocRejectsAllocation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "hot_alloc.elisa", `@hot(noalloc)
def bad() -> i64:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(7)
        return xs[0]
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "@hot(noalloc) function") || !strings.Contains(all, "Memory.Allocate") {
		t.Fatalf("expected a @hot(noalloc) allocation rejection, got:\n%s", all)
	}
}

// The ban is transitive: a @hot(noalloc) function that calls an allocating function is
// rejected too, so the guarantee holds across call boundaries.
func TestAnalyzeHotContractNoAllocRejectsTransitiveAllocation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "hot_transitive.elisa", `def allocates() -> i64:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(1)
        return xs[0]

@hot(noalloc)
def caller() -> i64:
    can Memory.Allocate, Abort.Panic:
        return allocates()
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `@hot(noalloc) function "caller"`) || !strings.Contains(all, "Memory.Allocate") {
		t.Fatalf("expected a transitive @hot(noalloc) allocation rejection, got:\n%s", all)
	}
}

// Raw-pointer chasing is rejected — the pointer-jungle traversal is exactly what @hot bans.
func TestAnalyzeHotContractRejectsRawPointerChase(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "hot_rawptr.elisa", `@hot
def chase(p: heap i64&) -> i64:
    can Unsafe.PointerArithmetic, Unsafe.PointerCast:
        return (p + 1u).u64().i64()
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "@hot function") || !strings.Contains(all, "Unsafe.PointerArithmetic") {
		t.Fatalf("expected a @hot raw-pointer rejection, got:\n%s", all)
	}
}

// Fast-unsafe that REMOVES overhead stays allowed: an unchecked index in a @hot loop is
// the fast pattern, not the slow one, so it must not be banned.
func TestAnalyzeHotContractAllowsUncheckedIndex(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "hot_unchecked.elisa", `@hot
def sum_unchecked(xs: darray[i64]&, n: usize) -> i64:
    acc: mutable i64 = 0
    trusted Unsafe.UncheckedIndex:
        for i in 0..<n:
            acc <- acc + xs[i]
    return acc
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected @hot with UncheckedIndex (fast-unsafe) to be allowed, got:\n%s", strings.Join(errs, "\n"))
	}
}
