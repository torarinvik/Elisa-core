package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeShapeChangingResizeReturnsFreshShape(t *testing.T) {
	src := `extern resize(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]

def bad(array: DArray[i32, row]) -> DArray[i32, row]:
    return resize(array, 8)
`
	_, errs := parseAndAnalyze(t, "resize_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects DArray[i32, row], got DArray[i32, shape_out#") {
		t.Fatalf("expected fresh resize witness diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeShapeChangingPushReturnsFreshShape(t *testing.T) {
	src := `extern push(array: DArray[i32, shape_in], element: i32) -> DArray[i32, shape_out]

def bad(array: DArray[i32, row]) -> DArray[i32, row]:
    return push(array, 1)
`
	_, errs := parseAndAnalyze(t, "push_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects DArray[i32, row], got DArray[i32, shape_out#") {
		t.Fatalf("expected fresh push witness diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeShapeChangingConcatReturnsFreshShape(t *testing.T) {
	src := `extern concat(array1: DArray[i32, shape_left], array2: DArray[i32, shape_right]) -> DArray[i32, shape_result]

def bad(left: DArray[i32, row], right: DArray[i32, col]) -> DArray[i32, row]:
    return concat(left, right)
`
	_, errs := parseAndAnalyze(t, "concat_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects DArray[i32, row], got DArray[i32, shape_result#") {
		t.Fatalf("expected fresh concat witness diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeFreshShapeMismatchAddsHelpfulNote(t *testing.T) {
	src := `extern resize(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]

def bad(array: DArray[i32, row]) -> DArray[i32, row]:
    return resize(array, 8)
`
	_, errs := parseAndAnalyze(t, "fresh_shape_note.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "note: resize returns a fresh logical shape for shape_out") {
		t.Fatalf("expected fresh shape note, got:\n%s", all)
	}
}

func TestAnalyzeShapeChangingCallCanBeDiscarded(t *testing.T) {
	src := `extern resize(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]

def grow(array: DArray[i32, row]) -> void:
    _ = resize(array, 8)
`
	_, errs := parseAndAnalyze(t, "discard_shape_changing_call.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeShapeChangingStringConcatReturnsFreshShape(t *testing.T) {
	src := `extern concat(left: DStr[shape_left], right: DStr[shape_right]) -> DStr[shape_result]

def bad(left: DStr[row], right: DStr[col]) -> DStr[row]:
    return concat(left, right)
`
	_, errs := parseAndAnalyze(t, "concat_dstr_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects DStr[row], got DStr[shape_result#") {
		t.Fatalf("expected fresh DStr concat witness diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeWrapperCanReturnFreshResizeShape(t *testing.T) {
	src := `extern resize(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]

def grow(array: DArray[i32, row]) -> DArray[i32, shape_after]:
    return resize(array, 8)
`
	_, errs := parseAndAnalyze(t, "wrapper_returns_fresh_resize_shape.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeWrapperCanReturnFreshStringShape(t *testing.T) {
	src := `extern strcat(left: DStr[shape_left], right: DStr[shape_right]) -> DStr[shape_result]

def merge(left: DStr[row], right: DStr[col]) -> DStr[shape_after]:
    return strcat(left, right)
`
	_, errs := parseAndAnalyze(t, "wrapper_returns_fresh_string_shape.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeWrapperCallPropagatesFreshArrayShape(t *testing.T) {
	src := `extern resize(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]

def grow(array: DArray[i32, row]) -> DArray[i32, shape_after]:
    return resize(array, 8)

def same(left: DArray[i32, shape_pair], right: DArray[i32, shape_pair]) -> void:
    pass

def bad(array: DArray[i32, row]) -> void:
    same(grow(array), grow(array))
`
	_, errs := parseAndAnalyze(t, "wrapper_call_propagates_fresh_array_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument 2 to \"same\" expects DArray[i32, shape_after#") || !strings.Contains(all, "got DArray[i32, shape_after#") {
		t.Fatalf("expected fresh wrapper array shape diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeWrapperCallPropagatesFreshStringShape(t *testing.T) {
	src := `extern strcat(left: DStr[shape_left], right: DStr[shape_right]) -> DStr[shape_result]

def merge(left: DStr[row], right: DStr[col]) -> DStr[shape_after]:
    return strcat(left, right)

def same(left: DStr[shape_pair], right: DStr[shape_pair]) -> void:
    pass

def bad(left: DStr[row], right: DStr[col]) -> void:
    same(merge(left, right), merge(left, right))
`
	_, errs := parseAndAnalyze(t, "wrapper_call_propagates_fresh_string_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument 2 to \"same\" expects DStr[shape_after#") || !strings.Contains(all, "got DStr[shape_after#") {
		t.Fatalf("expected fresh wrapper string shape diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeWrapperFreshShapeMismatchExplainsSeparateCalls(t *testing.T) {
	src := `extern resize(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]

def grow(array: DArray[i32, row]) -> DArray[i32, shape_after]:
    return resize(array, 8)

def same(left: DArray[i32, shape_pair], right: DArray[i32, shape_pair]) -> void:
    pass

def bad(array: DArray[i32, row]) -> void:
    same(grow(array), grow(array))
`
	_, errs := parseAndAnalyze(t, "fresh_wrapper_note.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "note: grow returns a fresh logical shape for shape_after") || !strings.Contains(all, "note: separate calls that produce fresh shapes do not share the same logical shape identity") {
		t.Fatalf("expected wrapper fresh shape notes, got:\n%s", all)
	}
}

func TestAnalyzeAdditionalShapeChangingBuiltinsReturnFreshShapes(t *testing.T) {
	src := `extern append_many(array: DArray[i32, shape_in], items: DArray[i32, chunk]) -> DArray[i32, shape_out]
extern truncate(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]
extern clear(array: DArray[i32, shape_in]) -> DArray[i32, shape_out]

def bad_append(array: DArray[i32, row], items: DArray[i32, col]) -> DArray[i32, row]:
    return append_many(array, items)

def bad_truncate(array: DArray[i32, row]) -> DArray[i32, row]:
    return truncate(array, 2)

def bad_clear(array: DArray[i32, row]) -> DArray[i32, row]:
    return clear(array)
`
	_, errs := parseAndAnalyze(t, "additional_shape_builtins.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	for _, want := range []string{
		"return type expects DArray[i32, row], got DArray[i32, shape_out#",
		"note: append_many returns a fresh logical shape for shape_out",
		"note: truncate returns a fresh logical shape for shape_out",
		"note: clear returns a fresh logical shape for shape_out",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected diagnostic containing %q, got:\n%s", want, all)
		}
	}
}

func TestAnalyzeArenaAppendReturnsFreshShape(t *testing.T) {
	src := `repr(c) struct Arena:
    dummy: usize

repr(c) struct DynArray[T]:
    items: mutable any T&?
    count: mutable usize
    capacity: mutable usize

def arena_da_append[T](a: any Arena&, da: any DArray[T, shape_in]&, item: T) -> any DArray[T, shape_out]&:
    if da.count >= da.capacity:
        pass
    return da

def bad(a: any Arena&, da: any DArray[i32, row]&) -> any DArray[i32, row]&:
    return arena_da_append(a, da, 1)
`
	_, errs := parseAndAnalyze(t, "arena_append_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects any DArray[i32, row]&, got any DArray[i32, shape_out#") || !strings.Contains(all, "note: arena_da_append returns a fresh logical shape for shape_out") {
		t.Fatalf("expected fresh arena append diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStage1StringConcatWrapperReturnsFreshShape(t *testing.T) {
	src := `def ctx_stage0_concat2(lhs: any u8&?, rhs: any u8&?) -> any u8&:
	return lhs if lhs != null else rhs.cast[any u8&]()

def ctx_stage1rt_concat2(lhs: DStr[shape_left], rhs: DStr[shape_right]) -> DStr[shape_result]:
    return ctx_stage0_concat2(lhs, rhs)

def bad(left: DStr[row], right: DStr[col]) -> DStr[row]:
    return ctx_stage1rt_concat2(left, right)
`
	_, errs := parseAndAnalyze(t, "stage1_string_concat_wrapper.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DStr[row], got DStr[shape_result#") || !strings.Contains(all, "note: ctx_stage1rt_concat2 returns a fresh logical shape for shape_result") {
		t.Fatalf("expected fresh stage1 string concat diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStage1ListPushWrapperReturnsFreshShape(t *testing.T) {
	src := `repr(c) struct CtxList:
    len: mutable i64

extern make_list() -> any CtxList&

def ctx_stage0_list_push(values: any CtxList&?, elem: any void&?, elem_size: i64) -> any CtxList&?:
    return values

def ctx_stage1rt_list_push(values: DArray[any void&, shape_in], elem: any void&?, elem_size: i64) -> DArray[any void&, shape_out]:
    return ctx_stage0_list_push(values, elem, elem_size)

def bad(values: DArray[any void&, row], elem: any void&) -> DArray[any void&, row]:
    return ctx_stage1rt_list_push(values, elem, 8)
`
	_, errs := parseAndAnalyze(t, "stage1_list_push_wrapper.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DArray[any void&, row], got DArray[any void&, shape_out#") || !strings.Contains(all, "note: ctx_stage1rt_list_push returns a fresh logical shape for shape_out") || !strings.Contains(all, "note: CtxList-backed list wrappers keep the same runtime layout; this mismatch is about the logical shape witness") {
		t.Fatalf("expected fresh stage1 list push diagnostic, got:\n%s", all)
	}
}
