package semantic

import (
	"os/exec"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// countLoopInvariantProof reports how many proof-report entries match a `loop invariant` subject,
// the given predicate ("establish"/"preserve"), and outcome.
func countLoopInvariantProof(result *Result, predicate string, outcome ProofOutcome) int {
	n := 0
	for _, f := range result.ProofReport {
		if f.Subject == "loop invariant" && f.Predicate == predicate && f.Outcome == outcome {
			n++
		}
	}
	return n
}

func analyzeLoopInvariantWithSMT(t *testing.T, filename, src string, strict bool) *Result {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; SMT loop-invariant test skipped")
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
	return AnalyzeWithOptions(file, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: strict})
}

// A `invariant i >= 0` over an unsigned counter is established on entry by the affine prover alone
// (no SMT needed): the unsigned type already guarantees i >= 0.
func TestLoopInvariantEstablishedByAffine(t *testing.T) {
	src := `
def count_up(n: usize) -> usize:
    i: mutable usize = 0
    while i < n:
        invariant i >= 0
        i <- i + 1
    return i
`
	result := analyzeTreeTestSource(t, "loop_inv_establish_affine.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	if got := countLoopInvariantProof(result, "establish", ProofProvenLinear); got != 1 {
		t.Fatalf("expected the invariant to be established by the affine prover, got %d: %+v", got, result.ProofReport)
	}
}

// An invariant that is FALSE on entry (`i >= 5` when i starts at 0) cannot be established. By
// default that is a warning; under -strict it is a hard error — turning a previously silently
// trusted invariant into a verified one.
func TestLoopInvariantUnestablishedIsFlagged(t *testing.T) {
	src := `
def count_up(n: usize) -> usize:
    i: mutable usize = 0
    while i < n:
        invariant i >= 5
        i <- i + 1
    return i
`
	result := analyzeTreeTestSource(t, "loop_inv_unestablished.elisa", src)
	warnings := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(warnings, "loop invariant could not be established on entry") {
		t.Fatalf("expected an establishment warning, got warnings:\n%s\nproof report: %+v", warnings, result.ProofReport)
	}

	strictResult := AnalyzeWithOptions(parseFileForTest(t, "loop_inv_unestablished_strict.elisa", src), AnalyzeOptions{EnforceStrictProofs: true})
	if !strings.Contains(strings.Join(strictResult.Errors(), "\n"), "loop invariant could not be established on entry") {
		t.Fatalf("expected -strict to escalate the establishment failure to an error, got: %v", strictResult.Errors())
	}
}

// The headline win: a RELATIONAL invariant `i <= n` is established on entry and proven PRESERVED by
// the body `i <- i + 1` via the SMT tier (the affine prover cannot relate two symbolic variables).
func TestLoopInvariantRelationalPreservedBySMT(t *testing.T) {
	src := `
def count_up(n: usize) -> usize:
    i: mutable usize = 0
    while i < n:
        invariant i <= n
        i <- i + 1
    return i
`
	result := analyzeLoopInvariantWithSMT(t, "loop_inv_preserve_smt.elisa", src, false)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	if got := countLoopInvariantProof(result, "preserve", ProofProvenSMT); got != 1 {
		t.Fatalf("expected the relational invariant to be proven preserved by SMT, got %d: %+v", got, result.ProofReport)
	}
}

// Soundness: a FALSE invariant `i < 5` under the loop condition `i < n` must NOT be proven
// preserved — `i + 1 < 5` does not follow from `i < n ∧ i < 5` (take i = 4). The SMT tier must
// decline, leaving the runtime check.
func TestLoopInvariantFalsePreservationDeclined(t *testing.T) {
	src := `
def count_up(n: usize) -> usize:
    i: mutable usize = 0
    while i < n:
        invariant i < 5
        i <- i + 1
    return i
`
	result := analyzeLoopInvariantWithSMT(t, "loop_inv_false_preserve.elisa", src, false)
	if got := countLoopInvariantProof(result, "preserve", ProofProvenSMT); got != 0 {
		t.Fatalf("SMT must not prove a non-inductive invariant preserved (i=4 is a counterexample): %+v", result.ProofReport)
	}
	if got := countLoopInvariantProof(result, "preserve", ProofRuntime); got != 1 {
		t.Fatalf("expected the non-inductive invariant to fall back to a runtime preservation check, got %d: %+v", got, result.ProofReport)
	}
}

func parseFileForTest(t *testing.T, filename, src string) *ast.File {
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
	return file
}
