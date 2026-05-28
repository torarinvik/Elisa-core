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

// `get arr[a:b]` is a bounds-checked slice: it needs no unchecked-slice grant,
// and the bounded view it yields carries a static length so a constant inner
// index is zero-cost.
func TestGetSliceIsCheckedAndInnerIndexProven(t *testing.T) {
	analyzeFunctionAnalysisTestSourceWithOptions(t, "get_slice_ok.elisa", `def f(xs: darray[i32]&) -> i32:
    s = get xs[0:3] else return -1
    return s[0] + s[1] + s[2]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
}

// `get arr[a:b]` with no else propagates absence; the enclosing function must
// return an optional. A constant inner index stays zero-cost.
func TestGetSlicePropagationRequiresOptionalReturn(t *testing.T) {
	analyzeFunctionAnalysisTestSourceWithOptions(t, "get_slice_prop.elisa", `def f(xs: darray[i32]&) -> i32?:
    s = get xs[0:3]
    return s[1]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
}

// `if let s = arr[a:b]:` is a bounds-checked slice binding: it typechecks
// without an unchecked-slice grant, and inside the block the bounded view's
// static length makes constant inner indexing zero-cost.
func TestIfLetSliceBindsBoundedViewAndProvesInnerIndex(t *testing.T) {
	analyzeFunctionAnalysisTestSourceWithOptions(t, "iflet_slice_ok.elisa", `def f(xs: darray[i32]&) -> i32:
    if let s = xs[0:3]:
        return s[0] + s[1] + s[2]
    else:
        return -1
`, AnalyzeOptions{EnforceUnsafePermissions: true})
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
