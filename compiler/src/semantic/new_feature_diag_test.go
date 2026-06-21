package semantic

import (
	"strings"
	"testing"
)

// Diagnostic-completeness tests for the recently-added verification features (docs/98 family):
// lexicographic decreases, quantified struct invariants, and set/dict container quantifiers.
// Every assertion targets the diagnostic TEXT (the witness), never acceptance — these are advisory.

// --- 1. LEXICOGRAPHIC decreases ------------------------------------------------------------------

// A lexicographic `decreases (a, b)` where the first component is unchanged and the second INCREASES
// fails the strict-descent proof. The error must name the failing component `b` and its entry→call
// transition (b -> b + 1).
func TestLexicographicDecreaseNamesFailingComponent(t *testing.T) {
	src := `
def bad(a: usize, b: usize) -> usize:
    decreases (a, b)
    return bad(a, b + 1)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lexdiag_bad.elisa", src)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove the `decreases` measure strictly decreases") {
		t.Fatalf("expected the non-termination error, got: %s", errText)
	}
	if !strings.Contains(errText, "component `b`") {
		t.Fatalf("expected the lexicographic diagnostic to name failing component `b`, got: %s", errText)
	}
	if !strings.Contains(errText, "b -> (b + 1)") {
		t.Fatalf("expected the entry->call transition `b -> (b + 1)`, got: %s", errText)
	}
}

// A single-component `decreases n` with a non-decreasing recursive call `spin(n)` reports the
// measure transition n -> n.
func TestSingleComponentDecreaseReportsTransition(t *testing.T) {
	src := `
def spin(n: usize) -> usize:
    decreases n
    return spin(n)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lexdiag_spin.elisa", src)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "measure `n`") {
		t.Fatalf("expected the diagnostic to name measure `n`, got: %s", errText)
	}
	if !strings.Contains(errText, "n -> n") {
		t.Fatalf("expected the transition `n -> n`, got: %s", errText)
	}
}

// A PROVABLE lexicographic measure (first component strictly decreases) must NOT report any
// non-termination diagnostic — no false witness.
func TestProvableLexicographicDecreaseStaysSilent(t *testing.T) {
	src := `
def step(a: usize, b: usize) -> usize:
    decreases (a, b)
    if a == 0:
        return b
    return step(a - 1, b + 1000)
`
	result := analyzeTreeTestSource(t, "lexdiag_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("provable lexicographic measure must analyze cleanly, got: %v", errs)
	}
}
