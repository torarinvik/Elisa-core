package semantic

import (
	"os/exec"
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func analyzeFrameSurvivalWithSMT(t *testing.T, filename, src string) *Result {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; frame-fact-survival SMT test skipped")
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

func countCalleeRequiresProof(result *Result, calleeName string, outcome ProofOutcome) int {
	n := 0
	for _, f := range result.ProofReport {
		if f.Outcome == outcome && strings.Contains(f.Subject, "precondition of "+calleeName) {
			n++
		}
	}
	return n
}

// A fact about a field the callee's `changes` frame does NOT touch survives the call: after
// `bump_flags(s)` (which changes only `s.flags`), the established `s.size > 0` is still known and
// discharges `need_pos`'s precondition.
func TestFrameFactSurvivesDisjointChange(t *testing.T) {
	src := `
struct S:
    size: mutable i64
    flags: mutable i64

def bump_flags(s: mutable S&) changes s.flags:
    s.flags <- s.flags + 1

def need_pos(n: i64) -> i64:
    requires n > 0
    return n

def caller(s: mutable S&) -> i64:
    assert s.size > 0
    bump_flags(s)
    return need_pos(s.size)
`
	result := analyzeFrameSurvivalWithSMT(t, "frame_survive_disjoint.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	if got := countCalleeRequiresProof(result, "need_pos", ProofProvenSMT); got != 1 {
		t.Fatalf("expected `s.size > 0` to survive the disjoint-frame call and discharge need_pos, got %d: %+v", got, result.ProofReport)
	}
}

// Soundness: when the callee's frame DOES cover the read place, the fact must NOT survive. Here
// `bump_size` changes `s.size`, so `s.size > 0` is dropped and need_pos's precondition falls back to
// a runtime check (not an SMT proof).
func TestFrameFactDroppedOnOverlappingChange(t *testing.T) {
	src := `
struct S:
    size: mutable i64
    flags: mutable i64

def bump_size(s: mutable S&) changes s.size:
    s.size <- s.size + 1

def need_pos(n: i64) -> i64:
    requires n > 0
    return n

def caller(s: mutable S&) -> i64:
    assert s.size > 0
    bump_size(s)
    return need_pos(s.size)
`
	result := analyzeFrameSurvivalWithSMT(t, "frame_survive_overlap.elisa", src)
	if got := countCalleeRequiresProof(result, "need_pos", ProofProvenSMT); got != 0 {
		t.Fatalf("a fact about a CHANGED field must not survive the call: %+v", result.ProofReport)
	}
}

// An UNFRAMED callee (no `changes`/`preserves`) must drop the fact conservatively — survival is
// earned only by a declared frame.
func TestFrameFactDroppedWhenCalleeUnframed(t *testing.T) {
	src := `
struct S:
    size: mutable i64
    flags: mutable i64

def touch(s: mutable S&) -> void:
    s.flags <- s.flags + 1

def need_pos(n: i64) -> i64:
    requires n > 0
    return n

def caller(s: mutable S&) -> i64:
    assert s.size > 0
    touch(s)
    return need_pos(s.size)
`
	result := analyzeFrameSurvivalWithSMT(t, "frame_survive_unframed.elisa", src)
	if got := countCalleeRequiresProof(result, "need_pos", ProofProvenSMT); got != 0 {
		t.Fatalf("an unframed callee must drop the fact conservatively: %+v", result.ProofReport)
	}
}
