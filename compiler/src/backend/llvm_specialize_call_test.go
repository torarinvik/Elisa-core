//go:build cgo

package backend

import (
	"elisacore/src/semantic"
	"fmt"
	"strings"
	"testing"
)

func TestGenerateLLVMIRKeepsGenericCallBindingsPerSpecialization(t *testing.T) {
	src := `struct SharedGate:
    handle: i64

def ctx_concurrency_work1_new[A, R](fn: fn(A) -> R, arg: A) -> R:
    return fn(arg)

def run_pair[A, R](fn: fn(A) -> R, arg: A) -> R:
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
	i64type := &semantic.BuiltinType{Name: "i64"}
	gateType := &semantic.StructType{Name: "SharedGate"}
	pairInt := genericIRSymbolForTest(t, "run_pair", i64type, i64type)
	callInt := genericIRSymbolForTest(t, "ctx_concurrency_work1_new", i64type, i64type)
	pairGate := genericIRSymbolForTest(t, "run_pair", gateType, i64type)
	callGate := genericIRSymbolForTest(t, "ctx_concurrency_work1_new", gateType, i64type)
	if !strings.Contains(output, fmt.Sprintf("define i64 @%s(ptr %%0, i64 %%1)", pairInt)) {
		t.Fatalf("expected i64 specialization of run_pair, got:\n%s", output)
	}
	if !strings.Contains(output, fmt.Sprintf("call i64 @%s(ptr %%fn1, i64 %%arg2)", callInt)) {
		t.Fatalf("expected i64 specialization of ctx_concurrency_work1_new call inside run_pair__i64__i64, got:\n%s", output)
	}
	if !strings.Contains(output, fmt.Sprintf("define i64 @%s(ptr %%0, %%SharedGate %%1)", pairGate)) {
		t.Fatalf("expected SharedGate specialization of run_pair, got:\n%s", output)
	}
	if !strings.Contains(output, fmt.Sprintf("call i64 @%s(ptr %%fn1, %%SharedGate %%arg2)", callGate)) {
		t.Fatalf("expected SharedGate specialization of ctx_concurrency_work1_new call inside run_pair__SharedGate__i64, got:\n%s", output)
	}
}

func TestGenerateLLVMIRSharesTypeSpecializationAcrossExplicitLifetimes(t *testing.T) {
	source := `def make[T, @r](value: T) -> darray[T] @r:
    can Memory.Allocate, Abort.Panic:
        values: mutable darray[T] @r = []
        values.push(value)
        return values

def main() -> i64:
    can Memory.Allocate, Abort.Panic:
        total: mutable i64 = 0
        region first:
            values: darray[i64] @first = make[i64, first](7)
            total <- total + values[0]
        region second:
            values: darray[i64] @second = make[i64, second](9)
            total <- total + values[0]
        return total
`
	result := parseAndAnalyzeBackendTest(t, "mixed_explicit_lifetime.elisa", source)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatal(err)
	}
	makeSymbol := genericIRSymbolForTest(t, "make", &semantic.BuiltinType{Name: "i64"})
	definitions, calls := 0, 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "define ") && strings.Contains(line, `@"make$generic_v1$`) {
			definitions++
		}
		if strings.Contains(line, "call ") && strings.Contains(line, "@"+makeSymbol+"(") {
			calls++
		}
	}
	if definitions != 1 || calls != 2 {
		t.Fatalf("expected one type-specialized body shared by two lifetime applications, got definitions=%d calls=%d", definitions, calls)
	}
}
