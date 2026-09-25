//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRPreservesGenericSViewRegionThroughDArrayGrowth(t *testing.T) {
	src := `struct StringHandle[@r]:
    view: sview @r

def unwrap_view[@r](handle: StringHandle[r]) -> sview @r:
    return handle.view

def collect_view[@r](handle: StringHandle[r]) -> usize:
    output: mutable darray[sview] @r = []
    value: sview @r = unwrap_view(handle)
    output.push(value)
    count: usize = output.count
    output.clear()
    return count
`
	result := parseAndAnalyzeBackendTest(t, "backend_generic_sview_darray_region.elisa", src)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("generic sview region through darray growth should lower: %v", err)
	}
	if strings.Contains(output, "<null operand!") {
		t.Fatalf("generic sview region must lower with valid hidden arena operands, got invalid LLVM IR:\n%s", output)
	}
	if !strings.Contains(output, "call %StringView @unwrap_view__r") {
		t.Fatalf("generic view accessor should receive the borrowed region:\n%s", output)
	}
	if !strings.Contains(output, "call ptr @arena_alloc(ptr %1,") || !strings.Contains(output, "call ptr @arena_realloc(ptr %1,") {
		t.Fatalf("darray growth must allocate and reallocate in the same borrowed region:\n%s", output)
	}
}
