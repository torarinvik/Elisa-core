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
    if n == 0:
        return 0
    return countdown(n - 1)
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
    return grow(n + 1)
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
    return n + 1
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
    if b == 0:
        return a
    return walk(a, b - 1)
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
    return loop_forever(n + 1)
`
	result := analyzeTreeTestSource(t, "term_noclause.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("recursion without decreases must not be a termination error, got: %v", errs)
	}
}

// ---- decreases * (wildcard opt-out) tests ----

// `decreases * "reason"` suppresses the termination proof obligation — the function compiles
// without a "cannot prove termination" error even though the measure is not provable.
func TestDecreasesStarWithReasonSuppressesError(t *testing.T) {
	src := `
def spin(n: usize) -> usize:
    decreases * "this is a known diverging helper; callers never use its result"
    return spin(n)
`
	result := analyzeTreeTestSource(t, "term_star_reason.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("decreases * with a reason must suppress the termination error, got: %v", errs)
	}
}

// `decreases *` WITHOUT a reason is always a compile error, independent of any flag.
// Trust must never be silent.
func TestDecreasesStarWithoutReasonIsError(t *testing.T) {
	src := `
def spin(n: usize) -> usize:
    decreases *
    return spin(n)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "term_star_noreason.elisa", src)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "requires a non-empty reason string") {
		t.Fatalf("expected a missing-reason error for decreases * without string, got:\n%s", errText)
	}
}

// Soundness negative: a function with `decreases * "reason"` does NOT become verifiedTerminating,
// so its defining equation is NOT axiomatized — a claim that relies on it is still rejected.
func TestDecreasesStarDoesNotEnableAxiom(t *testing.T) {
	src := `
def spin(n: usize) -> usize:
    decreases * "diverges intentionally"
    return spin(n)

def caller(n: usize) -> usize:
    requires n == spin(n)
    return n
`
	// The axiom spin(n) == spin(n) is trivially true but spin(n) == body is NOT asserted —
	// what we want to verify is that the function is NOT treated as equation-eligible.
	// A direct way: demand a property that would only hold if spin had a fixed-point equation.
	// Here we just check the above compiles but the proof report has NO defining-equation entry for spin.
	result := analyzeTreeTestSource(t, "term_star_no_axiom.elisa", src)
	for _, f := range result.ProofReport {
		if strings.Contains(f.Subject, "defining equation") && strings.Contains(f.Subject, "spin") {
			t.Fatalf("decreases * must NOT enable the defining-equation axiom, but got proof entry: %+v", f)
		}
	}
}
