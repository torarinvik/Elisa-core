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

func TestGenerateLLVMIRLowersBuilderAlias(t *testing.T) {
	src := `struct DArrayBuilder[T]:
    value: T

def builder(owner: Arena&) -> DArrayBuilder[i64]:
    _ = owner
    return DArrayBuilder[i64]{value: 12}

def build(owner: Arena) -> i64:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    items: Builder[i64] in alloc
    return items.value
`
	result := parseAndAnalyzeBackendTest(t, "backend_builder_alias.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "DArrayBuilder") {
		t.Fatalf("expected Builder alias to lower through DArrayBuilder runtime storage, got:\n%s", output)
	}
	if !strings.Contains(output, "call %DArrayBuilder__i64 @builder") {
		t.Fatalf("expected Builder alias local to use DArrayBuilder values, got:\n%s", output)
	}
}

func TestGenerateLLVMIRSpecializesGenericExternMethodBuilderPush(t *testing.T) {
	src := `struct DArrayBuilder[T]:
    count: usize


extern push[T](builder: mutable DArrayBuilder[T]&, item: T) -> void

def build() -> void:
    items: mutable DArrayBuilder[i64] = zeroed
    items.push(7)
`
	result := parseAndAnalyzeBackendTest(t, "backend_generic_extern_method_builder_push.elisa", src)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "call void @push__i64(ptr %items, i64 7)") {
		t.Fatalf("expected generic extern UFCS call to use a specialized i64 symbol, got:\n%s", output)
	}
	if strings.Contains(output, "DArrayBuilder_T__push(ptr %items, i64 7)") {
		t.Fatalf("expected generic extern UFCS call to avoid unspecialized T ABI, got:\n%s", output)
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

func TestGenerateLLVMIRLowersDArrayLiteralWithExplicitOwner(t *testing.T) {
	src := `def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    xs: darray[i64] @alloc = [1, 2, 3]
    return xs.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_literal_explicit_owner.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@arena_alloc") || !strings.Contains(output, "darray.literal.owner.arena") {
		t.Fatalf("expected explicit-owner darray literal to allocate from its owner, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersDArrayLiteralWithSpreadElements(t *testing.T) {
	src := `def build(owner: Arena, first: i64, rest: darray[i64]) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    xs: darray[i64] @alloc = [first, ...rest]
    return xs.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_literal_spread.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "darray.push.slot") {
		t.Fatalf("expected scalar spread-literal element to lower through darray push, got:\n%s", output)
	}
	if !strings.Contains(output, "darray.extend.memcpy") {
		t.Fatalf("expected spread darray element to lower through darray extend, got:\n%s", output)
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

func TestGenerateLLVMIRLowersListComprehensionExprWithExplicitOwner(t *testing.T) {
	src := `def build(owner: Arena, items: darray[i64]) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    xs: darray[i64] @alloc = [item + 1 for item in items if item > 0]
    return xs.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_list_comprehension_explicit_owner.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "call ptr @arena_alloc(ptr %owner") {
		t.Fatalf("expected explicit-owner list comprehension growth to allocate from its owner, got:\n%s", output)
	}
	if !strings.Contains(output, "darray.push.slot") {
		t.Fatalf("expected explicit-owner list comprehension to push elements, got:\n%s", output)
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
	// A no-filter range comprehension over re-evaluable bounds now lowers to the vectorizable
	// presized indexed-store shape (docs/79 P2b): the result is resized to the element count up
	// front and the loop writes by index, rather than pushing element-by-element.
	if !strings.Contains(output, "resize") {
		t.Fatalf("expected range list comprehension to presize the result (indexed-store lowering), got:\n%s", output)
	}
	if strings.Contains(output, "darray.push.slot") {
		t.Fatalf("range list comprehension should no longer push per element (it uses indexed stores), got:\n%s", output)
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

func TestGenerateLLVMIRLowersEachProjectionQueryExpr(t *testing.T) {
	src := `struct Entry:
    name: i64
    enabled: bool

def enabled_names(owner: Arena, entries: darray[Entry]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        return entry.name for each entry in entries
`
	result := parseAndAnalyzeBackendTest(t, "backend_each_projection_query.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "query.result") {
		t.Fatalf("expected each projection query to lower through query result temporary, got:\n%s", output)
	}
	if !strings.Contains(output, "darray.push") {
		t.Fatalf("expected each projection query to push projected elements, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersEachIdentityQueryExpr(t *testing.T) {
	src := `def positives(owner: Arena, items: darray[i64]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        return each item in items where item > 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_each_identity_query.elisa", src)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"query.result", ".push", "icmp sgt"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected identity each query to lower through query push path, got:\n%s", output)
		}
	}
}

func TestGenerateLLVMIRLowersEachIdentityQueryPatternFilterGuard(t *testing.T) {
	src := `enum Expr:
    Int(value: i64)
    Missing

def ints(owner: Arena, items: darray[Expr]) -> darray[Expr]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        return each item in items where item is Expr.Int(value): value > 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_each_identity_query_pattern_guard.elisa", src)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"query.result", "iter.pattern.filter.body", "match.tag", ".push"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected guarded identity each query to lower through pattern-filter push path, got:\n%s", output)
		}
	}
}

func TestGenerateLLVMIRLowersEachProjectionQueryPatternFilter(t *testing.T) {
	src := `enum Expr:
    Int(value: i64)
    Missing

def ints(owner: Arena, items: darray[Expr]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        return value for each item in items where Expr.Int(value)
`
	result := parseAndAnalyzeBackendTest(t, "backend_each_projection_query_pattern.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"iter.pattern.filter.body", "match.tag", "darray.push"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected pattern-filter projection query IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersSubjectIsEachProjectionQueryPatternFilter(t *testing.T) {
	src := `enum Expr:
    Int(value: i64)
    Missing

def ints(owner: Arena, items: darray[Expr]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        return value for each item in items where item is Expr.Int(value)
`
	result := parseAndAnalyzeBackendTest(t, "backend_subject_is_each_projection_query_pattern.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"iter.pattern.filter.body", "match.tag", "darray.push"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected subject-is pattern-filter projection query IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersEnumerateWhereSubjectPatternFilter(t *testing.T) {
	src := `enum Expr:
    Int(value: i64)
    Missing

def sum_positive_after_index(items: darray[Expr]) -> i64:
    total: mutable i64 = 0
    for index, item in (items.enumerate() where index, item is Expr.Int(value): value > index):
        total <- total + index
    return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_enumerate_where_subject_pattern_filter.elisa", src)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"match.expr", "match.tag", "icmp sgt", "define i64 @sum_positive_after_index"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected enumerate subject pattern filter IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersEachProjectionQueryPatternFilterGuard(t *testing.T) {
	src := `enum Expr:
    Int(value: i64)
    Missing

def ints(owner: Arena, items: darray[Expr]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        return value for each item in items where Expr.Int(value): value > 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_each_projection_query_pattern_guard.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"iter.pattern.filter.body", "iter.filter.body", "darray.push"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected guarded pattern-filter projection query IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersEachProjectionQueryWithExplicitOwner(t *testing.T) {
	src := `struct Entry:
    name: i64

def names(owner: Arena, entries: darray[Entry]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    return entry.name for each entry in entries with alloc
`
	result := parseAndAnalyzeBackendTest(t, "backend_each_projection_query_explicit_owner.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "darray.push") {
		t.Fatalf("expected explicit-owner projection query to push projected elements, got:\n%s", output)
	}
	if !strings.Contains(output, "darray.push.owner.arena") || !strings.Contains(output, "call ptr @arena_alloc(ptr %darray.push.owner.arena") {
		t.Fatalf("expected explicit-owner projection query to allocate from its owner, got:\n%s", output)
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
