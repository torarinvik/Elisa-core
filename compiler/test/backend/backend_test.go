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
		"%DynArrayView = type { ptr, i64 }",
		"%CtxListView = type { ptr, i64 }",
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
		"ret i64 16",
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
		"%DynArrayView = type { ptr, i64 }",
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
		"%CtxList = type { ptr, i64, i64 }",
		"%CtxListView = type { ptr, i64 }",
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

func TestGenerateLLVMIRIndexesDStrViaRuntimeHelper(t *testing.T) {
	src := `def read_codepoint(text: DStr[row]) -> i64:
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
