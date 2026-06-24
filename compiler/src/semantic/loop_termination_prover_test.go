//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// POSITIVE (B): a canonical array-shift loop body `arr[j] <- arr[j+1]` no longer blocks capture. The
// store mutates no scalar, so the scalar measure `n - (j+1)` still proves strictly decreasing — no
// hatch required. Before this extension the array READ `arr[j+1]` in the store value forced the body
// to be unmodelable, falling back to the runtime progress check.
func TestLoopDecreasesArrayShiftReadProves(t *testing.T) {
	src := `def shift(arr: darray[i64], n: i64):
    requires n >= 0
    j: mutable i64 = 0
    while j < n:
        invariant j >= 0
        decreases n - j
        arr[j] <- arr[j + 1]
        j <- j + 1
`
	result := analyzeContractStrict(t, "array_shift.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, "terminate") || strings.Contains(e, "decreases") || strings.Contains(e, "could not be proven") {
			t.Fatalf("array-shift loop with a pure read in the store value must prove termination, got: %v", e)
		}
	}
	if got := countLoopTerminationProof(result, ProofProvenSMT); got != 1 {
		t.Fatalf("expected array-shift loop termination proven by SMT, got %d: %+v", got, result.ProofReport)
	}
}

// POSITIVE (A): a `.usize()` value-form numeric conversion in the loop body assignment is admitted as
// pure arithmetic, so the countdown still proves terminating. The conversion is value-preserving in
// the integer model, so the SMT term translator sees through it.
func TestLoopDecreasesNumericCastProves(t *testing.T) {
	src := `def count_cast(n: usize) -> usize:
    i: mutable usize = n
    while i > 0:
        decreases i
        i <- (i - 1).usize()
    return i
`
	result := analyzeContractStrict(t, "count_cast.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, "terminate") || strings.Contains(e, "decreases") || strings.Contains(e, "could not be proven") {
			t.Fatalf("a `.usize()` conversion in the body must not block termination, got: %v", e)
		}
	}
	if got := countLoopTerminationProof(result, ProofProvenSMT); got != 1 {
		t.Fatalf("expected numeric-cast loop termination proven by SMT, got %d: %+v", got, result.ProofReport)
	}
}

// NEGATIVE / SOUNDNESS: a method/free-function CALL in the array-store VALUE (`arr[j] <- f(j)`) is NOT
// side-effect-free (the call could mutate the counter or diverge), so the body stays unmodelable and
// the loop is NOT proven — under strict it must error (still needs a hatch).
func TestLoopDecreasesArrayStoreCallStrictErrors(t *testing.T) {
	src := `def f(x: i64) -> i64:
    return x

def loop(arr: darray[i64], n: i64):
    requires n >= 0
    j: mutable i64 = 0
    while j < n:
        decreases n - j
        arr[j] <- f(j)
        j <- j + 1
`
	result := analyzeContractStrict(t, "array_store_call.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven") {
		t.Fatalf("a CALL in the array-store value must keep the loop unprovable (error under strict), got: %v", result.Errors())
	}
}

// NEGATIVE / SOUNDNESS: a CONDITIONAL progress step (`if cond: j <- j + 1`) must NOT prove — the
// extension never models `if` branches, so a guarded body remains unmodelable and errors under strict.
func TestLoopDecreasesConditionalProgressStrictErrors(t *testing.T) {
	src := `def loop(n: i64):
    requires n >= 0
    j: mutable i64 = 0
    while j < n:
        decreases n - j
        if j < n:
            j <- j + 1
`
	result := analyzeContractStrict(t, "conditional_progress.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven") {
		t.Fatalf("a conditional progress step must stay unmodelable (error under strict), got: %v", result.Errors())
	}
}

// NEGATIVE / SOUNDNESS: a `.cast[T]` REINTERPRET (not a value conversion) in the body assignment must
// NOT be admitted as pure arithmetic — the body stays unmodelable and errors under strict.
func TestLoopDecreasesReinterpretCastStrictErrors(t *testing.T) {
	src := `def loop(n: u64):
    i: mutable u64 = n
    while i > 0:
        decreases i
        i <- (i - 1).cast[u64]
`
	result := analyzeContractStrict(t, "reinterpret_cast.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven") {
		t.Fatalf("a `.cast[T]` reinterpret must not be admitted as pure arithmetic (error under strict), got: %v", result.Errors())
	}
}

// NEGATIVE / SOUNDNESS: a genuinely non-terminating loop (`while i > 0` with a non-decreasing body)
// must still be refuted — the measure does not strictly decrease.
func TestLoopDecreasesNonTerminatingArrayShiftRejected(t *testing.T) {
	src := `def loop(arr: darray[i64], n: i64):
    requires n >= 0
    j: mutable i64 = 0
    while j < n:
        decreases n - j
        arr[j] <- arr[j]
        j <- j - 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "nonterm_shift.elisa", src, AnalyzeOptions{EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a non-decreasing array-shift loop should decline without changing acceptance, got: %v", errs)
	}
	if got := countLoopTerminationProof(result, ProofRuntime); got != 1 {
		t.Fatalf("expected non-decreasing array-shift loop to fall back to runtime, got %d: %+v", got, result.ProofReport)
	}
}
