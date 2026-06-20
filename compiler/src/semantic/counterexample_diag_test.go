//go:build cgo

package semantic

import (
	"os/exec"
	"strings"
	"testing"
)

func requireZ3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; counterexample diagnostic test skipped")
	}
}

// A failed `ensure` at a return reports z3's concrete counterexample — the input that makes the
// postcondition false — instead of a bare "could not be proven".
func TestEnsureFailureShowsCounterexample(t *testing.T) {
	requireZ3(t)
	src := `
def identity(x: i64) -> i64:
    ensure result >= 0
    return x
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ce_ensure.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	if !contains(diags, "it can fail when") {
		t.Fatalf("expected an ensure failure to include a counterexample hint, got: %s", diags)
	}
}

// A failed refinement on a return reports a counterexample (a value of the subject that violates the
// predicate). `x is NonNeg` fails for a free `i64`, witnessed by a negative value.
func TestRefinementFailureShowsCounterexample(t *testing.T) {
	requireZ3(t)
	src := `
law NonNeg(self: i64) = self >= 0

def passthrough(x: i64) -> i64 is NonNeg:
    return x
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ce_refine.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	if !contains(diags, "it can fail when") {
		t.Fatalf("expected a refinement failure to include a counterexample hint, got: %s", diags)
	}
}

// A lemma whose `ensure` does not follow reports the counterexample at its definition. `x >= 0` does
// not entail `x >= 100`; z3 witnesses an `x` in [0, 100).
func TestLemmaEnsureFailureShowsCounterexample(t *testing.T) {
	requireZ3(t)
	src := `
lemma bogus(x: i64):
    requires x >= 0
    ensure x >= 100
    pass
`
	result := analyzeWithSMT(t, "ce_lemma.elisa", src)
	diags := allDiagnostics(result)
	if !contains(diags, "it can fail when") {
		t.Fatalf("expected the unprovable lemma ensure to include a counterexample hint, got: %s", diags)
	}
}

// A loop invariant established on entry but NOT preserved reports a counterexample loop state — the
// value of the counter for which one body step breaks the invariant (`i = 4` for `invariant i < 5`).
func TestLoopInvariantPreservationFailureShowsCounterexample(t *testing.T) {
	requireZ3(t)
	src := `
def count_up(n: usize) -> usize:
    i: mutable usize = 0
    while i < n:
        invariant i < 5
        i <- i + 1
    return i
`
	result := analyzeLoopInvariantWithSMT(t, "ce_loop.elisa", src, false)
	diags := allDiagnostics(result)
	if !contains(diags, "it can fail when") {
		t.Fatalf("expected the non-preserved loop invariant to include a counterexample hint, got: %s", diags)
	}
	if !strings.Contains(diags, "i=") {
		t.Fatalf("expected the loop-invariant counterexample to name the counter `i`, got: %s", diags)
	}
}
