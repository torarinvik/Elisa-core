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

// The headline: a quantified ENSURE postcondition `ensure forall k: (0 <= k and k < n) implies
// result[k] == 0` discharges from the loop's proven invariant at the return site. The loop's proven
// inductive invariant `forall k: 0<=k<i implies arr[k]==0` is seeded as a post-loop assert fact via
// seedLoopExitFacts, together with the exit condition `not (i < n)` (i.e. i >= n). At `return arr`
// the ensure discharges: `result` substitutes to `arr`, so the goal is `forall k: 0<=k<n => arr[k]==0`,
// which follows from the seeded fact `forall k: 0<=k<i => arr[k]==0` plus `i >= n` (n <= i).
func TestQuantifiedEnsureOverResultArrayDischargesFromLoopInvariant(t *testing.T) {
	src := `
def fill_zero(arr: darray[i64], n: usize) -> darray[i64]:
    ensure forall k: (0 <= k and k < n) implies result[k] == 0
    i: mutable usize = 0
    while i < n:
        invariant forall k: (0 <= k and k < i) implies arr[k] == 0
        arr[i] <- 0
        i <- i + 1
    return arr
`
	result := analyzeLoopInvariantWithSMT(t, "quant_ensure_result.elisa", src, true)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors: quantified ensure should discharge from loop's proven invariant; got:\n%s\nproof report: %+v", errs, result.ProofReport)
	}
	// The ensure must be proven (not left as a runtime check) by the SMT tier.
	provenCount := 0
	for _, f := range result.ProofReport {
		if f.Subject == "ensure fill_zero" && (f.Outcome == ProofProvenSMT || f.Outcome == ProofProvenLinear) {
			provenCount++
		}
	}
	if provenCount == 0 {
		t.Fatalf("expected the quantified ensure to be proven statically (SMT), not left as runtime check; proof report: %+v", result.ProofReport)
	}
}

// Soundness: a FALSE quantified ensure (claims result[k] == 1 but body fills with 0) must NOT
// discharge. Under -strict it must be a hard error (refuted or unprovable).
func TestQuantifiedEnsureOverResultArrayFalseRejected(t *testing.T) {
	src := `
def fill_zero_bad(arr: darray[i64], n: usize) -> darray[i64]:
    ensure forall k: (0 <= k and k < n) implies result[k] == 1
    i: mutable usize = 0
    while i < n:
        invariant forall k: (0 <= k and k < i) implies arr[k] == 0
        arr[i] <- 0
        i <- i + 1
    return arr
`
	result := analyzeLoopInvariantWithSMT(t, "quant_ensure_result_false.elisa", src, true)
	// Under -strict the unprovable/refuted ensure must be an error.
	if len(result.Errors()) == 0 {
		t.Fatalf("expected -strict to reject a false quantified ensure (body fills 0, ensure claims 1); proof report: %+v", result.ProofReport)
	}
	// The ensure must NOT be proven.
	for _, f := range result.ProofReport {
		if f.Subject == "ensure fill_zero_bad" && (f.Outcome == ProofProvenSMT || f.Outcome == ProofProvenLinear) {
			t.Fatalf("false quantified ensure must not be proven; proof report: %+v", result.ProofReport)
		}
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
