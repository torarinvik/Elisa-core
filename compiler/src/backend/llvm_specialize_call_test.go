//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRKeepsGenericCallBindingsPerSpecialization(t *testing.T) {
	src := `struct SharedGate:
    handle: i64

def ctx_concurrency_work1_new[A, R](fn: func(A) -> R, arg: A) -> R:
    return fn(arg)

def run_pair[A, R](fn: func(A) -> R, arg: A) -> R:
    return ctx_concurrency_work1_new(fn, arg)

def work_int(value: i64) -> i64:
    return value + 1

def work_gate(gate: SharedGate) -> i64:
    return gate.handle

def use_int() -> i64:
    return run_pair(work_int, 7)

def use_gate() -> i64:
    gate: SharedGate = SharedGate{handle: 1}
    return run_pair(work_gate, gate)
`
	result := parseAndAnalyzeBackendTest(t, "backend_generic_call_binding_specializations.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "define i64 @run_pair__i64__i64(ptr %0, i64 %1)") {
		t.Fatalf("expected i64 specialization of run_pair, got:\n%s", output)
	}
	if !strings.Contains(output, "call i64 @ctx_concurrency_work1_new__i64__i64(ptr %fn1, i64 %arg2)") {
		t.Fatalf("expected i64 specialization of ctx_concurrency_work1_new call inside run_pair__i64__i64, got:\n%s", output)
	}
	if !strings.Contains(output, "define i64 @run_pair__SharedGate__i64(ptr %0, %SharedGate %1)") {
		t.Fatalf("expected SharedGate specialization of run_pair, got:\n%s", output)
	}
	if !strings.Contains(output, "call i64 @ctx_concurrency_work1_new__SharedGate__i64(ptr %fn1, %SharedGate %arg2)") {
		t.Fatalf("expected SharedGate specialization of ctx_concurrency_work1_new call inside run_pair__SharedGate__i64, got:\n%s", output)
	}
}
