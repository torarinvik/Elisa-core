//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRUsesLibcMemsetCIntABI(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_memset_c_int_abi.elisa", `
extern memset(dest: mutable void&, value: i32, size: usize) -> mutable void&

def arena_da_fill[T](dst: view[T], value: T) -> void:
	return

def kernel(buf: view[u8]) -> void:
	whole: view[u8] = buf[0:16]
	arena_da_fill(whole, 7)
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "declare ptr @memset(ptr, i32, i64)") {
		t.Fatalf("expected libc memset to use the C int/i32 ABI, got:\n%s", output)
	}
	if strings.Contains(output, "declare ptr @memset(ptr, i64, i64)") {
		t.Fatalf("Elisa's 64-bit int must not be used for libc memset's fill parameter:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesLibcMemcmpCIntABI(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_memcmp_c_int_abi.elisa", `
extern memcmp(left: void&, right: void&, size: usize) -> i32

def equal(left: sview, right: sview) -> bool:
	return left == "this static string is long enough to require memcmp"
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "declare i32 @memcmp(ptr, ptr, i64)") {
		t.Fatalf("expected libc memcmp to use the C int/i32 ABI, got:\n%s", output)
	}
	if !strings.Contains(output, "call i32 @memcmp(") {
		t.Fatalf("expected dynamic string equality to call the declared memcmp ABI, got:\n%s", output)
	}
	if strings.Contains(output, "declare i64 @memcmp(ptr, ptr, i64)") {
		t.Fatalf("Elisa's 64-bit int must not be used for libc memcmp's result:\n%s", output)
	}
}
