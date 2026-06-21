//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Lexicographic induction and multi-argument termination measures (docs/86 brick 86-7 extensions).
//
// `decreases` supports a TUPLE of components: the measure strictly decreases iff there exists an index k
// such that components 0..(k-1) are provably unchanged and component k strictly decreases. The tests
// below pin the Ackermann-function shape (two recursive calls, one with the first component strictly
// decreasing regardless of the second), lexicographic IH for inductive lemmas, and soundness negatives.

// POSITIVE: Ackermann termination with a 2-component lexicographic measure.
//
// ack(m, n) has two recursive calls:
//   - ack(m, n-1):           m unchanged, n decreases -> proven at k=1
//   - ack(m-1, ack(m,n-1)): m decreases by 1         -> proven at k=0
//
// Both calls are covered; the function terminates.
func TestTerminationProvenAckermann(t *testing.T) {
	src := `
def ack(m: usize, n: usize) -> usize:
    decreases (m, n)
    if m == 0:
        return n + 1
    if n == 0:
        return ack(m - 1, 1)
    return ack(m - 1, ack(m, n - 1))
`
	result := analyzeTreeTestSource(t, "term_ack.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("Ackermann with lexicographic (m, n) measure should verify, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if strings.Contains(f.Subject, "termination of ack") {
			proven++
		}
	}
	if proven == 0 {
		t.Fatalf("expected at least one proven termination obligation for ack, got %d: %+v", proven, result.ProofReport)
	}
}

// POSITIVE: first component strictly decreases while second grows freely.
// This is the key shape for the outer Ackermann call: (m-1, <anything>).
func TestTerminationProvenFirstComponentDecreasesSecondGrows(t *testing.T) {
	src := `
def step(a: usize, b: usize) -> usize:
    decreases (a, b)
    if a == 0:
        return b
    return step(a - 1, b + 1000)
`
	result := analyzeTreeTestSource(t, "term_first_dec.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("when first component strictly decreases, second may grow freely: got %v", errs)
	}
}

// SOUNDNESS (negative): first unchanged, second INCREASES — NOT lexicographically smaller.
// Must be rejected; the sequence (a, b+1) > (a, b) lexicographically.
func TestTerminationRefutedFirstSameSecondIncreases(t *testing.T) {
	src := `
def bad(a: usize, b: usize) -> usize:
    decreases (a, b)
    return bad(a, b + 1)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "term_lex_bad1.elisa", src)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove the `decreases` measure strictly decreases") {
		t.Fatalf("first-same second-increasing must be rejected as non-terminating; got: %s", errText)
	}
}

// SOUNDNESS (negative): first INCREASES, second decreases — first component dominates the ordering;
// (a+1, b-1) > (a, b) lexicographically. Must be rejected.
func TestTerminationRefutedFirstIncreasesSecondDecreases(t *testing.T) {
	src := `
def bad2(a: usize, b: usize) -> usize:
    decreases (a, b)
    if b == 0:
        return a
    return bad2(a + 1, b - 1)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "term_lex_bad2.elisa", src)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove the `decreases` measure strictly decreases") {
		t.Fatalf("first-increases/second-decreases must be rejected (not lexicographically smaller); got: %s", errText)
	}
}

// SOUNDNESS (negative): both components unchanged — measure does not strictly decrease.
func TestTerminationRefutedBothUnchanged(t *testing.T) {
	src := `
def spin2(a: usize, b: usize) -> usize:
    decreases (a, b)
    return spin2(a, b)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "term_lex_spin.elisa", src)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove the `decreases` measure strictly decreases") {
		t.Fatalf("both-unchanged must be rejected as non-terminating; got: %s", errText)
	}
}

// POSITIVE (inductive lemma, lexicographic IH): a lemma with decreases (a, b) may assume its own
// ensure (the inductive hypothesis) at a self-call that is lexicographically smaller. Here (a, b-1)
// < (a, b) because the first component is unchanged and the second strictly decreases.
func TestLexicographicIHAvailableAtSmallerCall(t *testing.T) {
	src := `
lemma lex_nonneg(a: i64, b: i64):
    requires a >= 0
    requires b >= 0
    ensure a >= 0
    decreases (a, b)
    if b == 0:
        pass
    else:
        lex_nonneg(a, b - 1)
`
	result := analyzeWithSMT(t, "lex_ih.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("inductive lemma with lexicographic (a,b) measure should verify, got: %v", errs)
	}
}

// SOUNDNESS (IH not granted at non-smaller call): a self-call with a LARGER second component while
// the first is unchanged is not lexicographically smaller. The termination check must reject the
// non-decreasing measure — confirming that no IH backdoor is opened at larger arguments.
func TestLexicographicIHNotAvailableAtLargerCall(t *testing.T) {
	src := `
lemma lex_bad_ih(a: i64, b: i64):
    requires a >= 0
    requires b >= 0
    ensure a >= 0
    decreases (a, b)
    lex_bad_ih(a, b + 1)
`
	result := analyzeWithSMT(t, "lex_ih_bad.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatalf("a lemma whose self-call has a LARGER argument must be rejected; IH must not be granted")
	}
}
