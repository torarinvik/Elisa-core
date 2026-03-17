package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeArenaArrayViewHelpersCarryElementTypes(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any DArray[T, shape_in]&, start: usize, end: usize) -> DArrayView[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&](), values.count, sizeof(T))
	return DynArrayView(null, 0, sizeof(T))

def arena_da_view_len[T](view: DArrayView[T]) -> usize:
	return view.len

def arena_da_view_slice[T](view: DArrayView[T], start: usize, end: usize) -> DArrayView[T]:
	_ = start
	_ = end
	return view

def arena_da_view_get[T](view: DArrayView[T], index: usize) -> T:
	_ = view
	_ = index
	return zeroed

def use(values: any DArray[i32, row]&) -> i32:
	view: DArrayView[i32] = arena_da_view(values, 0u, values.count)
	sub: DArrayView[i32] = arena_da_view_slice(view, 0u, 1u)
	if arena_da_view_len(sub) > 0u:
		return arena_da_view_get(sub, 0u)
	return 0
`
	_, errs := parseAndAnalyze(t, "arena_array_view_helpers.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeArenaArrayFromViewReturnsFreshShape(t *testing.T) {
	src := `repr(c) struct Arena:
	begin: mutable any void&?
	end: mutable any void&?

repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any DArray[T, shape_in]&, start: usize, end: usize) -> DArrayView[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&](), values.count, sizeof(T))
	return DynArrayView(null, 0, sizeof(T))

def arena_da_from_view[T](a: any Arena&, view: DArrayView[T]) -> DArray[T, shape_out]:
	_ = a
	_ = view
	return zeroed

def bad(a: any Arena&, values: any DArray[i32, row]&) -> DArray[i32, row]:
	view: DArrayView[i32] = arena_da_view(values, 0u, values.count)
	return arena_da_from_view(a, view)
`
	_, errs := parseAndAnalyze(t, "arena_array_from_view_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DArray[i32, row], got DArray[i32, shape_out#") || !strings.Contains(all, "note: arena_da_from_view returns a fresh logical shape for shape_out") {
		t.Fatalf("expected fresh arena from-view diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAdditionalStage1ListWrappersReturnFreshShapes(t *testing.T) {
	src := `repr(c) struct CtxList:
    len: mutable i64
    cap: mutable i64

def ctx_stage0_list_reserve(values: any CtxList&?, cap: i64, elem_size: i64) -> any CtxList&?:
    return values

def ctx_stage0_list_truncate(values: any CtxList&?, size: i64) -> any CtxList&?:
    return values

def ctx_stage0_list_clear(values: any CtxList&?) -> any CtxList&?:
    return values

def ctx_stage1rt_list_reserve(values: DArray[any void&, shape_in], cap: i64, elem_size: i64) -> DArray[any void&, shape_out]:
    return ctx_stage0_list_reserve(values, cap, elem_size)

def ctx_stage1rt_list_truncate(values: DArray[any void&, shape_in], size: i64) -> DArray[any void&, shape_out]:
    return ctx_stage0_list_truncate(values, size)

def ctx_stage1rt_list_clear(values: DArray[any void&, shape_in]) -> DArray[any void&, shape_out]:
    return ctx_stage0_list_clear(values)

def bad_reserve(values: DArray[any void&, row]) -> DArray[any void&, row]:
    return ctx_stage1rt_list_reserve(values, 16, 8)

def bad_truncate(values: DArray[any void&, row]) -> DArray[any void&, row]:
    return ctx_stage1rt_list_truncate(values, 2)

def bad_clear(values: DArray[any void&, row]) -> DArray[any void&, row]:
    return ctx_stage1rt_list_clear(values)
`
	_, errs := parseAndAnalyze(t, "stage1_list_extra_wrappers.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	for _, want := range []string{
		"note: ctx_stage1rt_list_reserve returns a fresh logical shape for shape_out",
		"note: ctx_stage1rt_list_truncate returns a fresh logical shape for shape_out",
		"note: ctx_stage1rt_list_clear returns a fresh logical shape for shape_out",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected diagnostic containing %q, got:\n%s", want, all)
		}
	}
	if strings.Count(all, "return type expects DArray[any void&, row], got DArray[any void&, shape_out#") != 3 {
		t.Fatalf("expected 3 fresh list wrapper mismatch diagnostics, got:\n%s", all)
	}
}
