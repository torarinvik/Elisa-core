//go:build cgo

package semantic

import "testing"

// The headline: a QUANTIFIED loop invariant over the array being filled is proven inductive. The body
// stores `arr[i] <- 0` and advances `i`; the invariant `forall k: 0<=k<i implies arr[k] == 0` is
// established (vacuously, i=0) and preserved (the SMT array-store model `(store arr i 0)` discharges
// the inductive step: the just-written cell is 0, the rest by the hypothesis).
func TestQuantifiedLoopInvariantOverArrayFillPreserved(t *testing.T) {
	src := `
def fill_zero(arr: darray[i64], n: usize):
    i: mutable usize = 0
    while i < n:
        invariant forall k: (0 <= k and k < i) implies arr[k] == 0
        arr[i] <- 0
        i <- i + 1
`
	result := analyzeLoopInvariantWithSMT(t, "quant_loop_fill.elisa", src, false)
	if got := countLoopInvariantProof(result, "preserve", ProofProvenSMT); got != 1 {
		t.Fatalf("expected the quantified array-fill invariant to be proven preserved via the SMT store model, got %d: %+v", got, result.ProofReport)
	}
	if got := countLoopInvariantProof(result, "establish", ProofProvenSMT); got != 1 {
		t.Fatalf("expected the quantified invariant to be established (vacuously) on entry, got %d: %+v", got, result.ProofReport)
	}
}

// A constant fill value also works: `forall k: 0<=k<i implies arr[k] == 7` across `arr[i] <- 7`.
func TestQuantifiedLoopInvariantConstantFillPreserved(t *testing.T) {
	src := `
def fill_seven(arr: darray[i64], n: usize):
    i: mutable usize = 0
    while i < n:
        invariant forall k: (0 <= k and k < i) implies arr[k] == 7
        arr[i] <- 7
        i <- i + 1
`
	result := analyzeLoopInvariantWithSMT(t, "quant_loop_seven.elisa", src, false)
	if got := countLoopInvariantProof(result, "preserve", ProofProvenSMT); got != 1 {
		t.Fatalf("expected the constant-fill quantified invariant to be proven preserved, got %d: %+v", got, result.ProofReport)
	}
}

// Soundness: a FALSE quantified invariant over a fill must NOT be proven. The body writes 0 but the
// invariant claims every filled cell is 1 — z3 finds a counterexample (the just-written cell is 0),
// so preservation declines to the runtime check.
func TestQuantifiedLoopInvariantFalseFillDeclined(t *testing.T) {
	src := `
def fill_zero_bad(arr: darray[i64], n: usize):
    i: mutable usize = 0
    while i < n:
        invariant forall k: (0 <= k and k < i) implies arr[k] == 1
        arr[i] <- 0
        i <- i + 1
`
	result := analyzeLoopInvariantWithSMT(t, "quant_loop_false.elisa", src, false)
	if got := countLoopInvariantProof(result, "preserve", ProofProvenSMT); got != 0 {
		t.Fatalf("a false quantified fill invariant must NOT be proven preserved (the cell is 0, not 1): %+v", result.ProofReport)
	}
	if got := countLoopInvariantProof(result, "preserve", ProofRuntime); got != 1 {
		t.Fatalf("expected the false quantified invariant to fall back to a runtime preservation check, got %d: %+v", got, result.ProofReport)
	}
}
