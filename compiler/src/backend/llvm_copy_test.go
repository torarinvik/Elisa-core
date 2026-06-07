//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// copy[array[T, N]](src) lowers to a fixed-size, stack-owned array materialized
// element-for-element from a fixed-size array source — no region allocation.
func TestGenerateLLVMIRLowersCopyBuiltinForFixedArray(t *testing.T) {
	src := `def dup(src: array[u8, 4]) -> array[u8, 4]:
    return copy[array[u8, 4]](src)
`
	result := parseAndAnalyzeBackendTest(t, "backend_copy_builtin.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "define [4 x i8] @dup") {
		t.Fatalf("expected fixed-array copy lowering to define [4 x i8] @dup, got:\n%s", output)
	}
	// Stack owner: must not call into the region/arena allocator.
	if strings.Contains(output, "arena_alloc") || strings.Contains(output, "clone.alloc") {
		t.Fatalf("expected copy to allocate no region storage, got:\n%s", output)
	}
}

// A darray grow lowers an allocation-size overflow guard: count*elemSize is
// checked against usize overflow and traps rather than under-allocating
// (deep audit #3/#4 — a no-Unsafe OOB path).
func TestGenerateLLVMIRDarrayGrowthHasOverflowGuard(t *testing.T) {
	src := `def build(owner: mutable Arena&, n: usize) -> usize:
    xs: mutable darray[u32] = []
    in owner:
        for i in 0..<n:
            xs.push(7)
    return xs.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_overflow.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "size.overflow") {
		t.Fatalf("expected darray growth to emit an allocation-size overflow guard, got:\n%s", output)
	}
}

func TestGenerateLLVMIRDArrayReserveBoundArithmeticIsChecked(t *testing.T) {
	src := `def build(owner: mutable Arena&, n: usize, m: usize) -> usize:
    xs: mutable darray[u8] = []
    in owner:
        xs.reserve(n * m)
    return xs.capacity
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_reserve_bound_overflow.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"llvm.umul.with.overflow", "darray.reserve.bound.mul.overflow"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected reserve bound arithmetic to emit %q, got:\n%s", check, output)
		}
	}
}

// clone[darray[u8]](sview) lowers to a region allocation plus a byte-copy loop
// from the view's (ptr, len), producing an owned darray[u8] / dstr.
func TestGenerateLLVMIRLowersCloneSViewIntoOwnedBytes(t *testing.T) {
	src := `def persist(owner: mutable Arena&, text: sview) -> darray[u8]:
    can Abort.Panic, Memory.Allocate:
        in owner:
            return clone[darray[u8]](text)
`
	result := parseAndAnalyzeBackendTest(t, "backend_clone_sview.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define", "@persist", "clone.alloc", "clone.body"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected sview clone lowering to include %q, got:\n%s", check, output)
		}
	}
}
