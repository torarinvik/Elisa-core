//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// named_contract_real_test.go — end-to-end coverage proving that named contracts (docs/97) work
// end-to-end: declaration, uses expansion, requires/ensure discharge under -strict, call-site
// precondition checking, and violation detection. These tests are the ground-truth audit of what
// actually works today.

// Satisfied requires + satisfied ensure → no errors under -strict.
func TestRealNamedContractSatisfiedClean(t *testing.T) {
	src := `
contract AtLeast(x: i64):
    requires x > 0
    ensure result >= x

def identity_positive(n: i64) -> i64:
    uses AtLeast(n)
    return n
`
	result := analyzeContractStrict(t, "real_nc_satisfied.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("body satisfies Bounded.ensure; expected clean analysis, got: %v", errs)
	}
}

// Violated ensure → error under -strict.
func TestRealNamedContractViolatedEnsureErrors(t *testing.T) {
	src := `
contract Bounded(x: i64):
    requires x > 0
    ensure result >= x

def sub_one(n: i64) -> i64:
    uses Bounded(n)
    return n - 1
`
	result := analyzeContractStrict(t, "real_nc_violated_ensure.elisa", src)
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "could not be proven statically") {
		t.Fatalf("ensure result >= n is violated by n-1; expected proof error, got:\n%s", diags)
	}
}

// Call site: passing a literal that satisfies the inherited requires → clean.
func TestRealNamedContractCallSiteSatisfied(t *testing.T) {
	src := `
contract Positive(x: i64):
    requires x > 0
    ensure result > 0

def use_it(x: i64) -> i64:
    uses Positive(x)
    return x

def caller() -> i64:
    return use_it(5)
`
	result := analyzeContractStrict(t, "real_nc_callsite_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("calling use_it(5) satisfies requires x > 0; expected clean, got: %v", errs)
	}
}

// Call site: passing a literal that violates the inherited requires → error.
func TestRealNamedContractCallSiteViolatedErrors(t *testing.T) {
	src := `
contract Positive(x: i64):
    requires x > 0
    ensure result > 0

def use_it(x: i64) -> i64:
    uses Positive(x)
    return x

def bad_caller() -> i64:
    return use_it(0 - 3)
`
	result := analyzeContractStrict(t, "real_nc_callsite_bad.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatal("calling use_it(-3) violates requires x > 0; expected error, got none")
	}
}

// Multiple functions share a contract — both get the same obligations independently.
func TestRealNamedContractSharedByTwoFunctions(t *testing.T) {
	src := `
contract InRange(lo: i64, hi: i64, x: i64):
    requires lo <= x
    requires x <= hi
    ensure result >= lo
    ensure result <= hi

def clamp_a(x: i64) -> i64:
    uses InRange(0, 100, x)
    requires x >= 0
    requires x <= 100
    return x

def clamp_b(x: i64) -> i64:
    uses InRange(0, 100, x)
    requires x >= 0
    requires x <= 100
    return x
`
	result := analyzeContractStrict(t, "real_nc_shared.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("both functions satisfy InRange; expected clean, got: %v", errs)
	}
}

// Unknown contract name → error mentioning the contract name.
func TestRealNamedContractUnknownErrors(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    uses GhostContract(x)
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "real_nc_unknown.elisa", src, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "unknown contract") {
		t.Fatalf("unknown contract name must error; got:\n%s", allDiagnostics(result))
	}
}

// Arity mismatch: contract has 2 params, uses supplies 1 → error.
func TestRealNamedContractArityMismatchErrors(t *testing.T) {
	src := `
contract Pair(a: i64, b: i64):
    requires a <= b

def f(x: i64) -> i64:
    uses Pair(x)
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "real_nc_arity.elisa", src, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "argument(s)") {
		t.Fatalf("arity mismatch must error; got:\n%s", allDiagnostics(result))
	}
}

// Type mismatch: contract formal is bool, uses passes i64 → error naming the formal.
func TestRealNamedContractFormalTypeMismatchErrors(t *testing.T) {
	src := `
contract NeedsBool(flag: bool):
    requires flag

def f(x: i64) -> bool:
    uses NeedsBool(x)
    return true
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "real_nc_type_mismatch.elisa", src, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "expects bool") {
		t.Fatalf("type mismatch on contract formal must error; got:\n%s", allDiagnostics(result))
	}
}

// Contract with no-params must be rejected.
func TestRealNamedContractNoParamsIsError(t *testing.T) {
	src := `
contract Empty():
    requires true

def f() -> bool:
    return true
`
	result := analyzeContractStrict(t, "real_nc_no_params.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatal("contract with no params must error; got none")
	}
}
