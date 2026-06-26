//go:build cgo

package semantic

// smt_ce_ensure_test.go: SMT counterexample enrichment for ensure/postcondition obligations.
//
// These tests gate the behaviour added in analyzer_smt_discharge.go that registers the `result`
// term (and any named binder involved) into ceExprs before the Sat read-back, so a failing
// `ensure` diagnostic shows the witness value for `result` alongside the free-variable model —
// e.g. "it can fail when x=0, offset=-1, result=-1" instead of only "x=0, offset=-1".
//
// The "it can fail when" substring must always be preserved (pre-existing test contract).
// All tests require z3 on PATH and skip cleanly when it is absent.

import (
	"os/exec"
	"strings"
	"testing"
)

// requireZ3ForCEEnsure is the per-file z3 availability check following the pattern in
// smt_counterexample_test.go and smt_discharge_test.go.
func requireZ3ForCEEnsure(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; SMT ensure-counterexample test skipped")
	}
}

// TestSMTCEEnsure_CompoundReturnShowsResult checks that a failed `ensure result >= 0` on a
// function returning a compound expression (x + offset) includes both "it can fail when" and
// a concrete witness for `result` in the diagnostic.
func TestSMTCEEnsure_CompoundReturnShowsResult(t *testing.T) {
	requireZ3ForCEEnsure(t)
	src := `
def add_offset(x: i64, offset: i64) -> i64:
    ensure result >= 0
    return x + offset
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_ce_ensure_compound.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	// Preservation guard: the original phrase must survive any enrichment.
	if !contains(diags, "it can fail when") {
		t.Fatalf("expected 'it can fail when' in diagnostics, got: %s", diags)
	}
	// Enrichment: the refuting model must name the concrete value of `result`.
	if !strings.Contains(diags, "result=") {
		t.Fatalf("expected 'result=<value>' in counterexample for compound return, got: %s", diags)
	}
}

// TestSMTCEEnsure_PreservesItCanFailWhen is the explicit regression guard: whatever enrichment
// is applied to ensure counterexamples, the "it can fail when" substring must always appear.
func TestSMTCEEnsure_PreservesItCanFailWhen(t *testing.T) {
	requireZ3ForCEEnsure(t)
	src := `
def subtract(a: i64, b: i64) -> i64:
    ensure result >= 0
    return a - b
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_ce_ensure_itcanfail.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	if !contains(diags, "it can fail when") {
		t.Fatalf("'it can fail when' must be present in SMT refutation diagnostics, got: %s", diags)
	}
}

// TestSMTCEEnsure_FreeVarsAndResultBothPresent checks that both the free-variable assignments
// (a, b) AND the computed `result` appear in the counterexample — the enrichment is additive,
// not a replacement.
func TestSMTCEEnsure_FreeVarsAndResultBothPresent(t *testing.T) {
	requireZ3ForCEEnsure(t)
	src := `
def difference(a: i64, b: i64) -> i64:
    ensure result >= 0
    return a - b
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_ce_ensure_freeandresult.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	if !contains(diags, "it can fail when") {
		t.Fatalf("expected 'it can fail when', got: %s", diags)
	}
	if !strings.Contains(diags, "a=") || !strings.Contains(diags, "b=") {
		t.Fatalf("expected free-variable witnesses for 'a' and 'b' in counterexample, got: %s", diags)
	}
	if !strings.Contains(diags, "result=") {
		t.Fatalf("expected 'result=<value>' in counterexample for compound return, got: %s", diags)
	}
}

// TestSMTCEEnsure_NoDuplicateForSimpleParam verifies that when the return value is a bare
// parameter (not a compound expression), the counterexample does NOT contain a redundant
// "result=..." entry — the free variable already covers it under its own name.
func TestSMTCEEnsure_NoDuplicateForSimpleParam(t *testing.T) {
	requireZ3ForCEEnsure(t)
	src := `
def identity(x: i64) -> i64:
    ensure result >= 0
    return x
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_ce_ensure_nodup.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	if !contains(diags, "it can fail when") {
		t.Fatalf("expected 'it can fail when', got: %s", diags)
	}
	// A plain `return x` should NOT add a redundant "result=x" entry; "x=<v>" suffices.
	if strings.Contains(diags, "result=") {
		t.Fatalf("simple-param return should not duplicate value under 'result=', got: %s", diags)
	}
}
