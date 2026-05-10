package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersOptionalMatchWithNullArm(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_optional_match_null_arm.elisa", `
enum Expr:
    Int(value: i64)
    Missing

def score(maybe: Expr?) -> i64:
    match maybe:
        null:
            return 0
        Expr.Int(value):
            return value
        _:
            return 2
    return 3
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"optional.present", "optional.payload", "match.optional"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected optional match IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersOptionalMatchExpressionWithNullArm(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_optional_match_expr_null_arm.elisa", `
enum Expr:
    Int(value: i64)
    Missing

def score(maybe: Expr?) -> i64:
    return match maybe:
        null:
            0
        Expr.Int(value):
            value
        _:
            2
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"optional.present", "optional.payload", "match.optional.expr", "match.optional.expr.result"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected optional match expression IR to contain %q, got:\n%s", check, output)
		}
	}
}
