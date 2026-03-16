package backend_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"llcontext/src/backend"
	"llcontext/src/lexer"
	"llcontext/src/parser"
	"llcontext/src/semantic"
)

func parseAndAnalyze(t *testing.T, filename string, src string) *semantic.Result {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parse errors:\n%s", strings.Join(errs, "\n"))
	}
	result := semantic.Analyze(file)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("semantic errors:\n%s", strings.Join(errs, "\n"))
	}
	return result
}

func TestGenerateLLVMIRDefinesSimpleFunctionBody(t *testing.T) {
	src := `repr(c) struct Box:
    value: i32

extern alloc_box() -> Box&
extern take_box(box: Box) -> void
extern errno_value: i32

def read_box(box: Box&) -> i32:
    return box.value
`
	result := parseAndAnalyze(t, "backend_box.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Box = type { i32 }",
		"@errno_value = external global i32",
		"declare ptr @alloc_box()",
		"declare void @take_box(%Box)",
		"define i32 @read_box(ptr",
		"getelementptr inbounds",
		"%Box, ptr",
		"ret i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersLocalsCallsAndControlFlow(t *testing.T) {
	src := `extern add_one(value: i32) -> i32

def countdown(start: i32) -> i32:
    value: mutable i32 = start
    total: mutable i32 = 0
    while value > 0:
        if value == 2:
            total <- add_one(total)
        else:
            total <- total + 1
        value <- value - 1
    return total
`
	result := parseAndAnalyze(t, "backend_control_flow.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i32 @add_one(i32)",
		"define i32 @countdown(i32",
		"call i32 @add_one(i32",
		"icmp sgt i32",
		"icmp eq i32",
		"br i1",
		"store i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersVoidReturnCalls(t *testing.T) {
	src := `extern touch(value: i32)

def call_touch(value: i32) -> i32:
	touch(value)
	return value
`
	result := parseAndAnalyze(t, "backend_void_call.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare void @touch(i32)",
		"define i32 @call_touch(i32",
		"call void @touch(i32",
		"ret i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPanicViaTrapCall(t *testing.T) {
	src := `def fail() -> void:
	panic("boom")
`
	result := parseAndAnalyze(t, "backend_panic_stmt.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare void @llvm.trap()",
		"define void @fail()",
		"call void @llvm.trap()",
		"unreachable",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersBuiltinStringAndSViewSyntax(t *testing.T) {
	src := `def first_char(text: str[4]) -> char:
	return text[1]

def first_code(text: str[4]) -> i64:
	return text[1].i64()

def slice_text(text: str[4]) -> sview[1, 3]:
    return text[1:3]

def view_char(text: sview[0, 4]) -> char:
    return text[1]
`
	result := parseAndAnalyze(t, "backend_builtin_string_surface.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%CtxStringView = type { ptr, i64 }",
		"define i64 @first_char([4 x i8]",
		"define i64 @first_code([4 x i8]",
		"zext i8",
		"define %CtxStringView @slice_text([4 x i8]",
		"insertvalue %CtxStringView",
		"define i64 @view_char(%CtxStringView",
		"declare i64 @ctx_stage1rt_string_view_index(%CtxStringView, i64)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "@ctx_stage1rt_string_view(ptr") {
		t.Fatalf("expected fixed string slice lowering to avoid runtime string view helper, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersStandaloneCharValues(t *testing.T) {
	src := `def normalize(code: i64) -> char:
	ch: char = code.char()
	if ch == 0.char():
		return 65.char()
	return ch

def bump(ch: char) -> i64:
	return (ch + 1).i64()
`
	result := parseAndAnalyze(t, "backend_standalone_char_values.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @normalize(i64",
		"icmp eq i64",
		"ret i64 65",
		"define i64 @bump(i64",
		"add i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersReferenceComparisons(t *testing.T) {
	src := `repr(c) struct Box:
    value: i32

extern maybe_box() -> Box&?

def is_missing() -> bool:
    return maybe_box() == null

def is_present() -> bool:
    return maybe_box() != null

def same_box(left: Box&, right: Box&) -> bool:
    return left == right
`
	result := parseAndAnalyze(t, "backend_reference_comparisons.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare ptr @maybe_box()",
		"define i1 @is_missing()",
		"define i1 @is_present()",
		"define i1 @same_box(ptr",
		"icmp eq ptr",
		"icmp ne ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRTernaryUsesPhi(t *testing.T) {
	src := `def choose(flag: bool, left: i32, right: i32) -> i32:
    return left if flag else right
`
	result := parseAndAnalyze(t, "backend_ternary.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i32 @choose(i1",
		"phi i32",
		"br i1",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRDefinesGlobalsWithInitializers(t *testing.T) {
	src := `repr(c) struct Pair:
    left: i32
    right: i32

const ANSWER = 42

global seed: i32 = ANSWER
global offset: i32 = ANSWER + 8
global choice: i32 = 7 if ANSWER > 0 else 9
global negated: i32 = -(ANSWER / 21)
global pair: Pair = Pair(ANSWER - 41, 1 + 1)
global flags: i32[4] = zeroed
`
	result := parseAndAnalyze(t, "backend_globals.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Pair = type { i32, i32 }",
		"@seed = global i32 42",
		"@offset = global i32 50",
		"@choice = global i32 7",
		"@negated = global i32 -2",
		"@pair = global %Pair { i32 1, i32 2 }",
		"@flags = global [4 x i32] zeroinitializer",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRSpecializesGenericFunctions(t *testing.T) {
	src := `def identity[T](value: T) -> T:
    return value

def use_identity(value: i32) -> i32:
    return identity(value)
`
	result := parseAndAnalyze(t, "backend_generic_specialization.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i32 @use_identity(i32",
		"define i32 @identity__i32(i32",
		"call i32 @identity__i32(i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersExportWrappers(t *testing.T) {
	src := `repr(c) struct Vec[T]:
	x: mutable T
	y: mutable T

export type Vec[i32] as Vec2i

def vec_add_i32(left: Vec[i32], right: Vec[i32]) -> Vec[i32]:
	result: Vec[i32] = zeroed
	result.x <- left.x + right.x
	result.y <- left.y + right.y
	return result

def keep_left[T](left: T, right: T) -> T:
	return left

export func vec2i_add(left: Vec2i, right: Vec2i) -> Vec2i = vec_add_i32
export func vec2i_keep_left(left: Vec2i, right: Vec2i) -> Vec2i = keep_left[Vec[i32]]
`
	result := parseAndAnalyze(t, "backend_export_wrappers.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Vec__i32 = type { i32, i32 }",
		"define %Vec__i32 @vec_add_i32(%Vec__i32",
		"define %Vec__i32 @keep_left__Vec_i32(%Vec__i32",
		"define i64 @vec2i_add(i64",
		"define i64 @vec2i_keep_left(i64",
		"call %Vec__i32 @vec_add_i32(%Vec__i32",
		"call %Vec__i32 @keep_left__Vec_i32(%Vec__i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersVariadicExternCalls(t *testing.T) {
	src := `extern snprintf(buffer: u8&?, buffer_size: usize, format: u8&, ...) -> int

def format_len(format: u8&) -> int:
	return snprintf(null, 0u, format, 7, 9)
`
	result := parseAndAnalyze(t, "backend_variadic_call.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i64 @snprintf(ptr, i64, ptr, ...)",
		"define i64 @format_len(ptr",
		"call i64 (ptr, i64, ptr, ...) @snprintf(",
		"ptr null, i64 0",
		"i64 7, i64 9)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPointerIntegerCasts(t *testing.T) {
	src := `def ptr_bits(ptr: u8&) -> uintptr:
	return ptr.uintptr()

def bits_ptr(bits: uintptr) -> u8&:
	return bits.u8&()
`
	result := parseAndAnalyze(t, "backend_pointer_integer_casts.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @ptr_bits(ptr",
		"ptrtoint ptr",
		"define ptr @bits_ptr(i64",
		"inttoptr i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersRawListRuntimeBridgeCalls(t *testing.T) {
	src := `repr(c) struct CtxList:
	len: mutable i64
	cap: mutable i64
	elem_size: mutable i64
	data: mutable void&&?
	inline_boxes: mutable u8&?
	inline_box_stride: mutable i64

extern ctx_stage0_list_len(list: CtxList&?) -> i64

def raw_list_len(values: DArray[void&, row]) -> i64:
	return ctx_stage0_list_len(values)
`
	result := parseAndAnalyze(t, "backend_raw_list_bridge.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i64 @ctx_stage0_list_len(ptr)",
		"define i64 @raw_list_len(ptr",
		"call i64 @ctx_stage0_list_len(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersNestedFieldAccessOnReturnedStructValues(t *testing.T) {
	src := `repr(c) struct Inner:
	value: i32

repr(c) struct Outer:
	inner: Inner

extern make_outer() -> Outer

def read_nested_return() -> i32:
	return make_outer().inner.value
`
	result := parseAndAnalyze(t, "backend_nested_return_field_chain.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Inner = type { i32 }",
		"%Outer = type { %Inner }",
		"declare %Outer @make_outer()",
		"call %Outer @make_outer()",
		"extractvalue %Outer",
		"ret i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowerRuntimeBackedTypes(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
    items: mutable T&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct DynArrayView:
    items: mutable void&?
    count: mutable usize

repr(c) struct CtxList:
    items: mutable void&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct CtxListView:
    items: mutable void&?
    count: mutable usize

extern take_array(values: DArray[i32, row]) -> void
extern take_array_view(view: DArrayView[i32]) -> usize
extern take_list(values: DList[i32, row]) -> void
extern take_list_view(view: DListView[i32]) -> usize
extern take_str(text: DStr[row]) -> void
`
	result := parseAndAnalyze(t, "backend_runtime_types.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArray__i32 = type { ptr, i64, i64 }",
		"%DynArrayView = type { ptr, i64, i64 }",
		"%CtxListView = type { ptr, i64, i64 }",
		"declare void @take_array(%DynArray__i32)",
		"declare i64 @take_array_view(%DynArrayView)",
		"declare void @take_list(ptr)",
		"declare i64 @take_list_view(%CtxListView)",
		"declare void @take_str(ptr)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestWriteLLVMBitcodeFile(t *testing.T) {
	src := `def increment(value: i32) -> i32:
    return value + 1
`
	result := parseAndAnalyze(t, "backend_bitcode.llcontext", src)
	outputPath := filepath.Join(t.TempDir(), "module.bc")

	if err := backend.WriteLLVMBitcodeFile(result, outputPath); err != nil {
		t.Fatalf("WriteLLVMBitcodeFile returned error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("expected bitcode file to exist: %v", err)
	}
	if len(data) < 4 {
		t.Fatalf("expected non-empty bitcode output, got %d bytes", len(data))
	}
	if !bytes.HasPrefix(data, []byte{'B', 'C'}) {
		t.Fatalf("expected bitcode magic prefix, got % x", data[:min(len(data), 4)])
	}
}

func TestWriteLLVMObjectFile(t *testing.T) {
	src := `def increment(value: i32) -> i32:
    return value + 1
`
	result := parseAndAnalyze(t, "backend_object.llcontext", src)
	outputPath := filepath.Join(t.TempDir(), "module.o")

	if err := backend.WriteLLVMObjectFile(result, outputPath); err != nil {
		t.Fatalf("WriteLLVMObjectFile returned error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("expected object file to exist: %v", err)
	}
	if len(data) < 4 {
		t.Fatalf("expected non-empty object output, got %d bytes", len(data))
	}
	if !looksLikeObjectFile(data) {
		t.Fatalf("expected native object file magic, got % x", data[:min(len(data), 4)])
	}
}

func TestGenerateLLVMIRUsesABISizeofForPaddedStructs(t *testing.T) {
	src := `repr(c) struct Padded:
    tag: i8
    value: i32

def padded_size() -> usize:
    return sizeof(Padded)

def array_view_size() -> usize:
    return sizeof(DArrayView[i32])
`
	result := parseAndAnalyze(t, "backend_sizeof.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"target datalayout =",
		"target triple =",
		"define i64 @padded_size()",
		"ret i64 8",
		"define i64 @array_view_size()",
		"ret i64 24",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersModuloAndModuloAssign(t *testing.T) {
	src := `global folded_mod: i32 = 20 % 6

def rem_signed(left: i32, right: i32) -> i32:
    return left % right

def rem_unsigned() -> u32:
    value: mutable u32 = 10u32
    value %= 4u32
    return value
`
	result := parseAndAnalyze(t, "backend_modulo.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"@folded_mod = global i32 2",
		"define i32 @rem_signed(i32",
		"srem i32",
		"define i32 @rem_unsigned()",
		"urem i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "urem i32") < 1 {
		t.Fatalf("expected modulo assignment to lower via urem, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPointerArithmetic(t *testing.T) {
	src := `def advance(ptr: u8&, offset: usize) -> u8&:
    return ptr + offset

def advance_commutative(offset: usize, ptr: u8&) -> u8&:
    return offset + ptr

def rewind(ptr: u8&, offset: usize) -> u8&:
    return ptr - offset
`
	result := parseAndAnalyze(t, "backend_pointer_arithmetic.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define ptr @advance(ptr",
		"define ptr @advance_commutative(i64",
		"define ptr @rewind(ptr",
		"getelementptr i8, ptr",
		"sub i64 0,",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "getelementptr i8, ptr") < 3 {
		t.Fatalf("expected pointer arithmetic to lower via GEP in all functions, got:\n%s", output)
	}
}

func TestGenerateLLVMIRIndexesRuntimeBackedArraysAndViews(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
    items: mutable T&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct DynArrayView:
    items: mutable void&?
    count: mutable usize

def read_array(values: DArray[i32, row]) -> i32:
    return values[1]

def read_view(view: DArrayView[i32]) -> i32:
    return view[2]
`
	result := parseAndAnalyze(t, "backend_runtime_index.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArray__i32 = type { ptr, i64, i64 }",
		"%DynArrayView = type { ptr, i64, i64 }",
		"define i32 @read_array(%DynArray__i32",
		"define i32 @read_view(%DynArrayView",
		"getelementptr inbounds nuw %DynArray__i32",
		"getelementptr inbounds nuw %DynArrayView",
		"getelementptr i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRIndexesRuntimeBackedListsAndViews(t *testing.T) {
	src := `repr(c) struct CtxList:
    items: mutable void&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct CtxListView:
    items: mutable void&?
    count: mutable usize

def read_list(values: DList[i32, row]) -> i32:
    return values[1]

def read_view(view: DListView[i32]) -> i32:
    return view[2]
`
	result := parseAndAnalyze(t, "backend_runtime_list_index.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%CtxListView = type { ptr, i64, i64 }",
		"define i32 @read_list(ptr",
		"define i32 @read_view(%CtxListView",
		"getelementptr i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRIndexesRuntimeBackedListRefs(t *testing.T) {
	src := `repr(c) struct CtxList:
    items: mutable void&?
    count: mutable usize
    capacity: mutable usize

def read_list_ref(values: DList[i32, row]&) -> i32:
    return values[1]
`
	result := parseAndAnalyze(t, "backend_runtime_list_ref_index.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i32 @read_list_ref(ptr",
		"getelementptr i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRIndexesDStrViaRuntimeHelper(t *testing.T) {
	src := `def read_codepoint(text: DStr[row]) -> char:
    return text[1]
`
	result := parseAndAnalyze(t, "backend_runtime_dstr_index.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @read_codepoint(ptr",
		"declare i64 @ctx_stage1rt_string_index(ptr, i64)",
		"call i64 @ctx_stage1rt_string_index(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRIndexesCtxStringViewViaRuntimeHelper(t *testing.T) {
	src := `def read_view(view: CtxStringView) -> char:
    return view[1]
`
	result := parseAndAnalyze(t, "backend_runtime_string_view_index.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%CtxStringView = type { ptr, i64 }",
		"define i64 @read_view(%CtxStringView",
		"declare i64 @ctx_stage1rt_string_view_index(%CtxStringView, i64)",
		"call i64 @ctx_stage1rt_string_view_index(%CtxStringView",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersRuntimeStringEqualityHelpers(t *testing.T) {
	src := `def same_text(left: DStr[row], right: DStr[col]) -> bool:
    return left == right

def same_view_text(view: CtxStringView, text: DStr[row]) -> bool:
    return view == text

def same_text_view(text: DStr[row], view: CtxStringView) -> bool:
    return text == view

def different_views(left: CtxStringView, right: CtxStringView) -> bool:
    return left != right
`
	result := parseAndAnalyze(t, "backend_runtime_string_equality.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i64 @ctx_stage1rt_streq(ptr, ptr)",
		"declare i64 @ctx_stage1rt_string_view_eq(%CtxStringView, ptr)",
		"declare i64 @ctx_stage1rt_string_views_eq(%CtxStringView, %CtxStringView)",
		"call i64 @ctx_stage1rt_streq(ptr",
		"call i64 @ctx_stage1rt_string_view_eq(%CtxStringView",
		"call i64 @ctx_stage1rt_string_views_eq(%CtxStringView",
		"icmp ne i64",
		"icmp eq i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersDStrLenFieldViaRuntimeHelper(t *testing.T) {
	src := `def text_len(text: DStr[row]) -> i64:
    return text.len
`
	result := parseAndAnalyze(t, "backend_dstr_len.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @text_len(ptr",
		"declare i64 @ctx_stage1rt_strlen(ptr)",
		"call i64 @ctx_stage1rt_strlen(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersListSliceSyntaxViaRuntimeHelpers(t *testing.T) {
	src := `def head_of_middle(values: DList[i32, row]) -> i32:
	part: DListView[i32] = values[1:3]
    return part[0]
`
	result := parseAndAnalyze(t, "backend_list_slice_syntax.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i32 @head_of_middle(ptr",
		"declare %CtxListView @ctx_stage1rt_tlist_view(ptr, i64, i64)",
		"call %CtxListView @ctx_stage1rt_tlist_view(ptr",
		"getelementptr i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersArraySliceSyntaxViaRuntimeHelpers(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
    items: mutable T&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct DynArrayView:
    data: mutable void&?
    len: mutable usize
    elem_size: mutable usize

def head_owned(values: DArray[i32, row]) -> i32:
    part: DArrayView[i32] = values[1u:3u]
    return part[0u]

def head_view(view: DArrayView[i32]) -> i32:
    part: DArrayView[i32] = view[0u:1u]
    return part[0u]
`
	result := parseAndAnalyze(t, "backend_array_slice_syntax.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArray__i32 = type { ptr, i64, i64 }",
		"%DynArrayView = type { ptr, i64, i64 }",
		"define i32 @head_owned(%DynArray__i32",
		"define i32 @head_view(%DynArrayView",
		"declare %DynArrayView @arena_da_view(ptr, i64, i64)",
		"declare %DynArrayView @arena_da_view_slice(%DynArrayView, i64, i64)",
		"call %DynArrayView @arena_da_view(ptr",
		"call %DynArrayView @arena_da_view_slice(%DynArrayView",
		"getelementptr i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersFixedArraySliceSyntaxWithoutRuntimeHelpers(t *testing.T) {
	src := `repr(c) struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

def slice_owned(values: i32[4]) -> DArrayView[i32]:
	return values[1u:3u]

def head_ref(values: i32[4]&) -> i32:
	return values[1u:3u][0u]
`
	result := parseAndAnalyze(t, "backend_fixed_array_slice.llcontext", src)
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

func TestGenerateLLVMIRLowersNestedCollectionAccessOnReturnedValues(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
    items: mutable T&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct DynArrayView:
    data: mutable void&?
    len: mutable usize
    elem_size: mutable usize

repr(c) struct CtxListView:
    items: mutable void&?
    count: mutable usize

extern make_array() -> DArray[i32, row]
extern make_array_view() -> DArrayView[i32]
extern make_list_view() -> DListView[i32]

def read_array_index() -> i32:
    return make_array()[1u]

def read_array_slice_index() -> i32:
    return make_array()[1u:3u][0u]

def read_array_view_index() -> i32:
    return make_array_view()[0u]

def read_list_view_index() -> i32:
    return make_list_view()[0]
`
	result := parseAndAnalyze(t, "backend_nested_collection_access_returns.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare %DynArray__i32 @make_array()",
		"declare %DynArrayView @make_array_view()",
		"declare %CtxListView @make_list_view()",
		"call %DynArray__i32 @make_array()",
		"call %DynArrayView @make_array_view()",
		"call %CtxListView @make_list_view()",
		"call %DynArrayView @arena_da_view(ptr",
		"getelementptr i32, ptr",
		"alloca %DynArray__i32",
		"alloca %DynArrayView",
		"alloca %CtxListView",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersArrayLiteralAndInferredLocalViaFixedArrayLowering(t *testing.T) {
	src := `def head_of_middle() -> int:
	values = [1, 2, 3, 4]
	part: view[int] = values[1:3]
	return part[0]
`
	result := parseAndAnalyze(t, "backend_array_literal_inferred_local.llcontext", src)
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
	if strings.Contains(output, "@ctx_stage1rt_tlist_new") || strings.Contains(output, "@ctx_stage1rt_tlist_push") {
		t.Fatalf("expected fixed array literals not to lower through typed-list runtime helpers, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersStringSliceSyntaxViaRuntimeHelpers(t *testing.T) {
	src := `repr(c) struct CtxStringView:
    data: mutable u8&
    len: mutable i64

def head_codepoint(text: DStr[row]) -> char:
    view: CtxStringView = text[1:3]
    return view[0]
`
	result := parseAndAnalyze(t, "backend_string_slice_syntax.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%CtxStringView = type { ptr, i64 }",
		"declare %CtxStringView @ctx_stage1rt_string_view(ptr, i64, i64)",
		"call %CtxStringView @ctx_stage1rt_string_view(ptr",
		"call i64 @ctx_stage1rt_string_view_index(%CtxStringView",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
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
	result := parseAndAnalyze(t, "backend_static_if.llcontext", src)
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

func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
