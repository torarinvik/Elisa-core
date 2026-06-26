//go:build cgo

package semantic

// ensure_alias_return_test.go
//
// Tests for two gaps closed in analyzer_law_is.go:
//
//  (1) SOUNDNESS: a return-type `is`-refinement predicate that is PROVABLY REFUTED by the linear
//      prover is now a HARD ERROR, not just a lint. Only a definite refutation escalates;
//      merely-unprovable obligations stay a lint (not changed).
//
//  (2) OBSERVABILITY: a proven `ensure` postcondition now records a ProofProvenSMT (or equivalent)
//      entry in the proof report via recordProof. Previously the proven branch was silent.
//
// Run with: go test ./src/semantic -run 'EnsureAliasReturn' -tags cgo

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// (1) return-position is-refinement: provably refuted -> hard error
// ---------------------------------------------------------------------------

// TestEnsureAliasReturn_ReturnIsRefuted_HardError asserts that a function returning a constant
// that provably violates its is-refinement return type is a HARD ERROR independent of -strict.
func TestEnsureAliasReturn_ReturnIsRefuted_HardError(t *testing.T) {
	src := `
law Positive(x: i64) = x > 0

def bad() -> i64 is Positive:
    return -1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "alias_return_refuted.elisa", src, AnalyzeOptions{})

	if len(result.Errors()) == 0 {
		t.Fatalf("SOUNDNESS VIOLATION: returning -1 for `-> i64 is Positive` must be a hard error; "+
			"errors=%v warnings=%v proof=%v", result.Errors(), result.Warnings(), result.ProofReport)
	}
	combined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(combined, "violated") && !strings.Contains(combined, "refin") {
		t.Errorf("expected a refinement-violation error message; got: %s", combined)
	}
	found := false
	for _, p := range result.ProofReport {
		if p.Outcome == ProofRefuted {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a ProofRefuted entry in the proof report; got %v", result.ProofReport)
	}
}

// TestEnsureAliasReturn_ReturnIsRefuted_WhereHardError asserts that a return-where refinement
// that is provably violated is (and remains) a hard error.
func TestEnsureAliasReturn_ReturnIsRefuted_WhereHardError(t *testing.T) {
	src := `
def bad_where() -> i64 where result > 0:
    return -5
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "alias_return_where_refuted.elisa", src, AnalyzeOptions{})

	if len(result.Errors()) == 0 {
		t.Fatalf("SOUNDNESS VIOLATION: returning -5 for `where result > 0` must be a hard error; "+
			"errors=%v warnings=%v", result.Errors(), result.Warnings())
	}
	found := false
	for _, p := range result.ProofReport {
		if p.Outcome == ProofRefuted {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a ProofRefuted entry in the proof report; got %v", result.ProofReport)
	}
}

// TestEnsureAliasReturn_ReturnIsUnprovable_StaysLint asserts that an UNPROVABLE return refinement
// (value unknown at compile time) stays a lint, NOT a hard error.
func TestEnsureAliasReturn_ReturnIsUnprovable_StaysLint(t *testing.T) {
	src := `
law Positive(x: i64) = x > 0

def maybe_pos(n: i64) -> i64 is Positive:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "alias_return_unprovable.elisa", src, AnalyzeOptions{})

	if len(result.Errors()) != 0 {
		t.Fatalf("REGRESSION: unprovable return refinement must stay a lint (not an error) without -strict; "+
			"errors=%v", result.Errors())
	}
}

// ---------------------------------------------------------------------------
// (2) ensure postcondition proven -> ProofProven entry recorded
// ---------------------------------------------------------------------------

// TestEnsureAliasReturn_EnsureProven_RecordsProof asserts that a proven `ensure` postcondition
// now records a ProofProven* entry in the proof report.
func TestEnsureAliasReturn_EnsureProven_RecordsProof(t *testing.T) {
	src := `
def f() -> i64:
    ensure result >= 0
    return 5
`
	result := analyzeContractStrict(t, "ensure_proven_proof.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	found := false
	for _, p := range result.ProofReport {
		if (p.Outcome == ProofProvenSMT || p.Outcome == ProofProvenLinear ||
			p.Outcome == ProofProvenConst || p.Outcome == ProofProvenContract) &&
			strings.Contains(p.Subject, "ensure") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("OBSERVABILITY: expected a ProofProven* entry with subject containing 'ensure'; "+
			"got %+v", result.ProofReport)
	}
}

// TestEnsureAliasReturn_EnsureProven_PositiveReturn asserts an SMT-provable ensure case
// records a ProofProvenSMT (not just passes silently).
func TestEnsureAliasReturn_EnsureProven_PositiveReturn(t *testing.T) {
	src := `
def positive() -> i64:
    ensure result > 0
    return 42
`
	result := analyzeContractStrict(t, "ensure_proven_smt.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	found := false
	for _, p := range result.ProofReport {
		if (p.Outcome == ProofProvenSMT || p.Outcome == ProofProvenLinear) &&
			strings.Contains(p.Subject, "ensure") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("OBSERVABILITY: expected ProofProvenSMT/Linear for ensure-proven path; got %+v", result.ProofReport)
	}
}
