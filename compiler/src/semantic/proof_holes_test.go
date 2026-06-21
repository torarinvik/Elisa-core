//go:build cgo

package semantic

import (
	"os/exec"
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func analyzeProofHole(t *testing.T, filename, src string) *Result {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; proof-hole test skipped")
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
	return AnalyzeWithOptions(file, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true, EmitProofHoleHints: true})
}

func analyzeProofHoleWithOptions(t *testing.T, filename, src string, options AnalyzeOptions) *Result {
	t.Helper()
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
	return AnalyzeWithOptions(file, options)
}

// docs/98 — proof holes. A strict `assert(i < n)` is unprovable: nothing in scope bounds `i` above
// relative to `n`. The diagnostic must be CONSTRUCTIVE rather than a raw counterexample — it surfaces
// the GOAL, at least one KNOWN FACT in scope (here `i >= 0`, seeded by the `requires`), and a SUGGESTED
// missing fact (the unbounded-upper-bound heuristic ⇒ `requires i < n`).
func TestProofHoleUnboundedIndexSuggestsMissingFact(t *testing.T) {
	src := `
def get(i: i64, n: i64):
    requires i >= 0
    assert(i < n)
`
	result := analyzeProofHole(t, "proof_hole_index.elisa", src)
	// The proof-hole report is a NON-FATAL hint (the assert stays a runtime check), so it surfaces on
	// the warnings channel, not as a hard error.
	joined := strings.Join(result.Warnings(), "\n")

	if !strings.Contains(joined, "proof hole: assertion could not be proven") {
		t.Fatalf("expected the structured proof-hole header, got: %v", result.Warnings())
	}
	if !strings.Contains(joined, "goal:") || !strings.Contains(joined, "i < n") {
		t.Fatalf("expected the goal `i < n` to be surfaced, got: %v", result.Warnings())
	}
	// The `requires i >= 0` precondition is a known fact in scope and must be listed.
	if !strings.Contains(joined, "i >= 0") {
		t.Fatalf("expected the known fact `i >= 0` to be listed, got: %v", result.Warnings())
	}
	// The unbounded-upper-bound heuristic must propose the missing precondition.
	if !strings.Contains(joined, "suggested:") || !strings.Contains(joined, "requires i < n") {
		t.Fatalf("expected a `requires i < n` suggestion, got: %v", result.Warnings())
	}
}

// Soundness/quietness: an assert the prover CAN discharge from the in-scope facts produces NO proof
// hole. `i > 1` follows from `requires i >= 3`, so the SMT tier closes it and the diagnostic stays
// silent — the new error never fires on a genuinely entailed assert.
func TestProofHoleSilentWhenProvable(t *testing.T) {
	src := `
def ok(i: i64):
    requires i >= 3
    assert(i > 1)
`
	result := analyzeProofHole(t, "proof_hole_ok.elisa", src)
	joined := strings.Join(append(append([]string{}, result.Errors()...), result.Warnings()...), "\n")
	if strings.Contains(joined, "proof hole") {
		t.Fatalf("a provable assert (i > 1 from i >= 3) must not raise a proof hole, got: %v", joined)
	}
}

func TestAssertHoleStmtEmitsExplicitHoleReport(t *testing.T) {
	src := `
def ask(i: i64):
    requires i >= 0
    assert ?
`
	result := analyzeProofHoleWithOptions(t, "assert_hole.elisa", src, AnalyzeOptions{})
	joined := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(joined, "proof hole: explicit assertion hole") {
		t.Fatalf("expected explicit proof-hole report, got: %v", result.Warnings())
	}
	if !strings.Contains(joined, "goal:        ?") {
		t.Fatalf("expected placeholder goal, got: %v", result.Warnings())
	}
	if !strings.Contains(joined, "i >= 0") {
		t.Fatalf("expected in-scope known fact, got: %v", result.Warnings())
	}
}

func TestPlainAssertProofHoleHintsRequireOptIn(t *testing.T) {
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; proof-hole test skipped")
	}
	src := `
def get(i: i64, n: i64):
    requires i >= 0
    assert(i < n)
`
	base := AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true}
	quiet := analyzeProofHoleWithOptions(t, "plain_assert_quiet.elisa", src, base)
	if joined := strings.Join(quiet.Warnings(), "\n"); strings.Contains(joined, "proof hole") {
		t.Fatalf("plain assert proof-hole hints must be opt-in, got: %v", quiet.Warnings())
	}

	base.EmitProofHoleHints = true
	noisy := analyzeProofHoleWithOptions(t, "plain_assert_explain.elisa", src, base)
	if joined := strings.Join(noisy.Warnings(), "\n"); !strings.Contains(joined, "proof hole: assertion could not be proven") {
		t.Fatalf("expected opt-in proof-hole hint, got: %v", noisy.Warnings())
	}
}
