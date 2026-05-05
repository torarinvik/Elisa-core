package backend_test

import (
	"elisacore/src/backend"
	"strings"
	"testing"
)

func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughNestedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewHolder:
	items: array[Views, 1]

struct NestedHolder:
	holder: ViewHolder

@borrows_return_field(holder.items[0].left, left, holder.items[0].right, right)
extern wrap_nested_indexed_views(left: view[i32], right: view[i32]) -> NestedHolder

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_nested_helper_indexed(values: array[i32, 4]) -> void:
	wrapped: NestedHolder = wrap_nested_indexed_views(values[0:2], values[2:4])
	arena_da_copy_exact(wrapped.holder.items[0].left, wrapped.holder.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_nested_helper_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyNestedHelperIndexedBody := functionIR(output, "copy_nested_helper_indexed")
	if copyNestedHelperIndexedBody == "" {
		t.Fatalf("expected to find copy_nested_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyNestedHelperIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_nested_helper_indexed to avoid helper fallback, got:\n%s", copyNestedHelperIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyNestedHelperIndexedBody)
}
func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewWindow:
	items: view[Views]

@borrows_return_field_rebased(items, src)
extern wrap_sub(src: view[Views], start: usize, end: usize) -> ViewWindow

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_rebased_helper_indexed(values: array[i32, 4]) -> void:
	items: array[Views, 2] = [Views(values[0:1], values[1:2]), Views(values[2:3], values[3:4])]
	wrapped: ViewWindow = wrap_sub(items[1:2], 0, 1)
	arena_da_copy_exact(wrapped.items[0].left, wrapped.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_rebased_helper_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyRebasedHelperIndexedBody := functionIR(output, "copy_rebased_helper_indexed")
	if copyRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find copy_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyRebasedHelperIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_rebased_helper_indexed to avoid helper fallback, got:\n%s", copyRebasedHelperIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyRebasedHelperIndexedBody)
}
func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewWindow:
	items: view[Views]

@borrows_return_field_rebased(items[*].left, src[*].left, items[*].right, src[*].right)
extern wrap_sub_wild(src: view[Views], start: usize, end: usize) -> ViewWindow

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_wildcard_rebased_helper_indexed(values: array[i32, 8]) -> void:
	items: array[Views, 4] = [Views(values[0:1], values[1:2]), Views(values[2:3], values[3:4]), Views(values[4:5], values[5:6]), Views(values[6:7], values[7:8])]
	wrapped: ViewWindow = wrap_sub_wild(items[1:3], 0, 2)
	arena_da_copy_exact(wrapped.items[0].left, wrapped.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_wildcard_rebased_helper_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyWildcardRebasedHelperIndexedBody := functionIR(output, "copy_wildcard_rebased_helper_indexed")
	if copyWildcardRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find copy_wildcard_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyWildcardRebasedHelperIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_wildcard_rebased_helper_indexed to avoid helper fallback, got:\n%s", copyWildcardRebasedHelperIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyWildcardRebasedHelperIndexedBody)
}
func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughNestedWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct Meta:
	items: view[Views]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].left, src[*].left, meta.items[*].right, src[*].right)
extern wrap_submeta_wild(src: view[Views], start: usize, end: usize) -> Wrapper

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_nested_wildcard_rebased_helper_indexed(values: array[i32, 8]) -> void:
	items: array[Views, 4] = [Views(values[0:1], values[1:2]), Views(values[2:3], values[3:4]), Views(values[4:5], values[5:6]), Views(values[6:7], values[7:8])]
	wrapped: Wrapper = wrap_submeta_wild(items[1:3], 0, 2)
	arena_da_copy_exact(wrapped.meta.items[0].left, wrapped.meta.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_nested_wildcard_rebased_helper_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyNestedWildcardRebasedHelperIndexedBody := functionIR(output, "copy_nested_wildcard_rebased_helper_indexed")
	if copyNestedWildcardRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find copy_nested_wildcard_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyNestedWildcardRebasedHelperIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_nested_wildcard_rebased_helper_indexed to avoid helper fallback, got:\n%s", copyNestedWildcardRebasedHelperIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyNestedWildcardRebasedHelperIndexedBody)
}
func TestGenerateLLVMIRKeepsCopyOverlapGuardrailsThroughWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewWindow:
	items: view[Views]

