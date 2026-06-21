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
	return AnalyzeWithOptions(file, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
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
	joined := strings.Join(result.Errors(), "\n")

	if !strings.Contains(joined, "proof hole: assertion could not be proven") {
		t.Fatalf("expected the structured proof-hole header, got: %v", result.Errors())
	}
	if !strings.Contains(joined, "goal:") || !strings.Contains(joined, "i < n") {
		t.Fatalf("expected the goal `i < n` to be surfaced, got: %v", result.Errors())
	}
	// The `requires i >= 0` precondition is a known fact in scope and must be listed.
	if !strings.Contains(joined, "i >= 0") {
		t.Fatalf("expected the known fact `i >= 0` to be listed, got: %v", result.Errors())
	}
	// The unbounded-upper-bound heuristic must propose the missing precondition.
	if !strings.Contains(joined, "suggested:") || !strings.Contains(joined, "requires i < n") {
		t.Fatalf("expected a `requires i < n` suggestion, got: %v", result.Errors())
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
	joined := strings.Join(result.Errors(), "\n")
	if strings.Contains(joined, "proof hole") {
		t.Fatalf("a provable assert (i > 1 from i >= 3) must not raise a proof hole, got: %v", result.Errors())
	}
}
