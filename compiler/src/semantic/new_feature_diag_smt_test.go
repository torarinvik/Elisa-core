//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// --- 2. QUANTIFIED STRUCT INVARIANTS -------------------------------------------------------------

// A quantified container refinement of the `forall i in xs.indices: P` shape (the same shape a struct
// field invariant uses) reports the violating index AND the selected element when it fails. This
// exercises the array-index quantifier counterexample extraction reached via the unified entry point.
func TestStructStyleIndexInvariantReportsIndexWitness(t *testing.T) {
	requireZ3(t)
	src := `
law AllNonNeg(self: darray[i64]) = forall i in self.indices: self[i] >= 0

def check(xs: darray[i64]) -> void:
    requires xs.count == 3
    y: darray[i64] is AllNonNeg = xs
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "newdiag_struct_idx.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(result)
	if !contains(diags, "it can fail when") {
		t.Fatalf("expected the failed index invariant to include a counterexample, got: %s", diags)
	}
	if !strings.Contains(diags, "i=") || !strings.Contains(diags, "xs[i]=") {
		t.Fatalf("expected the witness to name the index `i` and the element `xs[i]`, got: %s", diags)
	}
}

// --- 3. SET / DICT QUANTIFIERS -------------------------------------------------------------------

// A failed `forall x in xs` container (element) quantifier refinement reports a concrete violating
// element witness via the set/dict counterexample path.
func TestSetElementQuantifierReportsElementWitness(t *testing.T) {
	requireZ3(t)
	src := `
law AllPositive(self: darray[i64]) = forall x in self: x > 0

def check(xs: darray[i64]) -> void:
    requires xs.count == 3
    y: darray[i64] is AllPositive = xs
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "newdiag_set_elem.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(result)
	if !contains(diags, "it can fail when") {
		t.Fatalf("expected the failed element quantifier to include a counterexample, got: %s", diags)
	}
	if !strings.Contains(diags, "x=") {
		t.Fatalf("expected the witness to name a violating element `x`, got: %s", diags)
	}
}

// A PROVABLE container quantifier (the precondition already establishes the predicate) must NOT report
// any counterexample — no false witness.
func TestProvableSetElementQuantifierStaysSilent(t *testing.T) {
	requireZ3(t)
	src := `
law AllPositive(self: darray[i64]) = forall x in self: x > 0

def check(xs: darray[i64]) -> void:
    requires xs.count == 3
    requires forall x in xs: x > 0
    y: darray[i64] is AllPositive = xs
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "newdiag_set_ok.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(result)
	if strings.Contains(diags, "it can fail when") {
		t.Fatalf("provable container quantifier should not report a counterexample, got: %s", diags)
	}
}