@borrows_return_field_rebased(items[*].left, src[*].left, items[*].right, src[*].right)
extern wrap_sub_wild(src: view[Views], start: usize, end: usize) -> ViewWindow

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_wildcard_rebased_overlap(values: array[i32, 8]) -> void:
	items: array[Views, 2] = [Views(values[0:3], values[1:4]), Views(values[4:7], values[5:8])]
	wrapped: ViewWindow = wrap_sub_wild(items[0:1], 0, 1)
	arena_da_copy_exact(wrapped.items[0].left, wrapped.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_wildcard_rebased_helper_indexed_overlap_guardrails.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyWildcardRebasedOverlapBody := functionIR(output, "copy_wildcard_rebased_overlap")
	if copyWildcardRebasedOverlapBody == "" {
		t.Fatalf("expected to find copy_wildcard_rebased_overlap body, got:\n%s", output)
	}
	if strings.Contains(copyWildcardRebasedOverlapBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_wildcard_rebased_overlap to avoid helper fallback, got:\n%s", copyWildcardRebasedOverlapBody)
	}
	if strings.Contains(copyWildcardRebasedOverlapBody, "call ptr @arena_memcpy") {
		t.Fatalf("expected copy_wildcard_rebased_overlap to preserve overlap semantics instead of arena_memcpy, got:\n%s", copyWildcardRebasedOverlapBody)
	}
	if !strings.Contains(copyWildcardRebasedOverlapBody, "load i32, ptr") || !strings.Contains(copyWildcardRebasedOverlapBody, "store i32") {
		t.Fatalf("expected copy_wildcard_rebased_overlap to lower through direct element loads/stores, got:\n%s", copyWildcardRebasedOverlapBody)
	}
}
func TestGenerateLLVMIRKeepsCopyOverlapGuardrailsThroughNestedWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct Meta:
	items: view[Views]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].left, src[*].left, meta.items[*].right, src[*].right)
extern wrap_submeta_wild(src: view[Views], start: usize, end: usize) -> Wrapper

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_nested_wildcard_rebased_overlap(values: array[i32, 8]) -> void:
	items: array[Views, 2] = [Views(values[0:3], values[1:4]), Views(values[4:7], values[5:8])]
	wrapped: Wrapper = wrap_submeta_wild(items[0:1], 0, 1)
	arena_da_copy_exact(wrapped.meta.items[0].left, wrapped.meta.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_nested_wildcard_rebased_helper_indexed_overlap_guardrails.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyNestedWildcardRebasedOverlapBody := functionIR(output, "copy_nested_wildcard_rebased_overlap")
	if copyNestedWildcardRebasedOverlapBody == "" {
		t.Fatalf("expected to find copy_nested_wildcard_rebased_overlap body, got:\n%s", output)
	}
	if strings.Contains(copyNestedWildcardRebasedOverlapBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_nested_wildcard_rebased_overlap to avoid helper fallback, got:\n%s", copyNestedWildcardRebasedOverlapBody)
	}
	if strings.Contains(copyNestedWildcardRebasedOverlapBody, "call ptr @arena_memcpy") {
		t.Fatalf("expected copy_nested_wildcard_rebased_overlap to preserve overlap semantics instead of arena_memcpy, got:\n%s", copyNestedWildcardRebasedOverlapBody)
	}
	if !strings.Contains(copyNestedWildcardRebasedOverlapBody, "load i32, ptr") || !strings.Contains(copyNestedWildcardRebasedOverlapBody, "store i32") {
		t.Fatalf("expected copy_nested_wildcard_rebased_overlap to lower through direct element loads/stores, got:\n%s", copyNestedWildcardRebasedOverlapBody)
	}
}
func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughNestedRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct Meta:
	items: view[Views]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Views], start: usize, end: usize) -> Wrapper

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_nested_rebased_helper_indexed(values: array[i32, 4]) -> void:
	items: array[Views, 2] = [Views(values[0:1], values[1:2]), Views(values[2:3], values[3:4])]
	wrapped: Wrapper = wrap_submeta(items[1:2], 0, 1)
	arena_da_copy_exact(wrapped.meta.items[0].left, wrapped.meta.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_nested_rebased_helper_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyNestedRebasedHelperIndexedBody := functionIR(output, "copy_nested_rebased_helper_indexed")
	if copyNestedRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find copy_nested_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyNestedRebasedHelperIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_nested_rebased_helper_indexed to avoid helper fallback, got:\n%s", copyNestedRebasedHelperIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyNestedRebasedHelperIndexedBody)
}
func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughNestedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct NestedViews:
	inner: Views

