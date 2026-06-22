package semantic

import (
	"strings"
	"testing"
)

// These tests cover the enforceable performance contracts (@noalloc / @inbounds / @nolock): the
// perf analog of -strict. Each contract has a POSITIVE (a clean function compiles) and a NEGATIVE
// (a violating function is a hard error naming the offending site).

func perfContractErrors(t *testing.T, filename, src string) string {
	t.Helper()
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, filename, src)
	return strings.Join(result.Errors(), "\n")
}

func TestNoallocCleanRegionFunctionCompiles(t *testing.T) {
	errs := perfContractErrors(t, "noalloc_clean.elisa", `struct Node:
	v: int

@noalloc
def sum(xs: int[4]&) -> int:
	total: mutable int = 0
	for x in xs:
		total <- total + x
	return total

@noalloc
def region_alloc_ok() -> int:
	n = new Node(5)
	return n.v
`)
	if errs != "" {
		t.Fatalf("expected clean @noalloc functions (region/stack only) to compile, got errors:\n%s", errs)
	}
}

func TestNoallocHeapAllocationIsError(t *testing.T) {
	errs := perfContractErrors(t, "noalloc_heap.elisa", `@noalloc
def dirty() -> int:
	can Memory.Allocate:
		x: mutable darray[int] = []
		x.push(1)
		return x.count
`)
	if !strings.Contains(errs, "@noalloc function \"dirty\" allocates or frees on the heap") {
		t.Fatalf("expected @noalloc heap-allocation error, got:\n%s", errs)
	}
	if !strings.Contains(errs, "Memory.Allocate") {
		t.Fatalf("expected the offending effect named, got:\n%s", errs)
	}
}

func TestNoallocTransitiveHeapAllocationIsError(t *testing.T) {
	// A @noalloc function that calls an allocating callee fails too: the contract is judged against
	// the transitive (whole call-tree) permission set, so it cannot be vacuously satisfied.
	errs := perfContractErrors(t, "noalloc_transitive.elisa", `def helper() -> int:
	can Memory.Allocate:
		x: mutable darray[int] = []
		x.push(1)
		return x.count

@noalloc
def caller() -> int:
	return helper()
`)
	if !strings.Contains(errs, "@noalloc function \"caller\" allocates or frees on the heap") {
		t.Fatalf("expected transitive @noalloc error, got:\n%s", errs)
	}
}

func TestInboundsProvenIndexCompiles(t *testing.T) {
	errs := perfContractErrors(t, "inbounds_clean.elisa", `@inbounds
def sum(xs: darray[int]&) -> int:
	total: mutable int = 0
	for x in xs:
		total <- total + x
	return total

@inbounds
def guarded(xs: darray[int]&, i: usize) -> int:
	if i < xs.count:
		return xs[i]
	return 0
`)
	if errs != "" {
		t.Fatalf("expected clean @inbounds functions (iterated / guarded index) to compile, got errors:\n%s", errs)
	}
}

func TestInboundsUnprovenIndexIsError(t *testing.T) {
	errs := perfContractErrors(t, "inbounds_unproven.elisa", `@inbounds
def unproven(xs: darray[int]&, i: usize) -> int:
	return xs[i]
`)
	if !strings.Contains(errs, "@inbounds function \"unproven\" has an index access that is not statically proven in-bounds") {
		t.Fatalf("expected @inbounds unproven-index error, got:\n%s", errs)
	}
	// The error must name the offending index site (line 3 of the fixture).
	if !strings.Contains(errs, "inbounds_unproven.elisa:3:") {
		t.Fatalf("expected the offending index site named, got:\n%s", errs)
	}
}

func TestNolockCleanFunctionCompiles(t *testing.T) {
	errs := perfContractErrors(t, "nolock_clean.elisa", `@nolock
def pure(x: int) -> int:
	return x + 1
`)
	if errs != "" {
		t.Fatalf("expected clean @nolock function to compile, got errors:\n%s", errs)
	}
}

func TestNolockLockAcquireIsError(t *testing.T) {
	errs := perfContractErrors(t, "nolock_lock.elisa", `@nolock
def locky() -> void:
	can Sync.Lock, Sync.Unlock:
		pass
`)
	if !strings.Contains(errs, "@nolock function \"locky\" acquires a lock or waits on a condition") {
		t.Fatalf("expected @nolock lock-acquire error, got:\n%s", errs)
	}
	if !strings.Contains(errs, "Sync.Lock") {
		t.Fatalf("expected the offending effect named, got:\n%s", errs)
	}
}

func TestPerfContractAnnotationsRejectArguments(t *testing.T) {
	errs := perfContractErrors(t, "perf_args.elisa", `@noalloc(foo)
def f() -> int:
	return 0
`)
	if !strings.Contains(errs, "@noalloc on function \"f\" does not take arguments") {
		t.Fatalf("expected argument rejection, got:\n%s", errs)
	}
}
