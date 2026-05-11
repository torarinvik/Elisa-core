package backend_test

import (
	"elisacore/src/backend"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateLLVMIRSpecializesArenaDViewDynamicByteFill(t *testing.T) {
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
		return DynArrayView(values.items.cast[void&], values.count, size_of(T))
	return DynArrayView(null, 0, size_of(T))

def arena_da_fill[T](dst: dview[T], value: T):
	_ = dst
	_ = value

def fill_runtime_byte(values: darray[u8, 4]&, value: u8) -> void:
	base: dview[u8] = arena_da_view(values, 0, 4)
	left: dview[u8] = base[0:2]
	arena_da_fill(left, value)

def fill_runtime_wide(values: darray[i32, 4]&, value: i32) -> void:
	base: dview[i32] = arena_da_view(values, 0, 4)
	left: dview[i32] = base[0:2]
	arena_da_fill(left, value)

def fill_runtime_wide_unknown(view: dview[i32], value: i32) -> void:
	arena_da_fill(view, value)
`
	result := parseAndAnalyze(t, "backend_dview_fill_dynamic_byte.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	runtimeByteBody := functionIR(output, "fill_runtime_byte")
	if runtimeByteBody == "" {
		t.Fatalf("expected to find fill_runtime_byte body, got:\n%s", output)
	}
	if strings.Contains(runtimeByteBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_runtime_byte to avoid generic helper fallback, got:\n%s", runtimeByteBody)
	}
	if strings.Contains(runtimeByteBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_runtime_byte to use direct byte stores on the tiny exact fast path, got:\n%s", runtimeByteBody)
	}
	if strings.Count(runtimeByteBody, "store i8 %value") < 2 {
		t.Fatalf("expected fill_runtime_byte to lower through direct runtime-value byte stores, got:\n%s", runtimeByteBody)
	}

	runtimeWideBody := functionIR(output, "fill_runtime_wide")
	if runtimeWideBody == "" {
		t.Fatalf("expected to find fill_runtime_wide body, got:\n%s", output)
	}
	if strings.Contains(runtimeWideBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_runtime_wide to avoid helper fallback for small exact extents, got:\n%s", runtimeWideBody)
	}
	if strings.Contains(runtimeWideBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_runtime_wide to avoid memset specialization, got:\n%s", runtimeWideBody)
	}
	if !strings.Contains(runtimeWideBody, "store i32 %1") && !strings.Contains(runtimeWideBody, "store i32 %0") {
		t.Fatalf("expected fill_runtime_wide to lower through direct runtime-value stores, got:\n%s", runtimeWideBody)
	}

	runtimeWideUnknownBody := functionIR(output, "fill_runtime_wide_unknown")
	if runtimeWideUnknownBody == "" {
		t.Fatalf("expected to find fill_runtime_wide_unknown body, got:\n%s", output)
	}
	if !strings.Contains(runtimeWideUnknownBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_runtime_wide_unknown to keep helper fallback, got:\n%s", runtimeWideUnknownBody)
	}
}
func TestGenerateLLVMIRSpecializesArenaDViewCoercedByteFill(t *testing.T) {
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
		return DynArrayView(values.items.cast[void&], values.count, size_of(T))
	return DynArrayView(null, 0, size_of(T))

def arena_da_fill[T](dst: dview[T], value: T):
	_ = dst
	_ = value

def fill_literal_int_to_bytes(values: darray[u8, 4]&) -> void:
	base: dview[u8] = arena_da_view(values, 0, 4)
	left: dview[u8] = base[0:2]
	arena_da_fill(left, 7)

def fill_runtime_int_to_bytes(values: darray[u8, 4]&, value: int) -> void:
	base: dview[u8] = arena_da_view(values, 0, 4)
	left: dview[u8] = base[0:2]
	arena_da_fill(left, value)
`
	result := parseAndAnalyze(t, "backend_dview_fill_coerced_byte.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	literalBody := functionIR(output, "fill_literal_int_to_bytes")
	if literalBody == "" {
		t.Fatalf("expected to find fill_literal_int_to_bytes body, got:\n%s", output)
	}
	if strings.Contains(literalBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_literal_int_to_bytes to avoid generic helper fallback, got:\n%s", literalBody)
	}
	if strings.Contains(literalBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_literal_int_to_bytes to use direct byte stores on the tiny exact fast path, got:\n%s", literalBody)
	}
	if strings.Count(literalBody, "store i8 7, ptr %dview.fill.elem.ptr") < 2 {
		t.Fatalf("expected fill_literal_int_to_bytes to lower through direct byte stores, got:\n%s", literalBody)
	}

	runtimeBody := functionIR(output, "fill_runtime_int_to_bytes")
	if runtimeBody == "" {
		t.Fatalf("expected to find fill_runtime_int_to_bytes body, got:\n%s", output)
	}
	if strings.Contains(runtimeBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_runtime_int_to_bytes to avoid generic helper fallback, got:\n%s", runtimeBody)
	}
	if strings.Contains(runtimeBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_runtime_int_to_bytes to use direct byte stores on the tiny exact fast path, got:\n%s", runtimeBody)
	}
	if strings.Count(runtimeBody, "store i8 %") < 2 {
		t.Fatalf("expected fill_runtime_int_to_bytes to lower through direct runtime-value byte stores, got:\n%s", runtimeBody)
	}
}
func TestGenerateOptimizedLLVMIRSupportsArenaDViewByteFillMemsetFastPath(t *testing.T) {
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
		return DynArrayView(values.items.cast[void&], values.count, size_of(T))
	return DynArrayView(null, 0, size_of(T))

def arena_da_fill[T](dst: dview[T], value: T):
	_ = dst
	_ = value

def fill_runtime_byte(values: darray[u8, 4]&, value: u8) -> void:
	base: dview[u8] = arena_da_view(values, 0, 4)
	left: dview[u8] = base[0:2]
	arena_da_fill(left, value)
`
	result := parseAndAnalyze(t, "backend_dview_fill_dynamic_byte_optimized.elisa", src)
	output, err := backend.GenerateLLVMIRWithOpt(result, backend.OptimizationLevel3)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.TrimSpace(output) == "" {
		t.Fatalf("expected optimized output to be non-empty")
	}
	if strings.Contains(output, "llvm.memset.p0.i64.invalid") {
		t.Fatalf("expected optimized output to avoid malformed llvm.memset intrinsic names, got:\n%s", output)
	}
}
func TestGenerateOptimizedLLVMObjectFileSupportsArenaDViewByteFillWithRuntimeMemsetDecl(t *testing.T) {
	src := `extern memset(dest: void&, val: int, n: usize) -> void&

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
		return DynArrayView(values.items.cast[void&], values.count, size_of(T))
	return DynArrayView(null, 0, size_of(T))

def arena_da_fill[T](dst: dview[T], value: T):
	_ = dst
	_ = value

def fill_runtime_byte(values: darray[u8, 4]&, value: u8) -> void:
	base: dview[u8] = arena_da_view(values, 0, 4)
	left: dview[u8] = base[0:2]
	arena_da_fill(left, value)
`
	result := parseAndAnalyze(t, "backend_dview_fill_dynamic_byte_object.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	if !strings.Contains(output, "declare ptr @memset(ptr, i64, i64)") {
		t.Fatalf("expected runtime memset declaration to lower to an int-sized second argument, got:\n%s", output)
	}
	fillBody := functionIR(output, "fill_runtime_byte")
	if fillBody == "" {
		t.Fatalf("expected to find fill_runtime_byte body, got:\n%s", output)
	}
	if strings.Contains(fillBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_runtime_byte to use direct byte stores on the tiny exact fast path, got:\n%s", fillBody)
	}
	if strings.Count(fillBody, "store i8 %value") < 2 {
		t.Fatalf("expected fill_runtime_byte to lower through direct runtime-value byte stores, got:\n%s", fillBody)
	}

	outputPath := filepath.Join(t.TempDir(), "fill_runtime_byte.o")
	if err := backend.WriteLLVMObjectFileWithOpt(result, outputPath, backend.OptimizationLevel3); err != nil {
		t.Fatalf("WriteLLVMObjectFileWithOpt returned error: %v", err)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("expected optimized object file at %s: %v", outputPath, err)
	}
}
func TestGenerateOptimizedLLVMObjectFileSkipsRedundantPackedZeroPayloadStores(t *testing.T) {
	src := `struct Payload:
	data: mutable u8&?
	len: mutable i32

packed enum Node:
	Empty(Payload)
	Byte(u8)

def build() -> Node:
	region scratch(256)
	store: Node.Store[Local] = Node.Store(scratch)
	return new[store] Node.Empty(zeroed)
`
	result := parseAndAnalyze(t, "backend_packed_zero_payload_object.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	buildBody := functionIR(output, "build")
	if buildBody == "" {
		t.Fatalf("expected to find build body, got:\n%s", output)
	}
	if strings.Contains(buildBody, "store %Payload zeroinitializer, ptr %enum.payload.ptr") {
		t.Fatalf("expected packed zero payload constructor to avoid redundant aggregate zero stores into enum payload storage, got:\n%s", buildBody)
	}

	outputPath := filepath.Join(t.TempDir(), "packed_zero_payload.o")
	if err := backend.WriteLLVMObjectFileWithOpt(result, outputPath, backend.OptimizationLevel3); err != nil {
		t.Fatalf("WriteLLVMObjectFileWithOpt returned error: %v", err)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("expected optimized object file at %s: %v", outputPath, err)
	}
}
func TestGenerateLLVMIRSpecializesArenaDViewEqExact(t *testing.T) {
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
		return DynArrayView(values.items.cast[void&], values.count, size_of(T))
	return DynArrayView(null, 0, size_of(T))

def arena_da_eq_exact[T](left: dview[T], right: dview[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_split(values: darray[i32, 4]&) -> bool:
	base: dview[i32] = arena_da_view(values, 0, 4)
	left: dview[i32] = base[0:2]
	right: dview[i32] = base[2:4]
	return arena_da_eq_exact(left, right)

def eq_overlap(values: darray[i32, 4]&) -> bool:
	base: dview[i32] = arena_da_view(values, 0, 4)
	left: dview[i32] = base[0:3]
	right: dview[i32] = base[1:4]
	return arena_da_eq_exact(left, right)

def eq_same(values: darray[i32, 4]&) -> bool:
	base: dview[i32] = arena_da_view(values, 0, 4)
	return arena_da_eq_exact(base, base)

def eq_diff_extent(values: darray[i32, 4]&) -> bool:
	base: dview[i32] = arena_da_view(values, 0, 4)
	left: dview[i32] = base[0:1]
	right: dview[i32] = base[2:4]
	return arena_da_eq_exact(left, right)
`
	result := parseAndAnalyze(t, "backend_dview_eq_exact.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqSplitBody := functionIR(output, "eq_split")
	if eqSplitBody == "" {
		t.Fatalf("expected to find eq_split body, got:\n%s", output)
	}
	if strings.Contains(eqSplitBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_split to avoid helper fallback, got:\n%s", eqSplitBody)
	}
	requireTinyExactDViewEqBody(t, eqSplitBody, true)

	eqOverlapBody := functionIR(output, "eq_overlap")
	if eqOverlapBody == "" {
		t.Fatalf("expected to find eq_overlap body, got:\n%s", output)
	}
	if strings.Contains(eqOverlapBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_overlap to avoid helper fallback, got:\n%s", eqOverlapBody)
	}
	requireTinyExactDViewEqBody(t, eqOverlapBody, false)

	eqSameBody := functionIR(output, "eq_same")
	if eqSameBody == "" {
		t.Fatalf("expected to find eq_same body, got:\n%s", output)
	}
	if strings.Contains(eqSameBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_same to avoid helper fallback, got:\n%s", eqSameBody)
	}
	requireTinyExactDViewEqBody(t, eqSameBody, false)

	eqDiffExtentBody := functionIR(output, "eq_diff_extent")
	if eqDiffExtentBody == "" {
		t.Fatalf("expected to find eq_diff_extent body, got:\n%s", output)
	}
	if !strings.Contains(eqDiffExtentBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_diff_extent to keep helper fallback, got:\n%s", eqDiffExtentBody)
	}
	if strings.Contains(eqDiffExtentBody, "call i64 @memcmp(ptr noalias") {
		t.Fatalf("expected eq_diff_extent to avoid direct memcmp specialization, got:\n%s", eqDiffExtentBody)
	}
}
func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

@borrows_return_field(left, left, right, right)
extern wrap_views(left: view[i32], right: view[i32]) -> Views

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_struct(values: array[i32, 4]) -> bool:
	boxed: Views = Views(values[0:2], values[2:4])
	return arena_da_eq_exact(boxed.left, boxed.right)

def eq_helper(values: array[i32, 4]) -> bool:
	boxed: Views = wrap_views(values[0:2], values[2:4])
	return arena_da_eq_exact(boxed.left, boxed.right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqStructBody := functionIR(output, "eq_struct")
	if eqStructBody == "" {
		t.Fatalf("expected to find eq_struct body, got:\n%s", output)
	}
	if strings.Contains(eqStructBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_struct to avoid helper fallback, got:\n%s", eqStructBody)
	}
	requireTinyExactDViewEqBody(t, eqStructBody, true)

	eqHelperBody := functionIR(output, "eq_helper")
	if eqHelperBody == "" {
		t.Fatalf("expected to find eq_helper body, got:\n%s", output)
	}
	if strings.Contains(eqHelperBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_helper to avoid helper fallback, got:\n%s", eqHelperBody)
	}
	requireTinyExactDViewEqBody(t, eqHelperBody, true)

}
func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_indexed(values: array[i32, 4]) -> bool:
	items: array[Views, 1] = [Views(values[0:2], values[2:4])]
	return arena_da_eq_exact(items[0].left, items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqIndexedBody := functionIR(output, "eq_indexed")
	if eqIndexedBody == "" {
		t.Fatalf("expected to find eq_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_indexed to avoid helper fallback, got:\n%s", eqIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqIndexedBody, true)
}
func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewHolder:
	items: array[Views, 1]

@borrows_return_field(items[0].left, left, items[0].right, right)
extern wrap_indexed_views(left: view[i32], right: view[i32]) -> ViewHolder

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_helper_indexed(values: array[i32, 4]) -> bool:
	wrapped: ViewHolder = wrap_indexed_views(values[0:2], values[2:4])
	return arena_da_eq_exact(wrapped.items[0].left, wrapped.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_helper_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqHelperIndexedBody := functionIR(output, "eq_helper_indexed")
	if eqHelperIndexedBody == "" {
		t.Fatalf("expected to find eq_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqHelperIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_helper_indexed to avoid helper fallback, got:\n%s", eqHelperIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqHelperIndexedBody, true)
}
func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughNestedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewHolder:
	items: array[Views, 1]

struct NestedHolder:
	holder: ViewHolder

@borrows_return_field(holder.items[0].left, left, holder.items[0].right, right)
extern wrap_nested_indexed_views(left: view[i32], right: view[i32]) -> NestedHolder

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_nested_helper_indexed(values: array[i32, 4]) -> bool:
	wrapped: NestedHolder = wrap_nested_indexed_views(values[0:2], values[2:4])
	return arena_da_eq_exact(wrapped.holder.items[0].left, wrapped.holder.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_nested_helper_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqNestedHelperIndexedBody := functionIR(output, "eq_nested_helper_indexed")
	if eqNestedHelperIndexedBody == "" {
		t.Fatalf("expected to find eq_nested_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqNestedHelperIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_nested_helper_indexed to avoid helper fallback, got:\n%s", eqNestedHelperIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqNestedHelperIndexedBody, true)
}
func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewWindow:
	items: view[Views]

@borrows_return_field_rebased(items, src)
extern wrap_sub(src: view[Views], start: usize, end: usize) -> ViewWindow

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_rebased_helper_indexed(values: array[i32, 4]) -> bool:
	items: array[Views, 2] = [Views(values[0:1], values[1:2]), Views(values[2:3], values[3:4])]
	wrapped: ViewWindow = wrap_sub(items[1:2], 0, 1)
	return arena_da_eq_exact(wrapped.items[0].left, wrapped.items[0].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_rebased_helper_indexed_field_projection.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqRebasedHelperIndexedBody := functionIR(output, "eq_rebased_helper_indexed")
	if eqRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find eq_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqRebasedHelperIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_rebased_helper_indexed to avoid helper fallback, got:\n%s", eqRebasedHelperIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqRebasedHelperIndexedBody, true)
}
