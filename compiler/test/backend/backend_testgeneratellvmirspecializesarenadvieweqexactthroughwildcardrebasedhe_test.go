package backend_test

import (
	"elisacore/src/backend"
	"strings"
	"testing"
)

func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewWindow:
	items: view[Views]

@borrows_return_field_rebased(items[*].left, src[*].left, items[*].right, src[*].right)
extern wrap_sub_wild(src: view[Views], start: usize, end: usize) -> ViewWindow

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_wildcard_rebased_helper_indexed(values: array[i32, 8]) -> bool:
	items: array[Views, 4] = [Views(values[0:1], values[1:2]), Views(values[2:3], values[3:4]), Views(values[4:5], values[5:6]), Views(values[6:7], values[7:8])]
	wrapped: ViewWindow = wrap_sub_wild(items[1:3], 0, 2)
	return arena_da_eq_exact(wrapped.items[0].left, wrapped.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_wildcard_rebased_helper_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqWildcardRebasedHelperIndexedBody := functionIR(output, "eq_wildcard_rebased_helper_indexed")
	if eqWildcardRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find eq_wildcard_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqWildcardRebasedHelperIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_wildcard_rebased_helper_indexed to avoid helper fallback, got:\n%s", eqWildcardRebasedHelperIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqWildcardRebasedHelperIndexedBody, true)
}
func TestGenerateLLVMIRKeepsOverlapGuardrailsThroughWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewWindow:
	items: view[Views]

@borrows_return_field_rebased(items[*].left, src[*].left, items[*].right, src[*].right)
extern wrap_sub_wild(src: view[Views], start: usize, end: usize) -> ViewWindow

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_wildcard_rebased_overlap(values: array[i32, 8]) -> bool:
	items: array[Views, 2] = [Views(values[0:3], values[1:4]), Views(values[4:7], values[5:8])]
	wrapped: ViewWindow = wrap_sub_wild(items[0:1], 0, 1)
	return arena_da_eq_exact(wrapped.items[0].left, wrapped.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_wildcard_rebased_helper_indexed_overlap_guardrails.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqWildcardRebasedOverlapBody := functionIR(output, "eq_wildcard_rebased_overlap")
	if eqWildcardRebasedOverlapBody == "" {
		t.Fatalf("expected to find eq_wildcard_rebased_overlap body, got:\n%s", output)
	}
	if strings.Contains(eqWildcardRebasedOverlapBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_wildcard_rebased_overlap to avoid helper fallback, got:\n%s", eqWildcardRebasedOverlapBody)
	}
	requireTinyExactDViewEqBody(t, eqWildcardRebasedOverlapBody, false)
}
func TestGenerateLLVMIRKeepsOverlapGuardrailsThroughNestedWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct Meta:
	items: view[Views]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].left, src[*].left, meta.items[*].right, src[*].right)
extern wrap_submeta_wild(src: view[Views], start: usize, end: usize) -> Wrapper

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_nested_wildcard_rebased_overlap(values: array[i32, 8]) -> bool:
	items: array[Views, 2] = [Views(values[0:3], values[1:4]), Views(values[4:7], values[5:8])]
	wrapped: Wrapper = wrap_submeta_wild(items[0:1], 0, 1)
	return arena_da_eq_exact(wrapped.meta.items[0].left, wrapped.meta.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_nested_wildcard_rebased_helper_indexed_overlap_guardrails.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqNestedWildcardRebasedOverlapBody := functionIR(output, "eq_nested_wildcard_rebased_overlap")
	if eqNestedWildcardRebasedOverlapBody == "" {
		t.Fatalf("expected to find eq_nested_wildcard_rebased_overlap body, got:\n%s", output)
	}
	if strings.Contains(eqNestedWildcardRebasedOverlapBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_nested_wildcard_rebased_overlap to avoid helper fallback, got:\n%s", eqNestedWildcardRebasedOverlapBody)
	}
	requireTinyExactDViewEqBody(t, eqNestedWildcardRebasedOverlapBody, false)
}
func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughNestedRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct Meta:
	items: view[Views]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Views], start: usize, end: usize) -> Wrapper

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_nested_rebased_helper_indexed(values: array[i32, 4]) -> bool:
	items: array[Views, 2] = [Views(values[0:1], values[1:2]), Views(values[2:3], values[3:4])]
	wrapped: Wrapper = wrap_submeta(items[1:2], 0, 1)
	return arena_da_eq_exact(wrapped.meta.items[0].left, wrapped.meta.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_nested_rebased_helper_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqNestedRebasedHelperIndexedBody := functionIR(output, "eq_nested_rebased_helper_indexed")
	if eqNestedRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find eq_nested_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqNestedRebasedHelperIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_nested_rebased_helper_indexed to avoid helper fallback, got:\n%s", eqNestedRebasedHelperIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqNestedRebasedHelperIndexedBody, true)
}
func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughNestedWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct Meta:
	items: view[Views]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].left, src[*].left, meta.items[*].right, src[*].right)
