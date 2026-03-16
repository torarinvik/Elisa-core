package semantic_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeStage1StringViewWrappersSupportBoundedViews(t *testing.T) {
	src := `repr(c) struct CtxStringView:
	data: mutable any u8&
	len: mutable i64

def ctx_stage0_string_view(value: any u8&?, start: i64, end: i64) -> CtxStringView:
	return CtxStringView("", 0)

def ctx_stage0_string_view_len(view: CtxStringView) -> i64:
	return view.len

def ctx_stage0_string_view_index(view: CtxStringView, index: i64) -> i64:
	return -1

def ctx_stage0_string_view_copy(view: CtxStringView) -> any u8&:
	return view.data

def ctx_stage1rt_string_view(value: DStr[shape_in], start: i64, end: i64) -> CtxStringView:
	return ctx_stage0_string_view(value, start, end)

def ctx_stage1rt_string_view_len(view: CtxStringView) -> i64:
	return ctx_stage0_string_view_len(view)

def ctx_stage1rt_string_view_index(view: CtxStringView, index: i64) -> i64:
	return ctx_stage0_string_view_index(view, index)

def ctx_stage1rt_string_from_view(view: CtxStringView) -> DStr[shape_out]:
	return ctx_stage0_string_view_copy(view)

def probe(text: DStr[row]) -> i64:
	view: CtxStringView = ctx_stage1rt_string_view(text, 0, 2)
	_ = ctx_stage1rt_string_view_index(view, 0)
	return ctx_stage1rt_string_view_len(view)

def bad(text: DStr[row]) -> DStr[row]:
	view: CtxStringView = ctx_stage1rt_string_view(text, 0, 2)
	return ctx_stage1rt_string_from_view(view)
`
	_, errs := parseAndAnalyze(t, "stage1_string_view_wrappers.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DStr[row], got DStr[shape_out#") || !strings.Contains(all, "note: ctx_stage1rt_string_from_view returns a fresh logical shape for shape_out") {
		t.Fatalf("expected bounded string view fresh-shape diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStage1StringViewHelpersAcceptSubviewAndEquality(t *testing.T) {
	src := `repr(c) struct CtxStringView:
	data: mutable any u8&
	len: mutable i64

def ctx_stage0_string_view(value: any u8&?, start: i64, end: i64) -> CtxStringView:
	return CtxStringView("", 0)

def ctx_stage0_string_view_len(view: CtxStringView) -> i64:
	return view.len

def ctx_stage0_string_view_slice(view: CtxStringView, start: i64, end: i64) -> CtxStringView:
	return view

def ctx_stage0_string_view_eq(view: CtxStringView, other: any u8&?) -> int:
	return 1

def ctx_stage0_string_views_eq(lhs: CtxStringView, rhs: CtxStringView) -> int:
	return 1

def ctx_stage1rt_string_view(value: DStr[shape_in], start: i64, end: i64) -> CtxStringView:
	return ctx_stage0_string_view(value, start, end)

def ctx_stage1rt_string_view_len(view: CtxStringView) -> i64:
	return ctx_stage0_string_view_len(view)

def ctx_stage1rt_string_view_slice(view: CtxStringView, start: i64, end: i64) -> CtxStringView:
	return ctx_stage0_string_view_slice(view, start, end)

def ctx_stage1rt_string_view_eq(view: CtxStringView, other: DStr[shape_other]) -> int:
	return ctx_stage0_string_view_eq(view, other)

def ctx_stage1rt_string_views_eq(lhs: CtxStringView, rhs: CtxStringView) -> int:
	return ctx_stage0_string_views_eq(lhs, rhs)

def probe(text: DStr[row], other: DStr[col]) -> int:
	view: CtxStringView = ctx_stage1rt_string_view(text, 0, 4)
	sub: CtxStringView = ctx_stage1rt_string_view_slice(view, 1, 3)
	if ctx_stage1rt_string_view_eq(sub, other) != 0:
		return ctx_stage1rt_string_views_eq(sub, view)
	return ctx_stage1rt_string_view_len(sub)
`
	_, errs := parseAndAnalyze(t, "stage1_string_view_helpers.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeStage1ListViewWrappersSupportBoundedViews(t *testing.T) {
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

def ctx_stage0_list_view_len(view: CtxListView) -> i64:
	return view.len

def ctx_stage0_list_view_get(view: CtxListView, index: i64, elem_size: i64) -> any void&:
	return 0.cast[any void&]()

def ctx_stage0_list_view_copy(view: CtxListView) -> any CtxList&:
	return CtxList(0, 0, 0, null, null, 0)

def ctx_stage1rt_list_view(values: DArray[any void&, shape_in], start: i64, end: i64) -> CtxListView:
	return ctx_stage0_list_view(values, start, end)

def ctx_stage1rt_list_view_len(view: CtxListView) -> i64:
	return ctx_stage0_list_view_len(view)

def ctx_stage1rt_list_view_get(view: CtxListView, index: i64, elem_size: i64) -> any void&:
	return ctx_stage0_list_view_get(view, index, elem_size)

def ctx_stage1rt_list_from_view(view: CtxListView) -> DArray[any void&, shape_out]:
	return ctx_stage0_list_view_copy(view)

def probe(values: DArray[any void&, row]) -> i64:
	view: CtxListView = ctx_stage1rt_list_view(values, 0, 1)
	_ = ctx_stage1rt_list_view_get(view, 0, 8)
	return ctx_stage1rt_list_view_len(view)

def bad(values: DArray[any void&, row]) -> DArray[any void&, row]:
	view: CtxListView = ctx_stage1rt_list_view(values, 0, 1)
	return ctx_stage1rt_list_from_view(view)
`
	_, errs := parseAndAnalyze(t, "stage1_list_view_wrappers.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DArray[any void&, row], got DArray[any void&, shape_out#") || !strings.Contains(all, "note: ctx_stage1rt_list_from_view returns a fresh logical shape for shape_out") || !strings.Contains(all, "note: CtxList-backed list wrappers keep the same runtime layout; this mismatch is about the logical shape witness") {
		t.Fatalf("expected bounded list view fresh-shape diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStage1ListViewHelpersAcceptNestedSubview(t *testing.T) {
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

def ctx_stage0_list_view_len(view: CtxListView) -> i64:
	return view.len

def ctx_stage0_list_view_slice(view: CtxListView, start: i64, end: i64) -> CtxListView:
	return view

def ctx_stage0_list_view_get(view: CtxListView, index: i64, elem_size: i64) -> any void&:
	return 0.cast[any void&]()

def ctx_stage1rt_list_view(values: DArray[any void&, shape_in], start: i64, end: i64) -> CtxListView:
	return ctx_stage0_list_view(values, start, end)

def ctx_stage1rt_list_view_len(view: CtxListView) -> i64:
	return ctx_stage0_list_view_len(view)

def ctx_stage1rt_list_view_slice(view: CtxListView, start: i64, end: i64) -> CtxListView:
	return ctx_stage0_list_view_slice(view, start, end)

def ctx_stage1rt_list_view_get(view: CtxListView, index: i64, elem_size: i64) -> any void&:
	return ctx_stage0_list_view_get(view, index, elem_size)

def probe(values: DArray[any void&, row]) -> i64:
	view: CtxListView = ctx_stage1rt_list_view(values, 0, 4)
	sub: CtxListView = ctx_stage1rt_list_view_slice(view, 1, 3)
	_ = ctx_stage1rt_list_view_get(sub, 0, 8)
	return ctx_stage1rt_list_view_len(sub)
`
	_, errs := parseAndAnalyze(t, "stage1_list_view_helpers.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeCtxListRuntimeBridgeWorksBothDirections(t *testing.T) {
	src := `repr(c) struct CtxList:
	len: mutable i64

def take_raw(values: any CtxList&) -> void:
	pass

def take_logical(values: DArray[any void&, shape_in]) -> void:
	pass

def roundtrip(values: DArray[any void&, row], raw: any CtxList&) -> DArray[any void&, row]:
	take_raw(values)
	take_logical(raw)
	logical: DArray[any void&, row] = raw
	bridged: any CtxList& = logical
	return bridged
`
	_, errs := parseAndAnalyze(t, "ctxlist_runtime_bridge_roundtrip.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeStage1RuntimeFileAcceptsShapeTypedWrappers(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "contextlang_runtime.llcontext")
	src := loadSourceWithIncludes(t, fixture, map[string]bool{})
	_, errs := parseAndAnalyze(t, fixture, src)
	requireNoErrors(t, errs)
}

func TestAnalyzePointerFixture(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "test_programs", "pointer_alloc.llcontext")
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	_, errs := parseAndAnalyze(t, fixture, string(src))
	requireNoErrors(t, errs)
}

func TestAnalyzeShapeOpsFixture(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "test_programs", "shape_ops.llcontext")
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("failed to read shape ops fixture: %v", err)
	}
	_, errs := parseAndAnalyze(t, fixture, string(src))
	requireNoErrors(t, errs)
}

func TestAnalyzeArenaRuntimeFile(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "arena.llcontext")
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("failed to read arena runtime fixture: %v", err)
	}
	_, errs := parseAndAnalyze(t, fixture, string(src))
	requireNoErrors(t, errs)
}

func TestAnalyzeContextRuntimeFile(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "contextlang_runtime.llcontext")
	src := loadSourceWithIncludes(t, fixture, map[string]bool{})
	_, errs := parseAndAnalyze(t, fixture, src)
	requireNoErrors(t, errs)
}
