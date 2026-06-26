package semantic

import (
	"strings"
	"testing"
)

// TestWhereFieldStoreViolated verifies that storing a constant value that violates a struct
// field's where-predicate is a hard error at the field-store site.
func TestWhereFieldStoreViolated(t *testing.T) {
	src := `
struct Pos:
    x: mutable i64 where x > 0

def f(p: mutable Pos) -> i64:
    p.x <- -1
    return p.x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "where_field_store_violated.elisa", src, AnalyzeOptions{})
	diagStr := allDiagnostics(result)
	if len(result.Errors()) == 0 {
		t.Errorf("expected a hard error for p.x <- -1 violating 'x > 0'; got errors=%v diags=%q",
			result.Errors(), diagStr)
	}
	if !strings.Contains(diagStr, "violated") && !strings.Contains(diagStr, "where refinement") {
		t.Errorf("expected a 'violated' or 'where refinement' diagnostic; got diags=%q", diagStr)
	}
}

// TestWhereFieldStoreClean verifies that storing a value that provably satisfies the field's
// where-predicate is accepted without diagnostics.
func TestWhereFieldStoreClean(t *testing.T) {
	src := `
struct Pos:
    x: mutable i64 where x > 0

def f(p: mutable Pos) -> i64:
    p.x <- 7
    return p.x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "where_field_store_clean.elisa", src, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Errorf("expected no errors for clean p.x <- 7; got %v", result.Errors())
	}
}

// TestWhereFieldStoreUnknownValue verifies that storing a non-constant (runtime) value into a
// where-typed field does NOT produce a hard error but DOES emit a runtime-lint proof entry,
// since the constraint cannot be statically proved.
func TestWhereFieldStoreUnknownValue(t *testing.T) {
	src := `
struct Pos:
    x: mutable i64 where x > 0

def f(p: mutable Pos, v: i64) -> i64:
    p.x <- v
    return p.x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "where_field_store_unknown.elisa", src, AnalyzeOptions{})
	// Must not be a hard error — the value might be valid at runtime.
	if len(result.Errors()) != 0 {
		t.Errorf("expected no hard error for unknown-value store; got %v", result.Errors())
	}
	// Must record a ProofRuntime entry signalling that the check is deferred to runtime.
	hasRuntime := false
	for _, pr := range result.ProofReport {
		if pr.Outcome == ProofRuntime {
			hasRuntime = true
			break
		}
	}
	if !hasRuntime {
		t.Errorf("expected a ProofRuntime entry for p.x <- v (unknown value); proof report: %v",
			result.ProofReport)
	}
}
