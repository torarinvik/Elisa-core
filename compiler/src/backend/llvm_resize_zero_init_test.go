//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// Deep audit #9: `da.resize(n)` advances the live count without writing the new slots.
// The bump allocator never zeroes, so for element types whose uninitialized bytes are
// unsafe to observe as a valid value (references, handles, nested dynamic containers),
// the grown tail must be zero-filled — otherwise a read-before-write sees recycled heap
// garbage as a wild pointer/handle (a no-Unsafe-needed memory-safety hole).
func TestGenerateLLVMIRResizeZeroFillsContainerElementTail(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_resize_zero_fill_container.elisa", `
def kernel() -> usize:
	xs: mutable darray[view[u8]] = []
	_ = xs.resize(8.usize())
	return xs.count
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "darray.resize.zerofill") {
		t.Fatalf("expected resize of a container-element darray to zero-fill the grown tail, got:\n%s", output)
	}
}

// Complement: a POD (scalar) element darray keeps the fast path — no zero-fill — because
// its uninitialized bytes are a benign garbage scalar and the resize is always paired with
// an index-fill loop that writes every slot. Zero-filling here would be a pure perf
// regression on the hot comprehension-vectorization path.
func TestGenerateLLVMIRResizeSkipsZeroFillForPODElements(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_resize_no_zero_fill_pod.elisa", `
def kernel() -> usize:
	xs: mutable darray[i64] = []
	_ = xs.resize(8.usize())
	return xs.count
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "darray.resize.zerofill") {
		t.Fatalf("expected POD-element resize to skip the zero-fill fast path, got:\n%s", output)
	}
}
