//go:build cgo

package backend

import (
	"elisacore/src/semantic"
	"strings"
	"testing"
)

func TestGenerateLLVMIRSpecializesOptionalNarrowingPayload(t *testing.T) {
	src := `def maybe(value: i64) -> i64?:
    return value if value > 0 else null

def map[T, U](value: T?, transform: fn(T) -> U) -> U?:
    return transform(present) if value is present else null

def add_one(value: i64) -> i64:
    return value + 1

def main() -> i64:
    mapped: i64? = map(maybe(4), add_one)
    return present if mapped is present else 10
`
	result := parseAndAnalyzeBackendTest(t, "backend_optional_generic_narrowing.elisa", src)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	symbol := genericIRSymbolForTest(t, "map", &semantic.BuiltinType{Name: "i64"}, &semantic.BuiltinType{Name: "i64"})
	start := strings.Index(output, "@"+symbol+"(")
	if start < 0 {
		t.Fatalf("expected i64 optional specialization, got:\n%s", output)
	}
	end := strings.Index(output[start:], "\ndefine ")
	if end < 0 {
		end = len(output) - start
	}
	body := output[start : start+end]
	if strings.Contains(body, "load ptr, ptr %present.cond") {
		t.Fatalf("optional payload narrowed as a pointer in specialized body:\n%s", body)
	}
	if !strings.Contains(body, "load i64") {
		t.Fatalf("expected specialized optional payload to remain i64:\n%s", body)
	}
}

func TestGenerateLLVMIRIndexesFieldOfNarrowedOptionalGlobal(t *testing.T) {
	src := `struct Snapshot:
    items: darray[i64]

global mutable saved: Snapshot? = null

def read_back() -> i64:
    can Global.Read:
        return -1 if saved == null
        return saved.items[1]
`
	result := parseAndAnalyzeBackendTest(t, "narrowed_optional_global.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "(%Optional__Snapshot, ptr @saved, i32 0, i32 1)") {
		t.Fatalf("indexing a narrowed global must address the optional payload:\n%s", output)
	}
}
