//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func countLoopTerminationProof(result *Result, outcome ProofOutcome) int {
	n := 0
	for _, f := range result.ProofReport {
		if f.Subject == "termination of loop" && f.Predicate == "decreases" && f.Outcome == outcome {
			n++
		}
	}
	return n
}

// A leading `decreases` measure that strictly drops and stays >= 0 each iteration proves the loop
// terminates — no error. `decreases i` for `while i > 0: i <- i - 1` is the canonical countdown.
func TestLoopDecreasesCountdownTerminates(t *testing.T) {
	src := `def count_down(n: usize) -> usize:
    i: mutable usize = n
    while i > 0:
        invariant i <= n
        decreases i
        i <- i - 1
    return i
`
	result := analyzeContractStrict(t, "countdown.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, "terminate") || strings.Contains(e, "decreases") {
			t.Fatalf("a strictly-decreasing bounded measure must prove termination, got: %v", e)
		}
	}
	if got := countLoopTerminationProof(result, ProofProvenSMT); got != 1 {
		t.Fatalf("expected loop termination to be proven by SMT, got %d: %+v", got, result.ProofReport)
	}
}

// A relational measure `n - i` proves termination once an `invariant i >= 0` supplies the bound the
// signed-overflow model needs (without it, n - i could overflow). Demonstrates measure + invariant.
func TestLoopDecreasesCountupWithInvariant(t *testing.T) {
	src := `def countup(n: i64):
    requires n >= 0
    i: mutable i64 = 0
    while i < n:
        invariant i >= 0
        decreases n - i
        i <- i + 1
`
	for _, e := range analyzeContractStrict(t, "countup.elisa", src).Errors() {
		if strings.Contains(e, "terminate") || strings.Contains(e, "decreases") {
			t.Fatalf("n - i with invariant i >= 0 must prove termination, got: %v", e)
		}
	}
}

func TestLoopDecreasesLexicographicTupleTerminates(t *testing.T) {
	src := `def nested(a: usize, b: usize) -> usize:
    i: mutable usize = a
    total: mutable usize = 0
    while i > 0:
        decreases (i, b)
        i <- i - 1
    return total
`
	result := analyzeContractStrict(t, "lex_loop.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, "terminate") || strings.Contains(e, "decreases") {
			t.Fatalf("lexicographic loop measure must prove termination, got: %v", e)
		}
	}
	if got := countLoopTerminationProof(result, ProofProvenSMT); got != 1 {
		t.Fatalf("expected lexicographic loop termination to be proven by SMT, got %d: %+v", got, result.ProofReport)
	}
}

func TestLoopDecreasesLexicographicTupleDeclinesWhenNotDecreasing(t *testing.T) {
	src := `def bad_lex_loop(a: usize, b: usize):
    i: mutable usize = a
    j: mutable usize = b
    while j > 0:
        decreases (i, j)
        i <- i + 1
        j <- j - 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "bad_lex_loop.elisa", src, AnalyzeOptions{EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a non-decreasing lexicographic loop measure should decline without changing acceptance, got: %v", errs)
	}
	if got := countLoopTerminationProof(result, ProofRuntime); got != 1 {
		t.Fatalf("expected non-decreasing lexicographic loop measure to fall back to runtime, got %d: %+v", got, result.ProofReport)
	}
}

// SOUNDNESS: a measure that INCREASES (`decreases i` while `i <- i + 1`) must be refuted — the loop
// may not terminate.
func TestLoopDecreasesIncreasingMeasureRejected(t *testing.T) {
	src := `def bad(n: i64):
    requires n >= 0
    i: mutable i64 = 0
    while i < n:
        decreases i
        i <- i + 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "bad_loop.elisa", src, AnalyzeOptions{EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("an increasing `decreases` measure should decline without changing acceptance, got: %v", errs)
	}
	if got := countLoopTerminationProof(result, ProofRuntime); got != 1 {
		t.Fatalf("expected non-decreasing loop measure to fall back to runtime, got %d: %+v", got, result.ProofReport)
	}
	warnings := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(warnings, "falling back to the runtime loop-progress check") {
		t.Fatalf("expected runtime-backstop warning, got:\n%s", warnings)
	}
}

