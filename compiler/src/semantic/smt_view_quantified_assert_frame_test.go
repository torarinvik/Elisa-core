package semantic

import (
	"fmt"
	"testing"
)

func TestSMTViewFrameRuntimeQuantifierAssertions(t *testing.T) {
	for _, condition := range []string{"forall index in 0..<1: index < 1", "exists index in 0..<1: index == 0"} {
		for _, expectedIndex := range []int{0, 1} {
			source := fmt.Sprintf(`def byte_value(text: sview, index: usize) -> usize:
    requires index < text.len
    return text[index].usize()
def same(text: sview) -> usize:
    requires text.len > 1
    ensure result == byte_value(text, %d.usize())
    before: usize = byte_value(text, 0.usize())
    assert %s
    return before
`, expectedIndex, condition)
			r := analyzeContractStrict(t, "quantifier_frame.elisa", source)
			if rejected := ensureFailuresFor(r.Errors(), "same") != 0; rejected != (expectedIndex != 0) {
				t.Fatalf("%s, index %d: rejected %v:\n%s", condition, expectedIndex, rejected, allDiagnostics(r))
			}
		}
	}
}

func TestSMTViewFrameQuantifierCallbackShadow(t *testing.T) {
	r := analyzeContractStrict(t, "quantifier_callback.elisa", `module Callbacks:
    public:
        def run() -> bool:
            return true
        def stale(text: sview, run: fn() -> bool) -> u8:
            requires text.len > 0
            ensure result == text[0]
            before: u8 = text[0]
            assert forall index in 0..<1: run()
            return before
`)
	if ensureFailuresFor(r.Errors(), "stale") == 0 {
		t.Fatalf("unknown callback inherited namespace helper effects:\n%s", allDiagnostics(r))
	}
}
