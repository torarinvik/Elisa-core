//go:build cgo

package semantic

// smt_counterexample_test.go: richer counterexample model rendering (docs/90).
//
// These tests verify that when SMT refutes an obligation it reports not only the bare free-variable
// assignments from the caller scope (already covered by counterexample_diag_test.go) but ALSO the
// concrete value of compound substituted expressions — so a diagnostic reads
//   "it can fail when x=0, offset=-1, result=-1"
// rather than only "it can fail when x=0, offset=-1".
//
// All tests require z3 on PATH; they skip cleanly when it is absent.

import (
	"os/exec"
	"strings"
	"testing"
)

// requireZ3ForCE is the per-file z3 skip helper (mirrors the pattern in counterexample_diag_test.go
// and smt_discharge_test.go so the gate is consistent across the SMT test suite).
func requireZ3ForCE(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; SMT counterexample test skipped")
	}
}

// TestSMTCounterexampleEnsureShowsResultValue checks that a failed `ensure result >= 0` on a
// function returning a compound expression (x + offset) includes the concrete value of `result`
// in the counterexample model, in addition to the free-variable values x and offset.
// The existing "it can fail when" phrase must be preserved; the richer model appears alongside it.
func TestSMTCounterexampleEnsureShowsResultValue(t *testing.T) {
	requireZ3ForCE(t)
	src := `
def add_offset(x: i64, offset: i64) -> i64:
    ensure result >= 0
    return x + offset
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_ce_ensure.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	// The existing phrase must be preserved (pre-existing test contract).
	if !contains(diags, "it can fail when") {
		t.Fatalf("expected 'it can fail when' in diagnostics, got: %s", diags)
	}
	// The richer model must name the substituted `result` value as a concrete number.
	if !strings.Contains(diags, "result=") {
		t.Fatalf("expected counterexample to include 'result=<value>' for the compound return expression, got: %s", diags)
	}
}

// TestSMTCounterexampleEnsureSimpleParamNotDuplicated verifies that when the return value is a
// simple parameter (not a compound expression), we do NOT add a spurious duplicate entry for it —
// e.g. `return x` should not produce both "x=-1" and "result=-1" in the same model.
// (The free variable already appears under its own name; duplicating it as "result" adds noise.)
func TestSMTCounterexampleEnsureSimpleParamNotDuplicated(t *testing.T) {
	requireZ3ForCE(t)
	src := `
def identity(x: i64) -> i64:
    ensure result >= 0
    return x
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_ce_identity.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	if !contains(diags, "it can fail when") {
		t.Fatalf("expected 'it can fail when' in diagnostics, got: %s", diags)
	}
	// When result == x (same symbol), `result=` should NOT appear separately to avoid "x=-1, result=-1"
	// noise. Only "x=<value>" should appear.
	if strings.Contains(diags, "result=") {
		t.Fatalf("simple-param return should not duplicate the parameter under 'result=', got: %s", diags)
	}
}

// TestSMTCounterexampleMultiParamEnsure checks a two-parameter compound expression: the model must
// include both free variables AND the computed result value so the developer can see exactly which
// combination fails.
func TestSMTCounterexampleMultiParamEnsure(t *testing.T) {
	requireZ3ForCE(t)
	// result = a - b; ensure result >= 0 fails when b > a.
	src := `
def subtract(a: i64, b: i64) -> i64:
    ensure result >= 0
    return a - b
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_ce_sub.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	if !contains(diags, "it can fail when") {
		t.Fatalf("expected 'it can fail when', got: %s", diags)
	}
	// Both free variables and the computed result should appear.
	if !strings.Contains(diags, "a=") || !strings.Contains(diags, "b=") {
		t.Fatalf("expected free-variable assignments for 'a' and 'b' in counterexample, got: %s", diags)
	}
	if !strings.Contains(diags, "result=") {
		t.Fatalf("expected computed 'result=<value>' in counterexample for compound return, got: %s", diags)
	}
}

// TestSMTCounterexamplePreservesItCanFailWhen is the explicit regression guard: whatever enrichment
// is added, the original "it can fail when" substring must always appear in diagnostics produced by
// a failed refinement. This mirrors the guard in counterexample_diag_test.go.
func TestSMTCounterexamplePreservesItCanFailWhen(t *testing.T) {
	requireZ3ForCE(t)
	src := `
law NonNeg(self: i64) = self >= 0

def passthrough(x: i64) -> i64 is NonNeg:
    return x
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_ce_nonneg.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	if !contains(diags, "it can fail when") {
		t.Fatalf("'it can fail when' must be present in SMT refutation diagnostics, got: %s", diags)
	}
}

// TestSMTCounterexampleRequiresCallerArgs checks that a failed `requires` at a call site still
// shows the caller argument values (the plain free-variable model) — regression guard for the
// requires path.
func TestSMTCounterexampleRequiresCallerArgs(t *testing.T) {
	requireZ3ForCE(t)
	src := `
def bounded(value: i64, lo: i64, hi: i64) -> void:
    requires value >= lo
    requires value <= hi

def caller(v: i64, lo: i64, hi: i64) -> void:
    bounded(v, lo, hi)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_ce_req_caller.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	if !contains(diags, "it can fail when") {
		t.Fatalf("expected 'it can fail when' for unprovable requires, got: %s", diags)
	}
	// At least one of the free caller variables (v, lo, hi) must appear.
	hasVar := strings.Contains(diags, "v=") || strings.Contains(diags, "lo=") || strings.Contains(diags, "hi=")
	if !hasVar {
		t.Fatalf("expected at least one caller free-variable in the counterexample, got: %s", diags)
	}
}