@borrows_return_field(inner.left, left, inner.right, right)
extern wrap_nested_views(left: view[i32], right: view[i32]) -> NestedViews

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_nested_struct(values: array[i32, 4]) -> void:
	boxed: NestedViews = NestedViews(Views(values[0:2], values[2:4]))
	arena_da_copy_exact(boxed.inner.left, boxed.inner.right)

def copy_nested_helper(values: array[i32, 4]) -> void:
	boxed: NestedViews = wrap_nested_views(values[0:2], values[2:4])
	arena_da_copy_exact(boxed.inner.left, boxed.inner.right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_nested_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyStructBody := functionIR(output, "copy_nested_struct")
	if copyStructBody == "" {
		t.Fatalf("expected to find copy_nested_struct body, got:\n%s", output)
	}
	if strings.Contains(copyStructBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_nested_struct to avoid helper fallback, got:\n%s", copyStructBody)
	}
	requireTinyExactDViewCopyBody(t, copyStructBody)

	copyHelperBody := functionIR(output, "copy_nested_helper")
	if copyHelperBody == "" {
		t.Fatalf("expected to find copy_nested_helper body, got:\n%s", output)
	}
	if strings.Contains(copyHelperBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_nested_helper to avoid helper fallback, got:\n%s", copyHelperBody)
	}
	requireTinyExactDViewCopyBody(t, copyHelperBody)
}
func TestGenerateLLVMIRSpecializesArenaDViewZeroFill(t *testing.T) {
	src := `struct DynArray[T]:
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

def arena_da_fill[T](dst: dview[T], value: T):
	_ = dst
	_ = value

def zero_split(values: darray[i32, 4]&) -> void:
	base: dview[i32] = arena_da_view(values, 0, 4)
	left: dview[i32] = base[0:2]
	arena_da_fill(left, 0)

def fill_split(values: darray[i32, 4]&) -> void:
	base: dview[i32] = arena_da_view(values, 0, 4)
	left: dview[i32] = base[0:2]
	arena_da_fill(left, 7)

def fill_unknown(view: dview[i32]) -> void:
	arena_da_fill(view, 7)
`
	result := parseAndAnalyze(t, "backend_dview_fill_zero.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	zeroBody := functionIR(output, "zero_split")
	if zeroBody == "" {
		t.Fatalf("expected to find zero_split body, got:\n%s", output)
	}
	if strings.Contains(zeroBody, "call void @arena_da_fill") {
		t.Fatalf("expected zero_split to avoid generic helper fallback, got:\n%s", zeroBody)
	}
	if strings.Contains(zeroBody, "call ptr @memset(ptr") {
		t.Fatalf("expected zero_split to use direct zero stores on the tiny exact fast path, got:\n%s", zeroBody)
	}
	if strings.Count(zeroBody, "store i32 0, ptr %dview.fill.elem.ptr") < 2 {
		t.Fatalf("expected zero_split to lower through direct zero stores, got:\n%s", zeroBody)
	}

	fillBody := functionIR(output, "fill_split")
	if fillBody == "" {
		t.Fatalf("expected to find fill_split body, got:\n%s", output)
	}
	if strings.Contains(fillBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_split to avoid helper fallback for small exact extents, got:\n%s", fillBody)
	}
	if strings.Contains(fillBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_split to avoid memset specialization, got:\n%s", fillBody)
	}
	if !strings.Contains(fillBody, "store i32 7") {
		t.Fatalf("expected fill_split to lower through direct stores, got:\n%s", fillBody)
	}

	fillUnknownBody := functionIR(output, "fill_unknown")
	if fillUnknownBody == "" {
		t.Fatalf("expected to find fill_unknown body, got:\n%s", output)
	}
	if !strings.Contains(fillUnknownBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_unknown to keep helper fallback, got:\n%s", fillUnknownBody)
	}
}
func TestGenerateLLVMIRSpecializesArenaDViewRepeatedByteFill(t *testing.T) {
	src := `struct DynArray[T]:
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

def arena_da_fill[T](dst: dview[T], value: T):
	_ = dst
	_ = value

def fill_bytes(values: darray[u8, 4]&) -> void:
	base: dview[u8] = arena_da_view(values, 0, 4)
	left: dview[u8] = base[0:2]
	arena_da_fill(left, 7)

def fill_all_ones(values: darray[i32, 4]&) -> void:
	base: dview[i32] = arena_da_view(values, 0, 4)
	left: dview[i32] = base[0:2]
	arena_da_fill(left, -1)

def fill_nonuniform(values: darray[i32, 4]&) -> void:
	base: dview[i32] = arena_da_view(values, 0, 4)
	left: dview[i32] = base[0:2]
	arena_da_fill(left, 7)

def fill_nonuniform_unknown(view: dview[i32]) -> void:
	arena_da_fill(view, 7)
`
	result := parseAndAnalyze(t, "backend_dview_fill_repeated_byte.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	byteBody := functionIR(output, "fill_bytes")
	if byteBody == "" {
		t.Fatalf("expected to find fill_bytes body, got:\n%s", output)
	}
	if strings.Contains(byteBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_bytes to avoid generic helper fallback, got:\n%s", byteBody)
	}
	if strings.Contains(byteBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_bytes to use direct byte stores on the tiny exact fast path, got:\n%s", byteBody)
	}
	if strings.Count(byteBody, "store i8 7, ptr %dview.fill.elem.ptr") < 2 {
		t.Fatalf("expected fill_bytes to lower through direct byte stores, got:\n%s", byteBody)
	}

	onesBody := functionIR(output, "fill_all_ones")
	if onesBody == "" {
		t.Fatalf("expected to find fill_all_ones body, got:\n%s", output)
	}
	if strings.Contains(onesBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_all_ones to avoid generic helper fallback, got:\n%s", onesBody)
	}
	if strings.Contains(onesBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_all_ones to use direct stores on the tiny exact fast path, got:\n%s", onesBody)
	}
	if strings.Count(onesBody, "store i32 -1, ptr %dview.fill.elem.ptr") < 2 {
		t.Fatalf("expected fill_all_ones to lower through direct stores, got:\n%s", onesBody)
	}

	nonUniformBody := functionIR(output, "fill_nonuniform")
	if nonUniformBody == "" {
		t.Fatalf("expected to find fill_nonuniform body, got:\n%s", output)
	}
	if strings.Contains(nonUniformBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_nonuniform to avoid generic helper fallback, got:\n%s", nonUniformBody)
	}
	if strings.Contains(nonUniformBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_nonuniform to avoid memset specialization, got:\n%s", nonUniformBody)
	}
	if !strings.Contains(nonUniformBody, "store i32 7") {
		t.Fatalf("expected fill_nonuniform to lower through direct stores, got:\n%s", nonUniformBody)
	}

	nonUniformUnknownBody := functionIR(output, "fill_nonuniform_unknown")
	if nonUniformUnknownBody == "" {
		t.Fatalf("expected to find fill_nonuniform_unknown body, got:\n%s", output)
	}
	if !strings.Contains(nonUniformUnknownBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_nonuniform_unknown to keep helper fallback, got:\n%s", nonUniformUnknownBody)
	}
}
