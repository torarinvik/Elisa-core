//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// where-refinement re-discharge on reassignment
//
// When a local declared `x: i64 where x > 0` is later REASSIGNED (`x <- expr`),
// the binding's declared type still advertises `x where x > 0`, so the new RHS
// must be re-proven against the where-predicate. These tests lock in:
//
//   (a) refuted reassignment (`x <- -1`) is a hard error
//   (b) satisfying reassignment (`x <- 10`) is clean
//   (c) unknown reassignment (from an unconstrained call) is NOT silent —
//       it produces a runtime lint rather than being dropped
//
// The re-discharge fires only for where-typed locals; a plain (non-where)
// local reassignment is unaffected (covered by the existing flow-revalidation
// suite).
// ---------------------------------------------------------------------------

func whReassignContains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// (a) An out-of-range reassignment of a where-typed local is statically
// refuted and must produce an error.
func TestWhereReassignRefutedIsError(t *testing.T) {
	src := `
def caller() -> i64:
    x: mutable i64 where x > 0 = 5
    x <- -1
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_reassign_refuted.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !whReassignContains(diag, "violated") && !whReassignContains(diag, "could not be proven") {
		t.Errorf("expected where re-discharge failure after x <- -1, got:\n%s", diag)
	}
}

// (b) A satisfying reassignment of a where-typed local is clean.
func TestWhereReassignSatisfyingIsClean(t *testing.T) {
	src := `
def caller() -> i64:
    x: mutable i64 where x > 0 = 5
    x <- 10
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_reassign_ok.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if whReassignContains(diag, "violated") || whReassignContains(diag, "could not be proven") {
		t.Errorf("satisfying reassignment x <- 10 should be clean, got:\n%s", diag)
	}
}

// (c) An unknown reassignment (RHS from an unconstrained call) must not be
// silently accepted: the re-discharge falls through to a runtime lint.
func TestWhereReassignUnknownIsNotSilent(t *testing.T) {
	src := `
def some_unknown() -> i64:
    return 0

def caller() -> i64:
    x: mutable i64 where x > 0 = 5
    x <- some_unknown()
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_reassign_unknown.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !whReassignContains(diag, "could not be proven") && !whReassignContains(diag, "where") {
		t.Errorf("unknown reassignment x <- some_unknown() should produce a runtime lint, not silence, got:\n%s", diag)
	}
}
