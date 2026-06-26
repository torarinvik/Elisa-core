//go:build cgo

package semantic

import (
	"os/exec"
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// analyzeVacuity runs the analyzer with SMT enabled (z3) and returns the Result, skipping the test
// when no solver is on PATH (the vacuity check, like the whole discharge ladder, declines silently
// without a solver).
func analyzeVacuity(t *testing.T, filename, src string) *Result {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; vacuity test skipped")
	}
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	return AnalyzeWithOptions(file, AnalyzeOptions{EnableSMT: true})
}

func warningsMentionVacuity(ws []string) bool {
	for _, w := range ws {
		if strings.Contains(w, "contradictory") && strings.Contains(w, "unsatisfiable") {
			return true
		}
	}
	return false
}

// (a) Contradictory preconditions (x > 0 AND x < 0) are PROVABLY unsatisfiable → flagged.
func TestVacuityFlagsContradictoryRequires(t *testing.T) {
	src := `
def bad(x: i64) -> i64:
    requires x > 0
    requires x < 0
    return x
`
	result := analyzeVacuity(t, "vacuity_contradictory.elisa", src)
	if !warningsMentionVacuity(result.Warnings()) {
		t.Fatalf("expected a vacuity warning for contradictory requires, got warnings: %v / errors: %v",
			result.Warnings(), result.Errors())
	}
}

// (a') The same contradiction inside a single clause (x > 0 and x < 0) is likewise flagged.
func TestVacuityFlagsContradictoryRequiresSingleClause(t *testing.T) {
	src := `
def bad(x: i64) -> i64:
    requires x > 0 and x < 0
    return x
`
	result := analyzeVacuity(t, "vacuity_single_clause.elisa", src)
	if !warningsMentionVacuity(result.Warnings()) {
		t.Fatalf("expected a vacuity warning for x>0 and x<0, got warnings: %v / errors: %v",
			result.Warnings(), result.Errors())
	}
}

// (b) A normal, comfortably-satisfiable precondition is NOT flagged.
func TestVacuityDoesNotFlagSatisfiableRequires(t *testing.T) {
	src := `
def ok(x: i64) -> i64:
    requires x > 0
    requires x < 100
    return x
`
	result := analyzeVacuity(t, "vacuity_satisfiable.elisa", src)
	if warningsMentionVacuity(result.Warnings()) {
		t.Fatalf("must NOT flag a satisfiable precondition, got warnings: %v", result.Warnings())
	}
}

// (c) A TIGHT but still-satisfiable precondition (exactly one value: x >= 5 and x <= 5) is NOT
// flagged — the solver returns sat (model x=5), so the "only unsat concludes" rule keeps it clean.
func TestVacuityDoesNotFlagTightSatisfiableRequires(t *testing.T) {
	src := `
def tight(x: i64) -> i64:
    requires x >= 5
    requires x <= 5
    return x
`
	result := analyzeVacuity(t, "vacuity_tight.elisa", src)
	if warningsMentionVacuity(result.Warnings()) {
		t.Fatalf("must NOT flag a tight-but-satisfiable precondition (x==5 works), got warnings: %v", result.Warnings())
	}
}

// Under -strict (EnforceStrictProofs) the vacuity diagnostic is promoted to a hard error, matching the
// proofLint severity convention used by the rest of the discharge ladder.
func TestVacuityIsErrorUnderStrict(t *testing.T) {
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; vacuity strict test skipped")
	}
	src := `
def bad(x: i64) -> i64:
    requires x > 0
    requires x < 0
    return x
`
	l := lexer.New("vacuity_strict.elisa", []byte(src))
	tokens := l.Tokenize()
	p := parser.New(tokens)
	file := p.ParseFile("vacuity_strict.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	result := AnalyzeWithOptions(file, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	found := false
	for _, e := range result.Errors() {
		if strings.Contains(e, "contradictory") && strings.Contains(e, "unsatisfiable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a hard error under -strict for contradictory requires, got errors: %v", result.Errors())
	}
}
