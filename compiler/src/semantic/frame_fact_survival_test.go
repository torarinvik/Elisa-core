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

func countCalleeRequiresProof(result *Result, calleeName string, outcomes ...ProofOutcome) int {
	n := 0
	for _, f := range result.ProofReport {
		if !strings.Contains(f.Subject, "precondition of "+calleeName) {
			continue
		}
		for _, outcome := range outcomes {
			if f.Outcome == outcome {
				n++
				break
			}
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
	// Since 10055e6a a surviving identical assert fact discharges the requires at the linear tier
	// before SMT is consulted; either static outcome demonstrates frame-aware survival.
	if got := countCalleeRequiresProof(result, "need_pos", ProofProvenSMT, ProofProvenLinear); got != 1 {
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
	if got := countCalleeRequiresProof(result, "need_pos", ProofProvenSMT, ProofProvenLinear); got != 0 {
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
	if got := countCalleeRequiresProof(result, "need_pos", ProofProvenSMT, ProofProvenLinear); got != 0 {
		t.Fatalf("an unframed callee must drop the fact conservatively: %+v", result.ProofReport)
	}
}

// --- predFact (law-narrowing) frame-aware survival ---

// POSITIVE (completeness): a predFact about a mutable variable `n` gained from `if n is Positive:`
// survives a call to a FRAMED callee that writes nothing through `n` (frame only covers a struct
// arg `r`). After the call, `n is Positive` is still known and discharges the downstream obligation
// on `needs_positive(n)` without a runtime check.
func TestFramePredFactSurvivesWhenCalleeWritesNothingThroughArg(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

struct R:
    value: mutable i64
    health: mutable i64

def bump_r(r: mutable R&, n: mutable i64&) changes r.value:
    r.value <- r.value + 1

def needs_positive(x: i64 is Positive) -> i64:
    return x

def caller(r: mutable R&, n: mutable i64) -> i64:
    if n is Positive:
        bump_r(r, &n)
        return needs_positive(n)
    return 0
`
	result := analyzeTreeTestSource(t, "frame_predfact_survive_disjoint.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("predFact about n must survive call that only changes r.value, got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("narrowed n is Positive must prove needs_positive(n) without runtime check, got %d checks", len(result.CallArgRefinementChecks))
	}
}

// SOUNDNESS (negative): when the framed callee's frame DOES cover the predFact's subject (`n`), the
// fact must be dropped. Here `changes n` means `n` may no longer be Positive after the call, so
// `needs_positive(n)` requires a runtime check (cannot be statically proven).
func TestFramePredFactDroppedWhenCalleeChangesTheArg(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

struct R:
    value: mutable i64

def bump_n(r: mutable R&, n: mutable i64&) changes n:
    n <- 0

def needs_positive(x: i64 is Positive) -> i64:
    return x

def caller(r: mutable R&, n: mutable i64) -> i64:
    if n is Positive:
        bump_n(r, &n)
        return needs_positive(n)
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "frame_predfact_dropped_on_change.elisa", src, AnalyzeOptions{})
	if len(result.CallArgRefinementChecks) == 0 && len(result.Errors()) == 0 {
		t.Fatalf("callee changes n: predFact must be dropped and needs_positive must require a runtime check or error")
	}
}

// SOUNDNESS (unframed): an UNFRAMED callee must drop the predFact about any mutable-ref arg
// conservatively, regardless of whether it actually writes it.
func TestFramePredFactDroppedForUnframedCallee(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

struct R:
    value: mutable i64

def touch(r: mutable R&, n: mutable i64&) -> void:
    r.value <- r.value + 1

def needs_positive(x: i64 is Positive) -> i64:
    return x

def caller(r: mutable R&, n: mutable i64) -> i64:
    if n is Positive:
        touch(r, &n)
        return needs_positive(n)
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "frame_predfact_dropped_unframed.elisa", src, AnalyzeOptions{})
	if len(result.CallArgRefinementChecks) == 0 && len(result.Errors()) == 0 {
		t.Fatalf("unframed callee: predFact must be dropped conservatively, expected a runtime check or error")
	}
}

// --- rangeFact frame-aware survival ---

// POSITIVE (completeness): a rangeFact about `n` seeded from a previous `ensures n is Bounded[0,100]`
// call survives a subsequent framed call that writes nothing through `n`. The Nat obligation on `n`
// (which requires n >= 0) proves without a runtime check from the surviving interval.
func TestFrameRangeFactSurvivesWhenCalleeWritesNothingThroughArg(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
law Nat(self: i64) = self >= 0

struct R:
    value: mutable i64

def clamp100(n: mutable i64&) -> void ensures n is Bounded[0, 100]:
    if n < 0:
        n <- 0
    if n > 100:
        n <- 100

def bump_r(r: mutable R&, n: mutable i64&) changes r.value:
    r.value <- r.value + 1

def needs_nat(x: i64 is Nat) -> i64:
    return x

def caller(r: mutable R&, n: mutable i64) -> i64:
    clamp100(&n)
    bump_r(r, &n)
    return needs_nat(n)
`
	result := analyzeTreeTestSource(t, "frame_rangefact_survive_disjoint.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("rangeFact Bounded[0,100] about n must survive call that only changes r.value, got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("Nat on n bounded [0,100] must prove without runtime check after framed disjoint call, got %d checks", len(result.CallArgRefinementChecks))
	}
}

// SOUNDNESS (negative): when the framed callee writes through `n` itself, the rangeFact must be
// dropped. After `wipe_n` (which `changes n`), `n`'s interval is gone and `needs_nat(n)` needs a
// runtime check.
func TestFrameRangeFactDroppedWhenCalleeChangesTheArg(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
law Nat(self: i64) = self >= 0

struct R:
    value: mutable i64

def clamp100(n: mutable i64&) -> void ensures n is Bounded[0, 100]:
    if n < 0:
        n <- 0
    if n > 100:
        n <- 100

def wipe_n(r: mutable R&, n: mutable i64&) changes n:
    n <- -1

def needs_nat(x: i64 is Nat) -> i64:
    return x

def caller(r: mutable R&, n: mutable i64) -> i64:
    clamp100(&n)
    wipe_n(r, &n)
    return needs_nat(n)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "frame_rangefact_dropped_on_change.elisa", src, AnalyzeOptions{})
	if len(result.CallArgRefinementChecks) == 0 && len(result.Errors()) == 0 {
		t.Fatalf("callee changes n: rangeFact must be dropped, needs_nat must require a runtime check or error")
	}
}
