//go:build cgo

package backend

import (
	"strings"
	"testing"

	"elisacore/src/semantic"
)

func TestGenericSpecializationKeepsFunctionFastMath(t *testing.T) {
	t.Setenv("ELISACORE_FAST_MATH", "")
	t.Setenv("ELISACORE_STRICT_FP", "")
	result := parseAndAnalyzeBackendTest(t, "generic_fast_math.elisa", `
@fast_math
def opted[T](value: f64) -> f64:
    return value + value

def ordinary[T](value: f64) -> f64:
    return value + value

def run(value: f64) -> f64:
    return opted[f64](value) + ordinary[f64](value)
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(output, "fadd fast double"); count != 1 {
		t.Fatalf("expected exactly the opted specialization to use full fast-math, got %d:\n%s", count, output)
	}
	if !strings.Contains(output, "fadd contract double") {
		t.Fatalf("ordinary floating-point code lost its default policy:\n%s", output)
	}
}

func TestBackendFunctionTypeSubstitutionKeepsFastMath(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		source := &semantic.FuncType{FastMath: enabled}
		result, ok := substituteType(source, nil, nil).(*semantic.FuncType)
		if !ok || result == source || result.FastMath != enabled {
			t.Fatalf("fast-math=%v: backend substitution lost floating-point policy", enabled)
		}
	}
}
