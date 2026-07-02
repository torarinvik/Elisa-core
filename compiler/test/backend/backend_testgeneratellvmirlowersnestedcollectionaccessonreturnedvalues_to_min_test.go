package backend_test

import (
	"bytes"
	"elisacore/src/backend"
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersNestedCollectionAccessOnReturnedValues(t *testing.T) {
	src := `struct DynArray[T]:
	items: mutable T&?
	count: mutable usize
	capacity: mutable usize

struct DynArrayView:
	data: mutable void&?
	len: mutable usize

extern make_array() -> darray[i32, row]
extern make_array_view() -> view[i32]

def read_array_index() -> i32:
	return make_array()[1]

def read_array_slice_index() -> i32:
	return make_array()[1:3][0]

def read_array_view_index() -> i32:
	return make_array_view()[0]
`
	result := parseAndAnalyze(t, "backend_nested_collection_access_returns.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare %DynArray__i32 @make_array()",
		"declare %DynArrayView @make_array_view()",
		"call %DynArray__i32 @make_array()",
		"call %DynArrayView @make_array_view()",
		"darrayslice.view.len.out = sub i64",
		"getelementptr i8, ptr",
		"extractvalue %DynArray__i32",
		"getelementptr i32, ptr",
		"alloca %DynArray__i32",
		"alloca %DynArrayView",
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
func TestGenerateLLVMIRLowersArrayLiteralAndInferredLocalViaFixedArrayLowering(t *testing.T) {
	src := `def head_of_middle() -> int:
	values = [1, 2, 3, 4]
	part: view[int] = values[1:3]
	return part[0]
`
	result := parseAndAnalyze(t, "backend_array_literal_inferred_local.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @head_of_middle()",
		"alloca [4 x i64]",
		"getelementptr [4 x i64], ptr",
		"insertvalue %DynArrayView",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "@rt_tlist_new") || strings.Contains(output, "@rt_tlist_push") {
		t.Fatalf("expected fixed array literals not to lower through typed-list runtime helpers, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersStringSliceSyntaxViaRuntimeHelpers(t *testing.T) {
	src := `struct StringView:
	data: mutable u8&
    len: mutable i64

def head_codepoint(text: cstr[row]) -> char:
	view: StringView = text[1:3]
    return view[0]
`
	result := parseAndAnalyze(t, "backend_string_slice_syntax.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%StringView = type { ptr, i64 }",
		"declare %StringView @ctx_string_view(ptr, i64, i64)",
		"call %StringView @ctx_string_view(ptr",
		"call i64 @ctx_string_view_index(%StringView",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRSpecializesExactStringSliceMaterialize(t *testing.T) {
	src := `extern memcpy(dest: void&?, src: void&?, n: usize) -> void&?
extern alloc_perm(size: i64) -> heap void&
extern register_perm_string_len(ptr: u8&?, len: usize)
extern intern_small_string(src: u8&, len: usize) -> heap u8&
extern ctx_strlen(value: cstr[shape_in]) -> i64
extern ctx_string_slice(value: cstr[shape_in], start: i64, end: i64) -> cstr[shape_out]

def copy_small(text: cstr[row]) -> cstr:
	return ctx_string_slice(text, 1, 3)

def copy_large(text: cstr[row]) -> cstr:
	return ctx_string_slice(text, 1, 13)

def copy_full(text: cstr[row]) -> cstr:
	return ctx_string_slice(text, 0, text.len)

def copy_unknown(text: cstr[row], start: i64, end: i64) -> cstr:
	return ctx_string_slice(text, start, end)
`
	result := parseAndAnalyze(t, "backend_exact_string_slice_materialize.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copySmallBody := functionIR(output, "copy_small")
	if copySmallBody == "" {
		t.Fatalf("expected to find copy_small body, got:\n%s", output)
	}
	if strings.Contains(copySmallBody, "call ptr @ctx_string_slice") {
		t.Fatalf("expected copy_small to avoid ctx_string_slice helper fallback, got:\n%s", copySmallBody)
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
	if strings.Contains(copyLargeBody, "call ptr @ctx_string_slice") {
		t.Fatalf("expected copy_large to avoid ctx_string_slice helper fallback, got:\n%s", copyLargeBody)
	}

	copyFullBody := functionIR(output, "copy_full")
	if copyFullBody == "" {
		t.Fatalf("expected to find copy_full body, got:\n%s", output)
	}
	for _, bad := range []string{"call ptr @ctx_string_slice", "call ptr @alloc_perm", "call ptr @intern_small_string", "call ptr @memcpy"} {
		if strings.Contains(copyFullBody, bad) {
			t.Fatalf("expected copy_full to lower as a direct return without %q, got:\n%s", bad, copyFullBody)
		}
	}
	if strings.Contains(copyFullBody, "call ") {
		t.Fatalf("expected copy_full to avoid all helper calls on the full-span fast path, got:\n%s", copyFullBody)
	}

	copyUnknownBody := functionIR(output, "copy_unknown")
	if copyUnknownBody == "" {
		t.Fatalf("expected to find copy_unknown body, got:\n%s", output)
	}
	if !strings.Contains(copyUnknownBody, "call ptr @ctx_string_slice(ptr") {
		t.Fatalf("expected copy_unknown to keep helper fallback when extent is not exact, got:\n%s", copyUnknownBody)
	}
}
func TestGenerateLLVMIRLowersStaticIfInFunctionBodies(t *testing.T) {
	src := `const ENABLE_FAST = 2 > 1

def choose() -> i32:
    static if ENABLE_FAST:
        return 7
	static else:
        return 9
`
	result := parseAndAnalyze(t, "backend_static_if.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i32 @choose()",
		"ret i32 7",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "ret i32 9") {
		t.Fatalf("expected inactive static-if branch to be omitted, got:\n%s", output)
	}
	if strings.Contains(output, "br i1") {
		t.Fatalf("expected static-if lowering not to emit a runtime conditional branch, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersForLoopRanges(t *testing.T) {
	src := `def sum(limit: int) -> int:
	total: mutable int = 0
	for i in 0..<limit:
		total <- total + i
	for j in limit..>0:
		total <- total + j
	for k in 0..4..2:
		total <- total + k
	return total
`
	result := parseAndAnalyze(t, "backend_for_loop_ranges.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	ir := functionIR(output, "sum")
	if ir == "" {
		t.Fatalf("expected to find LLVM IR for sum, got:\n%s", output)
	}
	for _, check := range []string{
		"define i64 @sum(i64",
		"for.cond",
		"for.body",
		"for.end",
		"for.next.asc",
		"for.next.desc",
		"select i1",
		"icmp slt",
		"icmp sgt",
		"icmp sle",
		"add i64",
		"sub i64",
	} {
		if !strings.Contains(ir, check) {
			t.Fatalf("expected sum IR to contain %q, got:\n%s", check, ir)
		}
	}
}
func TestGenerateLLVMIRLowersIterableForLoopDestructuring(t *testing.T) {
	src := `struct Pair:
	left: int
	right: int

def sum_pairs(items: array[Pair, 2]) -> int:
	total: mutable int = 0
	for Pair(left, right) in items:
		total <- total + left + right
	return total
`
	result := parseAndAnalyze(t, "backend_iterable_for_destructure.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	ir := functionIR(output, "sum_pairs")
	if ir == "" {
		t.Fatalf("expected to find LLVM IR for sum_pairs, got:\n%s", output)
	}
	for _, check := range []string{
		"iter.cond",
		"iter.body",
		"iter.end",
		"extractvalue %Pair",
		"add i64",
	} {
		if !strings.Contains(ir, check) {
			t.Fatalf("expected sum_pairs IR to contain %q, got:\n%s", check, ir)
		}
	}
}
func TestGenerateLLVMIRLowersIterableForLoopMutableRef(t *testing.T) {
	src := `struct Counter:
	value: mutable int

def bump() -> int:
	items: mutable array[Counter, 2] = [Counter(1), Counter(2)]
	for mutable item in items:
		item.value <- item.value + 1
	return items[0].value + items[1].value
`
	result := parseAndAnalyze(t, "backend_iterable_for_mutable_ref.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	ir := functionIR(output, "bump")
	if ir == "" {
		t.Fatalf("expected to find LLVM IR for bump, got:\n%s", output)
	}
	for _, check := range []string{
		"iter.cond",
		"iter.body",
		"iter.end",
		"iter.next",
		"getelementptr inbounds nuw %Counter",
		"store i64",
		"add i64",
	} {
		if !strings.Contains(ir, check) {
			t.Fatalf("expected bump IR to contain %q, got:\n%s", check, ir)
		}
	}
}
func TestGenerateLLVMIRLowersIterableForLoopOverDynamicString(t *testing.T) {
	src := `def checksum(text: cstr[row]) -> int:
	total: mutable int = 0
	for ch in text:
		total <- total + ch
	return total
`
	result := parseAndAnalyze(t, "backend_iterable_for_cstr.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	ir := functionIR(output, "checksum")
	if ir == "" {
		t.Fatalf("expected to find LLVM IR for checksum, got:\n%s", output)
	}
	for _, check := range []string{
		"iter.cond",
		"iter.body",
		"iter.end",
		"declare i64 @ctx_string_index",
		"add i64",
	} {
		if !strings.Contains(output, check) && !strings.Contains(ir, check) {
			t.Fatalf("expected iterable string lowering to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersIterableForLoopOverChunksExactView(t *testing.T) {
	src := `def checksum(values: darray[i32, 4]) -> i32:
	base: view[i32] = values[0:4]
	chunks: ChunksExactView[i32] = chunks_exact(readonly(base), 2)
	total: mutable i32 = 0
	for chunk in chunks:
		total <- total + chunk[0] + chunk[1]
	return total
`
	result := parseAndAnalyze(t, "backend_iterable_for_chunks_exact.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	if !strings.Contains(output, "%ChunksExactView__i32 = type { %DynArrayView, i64, i64 }") {
		t.Fatalf("expected output to declare the ChunksExactView carrier type, got:\n%s", output)
	}
	ir := functionIR(output, "checksum")
	if ir == "" {
		t.Fatalf("expected to find LLVM IR for checksum, got:\n%s", output)
	}
	for _, check := range []string{
		"iter.cond",
		"iter.body",
		"iter.end",
		"mul i64",
		"chunks.item.len.out = sub i64",
		"getelementptr i8, ptr",
	} {
		if !strings.Contains(ir, check) {
			t.Fatalf("expected checksum IR to contain %q, got:\n%s", check, ir)
		}
	}
}
func TestGenerateLLVMIRLowersProofCarryingViewHelpers(t *testing.T) {
	src := `def run(values: darray[i32, 4]) -> void:
	base: view[i32] = values[0:4]
	halves: SplitView[i32] = split_at(base, 2)
	left: view[i32] = halves.left
	chunks: ChunksExactView[i32] = chunks_exact(readonly(base), 2)
	first: view[i32] = chunks[0]
	_ = left
	_ = first
`
	result := parseAndAnalyze(t, "backend_proof_carrying_view_helpers.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%SplitView__i32 = type { %DynArrayView, %DynArrayView }",
		"%ChunksExactView__i32 = type { %DynArrayView, i64, i64 }",
		"define void @run(%DynArray__i32",
		"split.left.len.out = sub i64",
		"chunks.item.len.out = sub i64",
		"urem i64",
		"call void @llvm.trap()",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersReduceSumHelper(t *testing.T) {
	src := `def add_bias(value: i64, bias: i64) -> i64:
	return value + bias

def run(values: darray[i64, 4], bias: i64) -> i64:
	base: view[i64] = values[0:4]
	return reduce_sum(readonly(base), add_bias, bias)
`
	result := parseAndAnalyze(t, "backend_reduce_sum_helper.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	ir := functionIR(output, "run")
	if ir == "" {
		t.Fatalf("expected to find LLVM IR for run, got:\n%s", output)
	}
	for _, check := range []string{
		"define i64 @run(%DynArray__i64",
		"reduce_sum.cond",
		"reduce_sum.body",
		"reduce_sum.end",
		"call i64 @add_bias",
		"add i64",
	} {
		if !strings.Contains(ir, check) {
			t.Fatalf("expected run IR to contain %q, got:\n%s", check, ir)
		}
	}
}
func looksLikeObjectFile(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	magics := [][]byte{
		{0x7f, 'E', 'L', 'F'},
		{0xcf, 0xfa, 0xed, 0xfe},
		{0xce, 0xfa, 0xed, 0xfe},
		{0xfe, 0xed, 0xfa, 0xcf},
		{0xfe, 0xed, 0xfa, 0xce},
	}
	for _, magic := range magics {
		if bytes.Equal(data[:4], magic) {
			return true
		}
	}
	return false
}
func looksLikeBitcodeFile(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return bytes.HasPrefix(data, []byte{'B', 'C'}) || bytes.Equal(data[:4], []byte{0xde, 0xc0, 0x17, 0x0b})
}
func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
