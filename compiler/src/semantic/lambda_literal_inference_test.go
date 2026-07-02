//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// An untyped lambda whose body is a bare numeric literal, passed where a
// `func(i64) -> i64` is expected, must infer the literal as `i64` (contextual
// typing) rather than the abstract `int`, so the argument type-checks.
func TestUntypedLambdaNumericLiteralBodyInfersContextualReturn(t *testing.T) {
	src := `
def apply(f: func(i64) -> i64, x: i64) -> i64:
    return f(x)

def use() -> i64:
    return apply(fn(n) => 5, 3)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lambda_literal.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a bare numeric-literal lambda body must adopt the contextual i64 return; got: %v", errs)
	}
}

// The arithmetic-body variant (`n + 1`) must likewise type-check against the
// expected function type.
func TestUntypedLambdaArithmeticBodyInfersContextualReturn(t *testing.T) {
	src := `
def apply(f: func(i64) -> i64, x: i64) -> i64:
    return f(x)

def use() -> i64:
    return apply(fn(n) => n + 1, 3)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lambda_arith.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("an arithmetic lambda body must type-check against func(i64)->i64; got: %v", errs)
	}
}

// Contextual return typing must NOT mask a genuinely wrong-typed body: a lambda
// returning a string where an i64 is expected is still rejected.
func TestUntypedLambdaWrongTypedBodyStillErrors(t *testing.T) {
	src := `
def apply(f: func(i64) -> i64, x: i64) -> i64:
    return f(x)

def use() -> i64:
    return apply(fn(n) => "hi", 3)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lambda_wrong.elisa", src)
	joined := strings.Join(result.Errors(), "\n")
	if len(result.Errors()) == 0 {
		t.Fatalf("a string-bodied lambda passed where func(i64)->i64 is expected must error")
	}
	if !strings.Contains(joined, "i64") {
		t.Fatalf("the mismatch diagnostic should reference the expected i64 return; got: %v", result.Errors())
	}
}

// An explicitly-typed lambda parameter and return are unchanged by the
// contextual path.
func TestExplicitlyTypedLambdaUnchanged(t *testing.T) {
	src := `
def apply(f: func(i64) -> i64, x: i64) -> i64:
    return f(x)

def use() -> i64:
    return apply(fn(n: i64) -> i64 => n + 1, 3)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lambda_explicit.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("an explicitly-typed lambda must continue to type-check; got: %v", errs)
	}
}