// SOUNDNESS: a measure with NO provable lower bound (signed `n - i` without the i >= 0 invariant) must
// be refuted — the difference could overflow, so the decrease is not sound.
func TestLoopDecreasesUnboundedMeasureRejected(t *testing.T) {
	src := `def maybe(n: i64):
    requires n >= 0
    i: mutable i64 = 0
    while i < n:
        decreases n - i
        i <- i + 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "unbounded_loop.elisa", src, AnalyzeOptions{EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("an unbounded signed measure should decline without changing acceptance, got: %v", errs)
	}
	if got := countLoopTerminationProof(result, ProofRuntime); got != 1 {
		t.Fatalf("expected unbounded loop measure to fall back to runtime, got %d: %+v", got, result.ProofReport)
	}
}

// SOUNDNESS: a body the analyzer cannot model (a call) cannot be proven terminating — the explicit
// `decreases` claim is reported, not silently trusted.
func TestLoopDecreasesUnmodelableBodyRejected(t *testing.T) {
	src := `def sink(x: i64):
    return

def loop(n: i64):
    requires n >= 0
    i: mutable i64 = n
    while i > 0:
        decreases i
        sink(i)
        i <- i - 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "unmodelable_loop.elisa", src, AnalyzeOptions{EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("an unmodelable loop body should decline without changing acceptance, got: %v", errs)
	}
	if got := countLoopTerminationProof(result, ProofRuntime); got != 1 {
		t.Fatalf("expected unmodelable loop body to fall back to runtime, got %d: %+v", got, result.ProofReport)
	}
}

// Under strict proofs (-Wstrict / the verification-target default), an unmodelable `decreases`
// loop body escalates the advisory proofLint to a hard error: a bare loop with no `can` must
// still be flagged. This is the negative case the scoped escape hatch is contrasted against.
func TestLoopDecreasesUnmodelableBodyStrictErrors(t *testing.T) {
	src := `def sink(x: i64):
    return

def loop(n: i64):
    requires n >= 0
    i: mutable i64 = n
    while i > 0:
        decreases i
        sink(i)
        i <- i - 1
`
	result := analyzeContractStrict(t, "unmodelable_loop_strict.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven") {
		t.Fatalf("a bare unmodelable `decreases` loop must error under strict proofs, got: %v", result.Errors())
	}
}

// When a loop `decreases` measure cannot be proven, the diagnostic should include z3's counterexample
// witness (the variable assignment that breaks the obligation), mirroring how invariant-preservation
// failures surface a witness via counterexampleSuffix / lastSMTCounterexample.
func TestLoopDecreasesFailureSurfacesCounterexample(t *testing.T) {
	src := `def bad(n: i64):
    requires n >= 0
    i: mutable i64 = 0
    while i < n:
        decreases i
        i <- i + 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ce_loop.elisa", src, AnalyzeOptions{EnableSMT: true})
	warnings := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(warnings, "it can fail when") {
		t.Fatalf("expected decreases failure to include a z3 counterexample witness (\"it can fail when ...\"), got warnings:\n%s", warnings)
	}
}

// A scoped, auditable `can Unsafe.AssumeProgress:` wrapping the loop discharges the unprovable
// `decreases` termination lint even under strict proofs (the author asserts progress this analyzer
// cannot model), AND — unlike `trusted` — surfaces Unsafe.AssumeProgress in the function's effect
// signature so the assumption stays auditable and propagates to callers.
func TestLoopDecreasesAssumeProgressDischargesAndPropagates(t *testing.T) {
	src := `def sink(x: i64):
    return

def loop(n: i64) -> void:
    requires n >= 0
    i: mutable i64 = n
    can Unsafe.AssumeProgress:
        while i > 0:
            decreases i
            sink(i)
            i <- i - 1
`
	result := analyzeContractStrict(t, "assume_progress_loop.elisa", src)
	if strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven") {
		t.Fatalf("`can Unsafe.AssumeProgress` must discharge the unprovable loop termination lint, got: %v", result.Errors())
	}
	sym, ok := result.GlobalScope.Lookup("loop")
	if !ok {
		t.Fatal("expected loop symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected loop function type, got %T", sym.Type)
	}
	if !strings.Contains(PermissionRefsString(fnType.PermissionRefs), "Unsafe.AssumeProgress") {
		t.Fatalf("expected `can` to PROPAGATE Unsafe.AssumeProgress to the signature, got %q", PermissionRefsString(fnType.PermissionRefs))
	}
}
