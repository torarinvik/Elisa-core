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

def spawn1[A, R](fn: func(A) -> R, arg: A) -> R:
    return ctx_concurrency_work1_new(fn, arg)

def work_int(value: i64) -> i64:
    return value + 1

def work_gate(gate: SharedGate) -> i64:
    return gate.handle

def use_int() -> i64:
    return spawn1(work_int, 7)

def use_gate() -> i64:
    gate: SharedGate = SharedGate(1)
    return spawn1(work_gate, gate)
`
	result := parseAndAnalyzeBackendTest(t, "backend_generic_call_binding_specializations.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "define i64 @spawn1__i64__i64(ptr %0, i64 %1)") {
		t.Fatalf("expected i64 specialization of spawn1, got:\n%s", output)
	}
	if !strings.Contains(output, "call i64 @ctx_concurrency_work1_new__i64__i64(ptr %fn1, i64 %arg2)") {
		t.Fatalf("expected i64 specialization of ctx_concurrency_work1_new call inside spawn1__i64__i64, got:\n%s", output)
	}
	if !strings.Contains(output, "define i64 @spawn1__SharedGate__i64(ptr %0, %SharedGate %1)") {
		t.Fatalf("expected SharedGate specialization of spawn1, got:\n%s", output)
	}
	if !strings.Contains(output, "call i64 @ctx_concurrency_work1_new__SharedGate__i64(ptr %fn1, %SharedGate %arg2)") {
		t.Fatalf("expected SharedGate specialization of ctx_concurrency_work1_new call inside spawn1__SharedGate__i64, got:\n%s", output)
	}
}
