package semantic

import (
	"strings"
	"testing"
)

func TestLambdaCallbackParameterErrorFamilies(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lambda_callback_parameter_errors.elisa", `
error A:
    AFail
error B:
    BFail
def consume(first: fn() -> i64 error[A], second: fn() -> i64 error[B]) -> i64 error[A]:
    return 1
def good() -> i64 error[A]:
    return consume(fn() => raise A.AFail, fn() => raise B.BFail)
def bad_first() -> i64 error[A]:
    return consume(fn() => raise B.BFail, fn() => raise B.BFail)
def bad_second() -> i64 error[A]:
    return consume(fn() => raise A.AFail, fn() => raise A.AFail)
`)
	errors := strings.Join(result.Errors(), "\n")
	if n := strings.Count(errors, "raise cannot propagate tag"); n != 2 {
		t.Fatalf("expected the two mismatched callback errors, got %d:\n%s", n, errors)
	}
}

func TestLambdaCallbackSameNamedTagsRemainDistinct(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "lambda_callback_same_tag.elisa", `
error A:
    Bad
error B:
    Bad
def consume(first: fn() -> i64 error[A], second: fn() -> i64 error[B]) -> i64 error[A]:
    return 1
def good() -> i64 error[A]:
    return consume(fn() => raise A.Bad, fn() => raise B.Bad)
def bad_first() -> i64 error[A]:
    return consume(fn() => raise B.Bad, fn() => raise B.Bad)
def bad_second() -> i64 error[A]:
    return consume(fn() => raise A.Bad, fn() => raise A.Bad)
def good_named() -> i64 error[A]:
    return consume(second: fn() => raise B.Bad, first: fn() => raise A.Bad)
def bad_named() -> i64 error[A]:
    return consume(second: fn() => raise A.Bad, first: fn() => raise A.Bad)
`)
	if errors := strings.Join(result.Errors(), "\n"); strings.Count(errors, "raise cannot propagate tag") != 3 {
		t.Fatalf("expected exactly the three cross-family callback errors, got:\n%s", errors)
	}
}

func TestErrorTagMatchingAllowsUnqualifiedArmsOnly(t *testing.T) {
	set := &ErrorSetType{Tags: []string{"A.Bad"}}
	if _, ok := MatchErrorTag(set, "B.Bad"); ok {
		t.Fatal("a qualified tag from a different family matched by leaf")
	}
	if got, ok := MatchErrorTag(set, "Bad"); !ok || got != "A.Bad" {
		t.Fatalf("unqualified unique tag did not match: %q, %v", got, ok)
	}
}

func TestInfallibleCalleeStillChecksCallbackErrors(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "infallible_callee_callback.elisa", `
error A:
    Bad
error B:
    Bad
def consume(callback: fn() -> i64 error[A]) -> i64:
    return 1
def consume_pure(callback: fn() -> i64) -> i64:
    return 1
def good() -> i64:
    return consume(fn() => raise A.Bad)
def bad() -> i64:
    return consume(fn() => raise B.Bad)
def bad_pure() -> i64:
    return consume_pure(fn() => raise A.Bad)
`)
	errors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errors, "raise cannot propagate tag") || !strings.Contains(errors, `argument 1 to "consume_pure" expects`) {
		t.Fatalf("infallible callees must retain callback error contracts, got:\n%s", errors)
	}
}
