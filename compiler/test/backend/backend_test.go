package backend_test

import (
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
global pair: Pair = Pair(1, 2)
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
