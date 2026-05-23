package semantic

import (
	"strings"
	"testing"
)

// An interior reference into a darray dangles if the darray is grown afterward
// (the buffer can relocate). Using the reference after a push must be rejected as
// a stale reference, even when the index was bounds-proven (deep audit #6).
func TestDarrayInteriorRefInvalidatedByPush(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "darray_interior_ref_push.elisa", `def f(owner: mutable Arena&, i: usize) -> u8:
    xs: mutable darray[u8] = []
    in owner:
        xs.push(1)
        if i < xs.count:
            r: u8& = xs[i].ref[u8&]
            xs.push(2)
            return r
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected interior darray ref used after push to be a stale reference, got:\n%s", allDiagnostics(result))
	}
}

// An interior reference into a fixed array does NOT relocate, so taking one and
// later writing through the array must not be flagged as stale.
func TestFixedArrayInteriorRefNotInvalidated(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "fixed_array_interior_ref.elisa", `def f() -> u8:
    xs: mutable array[u8, 4] = [1, 2, 3, 4]
    r: mutable u8& = xs[0].ref[mutable u8&]
    xs[1] <- 9
    return r
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected fixed-array interior ref to stay valid (no relocation), got:\n%s", allDiagnostics(result))
	}
}
