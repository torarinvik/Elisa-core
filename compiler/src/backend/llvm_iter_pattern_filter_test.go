package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersIterablePatternFilter(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_iter_pattern_filter.elisa", `
enum Expr:
    Int(value: i64)
    Missing

def sum(items: array[Expr, 3]) -> i64:
    total: mutable i64 = 0
    for item in items where Expr.Int(value):
        total <- total + value
    return total
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"iter.pattern.filter.body", "match.tag", "iter.step"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected iterable pattern filter IR to contain %q, got:\n%s", check, output)
		}
	}
}
