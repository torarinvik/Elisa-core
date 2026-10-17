//go:build cgo

package semantic

import "testing"

func TestSMTReadOnlyAssertCountingFrame(t *testing.T) {
	result := analyzeContractStrict(t, "assert_frame.elisa", `def byte_value(text: sview, index: usize) -> usize:
    requires index < text.len
    return text[index].usize()

def same_after_loop(text: sview) -> usize:
    requires text.len > 0
    ensure result == byte_value(text, 0.usize())
    before: usize = byte_value(text, 0.usize())
    count: mutable usize = 0
    assert count == 0
    for index in 0..<3 |count|:
        assert index < 3
        count <- count + 1.usize()
    return before
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("read-only assertions broke view call congruence:\n%s", allDiagnostics(result))
	}
}
