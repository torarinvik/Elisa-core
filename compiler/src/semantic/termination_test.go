package semantic

import (
	"strings"
	"testing"
)

// A `decreases n` measure with a recursive call `f(n-1)` strictly decreases (by 1) and is bounded
// below (usize) — termination proven (docs/86 brick 86-7).
func TestTerminationProvenCountdown(t *testing.T) {
	src := `
def countdown(n: usize) -> usize:
    decreases n
    if n == 0u:
        return 0u
    return countdown(n - 1u)
`
	result := analyzeTreeTestSource(t, "term_countdown.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenLinear && strings.Contains(f.Subject, "termination of countdown") {
			proven++
		}
	}
	if proven != 1 {
		t.Fatalf("expected termination of countdown to be proven, got %d: %+v", proven, result.ProofReport)
	}
}

// A recursive call that does NOT decrease the measure (`f(n+1)`) is refuted — a non-termination error.
func TestTerminationRefutedIncreasing(t *testing.T) {
	src := `
def grow(n: usize) -> usize:
    decreases n
    return grow(n + 1u)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "term_grow.elisa", src)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove the `decreases` measure strictly decreases") {
		t.Fatalf("expected a non-termination error for an increasing measure, got:\n%s", errText)
	}
}

// A constant-argument recursive call (`f(n)`) does not decrease — refuted.
func TestTerminationRefutedConstantArg(t *testing.T) {
	src := `
def spin(n: usize) -> usize:
    decreases n
    return spin(n)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "term_spin.elisa", src)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "cannot prove the `decreases` measure strictly decreases") {
		t.Fatalf("expected a non-termination error for a non-decreasing measure, got:\n%s", errText)
	}
}

// A `decreases` on a non-recursive function is flagged as unused (no obligation, but a likely mistake).
func TestTerminationUnusedClauseWarns(t *testing.T) {
	src := `
def plain(n: usize) -> usize:
    decreases n
    return n + 1u
`
	result := analyzeTreeTestSource(t, "term_unused.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("an unused decreases clause should warn, not error, got: %v", errs)
	}
	if !strings.Contains(strings.Join(result.Warnings(), "\n"), "makes no direct recursive call") {
		t.Fatalf("expected an unused-decreases warning, got warnings: %v", result.Warnings())
	}
}

// A lexicographic measure: the first component is unchanged across the call and the second strictly
// decreases — termination proven via the tuple rule.
func TestTerminationProvenLexicographic(t *testing.T) {
	src := `
def walk(a: usize, b: usize) -> usize:
    decreases a
    decreases b
    if b == 0u:
        return a
    return walk(a, b - 1u)
`
	result := analyzeTreeTestSource(t, "term_lex.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis for the lexicographic measure, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenLinear && strings.Contains(f.Subject, "termination of walk") {
			proven++
		}
	}
	if proven != 1 {
		t.Fatalf("expected termination of walk to be proven lexicographically, got %d: %+v", proven, result.ProofReport)
	}
}

// Recursion WITHOUT a decreases clause is unaffected — no termination obligation (status quo).
func TestTerminationNoClauseNoObligation(t *testing.T) {
	src := `
def loop_forever(n: usize) -> usize:
    return loop_forever(n + 1u)
`
	result := analyzeTreeTestSource(t, "term_noclause.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("recursion without decreases must not be a termination error, got: %v", errs)
	}
}
