//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// A string view's `.len` is an i64. Its SMT length symbol used to carry only the usize bound
// (< 2^64), so `v.len - i` — i64 arithmetic modeled with two's-complement wrap — could "wrap"
// for an impossible length >= 2^63 and `decreases v.len - i` never proved, even for the plain
// counting loop. The bound is now i64.max.

func TestLoopDecreasesSViewLenMinusUnsignedIndexProves(t *testing.T) {
	src := `def scan(v: sview, w: sview) -> bool:
    i: mutable usize = 0
    while i < v.len:
        decreases v.len - i
        return false if i >= w.len
        i <- i + 1
    return true
`
	result := analyzeContractStrict(t, "sview_len_measure.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, "decreases") {
			t.Fatalf("`decreases v.len - i` over an immutable view must prove, got: %v", e)
		}
	}
	if got := countLoopTerminationProof(result, ProofProvenSMT); got != 1 {
		t.Fatalf("expected the view-length loop measure proven by SMT, got %d: %+v", got, result.ProofReport)
	}
}

// SOUNDNESS: with an i64 index unbounded below, `v.len - i` really can overflow i64 (i near
// i64.min), so the tighter length bound must not make this measure provable.
func TestLoopDecreasesSViewLenMinusSignedUnboundedIndexStillDeclines(t *testing.T) {
	src := `def scan(v: sview, start: i64) -> bool:
    i: mutable i64 = start
    while i < v.len:
        decreases v.len - i
        i <- i + 1
    return true
`
	result := analyzeContractStrict(t, "sview_len_signed_measure.elisa", src)
	if got := countLoopTerminationProof(result, ProofProvenSMT); got != 0 {
		t.Fatalf("an i64 index unbounded below can overflow `v.len - i`; the measure must not prove, got %d proofs", got)
	}
}