extern wrap_submeta_wild(src: view[Views], start: usize, end: usize) -> Wrapper

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_nested_wildcard_rebased_helper_indexed(values: array[i32, 8]) -> bool:
	items: array[Views, 4] = [Views(values[0:1], values[1:2]), Views(values[2:3], values[3:4]), Views(values[4:5], values[5:6]), Views(values[6:7], values[7:8])]
	wrapped: Wrapper = wrap_submeta_wild(items[1:3], 0, 2)
	return arena_da_eq_exact(wrapped.meta.items[0].left, wrapped.meta.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_nested_wildcard_rebased_helper_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqNestedWildcardRebasedHelperIndexedBody := functionIR(output, "eq_nested_wildcard_rebased_helper_indexed")
	if eqNestedWildcardRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find eq_nested_wildcard_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqNestedWildcardRebasedHelperIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_nested_wildcard_rebased_helper_indexed to avoid helper fallback, got:\n%s", eqNestedWildcardRebasedHelperIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqNestedWildcardRebasedHelperIndexedBody, true)
}
func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughNestedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct NestedViews:
	inner: Views

@borrows_return_field(inner.left, left, inner.right, right)
extern wrap_nested_views(left: view[i32], right: view[i32]) -> NestedViews

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_nested_struct(values: array[i32, 4]) -> bool:
	boxed: NestedViews = NestedViews(Views(values[0:2], values[2:4]))
	return arena_da_eq_exact(boxed.inner.left, boxed.inner.right)

