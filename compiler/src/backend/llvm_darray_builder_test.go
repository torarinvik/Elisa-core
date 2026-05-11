package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersDArrayBuilderSugar(t *testing.T) {
	src := `def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs: mutable darray[i64] = []
        xs.push(1)
        xs.push(2)
        ys: mutable darray[i64] = [3, 4]
        return xs.count + ys.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_builder.elisa", src)
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
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs: mutable darray[int] = []
        xr: mutable darray[int]& = (&xs).cast[mutable darray[int]&]
        xr.extend([1, 2, 3])
        return xs.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_extend.elisa", src)
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

func TestGenerateLLVMIRLowersListComprehensionExpr(t *testing.T) {
	src := `def build(owner: Arena, items: darray[i64]) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs = [item + 1 for item in items if item > 0]
        return xs.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_list_comprehension.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "darray.push.slot") {
		t.Fatalf("expected list comprehension to lower through darray push slots, got:\n%s", output)
	}
	if !strings.Contains(output, "iter.cond") {
		t.Fatalf("expected list comprehension to lower through iterable loop blocks, got:\n%s", output)
	}
	if !strings.Contains(output, "@arena_alloc") {
		t.Fatalf("expected list comprehension growth to lower through arena allocation, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersRangeListComprehensionExpr(t *testing.T) {
	src := `def build(owner: Arena, count: usize) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs = [index for index in 1..<count]
        return xs.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_range_list_comprehension.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "for.cond") {
		t.Fatalf("expected range list comprehension to lower through range loop blocks, got:\n%s", output)
	}
	if !strings.Contains(output, "darray.push.slot") {
		t.Fatalf("expected range list comprehension to push elements, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersQueryExprFamily(t *testing.T) {
	src := `def has_positive(items: darray[i64]) -> bool:
    return any item in items where item > 0

def all_positive(items: darray[i64]) -> bool:
    return all item in items where item > 0

def first_positive(items: darray[i64]) -> i64?:
    return first item in items where item > 0

def positive_count(items: darray[i64]) -> usize:
    return count item in items where item > 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_query_expr_family.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if strings.Count(output, "iter.cond") < 4 {
		t.Fatalf("expected query expressions to lower through iterable loop blocks, got:\n%s", output)
	}
	if !strings.Contains(output, "query.result") {
		t.Fatalf("expected query expressions to lower through query result temporaries, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersFirstProjectionQueryExpr(t *testing.T) {
	src := `struct Entry:
    name: i64
    enabled: bool

def first_enabled(entries: darray[Entry]) -> i64?:
    return entry.name for first entry in entries where entry.enabled
`
	result := parseAndAnalyzeBackendTest(t, "backend_first_projection_query.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "query.result") {
		t.Fatalf("expected projection query to lower through query result temporary, got:\n%s", output)
	}
	if !strings.Contains(output, "iter.cond") {
		t.Fatalf("expected projection query to lower through iterable loop blocks, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersDArrayReserveSugar(t *testing.T) {
	src := `def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs: mutable darray[i64] = []
        xr: mutable darray[i64]& = (&xs).cast[mutable darray[i64]&]
		xr.reserve(8)
        return xs.capacity
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_reserve.elisa", src)
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
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs: mutable darray[int] = [1, 2, 3]
        xr: mutable darray[int]& = (&xs).cast[mutable darray[int]&]
		xr.truncate(2)
        xr.clear()
        return xs.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_clear_truncate.elisa", src)
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
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        ints: mutable darray[i64] = []
        names: mutable darray[u32] = []
        ints.push(1)
        names.push(7u32)
        return ints.count + names.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_builder_multi_type.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "darray.push.slot") {
		t.Fatalf("expected multi-type darray builder lowering to emit push slots, got:\n%s", output)
	}
}
