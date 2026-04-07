package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersDArrayBuilderSugar(t *testing.T) {
	src := `def build(owner: Arena) -> usize:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
    in alloc:
        xs: mutable darray[i64] = []
        xs.push(1)
        xs.push(2)
        ys: mutable darray[i64] = [3, 4]
        return xs.count + ys.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_builder.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@arena_alloc") {
		t.Fatalf("expected darray push sugar to lower through arena allocation, got:\n%s", output)
	}
	if !strings.Contains(output, "darray.push.slot") {
		t.Fatalf("expected darray push sugar to compute an element slot, got:\n%s", output)
	}
	if !strings.Contains(output, "@arena_alloc") {
		t.Fatalf("expected non-empty darray literal to lower through arena_alloc, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersDArrayExtendSugar(t *testing.T) {
	src := `def build(owner: Arena) -> usize:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
    in alloc:
        xs: mutable darray[int] = []
        xr: mutable any darray[int]& = (&xs).cast[mutable any darray[int]&]
        xr.extend([1, 2, 3])
        return xs.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_extend.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@arena_memcpy") {
		t.Fatalf("expected darray extend sugar to lower to arena_memcpy, got:\n%s", output)
	}
	if !strings.Contains(output, "darray.extend.memcpy") {
		t.Fatalf("expected darray extend sugar to emit a memcpy call site, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersDArrayReserveSugar(t *testing.T) {
	src := `def build(owner: Arena) -> usize:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
    in alloc:
        xs: mutable darray[i64] = []
        xr: mutable any darray[i64]& = (&xs).cast[mutable any darray[i64]&]
        xr.reserve(8u)
        return xs.capacity
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_reserve.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@arena_alloc") {
		t.Fatalf("expected darray reserve sugar to lower through arena allocation, got:\n%s", output)
	}
	if !strings.Contains(output, "darray.capacity.ptr") {
		t.Fatalf("expected darray reserve sugar to address the capacity field, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersDArrayClearAndTruncateSugar(t *testing.T) {
	src := `def build(owner: Arena) -> usize:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
    in alloc:
        xs: mutable darray[int] = [1, 2, 3]
        xr: mutable any darray[int]& = (&xs).cast[mutable any darray[int]&]
        xr.truncate(2u)
        xr.clear()
        return xs.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_clear_truncate.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "darray.truncate.count") {
		t.Fatalf("expected truncate lowering to read the count field, got:\n%s", output)
	}
	if !strings.Contains(output, "darray.count.ptr") {
		t.Fatalf("expected clear/truncate lowering to address the darray count field, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersDArrayBuilderSugarAcrossElementTypes(t *testing.T) {
	src := `def build(owner: Arena) -> usize:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
    in alloc:
        ints: mutable darray[i64] = []
        names: mutable darray[u32] = []
        ints.push(1)
        names.push(7u32)
        return ints.count + names.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_builder_multi_type.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "darray.push.slot") {
		t.Fatalf("expected multi-type darray builder lowering to emit push slots, got:\n%s", output)
	}
}
