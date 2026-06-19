package semantic

import (
	"strings"
	"testing"
)

// A call whose argument the caller's facts entail satisfies the callee's `requires` is recorded as
// proven (linear) — the interprocedural precondition discharge (docs/86 brick 86-5).
func TestRequiresDischargeProvenAtCall(t *testing.T) {
	src := `
def use_off(buf: u8&, off: i32) -> u8:
    requires off >= 0
    return buf[off.usize()]

def caller(buf: u8&) -> u8:
    k: i32 = 5
    return use_off(buf, k)
`
	result := analyzeTreeTestSource(t, "requires_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenLinear && strings.Contains(f.Subject, "precondition of use_off") {
			proven++
		}
	}
	if proven != 1 {
		t.Fatalf("expected the precondition of use_off to be proven at the call site, got %d: %+v", proven, result.ProofReport)
	}
}

// A call whose argument the caller's facts prove ALWAYS violates the precondition is a hard error.
func TestRequiresDischargeRefutedAtCall(t *testing.T) {
	src := `
def use_off(buf: u8&, off: i32) -> u8:
    requires off >= 0
    return buf[off.usize()]

def caller(buf: u8&) -> u8:
    k: i32 = -3
    return use_off(buf, k)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "requires_refuted.elisa", src)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "precondition of \"use_off\" is violated") {
		t.Fatalf("expected a refuted precondition error, got:\n%s", errText)
	}
}

// A cross-variable precondition (`lo <= hi`) discharges when both arguments bound to constants.
func TestRequiresDischargeCrossVariable(t *testing.T) {
	src := `
def span(lo: i32, hi: i32) -> i32:
    requires lo <= hi
    return hi - lo

def caller() -> i32:
    a: i32 = 2
    b: i32 = 9
    return span(a, b)
`
	result := analyzeTreeTestSource(t, "requires_cross_var.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenLinear && strings.Contains(f.Subject, "precondition of span") {
			proven++
		}
	}
	if proven != 1 {
		t.Fatalf("expected the cross-variable precondition of span to be proven, got %d: %+v", proven, result.ProofReport)
	}
}

// An argument the caller cannot bound declines soundly: a warning (not an error) and a runtime
// outcome in the report, leaving the callee's own runtime check in force.
func TestRequiresDischargeUnknownWarns(t *testing.T) {
	src := `
def use_off(buf: u8&, off: i32) -> u8:
    requires off >= 0
    return buf[off.usize()]

def caller(buf: u8&, n: i32) -> u8:
    return use_off(buf, n)
`
	result := analyzeTreeTestSource(t, "requires_unknown.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("an unprovable precondition should warn, not error, got: %v", errs)
	}
	var runtime int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofRuntime && strings.Contains(f.Subject, "precondition of use_off") {
			runtime++
		}
	}
	if runtime != 1 {
		t.Fatalf("expected the unprovable precondition to be recorded as runtime, got %d: %+v", runtime, result.ProofReport)
	}
}

// docs/85 gap #4: an `extern f(...) requires <bool>` makes the FFI boundary a CHECKED contract —
// the precondition is discharged at every call site (refuted = compile error; unproven = -strict
// error / debug runtime check), so the caller cannot hand native code an out-of-domain argument.
func TestExternRequiresCheckedAtCallSite(t *testing.T) {
	src := `
extern checked_div(a: i64, b: i64) -> i64 requires b != 0

def bad_call() -> i64:
    return checked_div(10, 0)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ext_bad.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "precondition of \"checked_div\" is violated") {
		t.Fatalf("passing b=0 to an extern `requires b != 0` must be a compile error, got:\n%s", allDiagnostics(r))
	}

	ok := `
extern checked_div(a: i64, b: i64) -> i64 requires b != 0

def good_call(x: i64) -> i64:
    requires x != 0
    return checked_div(10, x)
`
	r2 := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ext_ok.elisa", ok, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r2.Errors(); len(errs) != 0 {
		t.Fatalf("a caller whose own `requires x != 0` establishes the extern precondition should discharge, got:\n%s", strings.Join(errs, "\n"))
	}
}
