package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeStage1TypedListWrappersReduceVoidBottleneck(t *testing.T) {
	src := `repr(c) struct CtxList:
	len: mutable i64
	cap: mutable i64
	elem_size: mutable i64
	data: mutable any void&&?
	inline_boxes: mutable any u8&?
	inline_box_stride: mutable i64

repr(c) struct CtxListView:
	data: mutable any void&&?
	len: mutable i64
	elem_size: mutable i64

extern ctx_stage0_list_new_reserve(cap: i64, elem_size: i64) -> any CtxList&

def ctx_stage0_list_push(values: any CtxList&?, elem: any void&?, elem_size: i64) -> any CtxList&?:
	return values

def ctx_stage0_list_view(values: any CtxList&?, start: i64, end: i64) -> CtxListView:
	return CtxListView(null, 0, 0)

def ctx_stage0_list_view_len(view: CtxListView) -> i64:
	return view.len

def ctx_stage0_list_view_slice(view: CtxListView, start: i64, end: i64) -> CtxListView:
	return view

def ctx_stage0_list_get(values: any CtxList&?, index: i64, elem_size: i64) -> any void&:
	return 0.cast[any void&]()

def ctx_stage0_list_view_get(view: CtxListView, index: i64, elem_size: i64) -> any void&:
	return 0.cast[any void&]()

extern ctx_stage0_list_view_copy(view: CtxListView) -> any CtxList&

def ctx_stage1rt_tlist_new[T](type_hint: any T&) -> DList[T, shape_out]:
	_ = type_hint
	return ctx_stage0_list_new_reserve(0, sizeof(T).i64())

def ctx_stage1rt_tlist_push[T](values: DList[T, shape_in], elem: any T&) -> DList[T, shape_out]:
	return ctx_stage0_list_push(values, elem.cast[any void&](), sizeof(T).i64())

def ctx_stage1rt_tlist_view[T](values: DList[T, shape_in], start: i64, end: i64) -> DListView[T]:
	return ctx_stage0_list_view(values, start, end)

def ctx_stage1rt_tlist_view_len[T](view: DListView[T]) -> i64:
	return ctx_stage0_list_view_len(view)

def ctx_stage1rt_tlist_view_slice[T](view: DListView[T], start: i64, end: i64) -> DListView[T]:
	return ctx_stage0_list_view_slice(view, start, end)

def ctx_stage1rt_tlist_get[T](values: DList[T, shape_in], index: i64) -> any T&:
	return ctx_stage0_list_get(values, index, sizeof(T).i64()).cast[any T&]()

def ctx_stage1rt_tlist_view_get[T](view: DListView[T], index: i64) -> any T&:
	return ctx_stage0_list_view_get(view, index, sizeof(T).i64()).cast[any T&]()

def ctx_stage1rt_tlist_from_view[T](view: DListView[T]) -> DList[T, shape_out]:
	return ctx_stage0_list_view_copy(view)

def use(seed: any i64&) -> any i64&:
	view: DListView[i64] = ctx_stage1rt_tlist_view(ctx_stage1rt_tlist_push(ctx_stage1rt_tlist_new(seed), seed), 0, 2)
	sub: DListView[i64] = ctx_stage1rt_tlist_view_slice(view, 0, 1)
	if ctx_stage1rt_tlist_view_len(sub) > 0:
		_ = ctx_stage1rt_tlist_view_get(sub, 0)
	return ctx_stage1rt_tlist_get(ctx_stage1rt_tlist_from_view(sub), 0)
`
	_, errs := parseAndAnalyze(t, "stage1_typed_list_wrappers.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeStage1TypedListConvenienceHelpersUseTypedViews(t *testing.T) {
	src := `repr(c) struct CtxList:
	len: mutable i64
	cap: mutable i64
	elem_size: mutable i64
	data: mutable any void&&?
	inline_boxes: mutable any u8&?
	inline_box_stride: mutable i64

repr(c) struct CtxListView:
	data: mutable any void&&?
	len: mutable i64
	elem_size: mutable i64

extern ctx_stage0_list_new_reserve(cap: i64, elem_size: i64) -> any CtxList&

def ctx_stage0_list_push(values: any CtxList&?, elem: any void&?, elem_size: i64) -> any CtxList&?:
	return values

def ctx_stage0_list_view(values: any CtxList&?, start: i64, end: i64) -> CtxListView:
	return CtxListView(null, 0, 0)

def ctx_stage0_list_view_len(view: CtxListView) -> i64:
	return view.len

def ctx_stage0_list_view_get(view: CtxListView, index: i64, elem_size: i64) -> any void&:
	return 0.cast[any void&]()

def ctx_stage1rt_tlist_new[T](type_hint: any T&) -> DList[T, shape_out]:
	_ = type_hint
	return ctx_stage0_list_new_reserve(0, sizeof(T).i64())

def ctx_stage1rt_tlist_push[T](values: DList[T, shape_in], elem: any T&) -> DList[T, shape_out]:
	return ctx_stage0_list_push(values, elem.cast[any void&](), sizeof(T).i64())

def ctx_stage1rt_tlist_view[T](values: DList[T, shape_in], start: i64, end: i64) -> DListView[T]:
	return ctx_stage0_list_view(values, start, end)

def ctx_stage1rt_tlist_view_len[T](view: DListView[T]) -> i64:
	return ctx_stage0_list_view_len(view)

def ctx_stage1rt_tlist_take[T](values: DList[T, shape_in], count: i64) -> DListView[T]:
	return ctx_stage1rt_tlist_view(values, 0, count)

def ctx_stage1rt_tlist_drop[T](values: DList[T, shape_in], count: i64) -> DListView[T]:
	return ctx_stage1rt_tlist_view(values, count, ctx_stage1rt_tlist_view_len(ctx_stage1rt_tlist_view(values, 0, 8)))

def ctx_stage1rt_tlist_get[T](values: DList[T, shape_in], index: i64) -> any T&:
	return ctx_stage0_list_view_get(ctx_stage0_list_view(values, index, index + 1), 0, sizeof(T).i64()).cast[any T&]()

def ctx_stage1rt_tlist_first[T](values: DList[T, shape_in]) -> any T&:
	return ctx_stage1rt_tlist_get(values, 0)

def ctx_stage1rt_tlist_view_get[T](view: DListView[T], index: i64) -> any T&:
	return ctx_stage0_list_view_get(view, index, sizeof(T).i64()).cast[any T&]()

def ctx_stage1rt_tlist_view_first[T](view: DListView[T]) -> any T&:
	return ctx_stage1rt_tlist_view_get(view, 0)

def use(seed: any i64&) -> any i64&:
	left: DListView[i64] = ctx_stage1rt_tlist_take(ctx_stage1rt_tlist_push(ctx_stage1rt_tlist_new(seed), seed), 1)
	right: DListView[i64] = ctx_stage1rt_tlist_drop(ctx_stage1rt_tlist_push(ctx_stage1rt_tlist_new(seed), seed), 0)
	if ctx_stage1rt_tlist_view_len(left) > 0:
		_ = ctx_stage1rt_tlist_view_first(left)
	return ctx_stage1rt_tlist_first(ctx_stage1rt_tlist_push(ctx_stage1rt_tlist_new(seed), ctx_stage1rt_tlist_view_first(right)))
`
	_, errs := parseAndAnalyze(t, "stage1_typed_list_convenience_helpers.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeStage1TypedListPushReturnsFreshShape(t *testing.T) {
	src := `repr(c) struct CtxList:
	len: mutable i64
	cap: mutable i64
	elem_size: mutable i64
	data: mutable any void&&?
	inline_boxes: mutable any u8&?
	inline_box_stride: mutable i64

extern ctx_stage0_list_new_reserve(cap: i64, elem_size: i64) -> any CtxList&

def ctx_stage0_list_push(values: any CtxList&?, elem: any void&?, elem_size: i64) -> any CtxList&?:
	return values

def ctx_stage1rt_tlist_new[T](type_hint: any T&) -> DList[T, shape_out]:
	_ = type_hint
	return ctx_stage0_list_new_reserve(0, sizeof(T).i64())

def ctx_stage1rt_tlist_push[T](values: DList[T, shape_in], elem: any T&) -> DList[T, shape_out]:
	return ctx_stage0_list_push(values, elem.cast[any void&](), sizeof(T).i64())

def bad(seed: any i64&) -> DList[i64, row]:
	return ctx_stage1rt_tlist_push(ctx_stage1rt_tlist_new(seed), seed)
`
	_, errs := parseAndAnalyze(t, "stage1_typed_list_push_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DList[i64, row], got DList[i64, shape_out#") || !strings.Contains(all, "note: ctx_stage1rt_tlist_push returns a fresh logical shape for shape_out") || !strings.Contains(all, "note: CtxList-backed list wrappers keep the same runtime layout; this mismatch is about the logical shape witness") {
		t.Fatalf("expected fresh typed-list push diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStage1TypedListFromViewReturnsFreshShape(t *testing.T) {
	src := `repr(c) struct CtxList:
	len: mutable i64
	cap: mutable i64
	elem_size: mutable i64
	data: mutable any void&&?
	inline_boxes: mutable any u8&?
	inline_box_stride: mutable i64

repr(c) struct CtxListView:
	data: mutable any void&&?
	len: mutable i64
	elem_size: mutable i64

def ctx_stage0_list_view(values: any CtxList&?, start: i64, end: i64) -> CtxListView:
	return CtxListView(null, 0, 0)

extern ctx_stage0_list_view_copy(view: CtxListView) -> any CtxList&

def ctx_stage1rt_tlist_view[T](values: DList[T, shape_in], start: i64, end: i64) -> DListView[T]:
	return ctx_stage0_list_view(values, start, end)

def ctx_stage1rt_tlist_from_view[T](view: DListView[T]) -> DList[T, shape_out]:
	return ctx_stage0_list_view_copy(view)

def bad(values: DList[i64, row]) -> DList[i64, row]:
	view: DListView[i64] = ctx_stage1rt_tlist_view(values, 0, 1)
	return ctx_stage1rt_tlist_from_view(view)
`
	_, errs := parseAndAnalyze(t, "stage1_typed_list_from_view_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DList[i64, row], got DList[i64, shape_out#") || !strings.Contains(all, "note: ctx_stage1rt_tlist_from_view returns a fresh logical shape for shape_out") || !strings.Contains(all, "note: CtxList-backed list wrappers keep the same runtime layout; this mismatch is about the logical shape witness") {
		t.Fatalf("expected fresh typed-list from-view diagnostic, got:\n%s", all)
	}
}

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
