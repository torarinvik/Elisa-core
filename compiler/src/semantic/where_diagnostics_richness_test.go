package semantic

import (
	"strings"
	"testing"
)

// TestWherePreconditionUnprovableHasGoal checks that an unprovable anonymous `where`
// precondition at a call site emits a diagnostic containing "goal:" (from proofDiagnostic.Format).
func TestWherePreconditionUnprovableHasGoal(t *testing.T) {
	src := `
def bounded(x: i64 where x >= 0) -> i64:
    return x

def caller(n: i64) -> i64:
    return bounded(n)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_diag_goal.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: false})
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "goal:") {
		t.Fatalf("expected where-precondition unprovable diagnostic to contain 'goal:', got: %s", diags)
	}
}

// TestWherePreconditionUnprovableHasSuggestion checks that an unprovable anonymous `where`
// precondition diagnostic contains a "suggestion:" line.
func TestWherePreconditionUnprovableHasSuggestion(t *testing.T) {
	src := `
def bounded(x: i64 where x >= 0) -> i64:
    return x

def caller(n: i64) -> i64:
    return bounded(n)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_diag_suggestion.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: false})
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "suggestion:") {
		t.Fatalf("expected where-precondition unprovable diagnostic to contain 'suggestion:', got: %s", diags)
	}
}

// TestLocalWhereRefinementUnprovableHasGoal checks that a local `where` refinement
// whose predicate cannot be proven statically emits a rich diagnostic with "goal:".
func TestLocalWhereRefinementUnprovableHasGoal(t *testing.T) {
	src := `
def use_local(n: i64) -> i64:
    x: i64 where x >= 0 = n
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_local_diag.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: false})
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "goal:") {
		t.Fatalf("expected local where refinement unprovable diagnostic to contain 'goal:', got: %s", diags)
	}
}

// TestLocalWhereRefinementUnprovableHasSuggestion checks that a local `where` refinement
// diagnostic has a "suggestion:" line.
func TestLocalWhereRefinementUnprovableHasSuggestion(t *testing.T) {
	src := `
def use_local(n: i64) -> i64:
    x: i64 where x >= 0 = n
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_local_diag2.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: false})
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "suggestion:") {
		t.Fatalf("expected local where refinement unprovable diagnostic to contain 'suggestion:', got: %s", diags)
	}
}

// TestWhereRefutedIsHardError checks that a statically refuted `where` precondition
// still emits a hard error (not a rich proof-lint), ensuring the REFUTED path is unchanged.
func TestWhereRefutedIsHardError(t *testing.T) {
	src := `
def bounded(x: i64 where x >= 0) -> i64:
    return x

def caller() -> i64:
    return bounded(-1)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_refuted.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: false})
	errs := result.Errors()
	found := false
	for _, e := range errs {
		if strings.Contains(e, "violated") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a hard 'violated' error for statically refuted where precondition, got errors: %v", errs)
	}
	// Should NOT contain a "goal:" proof-lint for refuted path.
	diags := allDiagnostics(result)
	if strings.Contains(diags, "goal:") {
		t.Fatalf("refuted where path should not emit a 'goal:' proof-lint, got: %s", diags)
	}
}
