//go:build cgo

package semantic

// ensure_refuted_soundness_test.go
//
// Guards the postcondition refutation gate (analyzer_law_is.go: ensureClauseRefuted /
// reportRefutedEnsure): an `ensure` postcondition that is PROVABLY FALSE on a reachable return path is a
// HARD ERROR at the return site, mirroring the refuted-`requires`/`where` gate. This is distinct from an
// UNPROVABLE postcondition, which must stay a runtime-fallback lint (a warning by default, escalated only
// under -strict). A SATISFIABLE postcondition must be clean.
//
// Soundness contract: only a provably-false verdict (linear prover `requiresRefuted`, or SMT validity of
// the negation) escalates to an error. Merely-unproven obligations never do.
//
// Run with: go test ./src/semantic -run 'Ensure' -tags cgo

import (
	"strings"
	"testing"
)

// hasRefutedEnsureProof reports whether the proof report carries a ProofRefuted entry attributed to an
// `ensure` obligation (subject "ensure <fn>", recorded by reportRefutedEnsure).
func hasRefutedEnsureProof(result *Result) bool {
	for _, p := range result.ProofReport {
		if p.Outcome == ProofRefuted && strings.HasPrefix(p.Subject, "ensure ") {
			return true
		}
	}
	return false
}

// TestEnsureRefuted_ReturnConstantIsHardError asserts that a function whose body provably violates its
// `ensure` postcondition (`ensure result > 0` returning a literal `0`) is a HARD ERROR — independent of
// -strict (no EnforceStrictProofs option set).
func TestEnsureRefuted_ReturnConstantIsHardError(t *testing.T) {
	src := `
def always_zero() -> i64:
    ensure result > 0
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "ensure_refuted_zero.elisa", src, AnalyzeOptions{})

	if len(result.Errors()) == 0 {
		t.Fatalf("SOUNDNESS VIOLATION: returning 0 for `ensure result > 0` must be a hard error; "+
			"errors=%v warnings=%v proof=%v", result.Errors(), result.Warnings(), result.ProofReport)
	}
	if !hasRefutedEnsureProof(result) {
		t.Errorf("expected a ProofRefuted ensure entry in the proof report; got %v", result.ProofReport)
	}
}

// TestEnsureRefuted_ReturnNegativeIsHardError asserts the same for a negative literal against
// `ensure result > 0` — the linear prover refutes `-1 > 0` over the whole (constant) range.
func TestEnsureRefuted_ReturnNegativeIsHardError(t *testing.T) {
	src := `
def negative() -> i64:
    ensure result > 0
    return -1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "ensure_refuted_negative.elisa", src, AnalyzeOptions{})

	if len(result.Errors()) == 0 {
		t.Fatalf("SOUNDNESS VIOLATION: returning -1 for `ensure result > 0` must be a hard error; "+
			"errors=%v warnings=%v", result.Errors(), result.Warnings())
	}
	if !hasRefutedEnsureProof(result) {
		t.Errorf("expected a ProofRefuted ensure entry; got %v", result.ProofReport)
	}
}

// TestEnsureRefuted_FiresWithoutStrict confirms the refutation escalation does NOT depend on -strict:
// with the default options a refuted ensure is already a hard error (not merely a warning).
func TestEnsureRefuted_FiresWithoutStrict(t *testing.T) {
	src := `
def bad() -> i64:
    ensure result > 0
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "ensure_refuted_nostrict.elisa", src, AnalyzeOptions{})

	if len(result.Errors()) == 0 {
		t.Errorf("SOUNDNESS VIOLATION: a refuted ensure must be a hard error even without -strict; "+
			"errors=%v warnings=%v", result.Errors(), result.Warnings())
	}
}

// TestEnsureRefuted_FiresUnderStrict confirms the gate still fires under -strict and does not crash or
// regress into a non-error path (the strict ladder is skipped once a clause is refuted).
func TestEnsureRefuted_FiresUnderStrict(t *testing.T) {
	src := `
def bad() -> i64:
    ensure result > 0
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "ensure_refuted_strict.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})

	if len(result.Errors()) == 0 {
		t.Errorf("SOUNDNESS VIOLATION: a refuted ensure must be a hard error under -strict; errors=%v",
			result.Errors())
	}
}

// TestEnsureUnprovable_StaysLintNotError asserts that an UNPROVABLE-but-not-refuted postcondition is NOT
// escalated to a hard error by the refutation gate. Returning an unconstrained parameter for
// `ensure result > 0` cannot be proven (the param may be <= 0) but is NOT refuted (it may also be > 0),
// so without -strict it must remain a non-error (warning / runtime fallback), never a compile error.
func TestEnsureUnprovable_StaysLintNotError(t *testing.T) {
	src := `
def passthrough(n: i64) -> i64:
    ensure result > 0
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "ensure_unprovable.elisa", src, AnalyzeOptions{})

	if len(result.Errors()) != 0 {
		t.Fatalf("SOUNDNESS VIOLATION: an UNPROVABLE (not refuted) ensure must not be a hard error "+
			"without -strict; errors=%v", result.Errors())
	}
	if hasRefutedEnsureProof(result) {
		t.Errorf("an unprovable-but-satisfiable ensure must NOT be recorded as ProofRefuted; got %v",
			result.ProofReport)
	}
}

// TestEnsureSatisfiable_IsClean asserts that a postcondition the body clearly satisfies produces no
// errors. Returning a positive literal for `ensure result > 0` is provable, hence clean even under
// -strict.
func TestEnsureSatisfiable_IsClean(t *testing.T) {
	src := `
def positive() -> i64:
    ensure result > 0
    return 7
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "ensure_satisfiable.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})

	if len(result.Errors()) != 0 {
		t.Errorf("a satisfiable ensure (`return 7` for `ensure result > 0`) must be clean; errors=%v",
			result.Errors())
	}
	if hasRefutedEnsureProof(result) {
		t.Errorf("a satisfiable ensure must NOT be recorded as ProofRefuted; got %v", result.ProofReport)
	}
}
