package semantic

import (
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
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
        x: i32& @scratch = new[scratch] 5
        _ = x
`)
}

// `leak` discharges the must-consume obligation (so the region is no longer
// reported unconsumed), but it is an audited memory-safety opt-out gated by
// Unsafe.Leak — ungranted use must be flagged.
// The explicit region-lifecycle statements `mark`/`restore`/`reset`/`leak`/
// `promote`/`adopt` were removed (region high-water rewinds and lifetime/ownership
// transfer are inferred, or expressed with nested scoped `region NAME:` blocks).
// Scoped/owned regions, `destroy`, and explicit size/backing are unchanged.
func TestRemovedRegionLifecycleStatementsAreRejected(t *testing.T) {
	prelude := "def f() -> void:\n    region scratch(64)\n    region other(64)\n"
	for _, stmt := range []string{
		"    mark scratch as cp\n",
		"    restore scratch from cp\n",
		"    reset scratch\n",
		"    leak scratch\n",
		"    promote scratch into other\n",
		"    adopt scratch into other\n",
	} {
		l := lexer.New("removed_region_lifecycle.elisa", []byte(prelude+stmt))
		p := parser.New(l.Tokenize())
		_ = p.ParseFile("removed_region_lifecycle.elisa")
		errs := p.Errors()
		if len(errs) == 0 {
			t.Fatalf("expected removed region-lifecycle statement to be rejected:\n%s", stmt)
		}
		if !strings.Contains(strings.Join(errs, "\n"), "has been removed") {
			t.Fatalf("expected removal diagnostic for %q, got: %v", strings.TrimSpace(stmt), errs)
		}
	}
}

// A closure that captures a region-dependent value carries that region
// dependency: using the closure after the region is destroyed is a
// use-after-free and must be rejected (the dep must not be laundered through
// the lambda capture).
func TestClosureCapturingRegionDepRejectedAfterDestroy(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "region_closure_uaf.elisa", `struct RegionNode[@owner]:
    next: RegionNode&? @owner
    value: i32

def f(seed: i32) -> i32:
    region r(64)
    first: RegionNode[r]& @r = new[r] RegionNode[r]{next: null, value: seed}
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
	analyzeTreeTestSource(t, "region_closure_ok.elisa", `struct RegionNode[@owner]:
    next: RegionNode&? @owner
    value: i32

def f(seed: i32) -> i32:
    region r(64)
    first: RegionNode[r]& @r = new[r] RegionNode[r]{next: null, value: seed}
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
    g: Guard& @r = new[r] Guard(true)
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
