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
