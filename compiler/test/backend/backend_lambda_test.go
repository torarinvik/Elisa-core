package backend_test

import (
	"strings"
	"testing"

	"elisacore/src/backend"
)

func TestGenerateLLVMIRLowersCapturelessLambdaHelpers(t *testing.T) {
	src := `def apply(fn: fn(i64) -> i64, value: i64) -> i64:
    return fn(value)

def run() -> i64:
    return apply(lambda value: value + 1, 41)
`

	result := parseAndAnalyze(t, "backend_lambda_captureless.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	if !strings.Contains(output, "define i64 @lambda_") {
		t.Fatalf("expected a synthetic lambda helper in output, got:\n%s", output)
	}
	if !strings.Contains(output, "call i64 @apply(ptr @lambda_") {
		t.Fatalf("expected captureless lambda to lower as a raw function pointer, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersCapturedLambdasThroughClosureDispatch(t *testing.T) {
	src := `def apply(fn: fn(i64) -> i64, value: i64) -> i64:
    return fn(value)

def run(offset: i64) -> i64:
    return apply(lambda value: value + offset, 41)
`

	result := parseAndAnalyze(t, "backend_lambda_closure.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	if !strings.Contains(output, "%ElisaCoreLambdaClosure = type { ptr, ptr }") {
		t.Fatalf("expected closure carrier type in output, got:\n%s", output)
	}
	if count := strings.Count(output, "call ptr @malloc(i64"); count < 2 {
		t.Fatalf("expected captured lambda lowering to allocate env and closure, got %d mallocs:\n%s", count, output)
	}
	applyIR := functionIR(output, "apply")
	for _, want := range []string{"calltmp.callee.is_closure", "calltmp.closure.code", "call i64 %calltmp.closure.code("} {
		if !strings.Contains(applyIR, want) {
			t.Fatalf("expected apply lowering to contain %q, got:\n%s", want, applyIR)
		}
	}
}
