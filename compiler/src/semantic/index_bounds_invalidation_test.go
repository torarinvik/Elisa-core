package semantic

import (
	"strings"
	"testing"
)

// A bounds proof must not survive a container mutation that can shrink it:
// after arr.truncate(0), the cached `i < arr.count` proof is stale and the
// index must again require the UncheckedIndex opt-out (deep audit #5).
func TestIndexBoundsInvalidatedByContainerMutation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "index_bounds_mutation.elisa", `def read_at(owner: mutable Arena&, xs: mutable darray[u8], i: usize) -> u8:
    if i < xs.count:
        in owner:
            xs.truncate(0)
        return xs[i]
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("expected stale bounds proof after truncate to require UncheckedIndex, got:\n%s", allDiagnostics(result))
	}
}

// A bounds proof must not survive reassignment of the index variable: after
// `i <- i + 99`, the cached `i < xs.count` proof no longer constrains the value.
func TestIndexBoundsInvalidatedByIndexReassignment(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "index_bounds_reassign.elisa", `def read_at(xs: darray[u8], i: mutable usize) -> u8:
    if i < xs.count:
        i <- i + 99
        return xs[i]
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("expected stale bounds proof after index reassignment to require UncheckedIndex, got:\n%s", allDiagnostics(result))
	}
}

// Sanity: an un-mutated proven index stays proven (no false invalidation).
func TestIndexBoundsSurviveWithoutMutation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "index_bounds_clean.elisa", `def read_at(xs: darray[u8], i: usize) -> u8:
    if i < xs.count:
        return xs[i]
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("expected a clean proven index to stay proven, got:\n%s", allDiagnostics(result))
	}
}
