//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// Repeated checked operations must retain every failure edge even when all
// trap sites reuse the module's intrinsic declaration and LLVM function type.
func TestRepeatedCheckedArithmeticTrapSignature(t *testing.T) {
	terms := make([]string, 64)
	for i := range terms {
		terms[i] = "value"
	}
	source := "def checked(value: i64) -> i64:\n    return " + strings.Join(terms, " + ") + "\n"
	result := parseAndAnalyzeBackendTest(t, "repeated_checked_traps.elisa", source)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatal(err)
	}
	if calls := strings.Count(output, "call void @llvm.trap()"); calls != len(terms)-1 {
		t.Fatalf("expected %d void trap calls, got %d", len(terms)-1, calls)
	}
	if declarations := strings.Count(output, "declare void @llvm.trap()"); declarations != 1 {
		t.Fatalf("expected one shared trap declaration, got %d", declarations)
	}
	if terminators := strings.Count(output, "unreachable"); terminators != len(terms)-1 {
		t.Fatalf("expected %d unreachable terminators, got %d", len(terms)-1, terminators)
	}
}
