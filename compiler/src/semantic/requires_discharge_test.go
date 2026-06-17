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
