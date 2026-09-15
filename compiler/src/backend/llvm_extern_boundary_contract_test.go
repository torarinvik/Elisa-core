//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// Native extern ensures are checked at the call boundary in checked builds. This protects
// memory-safety facts that the semantic analyzer intentionally assumes after the call.
func TestExternEnsureEmitsRuntimeBoundaryCheck(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "extern_boundary_check.elisa", `
@callconv(c)
extern answer() -> i64 ensure result == 42

def caller() -> i64:
    return answer()
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("fixture must analyze, got:\n%s", strings.Join(errs, "\n"))
	}
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(output, "extern postcondition failed") {
		t.Fatalf("expected an extern postcondition failure literal in checked IR, got:\n%s", output)
	}
	if !strings.Contains(output, "contract.fail") {
		t.Fatalf("expected an extern postcondition failure block in checked IR, got:\n%s", output)
	}
}

func TestTrustedExternSkipsRuntimeBoundaryCheck(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "trusted_extern_boundary.elisa", `
@callconv(c)
@trusted("validated by the C library test suite")
extern answer() -> i64 ensure result == 42

def caller() -> i64:
    return answer()
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("fixture must analyze, got:\n%s", strings.Join(errs, "\n"))
	}
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(output, "extern postcondition failed") {
		t.Fatalf("trusted extern must not emit a runtime postcondition check, got:\n%s", output)
	}
}
