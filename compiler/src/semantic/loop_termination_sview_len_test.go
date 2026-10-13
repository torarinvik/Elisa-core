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
	// `i < v.len` compares unsigned (codegen converts to the left type), so a view length of -1
	// would keep the loop running while `v.len - i` is negative: the measure needs v.len >= 0.
	src := `def scan(v: sview, w: sview) -> bool:
    requires v.len >= 0
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

// A body-local whose initializer calls a pure helper on a computed rvalue cannot touch the
// measure (`digits - index`, both non-reference locals/params), so it no longer blocks the proof.
func TestLoopDecreasesAdmitsCallInitializedBodyLocal(t *testing.T) {
	src := `def hv(b: u8) -> i64:
    return b.i64()

def sum(text: sview, digits: usize) -> i64:
    value: mutable i64 = 0
    index: mutable usize = 0
    while index < digits:
        decreases digits - index
        d: i64 = hv(text[index])
        return -1 if d < 0
        value <- value + d
        index <- index + 1
    return value
`
	result := analyzeContractStrict(t, "call_local_measure.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, "decreases") {
			t.Fatalf("a call-initialized body local must not block the measure proof, got: %v", e)
		}
	}
}

// SOUNDNESS: passing the measure variable to a mutable reference parameter can rewind it.
func TestLoopDecreasesRejectsMeasurePassedToMutableRef(t *testing.T) {
	src := `def bump(x: mutable usize&) -> i64:
    x <- x - 1
    return 0

def spin(digits: usize) -> i64:
    index: mutable usize = 0
    while index < digits:
        decreases digits - index
        d: i64 = bump(index)
        index <- index + 1
    return 0
`
	result := analyzeContractStrict(t, "mutref_measure.elisa", src)
	if got := countLoopTerminationProof(result, ProofProvenSMT); got != 0 {
		t.Fatalf("a measure variable passed to a mutable reference must not prove, got %d proofs", got)
	}
}
