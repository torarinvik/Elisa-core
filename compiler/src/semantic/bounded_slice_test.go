package semantic

import (
	"strings"
	"testing"
)

// A constant index into a view produced by a constant-bounded slice is provably
// in bounds (length is statically known), so it needs no Unsafe.UncheckedIndex
// grant — zero-cost in release, and the watchdog can verify it in debug.
func TestConstIndexIntoConstBoundedSliceIsProven(t *testing.T) {
	analyzeFunctionAnalysisTestSourceWithOptions(t, "bounded_slice_const_ok.elisa", `def f() -> i32:
    data: i32[16] = zeroed
    s = data[0:8]
    return s[5]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
}

// A runtime index into the same bounded slice is NOT proven and still requires
// the explicit unchecked-index opt-out (proof ends where the index is dynamic).
func TestRuntimeIndexIntoConstBoundedSliceStillGated(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "bounded_slice_runtime.elisa", `def f(i: usize) -> i32:
    data: i32[16] = zeroed
    s = data[0:8]
    return s[i]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("expected runtime index into bounded slice to remain gated, got:\n%s", allDiagnostics(result))
	}
}

// An out-of-range constant index must NOT be falsely proven by the static
// length: s[10] into a length-8 view is genuinely out of bounds.
func TestOutOfRangeConstIndexIntoBoundedSliceNotProven(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "bounded_slice_oob.elisa", `def f() -> i32:
    data: i32[16] = zeroed
    s = data[0:8]
    return s[10]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("expected out-of-range constant index to remain gated, got:\n%s", allDiagnostics(result))
	}
}
