package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeShapeChangingResizeReturnsFreshShape(t *testing.T) {
	src := `extern resize(array: darray[i32, shape_in], size: usize) -> darray[i32, shape_out]

def bad(array: darray[i32, row]) -> darray[i32, row]:
		return resize(array, 8)
`
	_, errs := parseAndAnalyze(t, "resize_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects darray[i32, row], got darray[i32, shape_out#") {
		t.Fatalf("expected fresh resize witness diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeShapeChangingPushReturnsFreshShape(t *testing.T) {
	src := `extern push(array: darray[i32, shape_in], element: i32) -> darray[i32, shape_out]

def bad(array: darray[i32, row]) -> darray[i32, row]:
		return push(array, 1)
`
	_, errs := parseAndAnalyze(t, "push_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects darray[i32, row], got darray[i32, shape_out#") {
		t.Fatalf("expected fresh push witness diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeShapeChangingConcatReturnsFreshShape(t *testing.T) {
	src := `extern concat(array1: darray[i32, shape_left], array2: darray[i32, shape_right]) -> darray[i32, shape_result]

def bad(left: darray[i32, row], right: darray[i32, col]) -> darray[i32, row]:
		return concat(left, right)
`
	_, errs := parseAndAnalyze(t, "concat_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects darray[i32, row], got darray[i32, shape_result#") {
		t.Fatalf("expected fresh concat witness diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeFreshShapeMismatchAddsHelpfulNote(t *testing.T) {
	src := `extern resize(array: darray[i32, shape_in], size: usize) -> darray[i32, shape_out]

def bad(array: darray[i32, row]) -> darray[i32, row]:
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
	src := `extern resize(array: darray[i32, shape_in], size: usize) -> darray[i32, shape_out]

def grow(array: darray[i32, row]) -> void:
		_ = resize(array, 8)
`
	_, errs := parseAndAnalyze(t, "discard_shape_changing_call.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeShapeChangingStringConcatReturnsFreshShape(t *testing.T) {
	src := `extern concat(left: dstr[shape_left], right: dstr[shape_right]) -> dstr[shape_result]

def bad(left: dstr[row], right: dstr[col]) -> dstr[row]:
		return concat(left, right)
`
	_, errs := parseAndAnalyze(t, "concat_dstr_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects dstr[row], got dstr[shape_result#") {
		t.Fatalf("expected fresh DStr concat witness diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeWrapperCanReturnFreshResizeShape(t *testing.T) {
	src := `extern resize(array: darray[i32, shape_in], size: usize) -> darray[i32, shape_out]

def grow(array: darray[i32, row]) -> darray[i32, shape_after]:
		return resize(array, 8)
`
	_, errs := parseAndAnalyze(t, "wrapper_returns_fresh_resize_shape.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeWrapperCanReturnFreshStringShape(t *testing.T) {
	src := `extern strcat(left: dstr[shape_left], right: dstr[shape_right]) -> dstr[shape_result]

def merge(left: dstr[row], right: dstr[col]) -> dstr[shape_after]:
		return strcat(left, right)
`
	_, errs := parseAndAnalyze(t, "wrapper_returns_fresh_string_shape.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeWrapperCallPropagatesFreshArrayShape(t *testing.T) {
	src := `extern resize(array: darray[i32, shape_in], size: usize) -> darray[i32, shape_out]

def grow(array: darray[i32, row]) -> darray[i32, shape_after]:
		return resize(array, 8)

def same(left: darray[i32, shape_pair], right: darray[i32, shape_pair]) -> void:
		pass

def bad(array: darray[i32, row]) -> void:
		same(grow(array), grow(array))
`
	_, errs := parseAndAnalyze(t, "wrapper_call_propagates_fresh_array_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument 2 to \"same\" expects darray[i32, shape_after#") || !strings.Contains(all, "got darray[i32, shape_after#") {
		t.Fatalf("expected fresh wrapper array shape diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeWrapperCallPropagatesFreshStringShape(t *testing.T) {
	src := `extern strcat(left: dstr[shape_left], right: dstr[shape_right]) -> dstr[shape_result]

def merge(left: dstr[row], right: dstr[col]) -> dstr[shape_after]:
		return strcat(left, right)

def same(left: dstr[shape_pair], right: dstr[shape_pair]) -> void:
		pass

def bad(left: dstr[row], right: dstr[col]) -> void:
		same(merge(left, right), merge(left, right))
`
	_, errs := parseAndAnalyze(t, "wrapper_call_propagates_fresh_string_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument 2 to \"same\" expects dstr[shape_after#") || !strings.Contains(all, "got dstr[shape_after#") {
		t.Fatalf("expected fresh wrapper string shape diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeWrapperFreshShapeMismatchExplainsSeparateCalls(t *testing.T) {
	src := `extern resize(array: darray[i32, shape_in], size: usize) -> darray[i32, shape_out]

def grow(array: darray[i32, row]) -> darray[i32, shape_after]:
		return resize(array, 8)

def same(left: darray[i32, shape_pair], right: darray[i32, shape_pair]) -> void:
		pass

def bad(array: darray[i32, row]) -> void:
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
	src := `extern append_many(array: darray[i32, shape_in], items: darray[i32, chunk]) -> darray[i32, shape_out]
extern truncate(array: darray[i32, shape_in], size: usize) -> darray[i32, shape_out]
extern clear(array: darray[i32, shape_in]) -> darray[i32, shape_out]

def bad_append(array: darray[i32, row], items: darray[i32, col]) -> darray[i32, row]:
		return append_many(array, items)

def bad_truncate(array: darray[i32, row]) -> darray[i32, row]:
		return truncate(array, 2)

def bad_clear(array: darray[i32, row]) -> darray[i32, row]:
		return clear(array)
`
	_, errs := parseAndAnalyze(t, "additional_shape_builtins.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	for _, want := range []string{
		"return type expects darray[i32, row], got darray[i32, shape_out#",
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

def arena_da_append[T](a: any Arena&, da: any darray[T, shape_in]&, item: T) -> any darray[T, shape_out]&:
	if da.count >= da.capacity:
		pass
	return da

def bad(a: any Arena&, da: any darray[i32, row]&) -> any darray[i32, row]&:
	return arena_da_append(a, da, 1)
`
	_, errs := parseAndAnalyze(t, "arena_append_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects darray[i32, row]&, got darray[i32, shape_out#") || !strings.Contains(all, "note: arena_da_append returns a fresh logical shape for shape_out") {
		t.Fatalf("expected fresh arena append diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStage1StringConcatWrapperReturnsFreshShape(t *testing.T) {
	src := `def concat2(lhs: any u8&?, rhs: any u8&?) -> any u8&:
	return lhs if lhs != null else rhs.cast[any u8&]

def rt_concat2(lhs: dstr[shape_left], rhs: dstr[shape_right]) -> dstr[shape_result]:
	return concat2(lhs, rhs)

def bad(left: dstr[row], right: dstr[col]) -> dstr[row]:
	return rt_concat2(left, right)
`
	_, errs := parseAndAnalyze(t, "stage1_string_concat_wrapper.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects dstr[row], got dstr[shape_result#") || !strings.Contains(all, "note: rt_concat2 returns a fresh logical shape for shape_result") {
		t.Fatalf("expected fresh stage1 string concat diagnostic, got:\n%s", all)
	}
}
