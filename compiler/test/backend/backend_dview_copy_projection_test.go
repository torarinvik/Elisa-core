package backend_test

import (
	"elisacore/src/backend"
	"strings"
	"testing"
)

func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

@borrows_return(field, left, left, right, right)
extern wrap_views(left: view[i32], right: view[i32]) -> Views

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_struct(values: array[i32, 4]) -> void:
	boxed: Views = Views{left: values[0:2], right: values[2:4]}
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
	if assertSourceHelperFallback(t, copyStructBody) {
		return
	}
	if copyStructBody == "" {
		t.Fatalf("expected to find copy_struct body, got:\n%s", output)
	}
	if strings.Contains(copyStructBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_struct to avoid helper fallback, got:\n%s", copyStructBody)
	}
	requireTinyExactDViewCopyBody(t, copyStructBody)

	copyHelperBody := functionIR(output, "copy_helper")
	if assertSourceHelperFallback(t, copyHelperBody) {
		return
	}
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
	items: array[Views, 1] = [Views{left: values[0:2], right: values[2:4]}]
	arena_da_copy_exact(items[0].left, items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyIndexedBody := functionIR(output, "copy_indexed")
	if assertSourceHelperFallback(t, copyIndexedBody) {
		return
	}
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
	items: array[Views, 2] = [Views{left: values[0:2], right: values[2:4]}, Views{left: values[4:6], right: values[6:8]}]
	window: view[Views] = arena_da_view_slice(items[1:2], 0, 1)
	arena_da_copy_exact(window[0].left, window[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_standard_view_slice_helper_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	body := functionIR(output, "copy_helper_view_slice")
	if assertSourceHelperFallback(t, body) {
		return
	}
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

@borrows_return(field, items[0].left, left, items[0].right, right)
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
	if assertSourceHelperFallback(t, copyHelperIndexedBody) {
		return
	}
	if copyHelperIndexedBody == "" {
		t.Fatalf("expected to find copy_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyHelperIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_helper_indexed to avoid helper fallback, got:\n%s", copyHelperIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyHelperIndexedBody)
}
