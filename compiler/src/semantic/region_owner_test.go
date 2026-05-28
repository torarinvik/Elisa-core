package semantic

import (
	"strings"
	"testing"
)

// A region owns a bulk allocation and is an affine must-consume owner: a bare
// `region NAME(...)` that is never `destroy`ed is a leak and must be a compile
// error rather than silently allowed.
func TestNonScopedRegionMustBeConsumed(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "region_leak.elisa", `def f() -> void:
    region scratch(64)
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "region owner") || !strings.Contains(all, "must be consumed before scope exit") {
		t.Fatalf("expected an undestroyed region to error as must-be-consumed; got: %s", all)
	}
}

// Destroying the region on the (only) path discharges the obligation.
func TestNonScopedRegionDestroyedIsClean(t *testing.T) {
	analyzeTreeTestSource(t, "region_destroyed.elisa", `def f() -> void:
    region scratch(64)
    destroy scratch
`)
}

// The scoped `region NAME(cap):` block discharges the owner automatically at
// block exit (RAII), so no explicit destroy is required.
func TestScopedRegionAutoDischarges(t *testing.T) {
	analyzeTreeTestSource(t, "region_scoped.elisa", `def f() -> void:
    region scratch(64):
        x: scratch i32& = new[scratch] 5
        _ = x
`)
}

// `leak` discharges the must-consume obligation (so the region is no longer
// reported unconsumed), but it is an audited memory-safety opt-out gated by
// Unsafe.Leak — ungranted use must be flagged.
func TestLeakDischargesButRequiresUnsafeLeak(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "region_leak_stmt.elisa", `def f() -> void:
    region scratch(64)
    leak scratch
`)
	if errs := strings.Join(result.Errors(), "\n"); strings.Contains(errs, "must be consumed before scope exit") {
		t.Fatalf("leak should satisfy the must-consume obligation; got errors: %s", errs)
	}
	// The Unsafe.Leak requirement is a warning in non-enforcing mode (a hard
	// error under EnforceUnsafePermissions, like other Unsafe.* ops).
	if warns := strings.Join(result.Warnings(), "\n"); !strings.Contains(warns, "leak requires") || !strings.Contains(warns, "Unsafe.Leak") {
		t.Fatalf("expected leak to require Unsafe.Leak; got warnings: %s", warns)
	}
}

// With the grant, leak is fully clean.
func TestLeakWithGrantIsClean(t *testing.T) {
	analyzeTreeTestSource(t, "region_leak_granted.elisa", `def f() -> void:
    region scratch(64)
    trusted Unsafe.Leak:
        leak scratch
`)
}

// A closure that captures a region-dependent value carries that region
// dependency: using the closure after the region is destroyed is a
// use-after-free and must be rejected (the dep must not be laundered through
// the lambda capture).
func TestClosureCapturingRegionDepRejectedAfterDestroy(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "region_closure_uaf.elisa", `struct RegionNode in owner:
    next: owner RegionNode&?
    value: i32

def f(seed: i32) -> i32:
    region r(64)
    first: r RegionNode[r]& = new[r] RegionNode[r](null, seed)
    g: func() -> i32 = lambda () => first.value
    destroy r
    return g()
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "cannot be used") || !strings.Contains(all, "region") {
		t.Fatalf("expected closure-after-destroy to be rejected as a region use-after-free; got: %s", all)
	}
}

// The same closure used while the region is still live is fine.
func TestClosureCapturingRegionDepCleanBeforeDestroy(t *testing.T) {
	analyzeTreeTestSource(t, "region_closure_ok.elisa", `struct RegionNode in owner:
    next: owner RegionNode&?
    value: i32

def f(seed: i32) -> i32:
    region r(64)
    first: r RegionNode[r]& = new[r] RegionNode[r](null, seed)
    g: func() -> i32 = lambda () => first.value
    out: i32 = g()
    destroy r
    return out
`)
}

// Approach A: `return move <region>` transfers ownership. The factory consumes
// the region locally (clean), the caller inherits the must-consume obligation,
// and the received region is a first-class owner that `destroy` discharges.
func TestRegionMoveReturnTransfersOwnership(t *testing.T) {
	// Factory alone is clean — the return consumes the region.
	analyzeTreeTestSource(t, "region_factory.elisa", `def make_arena(n: usize) -> Arena:
    region r(n)
    return move r
`)
	// Caller that forgets to consume the received region leaks it.
	leak := analyzeTreeTestSourceWithSemanticErrors(t, "region_recv_leak.elisa", `def make_arena(n: usize) -> Arena:
    region r(n)
    return move r

def use() -> void:
    a: Arena = make_arena(64)
`)
	if all := strings.Join(leak.Errors(), "\n"); !strings.Contains(all, "region owner") || !strings.Contains(all, "must be consumed") {
		t.Fatalf("expected received region to inherit must-consume; got: %s", all)
	}
	// Caller that destroys the received region is clean.
	analyzeTreeTestSource(t, "region_recv_ok.elisa", `def make_arena(n: usize) -> Arena:
    region r(n)
    return move r

def use() -> void:
    a: Arena = make_arena(64)
    destroy a
`)
}

// A function that transfers an owned region on one path but returns a plain
// value on another is rejected (the caller's inherited obligation would be
// unsound otherwise).
func TestRegionMixedOwnedAndPlainReturnRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "region_mixed_return.elisa", `def make(c: bool, borrowed: Arena) -> Arena:
    region r(64)
    if c:
        return move r
    return borrowed
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "owned region") {
		t.Fatalf("expected mixed owned/plain returns to be rejected; got: %s", all)
	}
}

// A region is a plain-bytes store: it frees its contents in bulk without
// consuming them. Allocating a linear (must-consume) handle into a region is
// therefore rejected — it could never be consumed and bulk-free would drop the
// must-consume obligation.
func TestLinearValueCannotBeAllocatedIntoRegion(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "region_linear_alloc.elisa", `linear struct Guard:
    armed: bool

def f() -> void:
    region r(64)
    g: r Guard& = new[r] Guard(true)
    _ = g
    destroy r
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "linear handles into region") {
		t.Fatalf("expected linear-value-into-region to be rejected; got: %s", all)
	}
}

// Destroying on only some branches still leaks on the others (relies on the
// branch-join meet treating consume-on-all-arms as consumed and consume-on-
// some-arms as still-live).
func TestRegionDestroyedOnSomeBranchesErrors(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "region_partial.elisa", `def f(c: bool) -> void:
    region scratch(64)
    if c:
        destroy scratch
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "must be consumed before scope exit") {
		t.Fatalf("expected partial region destroy to error; got: %s", all)
	}
}
