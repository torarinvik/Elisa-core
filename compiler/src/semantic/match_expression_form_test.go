package semantic

import (
	"strings"
	"testing"
)

// `match` in expression position (RHS of an assignment, each arm yielding a value, the whole
// match evaluating to the joined arm type). These pin parse + analyze for the value-yielding
// form over both enum and integer scrutinees, reusing the statement-match exhaustiveness checker.

// Enum match-expression: an exhaustive value-match assigns the joined arm type.
func TestAnalyzeEnumMatchExpressionAssignsValue(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "enum_match_expr.elisa", `enum Color:
    Red
    Green
    Blue

def f(c: Color) -> i64:
    x: i64 = match c:
        Color.Red: 0
        Color.Green: 1
        Color.Blue: 2
    return x
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Integer match-expression with a `_` wildcard assigns a value.
func TestAnalyzeIntegerMatchExpressionAssignsValue(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "int_match_expr.elisa", `def f(k: i64) -> i64:
    n: i64 = match k:
        0: 100
        _: 200
    return n
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Mismatched arm value types are an error (no common joined type).
func TestAnalyzeMatchExpressionMismatchedArmsError(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "mismatch_match_expr.elisa", `def f(k: i64) -> i64:
    n = match k:
        0: 100
        _: "hello"
    return 0
`, AnalyzeOptions{})
	errs := result.Errors()
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "incompatible") {
		t.Fatalf("expected an incompatible-arms error, got:\n%s", strings.Join(errs, "\n"))
	}
}

// A non-exhaustive value-match is an error (exhaustiveness applies just like statement match).
func TestAnalyzeMatchExpressionNonExhaustiveError(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "nonexhaustive_match_expr.elisa", `enum Color:
    Red
    Green
    Blue

def f(c: Color) -> i64:
    x: i64 = match c:
        Color.Red: 0
        Color.Green: 1
    return x
`, AnalyzeOptions{})
	errs := result.Errors()
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "non-exhaustive") {
		t.Fatalf("expected a non-exhaustive-match error, got:\n%s", strings.Join(errs, "\n"))
	}
}