def eq_nested_helper(values: array[i32, 4]) -> bool:
	boxed: NestedViews = wrap_nested_views(values[0:2], values[2:4])
	return arena_da_eq_exact(boxed.inner.left, boxed.inner.right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_nested_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqStructBody := functionIR(output, "eq_nested_struct")
	if eqStructBody == "" {
		t.Fatalf("expected to find eq_nested_struct body, got:\n%s", output)
	}
	if strings.Contains(eqStructBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_nested_struct to avoid helper fallback, got:\n%s", eqStructBody)
	}
	requireTinyExactDViewEqBody(t, eqStructBody, true)

	eqHelperBody := functionIR(output, "eq_nested_helper")
	if eqHelperBody == "" {
		t.Fatalf("expected to find eq_nested_helper body, got:\n%s", output)
	}
	if strings.Contains(eqHelperBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_nested_helper to avoid helper fallback, got:\n%s", eqHelperBody)
	}
	requireTinyExactDViewEqBody(t, eqHelperBody, true)
}
func TestGenerateLLVMIRSpecializesArenaDViewMaterialize(t *testing.T) {
	src := `struct Arena:
	begin: mutable heap void&?
	end: mutable heap void&?
	end_index: mutable usize

struct DynArray[T]:
	items: mutable T&?
	count: mutable usize
	capacity: mutable usize

struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[void&], values.count, sizeof(T))
	return DynArrayView(null, 0, sizeof(T))

def arena_da_from_view[T](a: Arena&, view: dview[T]) -> darray[T, shape_out]:
	_ = a
	_ = view
	out: darray[T, shape_out] = zeroed
	return out

def materialize_split(a: Arena&, values: darray[i32, 4]&) -> darray[i32]:
	base: dview[i32] = arena_da_view(values, 0, 4)
	left: dview[i32] = base[0:2]
	return arena_da_from_view(a, left)

def materialize_unknown(a: Arena&, view: dview[i32]) -> darray[i32]:
	return arena_da_from_view(a, view)
`
	result := parseAndAnalyze(t, "backend_dview_materialize.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	materializeSplitBody := functionIR(output, "materialize_split")
	if materializeSplitBody == "" {
		t.Fatalf("expected to find materialize_split body, got:\n%s", output)
	}
	if strings.Contains(materializeSplitBody, "call %DynArray__i32 @arena_da_from_view") {
		t.Fatalf("expected materialize_split to avoid helper fallback, got:\n%s", materializeSplitBody)
	}
	requireTinyExactDViewMaterializeBody(t, materializeSplitBody)

	materializeUnknownBody := functionIR(output, "materialize_unknown")
	if materializeUnknownBody == "" {
		t.Fatalf("expected to find materialize_unknown body, got:\n%s", output)
	}
	if !strings.Contains(materializeUnknownBody, "call %DynArray__i32 @arena_da_from_view") {
		t.Fatalf("expected materialize_unknown to keep helper fallback when extent is not exact, got:\n%s", materializeUnknownBody)
	}
}
func TestGenerateLLVMIRSpecializesStringViewMaterialize(t *testing.T) {
	src := `struct StringView:
	data: mutable u8&
	len: mutable i64

extern memcpy(dest: void&?, src: void&?, n: usize) -> void&?
extern alloc_perm(size: i64) -> heap void&
extern register_perm_string_len(ptr: u8&?, len: usize)
extern intern_small_string(src: u8&, len: usize) -> heap u8&

def sview(value: u8&?, start: i64, end: i64) -> StringView:
	src: u8& = value if value != null else "" as u8&
	_ = start
	return StringView(src, end)

def ctx_string_view(value: cstr[shape_in], start: i64, end: i64) -> StringView:
	return sview(value, start, end)

def string_view_copy(view: StringView) -> heap u8&:
	_ = view
	return intern_small_string("" as u8&, 0)

def ctx_string_from_view(view: StringView) -> cstr[shape_out]:
	return string_view_copy(view)

def copy_small(text: cstr[row]) -> cstr:
	view: StringView = ctx_string_view(text, 0, 2)
	return ctx_string_from_view(view)

def copy_large(text: cstr[row]) -> cstr:
	view: StringView = ctx_string_view(text, 0, 12)
	return ctx_string_from_view(view)

def copy_unknown(view: StringView) -> cstr:
	return ctx_string_from_view(view)

def copy_small_raw(text: cstr[row]) -> heap u8&:
	view: StringView = ctx_string_view(text, 0, 2)
	return string_view_copy(view)
`
	result := parseAndAnalyze(t, "backend_string_view_materialize.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copySmallBody := functionIR(output, "copy_small")
	if copySmallBody == "" {
		t.Fatalf("expected to find copy_small body, got:\n%s", output)
	}
	if strings.Contains(copySmallBody, "call ptr @ctx_string_from_view") {
		t.Fatalf("expected copy_small to avoid ctx_string_from_view helper fallback, got:\n%s", copySmallBody)
	}
	if !strings.Contains(copySmallBody, "call ptr @intern_small_string(ptr") {
		t.Fatalf("expected copy_small to lower through intern_small_string, got:\n%s", copySmallBody)
	}

	copyLargeBody := functionIR(output, "copy_large")
	if copyLargeBody == "" {
		t.Fatalf("expected to find copy_large body, got:\n%s", output)
	}
	for _, check := range []string{"call ptr @alloc_perm(i64 13)", "call ptr @memcpy(ptr", "call void @register_perm_string_len(ptr"} {
		if !strings.Contains(copyLargeBody, check) {
			t.Fatalf("expected copy_large to contain %q, got:\n%s", check, copyLargeBody)
		}
	}
	if strings.Contains(copyLargeBody, "call ptr @ctx_string_from_view") {
		t.Fatalf("expected copy_large to avoid ctx_string_from_view helper fallback, got:\n%s", copyLargeBody)
	}

	copyUnknownBody := functionIR(output, "copy_unknown")
	if copyUnknownBody == "" {
		t.Fatalf("expected to find copy_unknown body, got:\n%s", output)
	}
	if !strings.Contains(copyUnknownBody, "call ptr @ctx_string_from_view") {
		t.Fatalf("expected copy_unknown to keep helper fallback when extent is not exact, got:\n%s", copyUnknownBody)
	}

	copySmallRawBody := functionIR(output, "copy_small_raw")
	if copySmallRawBody == "" {
		t.Fatalf("expected to find copy_small_raw body, got:\n%s", output)
	}
	if strings.Contains(copySmallRawBody, "call ptr @string_view_copy") {
		t.Fatalf("expected copy_small_raw to avoid string_view_copy helper fallback, got:\n%s", copySmallRawBody)
	}
	if !strings.Contains(copySmallRawBody, "call ptr @intern_small_string(ptr") {
		t.Fatalf("expected copy_small_raw to lower through intern_small_string, got:\n%s", copySmallRawBody)
	}
}
func TestGenerateLLVMIRSpecializesStringViewLiteralWrapperCalls(t *testing.T) {
	src := `extern string_view_eq(view: StringView, other: u8&?) -> int

def frontend_sv_eq_literal(view: StringView, literal: static u8&) -> bool:
	return string_view_eq(view, literal as u8&) != 0

def same_short(view: StringView) -> bool:
	return frontend_sv_eq_literal(view, "def")

def same_long(view: StringView) -> bool:
	return frontend_sv_eq_literal(view, "destroy_region")
`
	result := parseAndAnalyze(t, "backend_runtime_string_literal_wrapper_eq.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i1 @same_short(%StringView",
		"define i1 @same_long(%StringView",
		"declare i64 @memcmp(ptr, ptr, i64)",
		"call i64 @memcmp(ptr",
		"load i8, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call i1 @frontend_sv_eq_literal") {
		t.Fatalf("expected wrapper literal lowering to inline away frontend_sv_eq_literal at call sites, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersDStrLenFieldViaRuntimeHelper(t *testing.T) {
	src := `def text_len(text: cstr[row]) -> i64:
    return text.len
`
	result := parseAndAnalyze(t, "backend_cstr_len.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @text_len(ptr",
		"declare i64 @ctx_strlen(ptr)",
		"call i64 @ctx_strlen(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersDArrayViewRuntimeFields(t *testing.T) {
	src := `def non_empty[T](view: dview[T]) -> bool:
	return view.len > 0 and view.elem_size > 0

def probe(view: dview[i64]) -> bool:
	return non_empty(view)
`
	result := parseAndAnalyze(t, "backend_darray_view_runtime_fields.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArrayView = type { ptr, i64, i64 }",
		"define i1 @non_empty__i64(%DynArrayView",
		"getelementptr inbounds nuw %DynArrayView",
		"icmp ugt i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersArraySliceSyntaxViaRuntimeHelpers(t *testing.T) {
	src := `struct DynArray[T]:
	items: mutable T&?
	count: mutable usize
	capacity: mutable usize

struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

def head_owned(values: darray[i32, row]) -> i32:
	part: dview[i32] = values[1:3]
	return part[0]

def head_view(view: dview[i32]) -> i32:
	part: dview[i32] = view[0:1]
	return part[0]
`
	result := parseAndAnalyze(t, "backend_array_slice_syntax.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArray__i32 = type { ptr, i64, i64 }",
		"%DynArrayView = type { ptr, i64, i64 }",
		"define i32 @head_owned(%DynArray__i32",
		"define i32 @head_view(%DynArrayView",
		"extractvalue %DynArray__i32",
		"darrayslice.view.len.out = sub i64",
		"slicetmp.len.out = sub i64",
		"getelementptr i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call %DynArrayView @arena_da_view(ptr") {
		t.Fatalf("expected darray slice syntax to build a view before slicing, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersFixedArraySliceSyntaxWithoutRuntimeHelpers(t *testing.T) {
	src := `struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

def slice_owned(values: i32[4]) -> view[i32]:
	return values[1:3]

def head_ref(values: i32[4]&) -> i32:
	return values[1:3][0]
`
	result := parseAndAnalyze(t, "backend_fixed_array_slice.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArrayView = type { ptr, i64, i64 }",
		"define %DynArrayView @slice_owned([4 x i32]",
		"define i32 @head_ref(ptr",
		"getelementptr [4 x i32], ptr",
		"insertvalue %DynArrayView",
		"getelementptr i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "@arena_da_view") || strings.Contains(output, "@arena_da_view_slice") {
		t.Fatalf("expected fixed-array slicing not to depend on dynamic array runtime helpers, got:\n%s", output)
	}
}
