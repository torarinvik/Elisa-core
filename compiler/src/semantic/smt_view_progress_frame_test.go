//go:build cgo

package semantic

import (
	"fmt"
	"testing"
)

func TestSMTViewFrameAssumeProgressReads(t *testing.T) {
	for _, expected := range []int{0, 1} {
		source := fmt.Sprintf(`def byte_value(view: sview, index: usize) -> usize:
    requires index < view.len
    return view[index].usize()
def same(view: sview) -> usize:
    requires view.len > 1
    ensure result == byte_value(view, %d.usize())
    before: usize = byte_value(view, 0.usize())
    trusted Unsafe.AssumeProgress:
        counter: mutable usize = 0
        while counter < 2:
            decreases 2 - counter
            counter <- counter + 1
    return before
`, expected)
		r := analyzeContractStrict(t, "progress_frame.elisa", source)
		if rejected := ensureFailuresFor(r.Errors(), "same") != 0; rejected != (expected != 0) {
			t.Fatalf("index %d: %s", expected, allDiagnostics(r))
		}
	}
}

func TestSMTViewFrameAssumeProgressDoesNotHideCallbacks(t *testing.T) {
	r := analyzeContractStrict(t, "progress_callback.elisa", `def byte_value(view: sview, index: usize) -> usize:
    requires index < view.len
    return view[index].usize()
def stale(view: sview, callback: fn() -> void) -> usize:
    requires view.len > 0
    ensure result == byte_value(view, 0.usize())
    before: usize = byte_value(view, 0.usize())
    trusted Unsafe.AssumeProgress:
        callback()
    return before
`)
	if ensureFailuresFor(r.Errors(), "stale") == 0 {
		t.Fatalf("callback must keep frame opaque: %s", allDiagnostics(r))
	}
}
