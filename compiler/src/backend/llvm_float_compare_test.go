//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRFloatNotEqualUsesUnorderedPredicate(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "float_not_equal_unordered.elisa", `
def not_equal(left: f64, right: f64) -> bool:
	return left != right
`)

	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	body := funcBody(t, output, "not_equal")
	if !strings.Contains(body, "fcmp une double") {
		t.Fatalf("expected float != to use unordered-not-equal, got:\n%s", body)
	}
	if strings.Contains(body, "fcmp one double") || strings.Contains(body, "fcmp ueq double") {
		t.Fatalf("float != must not use ordered-not-equal or unordered-equal, got:\n%s", body)
	}
}
