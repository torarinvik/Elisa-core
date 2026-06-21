//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/97: a function that `uses` a named contract inherits its requires/ensure, and when the body
// honours them the proof discharges under -strict. One contract, two functions sharing it.
func TestNamedContractProvesUnderStrict(t *testing.T) {
	src := `
contract NonNegOut(out: i64, src: i64):
    requires src >= 0
    ensure result >= src

def copy_floor(s: i64) -> i64:
    uses NonNegOut(0, s)
    return s

def echo_floor(s: i64) -> i64:
    uses NonNegOut(0, s)
    return s
`
	result := analyzeContractStrict(t, "named_contract_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("both functions honour NonNegOut and should prove under -strict, got: %v", errs)
	}
}

// A function that `uses` a contract but VIOLATES its ensure must be a hard error under -strict.
func TestNamedContractViolationErrors(t *testing.T) {
	src := `
contract NonNegOut(out: i64, src: i64):
    requires src >= 0
    ensure result >= src

def bad(s: i64) -> i64:
    uses NonNegOut(0, s)
    return s - 1
`
	result := analyzeContractStrict(t, "named_contract_bad.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("`return s - 1` violates the inherited ensure `result >= src`; must error, got: %v", result.Errors())
	}
}

// The inherited precondition is checked at CALL SITES of the applying function: calling it with an
// argument that fails the contract's `requires` is a hard error.
func TestNamedContractRequiresCheckedAtCallSite(t *testing.T) {
	src := `
contract NonNegSrc(src: i64):
    requires src >= 0
    ensure result >= 0

def use_it(s: i64) -> i64:
    uses NonNegSrc(s)
    return s

def caller() -> i64:
    return use_it(0 - 5)
`
	result := analyzeContractStrict(t, "named_contract_callsite.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatalf("calling use_it(-5) violates the inherited `requires src >= 0`; must error, got none")
	}
}

// Frame conditions in a contract propagate: a function that `uses` a contract whose `changes` set is
// {out} may write that place, but writing a place OUTSIDE it is a frame violation.
func TestNamedContractFramePropagates(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    py: mutable i32
    health: mutable i32

contract MovesOnly(r: mutable Render&):
    changes r.px, r.py

def good(r: mutable Render&) -> void:
    uses MovesOnly(r)
    r.px <- 1
    r.py <- 2

def bad(r: mutable Render&) -> void:
    uses MovesOnly(r)
    r.health <- 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_contract_frame.elisa", src, AnalyzeOptions{})
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "outside the `changes` set") {
		t.Fatalf("writing r.health outside the contract's changes set must error, got:\n%s", diags)
	}
}

// Applying an unknown contract is a hard error.
func TestNamedContractUnknownErrors(t *testing.T) {
	src := `
def f(s: i64) -> i64:
    uses NoSuchContract(s)
    return s
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_contract_unknown.elisa", src, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "unknown contract") {
		t.Fatalf("applying an undeclared contract must error, got:\n%s", allDiagnostics(result))
	}
}

// Arity mismatch between `uses` and the contract's parameters is a hard error.
func TestNamedContractArityErrors(t *testing.T) {
	src := `
contract Two(a: i64, b: i64):
    requires a >= b

def f(x: i64) -> i64:
    uses Two(x)
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_contract_arity.elisa", src, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "argument(s)") {
		t.Fatalf("arity mismatch must error, got:\n%s", allDiagnostics(result))
	}
}
