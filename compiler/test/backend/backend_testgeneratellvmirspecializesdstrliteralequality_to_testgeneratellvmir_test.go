package backend_test

import (
	"elisacore/src/backend"
	"strings"
	"testing"
)

func TestGenerateLLVMIRSpecializesDStrLiteralEquality(t *testing.T) {
	src := `extern ctx_streq(left: cstr[row], right: cstr[shape_other]) -> int

def literal_right(text: cstr[row]) -> bool:
	return text == "alphabet soup"

def literal_left(text: cstr[row]) -> bool:
	return "alphabet soup" == text

def direct_literal_right(text: cstr[row]) -> bool:
	return ctx_streq(text, "alphabet soup") != 0

def direct_literal_left(text: cstr[row]) -> bool:
	return ctx_streq("alphabet soup", text) != 0

def direct_empty_literal(text: cstr[row]) -> bool:
	return ctx_streq(text, "") != 0
`
	result := parseAndAnalyze(t, "backend_runtime_cstr_literal_eq.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	for _, name := range []string{"literal_right", "literal_left", "direct_literal_right", "direct_literal_left"} {
		body := functionIR(output, name)
		if body == "" {
			t.Fatalf("expected to find %s body, got:\n%s", name, output)
		}
		for _, want := range []string{"call i64 @ctx_strlen(ptr", "call i64 @memcmp(ptr"} {
			if !strings.Contains(body, want) {
				t.Fatalf("expected %s to contain %q, got:\n%s", name, want, body)
			}
		}
		if strings.Contains(body, "call i64 @ctx_streq") {
			t.Fatalf("expected %s to avoid ctx_streq helper fallback, got:\n%s", name, body)
		}
	}

	emptyBody := functionIR(output, "direct_empty_literal")
	if emptyBody == "" {
		t.Fatalf("expected to find direct_empty_literal body, got:\n%s", output)
	}
	if !strings.Contains(emptyBody, "call i64 @ctx_strlen(ptr") {
		t.Fatalf("expected direct_empty_literal to contain ctx_strlen length check, got:\n%s", emptyBody)
	}
	for _, bad := range []string{"call i64 @ctx_streq", "call i64 @memcmp("} {
		if strings.Contains(emptyBody, bad) {
			t.Fatalf("expected direct_empty_literal to avoid %q, got:\n%s", bad, emptyBody)
		}
	}
}
func TestGenerateLLVMIRSpecializesConstantStringSliceLiteralEquality(t *testing.T) {
	src := `extern ctx_string_slice(value: cstr[row], start: i64, end: i64) -> cstr[shape_out]
extern ctx_streq(left: cstr[shape_left], right: cstr[shape_right]) -> int

def slice_literal(text: cstr[row]) -> bool:
	return ctx_string_slice(text, 1, 10) == "lphabet s"

def direct_slice_literal(text: cstr[row]) -> bool:
	return ctx_streq(ctx_string_slice(text, 1, 10), "lphabet s") != 0
`
	result := parseAndAnalyze(t, "backend_runtime_string_slice_literal_eq.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	for _, name := range []string{"slice_literal", "direct_slice_literal"} {
		body := functionIR(output, name)
		if body == "" {
			t.Fatalf("expected to find %s body, got:\n%s", name, output)
		}
		for _, want := range []string{"call i64 @ctx_strlen(ptr", "call i64 @memcmp(ptr"} {
			if !strings.Contains(body, want) {
				t.Fatalf("expected %s to contain %q, got:\n%s", name, want, body)
			}
		}
		for _, bad := range []string{"call ptr @intern_small_string", "call ptr @alloc_perm", "call i64 @ctx_streq"} {
			if strings.Contains(body, bad) {
				t.Fatalf("expected %s to avoid %q, got:\n%s", name, bad, body)
			}
		}
	}
}
func TestGenerateLLVMIRSpecializesConstantStringSliceEquality(t *testing.T) {
	src := `extern ctx_string_slice_eq(value: cstr[row], start: i64, end: i64, other: cstr[col]) -> int

def slice_eq_const(text: cstr[row], other: cstr[col]) -> bool:
	return ctx_string_slice_eq(text, 1, 3, other) != 0

def slice_eq_empty(text: cstr[row], other: cstr[col]) -> bool:
	return ctx_string_slice_eq(text, 2, 2, other) != 0

def slice_eq_unknown(text: cstr[row], start: i64, end: i64, other: cstr[col]) -> bool:
	return ctx_string_slice_eq(text, start, end, other) != 0
`
	result := parseAndAnalyze(t, "backend_runtime_string_slice_eq.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	sliceEqConstBody := functionIR(output, "slice_eq_const")
	if sliceEqConstBody == "" {
		t.Fatalf("expected to find slice_eq_const body, got:\n%s", output)
	}
	for _, want := range []string{"call i64 @ctx_strlen(ptr", "call i64 @memcmp(ptr"} {
		if !strings.Contains(sliceEqConstBody, want) {
			t.Fatalf("expected slice_eq_const to contain %q, got:\n%s", want, sliceEqConstBody)
		}
	}
	if strings.Contains(sliceEqConstBody, "call i64 @ctx_string_slice_eq") {
		t.Fatalf("expected slice_eq_const to avoid ctx_string_slice_eq helper fallback, got:\n%s", sliceEqConstBody)
	}

	sliceEqEmptyBody := functionIR(output, "slice_eq_empty")
	if sliceEqEmptyBody == "" {
		t.Fatalf("expected to find slice_eq_empty body, got:\n%s", output)
	}
	if !strings.Contains(sliceEqEmptyBody, "call i64 @ctx_strlen(ptr") {
		t.Fatalf("expected slice_eq_empty to contain ctx_strlen length check, got:\n%s", sliceEqEmptyBody)
	}
	for _, bad := range []string{"call i64 @ctx_string_slice_eq"} {
		if strings.Contains(sliceEqEmptyBody, bad) {
			t.Fatalf("expected slice_eq_empty to avoid %q, got:\n%s", bad, sliceEqEmptyBody)
		}
	}

	sliceEqUnknownBody := functionIR(output, "slice_eq_unknown")
	if sliceEqUnknownBody == "" {
		t.Fatalf("expected to find slice_eq_unknown body, got:\n%s", output)
	}
	if !strings.Contains(sliceEqUnknownBody, "call i64 @ctx_string_slice_eq(ptr") {
		t.Fatalf("expected slice_eq_unknown to keep helper fallback when bounds are not constant, got:\n%s", sliceEqUnknownBody)
	}
}
func TestGenerateLLVMIRSpecializesConstantStringSlicesEquality(t *testing.T) {
	src := `extern ctx_string_slices_eq(lhs: cstr[row], lhs_start: i64, lhs_end: i64, rhs: cstr[col], rhs_start: i64, rhs_end: i64) -> int

def slices_eq_const(left: cstr[row], right: cstr[col]) -> bool:
	return ctx_string_slices_eq(left, 1, 4, right, 2, 5) != 0

def slices_eq_empty(left: cstr[row], right: cstr[col]) -> bool:
	return ctx_string_slices_eq(left, 3, 3, right, 7, 7) != 0

def slices_eq_unknown(left: cstr[row], left_start: i64, left_end: i64, right: cstr[col], right_start: i64, right_end: i64) -> bool:
	return ctx_string_slices_eq(left, left_start, left_end, right, right_start, right_end) != 0
`
	result := parseAndAnalyze(t, "backend_runtime_string_slices_eq.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	slicesEqConstBody := functionIR(output, "slices_eq_const")
	if slicesEqConstBody == "" {
		t.Fatalf("expected to find slices_eq_const body, got:\n%s", output)
	}
	for _, want := range []string{"call i64 @ctx_strlen(ptr", "call i64 @memcmp(ptr"} {
		if !strings.Contains(slicesEqConstBody, want) {
			t.Fatalf("expected slices_eq_const to contain %q, got:\n%s", want, slicesEqConstBody)
		}
	}
	if strings.Contains(slicesEqConstBody, "call i64 @ctx_string_slices_eq") {
		t.Fatalf("expected slices_eq_const to avoid ctx_string_slices_eq helper fallback, got:\n%s", slicesEqConstBody)
	}

	slicesEqEmptyBody := functionIR(output, "slices_eq_empty")
	if slicesEqEmptyBody == "" {
		t.Fatalf("expected to find slices_eq_empty body, got:\n%s", output)
	}
	if !strings.Contains(slicesEqEmptyBody, "call i64 @ctx_strlen(ptr") {
		t.Fatalf("expected slices_eq_empty to contain ctx_strlen length checks, got:\n%s", slicesEqEmptyBody)
	}
	for _, bad := range []string{"call i64 @ctx_string_slices_eq"} {
		if strings.Contains(slicesEqEmptyBody, bad) {
			t.Fatalf("expected slices_eq_empty to avoid %q, got:\n%s", bad, slicesEqEmptyBody)
		}
	}

	slicesEqUnknownBody := functionIR(output, "slices_eq_unknown")
	if slicesEqUnknownBody == "" {
		t.Fatalf("expected to find slices_eq_unknown body, got:\n%s", output)
	}
	if !strings.Contains(slicesEqUnknownBody, "call i64 @ctx_string_slices_eq(ptr") {
		t.Fatalf("expected slices_eq_unknown to keep helper fallback when bounds are not constant, got:\n%s", slicesEqUnknownBody)
	}
}
func TestGenerateLLVMIRSpecializesTinyStringViewLiteralEquality(t *testing.T) {
	src := `def same_empty(view: StringView) -> bool:
	return view == ""

def same_short(view: StringView) -> bool:
	return view == "def"

def differs_short(view: StringView) -> bool:
	return view != "region"
`
	result := parseAndAnalyze(t, "backend_runtime_string_literal_eq_tiny.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%StringView = type { ptr, i64 }",
		"define i1 @same_empty(%StringView",
		"define i1 @same_short(%StringView",
		"define i1 @differs_short(%StringView",
		"extractvalue %StringView",
		"getelementptr i8, ptr",
		"load i8, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"ctx_string_view_eq", "@memcmp("} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected tiny StringView literal equality to avoid %q, got:\n%s", bad, output)
		}
	}
}
func TestGenerateLLVMIRSpecializesLongStringViewLiteralHelperCalls(t *testing.T) {
	src := `extern string_view_eq(view: StringView, other: u8&?) -> int

def same_long(view: StringView) -> bool:
	return string_view_eq(view, "destroy_region".cast[u8&]) != 0
`
	result := parseAndAnalyze(t, "backend_runtime_string_literal_eq_long.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%StringView = type { ptr, i64 }",
		"define i1 @same_long(%StringView",
		"declare i64 @memcmp(ptr, ptr, i64)",
		"call i64 @memcmp(ptr",
		"zext i1",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call i64 @string_view_eq") {
		t.Fatalf("expected literal helper call lowering to avoid calling string_view_eq, got:\n%s", output)
	}
}
func TestGenerateLLVMIRMarksDisjointDViewMemcpyCallsNoAlias(t *testing.T) {
	src := `struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

extern arena_memcpy(dest: void&?, src: void&?, n: usize) -> void&?

def arena_da_view_prefix[T](view: view[T], end: usize) -> view[T]:
	_ = end
	return view

def arena_da_view_suffix[T](view: view[T], start: usize) -> view[T]:
	_ = start
	return view

def split_copy(view: view[i32]) -> void&?:
	prefix: view[i32] = arena_da_view_prefix(view, 2)
	suffix: view[i32] = arena_da_view_suffix(view, 2)
	return arena_memcpy(prefix.data, suffix.data, prefix.len * prefix.elem_size)
`
	result := parseAndAnalyze(t, "backend_dview_split_memcpy.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	body := functionIR(output, "split_copy")
	if body == "" {
		t.Fatalf("expected to find split_copy body, got:\n%s", output)
	}
	if !strings.Contains(body, "call ptr @arena_memcpy(ptr noalias") {
		t.Fatalf("expected split_copy to mark arena_memcpy operands noalias, got:\n%s", body)
	}
}
func TestGenerateLLVMIRSpecializesArenaDViewCopyExact(t *testing.T) {
	src := `struct DynArray[T]:
	items: mutable T&?
	count: mutable usize
	capacity: mutable usize

struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

extern arena_memcpy(dest: void&?, src: void&?, n: usize) -> void&?

def arena_da_view[T](values: darray[T, shape_in]&, start: usize, end: usize) -> view[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[void&], values.count, size_of(T))
	return DynArrayView(null, 0, size_of(T))

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	if dst.len != src.len:
		return
	_ = dst
	_ = src

def copy_split(values: darray[i32, 4]&) -> void:
	base: view[i32] = arena_da_view(values, 0, 4)
	left: view[i32] = base[0:2]
	right: view[i32] = base[2:4]
	arena_da_copy_exact(left, right)

def copy_overlap(values: darray[i32, 4]&) -> void:
	base: view[i32] = arena_da_view(values, 0, 4)
	left: view[i32] = base[0:3]
	right: view[i32] = base[1:4]
	arena_da_copy_exact(left, right)

def copy_overlap_backward(values: darray[i32, 4]&) -> void:
	base: view[i32] = arena_da_view(values, 0, 4)
	left: view[i32] = base[1:4]
	right: view[i32] = base[0:3]
	arena_da_copy_exact(left, right)

def copy_overlap_unknown(values: darray[i32, shape_in]&) -> void:
	base: view[i32] = arena_da_view(values, 0, values.count)
	left: view[i32] = base[0:values.count - 1]
	right: view[i32] = base[1:values.count]
	arena_da_copy_exact(left, right)
`
	result := parseAndAnalyze(t, "backend_dview_copy_exact.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copySplitBody := functionIR(output, "copy_split")
	if copySplitBody == "" {
		t.Fatalf("expected to find copy_split body, got:\n%s", output)
	}
	if strings.Contains(copySplitBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_split to avoid helper fallback, got:\n%s", copySplitBody)
	}
	requireTinyExactDViewCopyBody(t, copySplitBody)

	copyOverlapBody := functionIR(output, "copy_overlap")
	if copyOverlapBody == "" {
		t.Fatalf("expected to find copy_overlap body, got:\n%s", output)
	}
	if strings.Contains(copyOverlapBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_overlap to avoid helper fallback, got:\n%s", copyOverlapBody)
	}
	if strings.Contains(copyOverlapBody, "call ptr @arena_memcpy") {
		t.Fatalf("expected copy_overlap to preserve overlap semantics instead of arena_memcpy, got:\n%s", copyOverlapBody)
	}
	if !strings.Contains(copyOverlapBody, "load i32, ptr") || !strings.Contains(copyOverlapBody, "store i32") {
		t.Fatalf("expected copy_overlap to lower through direct element loads/stores, got:\n%s", copyOverlapBody)
	}

	copyOverlapBackwardBody := functionIR(output, "copy_overlap_backward")
	if copyOverlapBackwardBody == "" {
		t.Fatalf("expected to find copy_overlap_backward body, got:\n%s", output)
	}
	if strings.Contains(copyOverlapBackwardBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_overlap_backward to avoid helper fallback, got:\n%s", copyOverlapBackwardBody)
	}
	if strings.Contains(copyOverlapBackwardBody, "call ptr @arena_memcpy") {
		t.Fatalf("expected copy_overlap_backward to preserve overlap semantics instead of arena_memcpy, got:\n%s", copyOverlapBackwardBody)
	}
	if !strings.Contains(copyOverlapBackwardBody, "load i32, ptr") || !strings.Contains(copyOverlapBackwardBody, "store i32") {
		t.Fatalf("expected copy_overlap_backward to lower through direct element loads/stores, got:\n%s", copyOverlapBackwardBody)
	}

	copyOverlapUnknownBody := functionIR(output, "copy_overlap_unknown")
	if copyOverlapUnknownBody == "" {
		t.Fatalf("expected to find copy_overlap_unknown body, got:\n%s", output)
	}
	if strings.Contains(copyOverlapUnknownBody, "call ptr @arena_memcpy") {
		t.Fatalf("expected copy_overlap_unknown to preserve overlap semantics instead of arena_memcpy, got:\n%s", copyOverlapUnknownBody)
	}
	if !strings.Contains(copyOverlapUnknownBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_overlap_unknown to keep helper fallback when extent is not exact, got:\n%s", copyOverlapUnknownBody)
	}
}
func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

@borrows_return_field(left, left, right, right)
extern wrap_views(left: view[i32], right: view[i32]) -> Views

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_struct(values: array[i32, 4]) -> void:
	boxed: Views = Views(values[0:2], values[2:4])
	arena_da_copy_exact(boxed.left, boxed.right)

def copy_helper(values: array[i32, 4]) -> void:
	boxed: Views = wrap_views(values[0:2], values[2:4])
	arena_da_copy_exact(boxed.left, boxed.right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyStructBody := functionIR(output, "copy_struct")
	if copyStructBody == "" {
		t.Fatalf("expected to find copy_struct body, got:\n%s", output)
	}
	if strings.Contains(copyStructBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_struct to avoid helper fallback, got:\n%s", copyStructBody)
	}
	requireTinyExactDViewCopyBody(t, copyStructBody)

	copyHelperBody := functionIR(output, "copy_helper")
	if copyHelperBody == "" {
		t.Fatalf("expected to find copy_helper body, got:\n%s", output)
	}
	if strings.Contains(copyHelperBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_helper to avoid helper fallback, got:\n%s", copyHelperBody)
	}
	requireTinyExactDViewCopyBody(t, copyHelperBody)

}
func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_indexed(values: array[i32, 4]) -> void:
	items: array[Views, 1] = [Views(values[0:2], values[2:4])]
	arena_da_copy_exact(items[0].left, items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyIndexedBody := functionIR(output, "copy_indexed")
	if copyIndexedBody == "" {
		t.Fatalf("expected to find copy_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_indexed to avoid helper fallback, got:\n%s", copyIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyIndexedBody)
}
func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughStandardViewSliceHelperFieldProjection(t *testing.T) {
	src := `struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

struct Views:
	left: view[i32]
	right: view[i32]

def arena_da_view_slice(input: view[Views], start: usize, end: usize) -> view[Views]:
	_ = start
	_ = end
	return input

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_helper_view_slice(values: array[i32, 8]) -> void:
	items: array[Views, 2] = [Views(values[0:2], values[2:4]), Views(values[4:6], values[6:8])]
	window: view[Views] = arena_da_view_slice(items[1:2], 0, 1)
	arena_da_copy_exact(window[0].left, window[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_standard_view_slice_helper_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	body := functionIR(output, "copy_helper_view_slice")
	if body == "" {
		t.Fatalf("expected to find copy_helper_view_slice body, got:\n%s", output)
	}
	if strings.Contains(body, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_helper_view_slice to avoid helper fallback, got:\n%s", body)
	}
	requireTinyExactDViewCopyBody(t, body)
}
func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewHolder:
	items: array[Views, 1]

@borrows_return_field(items[0].left, left, items[0].right, right)
extern wrap_indexed_views(left: view[i32], right: view[i32]) -> ViewHolder

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_helper_indexed(values: array[i32, 4]) -> void:
	wrapped: ViewHolder = wrap_indexed_views(values[0:2], values[2:4])
	arena_da_copy_exact(wrapped.items[0].left, wrapped.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_helper_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyHelperIndexedBody := functionIR(output, "copy_helper_indexed")
	if copyHelperIndexedBody == "" {
		t.Fatalf("expected to find copy_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyHelperIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_helper_indexed to avoid helper fallback, got:\n%s", copyHelperIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyHelperIndexedBody)
}
