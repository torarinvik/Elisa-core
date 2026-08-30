//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// Large source-level aggregates use the internal sret ABI: the LLVM function returns void and
// receives a leading destination pointer. Keep this test close to the backend because a named
// LLVM call result is invalid for that shape, even though the Elisa expression has a value.
func TestGenerateLLVMIRLeavesLargeAggregateCallsUnnamed(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_sret_call.elisa", `struct LargeResult:
    words: i64[200]

def make_large() -> LargeResult:
    value: LargeResult = zeroed
    value.words[0] <- 7
    return value

def read_large() -> i64:
    value: LargeResult = make_large()
    return value.words[0]

def pass_large(value: LargeResult) -> LargeResult:
    return value

def read_param_large() -> i64:
    source: LargeResult = zeroed
    value: LargeResult = pass_large(source)
    return value.words[0]

def identity[T](value: T) -> T:
    return value

def read_generic_large() -> i64:
    value: LargeResult = identity[LargeResult](make_large())
    return value.words[0]
`)
	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()

	output := g.printModule()
	if !strings.Contains(output, "call void @make_large(ptr sret(%LargeResult)") {
		t.Fatalf("expected large aggregate call to use the sret ABI, got IR:\n%s", output)
	}
	if strings.Contains(output, "%call = call void @make_large") {
		t.Fatalf("large aggregate call must be unnamed, got invalid named void call:\n%s", output)
	}
	if !strings.Contains(output, "call void @pass_large(ptr sret(%LargeResult)") || !strings.Contains(output, "ptr byval(%LargeResult)") {
		t.Fatalf("large aggregate parameter/return call must use the memory-class ABI, got IR:\n%s", output)
	}
	genericCallStart := strings.Index(output, "call void @identity__LargeResult(")
	if genericCallStart < 0 {
		t.Fatalf("generic large aggregate call must use the specialized sret ABI, got IR:\n%s", output)
	}
	genericCallEnd := strings.Index(output[genericCallStart:], "\n")
	if genericCallEnd < 0 || !strings.Contains(output[genericCallStart:genericCallStart+genericCallEnd], "ptr sret(%LargeResult)") || !strings.Contains(output[genericCallStart:genericCallStart+genericCallEnd], "ptr byval(%LargeResult)") {
		t.Fatalf("generic large aggregate call must use sret and byval arguments, got IR:\n%s", output)
	}
}
