//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/101: a function-typed parameter carries a postcondition contract via `ensures(f, <pred>)`. A
// passed NAMED function whose declared `ensure` entails the predicate proves; inside the callee, a
// call through the contracted parameter may assume the predicate, discharging a downstream `ensure`.

// The satisfying case: `g` is declared `ensure result >= 0`, which entails `apply`'s required
// `ensures(f, result >= 0)`, and inside `apply` the call `f(x)` lets `ensure result >= 0` discharge.
func TestHigherOrderParamContractSatisfyingProves(t *testing.T) {
	src := `
def apply(f: func(i64) -> i64, x: i64) -> i64:
    requires ensures(f, result >= 0)
    ensure result >= 0
    return f(x)

def g(n: i64) -> i64:
    ensure result >= 0
    return 7

def use() -> i64:
    return apply(g, 3)
`
	result := analyzeContractStrict(t, "ho_satisfying.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("g satisfies apply's param contract and the body assumption discharges the ensure; got: %v", errs)
	}
}

// The non-satisfying case: `bad` ensures nothing relevant (`result <= 100`), so it does NOT entail
// `apply`'s required `ensures(f, result >= 0)` — the call site must error.
func TestHigherOrderParamContractNonSatisfyingErrors(t *testing.T) {
	src := `
def apply(f: func(i64) -> i64, x: i64) -> i64:
    requires ensures(f, result >= 0)
    ensure result >= 0
    return f(x)

def bad(n: i64) -> i64:
    ensure result <= 100
    return n

def use() -> i64:
    return apply(bad, 3)
`
	result := analyzeContractStrict(t, "ho_nonsatisfying.elisa", src)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "does not satisfy its contract") {
		t.Fatalf("bad does not entail `result >= 0`; the call site must error, got: %v", result.Errors())
	}
}

// A function with NO ensure cannot satisfy a non-trivial param contract: empty range entails nothing.
func TestHigherOrderParamContractNoEnsureErrors(t *testing.T) {
	src := `
def apply(f: func(i64) -> i64, x: i64) -> i64:
    requires ensures(f, result >= 0)
    ensure result >= 0
    return f(x)

def plain(n: i64) -> i64:
    return n

def use() -> i64:
    return apply(plain, 3)
`
	result := analyzeContractStrict(t, "ho_noensure.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "does not satisfy its contract") {
		t.Fatalf("a function with no ensure cannot satisfy the param contract; got: %v", result.Errors())
	}
}

// The body assumption is real: with `ensures(f, result >= 0)`, a downstream `assert` on the call
// result discharges. Without the param contract the same assert would not prove (covered indirectly by
// the no-ensure error path); here we confirm the positive direction reaches a downstream obligation.
func TestHigherOrderParamContractAssumptionDischargesAssert(t *testing.T) {
	src := `
def apply(f: func(i64) -> i64, x: i64) -> i64:
    requires ensures(f, result >= 5)
    ensure result >= 0
    return f(x)

def g(n: i64) -> i64:
    ensure result >= 5
    return 10

def use() -> i64:
    return apply(g, 0)
`
	result := analyzeContractStrict(t, "ho_assume.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`result >= 5` from f entails `result >= 0`; got: %v", errs)
	}
}

// docs/101 closure extension: a LAMBDA passed for a contracted function-typed parameter is verified
// against the param contract by inferring its body's result range, exactly as a named function's
// declared `ensure` is. A satisfying lambda proves; a non-satisfying one errors; an undecidable body
// declines (fail-closed → error).

// Satisfying lambda: `n if n >= 0 else 0` is always >= 0 (the n-arm is guarded n>=0, the else-arm is
// the literal 0), so it entails `ensures(f, result >= 0)`, and the body assumption discharges `ensure`.
func TestHigherOrderLambdaSatisfyingProves(t *testing.T) {
	src := `
def apply(f: func(i64) -> i64, x: i64) -> i64:
    requires ensures(f, result >= 0)
    ensure result >= 0
    return f(x)

def use() -> i64:
    return apply(fn(n) => n if n >= 0 else 0, 3)
`
	result := analyzeContractStrict(t, "ho_lambda_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("lambda result is always >= 0 and the body assumption discharges; got: %v", errs)
	}
}

// A constant lambda whose result is literally 5 entails `result >= 0`.
func TestHigherOrderLambdaConstantProves(t *testing.T) {
	src := `
def apply(f: func(i64) -> i64, x: i64) -> i64:
    requires ensures(f, result >= 0)
    ensure result >= 0
    return f(x)

def use() -> i64:
    return apply(fn(n) -> i64 => 5, 3)
`
	result := analyzeContractStrict(t, "ho_lambda_const.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("constant 5 entails result >= 0; got: %v", errs)
	}
}

// Non-satisfying lambda: `n - 1` has no provable lower bound, so it does NOT entail `result >= 0`.
// The call site must error (fail-closed), never silently pass.
func TestHigherOrderLambdaNonSatisfyingErrors(t *testing.T) {
	src := `
def apply(f: func(i64) -> i64, x: i64) -> i64:
    requires ensures(f, result >= 0)
    ensure result >= 0
    return f(x)

def use() -> i64:
    return apply(fn(n) => n - 1, 3)
`
	result := analyzeContractStrict(t, "ho_lambda_bad.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "does not satisfy its contract") {
		t.Fatalf("lambda `n - 1` is not provably >= 0; call site must error, got: %v", result.Errors())
	}
}

// The body assumption is real for lambdas too: with `ensures(f, result >= 5)` a satisfying lambda
// (`n if n >= 5 else 5`, always >= 5) lets the downstream `ensure result >= 0` discharge.
func TestHigherOrderLambdaAssumptionDischarges(t *testing.T) {
	src := `
def apply(f: func(i64) -> i64, x: i64) -> i64:
    requires ensures(f, result >= 5)
    ensure result >= 0
    return f(x)

def use() -> i64:
    return apply(fn(n) => n if n >= 5 else 5, 0)
`
	result := analyzeContractStrict(t, "ho_lambda_assume.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`result >= 5` from the lambda entails `result >= 0`; got: %v", errs)
	}
}

// Naming a non-function parameter in ensures(...) is a hard error (the clause is meaningless).
func TestHigherOrderParamContractNonFuncParamErrors(t *testing.T) {
	src := `
def apply(f: func(i64) -> i64, x: i64) -> i64:
    requires ensures(x, result >= 0)
    ensure result >= 0
    return f(x)
`
	result := analyzeContractStrict(t, "ho_nonfuncparam.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "not a function-typed parameter") {
		t.Fatalf("ensures(x, ...) names a non-function param; must error, got: %v", result.Errors())
	}
}
