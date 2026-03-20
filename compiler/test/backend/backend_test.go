package backend_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
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
	if errs := l.Errors(); len(errs) > 0 {
		t.Fatalf("lexer errors:\n%s", strings.Join(errs, "\n"))
	}
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

func loadFixtureSource(t *testing.T, relParts ...string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine test file path")
	}
	parts := append([]string{filepath.Dir(thisFile), "..", "..", ".."}, relParts...)
	path := filepath.Join(parts...)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	return string(raw)
}

func functionIR(output string, name string) string {
	marker := "@" + name + "("
	idx := strings.Index(output, marker)
	if idx < 0 {
		return ""
	}
	start := strings.LastIndex(output[:idx], "define ")
	if start < 0 {
		start = idx
	}
	rest := output[idx:]
	endOffset := strings.Index(rest, "\ndefine ")
	if endOffset < 0 {
		return output[start:]
	}
	return output[start : idx+endOffset]
}

func TestGenerateLLVMIRDefinesSimpleFunctionBody(t *testing.T) {
	src := `repr(c) struct Box:
    value: i32

extern alloc_box() -> any Box&
extern take_box(box: Box) -> void
extern errno_value: i32

def read_box(box: any Box&) -> i32:
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

func TestGenerateLLVMIRLowersNestedStructLiterals(t *testing.T) {
	src := `repr(c) struct ScratchPair:
	left: mutable int
	right: mutable int


repr(c) struct ScratchHolder:
	pair: mutable ScratchPair


def make_holder() -> ScratchHolder:
		return ScratchHolder(ScratchPair(8, 9))
`
	result := parseAndAnalyze(t, "backend_nested_struct_literals.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ScratchPair = type { i64, i64 }",
		"%ScratchHolder = type { %ScratchPair }",
		"define %ScratchHolder @make_holder()",
		"ret %ScratchHolder { %ScratchPair { i64 8, i64 9 } }",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersMoveAsStructDestructure(t *testing.T) {
	src := `repr(c) struct Pair:
    left: mutable i64
    right: mutable i64

def sum(pair: Pair) -> i64:
    move pair as Pair(left, right)
    return left + right
`
	result := parseAndAnalyze(t, "backend_move_as_struct.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Pair = type { i64, i64 }",
		"define i64 @sum(%Pair",
		"extractvalue %Pair",
		"store i64",
		"load i64",
		"add i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPackedMoveAsVariantDestructure(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def left(node: Expr, store: Expr.Store[Local]) -> Expr:
	move node in store as Expr.Add(lhs, rhs)
	_ = rhs
	return lhs
`
	result := parseAndAnalyze(t, "backend_move_as_packed_variant.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Expr__Store = type { ptr, i64, ptr }",
		"%Expr = type { i32, [2 x i64] }",
		"define ptr @left(ptr",
		"load i32, ptr",
		"call void @llvm.trap()",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "extractvalue %Expr,") {
		t.Fatalf("expected packed move-as destructure to decode through store-backed loads, got:\n%s", output)
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

func TestGenerateLLVMIRLowersHigherOrderFunctionCalls(t *testing.T) {
	src := `def apply_twice(fn: func(i64) -> i64, value: i64) -> i64:
    return fn(fn(value))

def inc(value: i64) -> i64:
    return value + 1

def run() -> i64:
    return apply_twice(inc, 40)
`
	result := parseAndAnalyze(t, "backend_higher_order_call.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @apply_twice(ptr",
		"call i64 %",
		"define i64 @inc(i64",
		"define i64 @run()",
		"call i64 @apply_twice(ptr @inc, i64 40)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "cannot call non-function") {
		t.Fatalf("expected function-typed parameter lowering, got:\n%s", output)
	}
	if count := strings.Count(output, "call i64 %"); count < 2 {
		t.Fatalf("expected at least two indirect calls through the function parameter, got %d:\n%s", count, output)
	}
}

func TestGenerateLLVMIRLowersFunctionValueErasureCasts(t *testing.T) {
	src := `def inc(value: i64) -> i64:
    return value + 1

def call_bits(bits: uintptr, value: i64) -> i64:
    fn: func(i64) -> i64 = bits.cast[func(i64) -> i64]()
    return fn(value)

def run() -> i64:
    bits: uintptr = inc.cast[uintptr]()
    return call_bits(bits, 41)
`
	result := parseAndAnalyze(t, "backend_function_value_erasure_casts.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @call_bits(i64",
		"inttoptr i64",
		"call i64 %",
		"ptrtoint (ptr @inc to i64)",
		"call i64 @call_bits(i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersExplicitGenericFunctionSpecializationValues(t *testing.T) {
	src := `def id[T](value: T) -> T:
    return value

def apply_i64(fn: func(i64) -> i64, value: i64) -> i64:
    return fn(value)

def run() -> i64:
    fn: func(i64) -> i64 = id.specialize[i64]()
    return apply_i64(fn, 7)
`
	result := parseAndAnalyze(t, "backend_explicit_generic_function_specialization.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @id__i64(i64",
		"define i64 @apply_i64(ptr",
		"store ptr @id__i64",
		"call i64 @apply_i64(ptr",
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
		"%StringView = type { ptr, i64 }",
		"define i64 @first_char([4 x i8]",
		"define i64 @first_code([4 x i8]",
		"zext i8",
		"define %StringView @slice_text([4 x i8]",
		"insertvalue %StringView",
		"define i64 @view_char(%StringView",
		"declare i64 @ctx_string_view_index(%StringView, i64)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "@ctx_string_view(ptr") {
		t.Fatalf("expected fixed string slice lowering to avoid runtime string view helper, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersEscapedStringLiteralBytes(t *testing.T) {
	src := `def newline_text() -> any u8&:
	return "line\nbreak".cast[any u8&]()

def quoted_text() -> any u8&:
	return "quote: \" slash: \\ hex: \x41".cast[any u8&]()

def unicode_text() -> any u8&:
	return "\u263A".cast[any u8&]()
`
	result := parseAndAnalyze(t, "backend_string_escapes.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define ptr @newline_text()",
		"define ptr @quoted_text()",
		"define ptr @unicode_text()",
		"c\"line\\0Abreak\\00\"",
		"c\"quote: \\22 slash: \\\\ hex: A\\00\"",
		"c\"\\E2\\98\\BA\\00\"",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
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

extern maybe_box() -> any Box&?

def is_missing() -> bool:
    return maybe_box() == null

def is_present() -> bool:
    return maybe_box() != null

def same_box(left: any Box&, right: any Box&) -> bool:
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

func TestGenerateLLVMIRAllowsAggregateGlobalReferencesInInitializers(t *testing.T) {
	src := `repr(c) struct Pair:
	left: i32
	right: i32

repr(c) struct Holder:
	pair: Pair

global base: Pair = Pair(1, 2)
global table: Pair[2] = [base, Pair(3, 4)]
global picked: Pair = table[1u]
global wrapped: Holder = Holder(table[0u])
global first_left: i32 = table[0u].left
`
	result := parseAndAnalyze(t, "backend_global_aggregate_refs.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Pair = type { i32, i32 }",
		"%Holder = type { %Pair }",
		"@base = global %Pair { i32 1, i32 2 }",
		"@table = global [2 x %Pair]",
		"%Pair { i32 1, i32 2 }",
		"%Pair { i32 3, i32 4 }",
		"@picked = global %Pair { i32 3, i32 4 }",
		"@wrapped = global %Holder { %Pair { i32 1, i32 2 } }",
		"@first_left = global i32 1",
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

global seed: i32 = 7

def vec_add_i32(left: Vec[i32], right: Vec[i32]) -> Vec[i32]:
	result: Vec[i32] = zeroed
	result.x <- left.x + right.x
	result.y <- left.y + right.y
	return result

def keep_left[T](left: T, right: T) -> T:
	return left

export global seed as ctx_seed
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
		"@seed = global i32 7",
		"@ctx_seed = alias i32, ptr @seed",
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

func TestGenerateCHeaderForExportedVec2i(t *testing.T) {
	src := `repr(c) struct Vec[T]:
	x: mutable T
	y: mutable T

export type Vec[i32] as Vec2i

global seed: i32 = 7

def vec_add_i32(left: Vec[i32], right: Vec[i32]) -> Vec[i32]:
	result: Vec[i32] = zeroed
	result.x <- left.x + right.x
	result.y <- left.y + right.y
	return result

def keep_left[T](left: T, right: T) -> T:
	return left

export global seed as ctx_seed
export func vec2i_add(left: Vec2i, right: Vec2i) -> Vec2i = vec_add_i32
export func vec2i_keep_left(left: Vec2i, right: Vec2i) -> Vec2i = keep_left[Vec[i32]]
`
	result := parseAndAnalyze(t, "backend_export_header.llcontext", src)
	header, err := backend.GenerateCHeader(result)
	if err != nil {
		t.Fatalf("GenerateCHeader returned error: %v", err)
	}
	checks := []string{
		"#ifndef BACKEND_EXPORT_HEADER_H",
		"#include <stdint.h>",
		"typedef struct Vec2i Vec2i;",
		"struct Vec2i {",
		"int32_t x;",
		"int32_t y;",
		"extern int32_t ctx_seed;",
		"Vec2i vec2i_add(Vec2i arg0, Vec2i arg1);",
		"Vec2i vec2i_keep_left(Vec2i arg0, Vec2i arg1);",
	}
	for _, check := range checks {
		if !strings.Contains(header, check) {
			t.Fatalf("expected header to contain %q, got:\n%s", check, header)
		}
	}
	if strings.Contains(header, "Vec__i32 vec2i_add") {
		t.Fatalf("expected public header not to leak backend mangled aggregate names, got:\n%s", header)
	}
}

func TestGenerateCHeaderOrdersAggregateDefinitionsByValueDependencies(t *testing.T) {
	src := `repr(c) struct Node:
	value: mutable i32
	next: mutable any Node&?

repr(c) struct Wrapper:
	node: mutable Node
	next_ref: mutable any Node&?

export type Wrapper as CtxWrapper
export type Node as CtxNode

global root: Wrapper = zeroed
export global root as ctx_root
`
	result := parseAndAnalyze(t, "backend_export_header_order.llcontext", src)
	header, err := backend.GenerateCHeader(result)
	if err != nil {
		t.Fatalf("GenerateCHeader returned error: %v", err)
	}
	nodeIndex := strings.Index(header, "struct CtxNode {")
	wrapperIndex := strings.Index(header, "struct CtxWrapper {")
	if nodeIndex == -1 || wrapperIndex == -1 {
		t.Fatalf("expected both exported structs in header, got:\n%s", header)
	}
	if nodeIndex > wrapperIndex {
		t.Fatalf("expected Node definition before Wrapper definition, got:\n%s", header)
	}
	if !strings.Contains(header, "CtxNode *next;") {
		t.Fatalf("expected pointer field to use forward-declared public name, got:\n%s", header)
	}
	if !strings.Contains(header, "extern CtxWrapper ctx_root;") {
		t.Fatalf("expected exported global declaration, got:\n%s", header)
	}
}

func TestGenerateLLVMIRLowersVariadicExternCalls(t *testing.T) {
	src := `extern snprintf(buffer: any u8&?, buffer_size: usize, format: any u8&, ...) -> int

def format_len(format: any u8&) -> int:
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
	src := `def ptr_bits(ptr: any u8&) -> uintptr:
	return ptr.uintptr()

def bits_ptr(bits: uintptr) -> any u8&:
	return bits.cast[any u8&]()
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
	items: mutable any T&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct DynArrayView:
	items: mutable any void&?
    count: mutable usize

extern take_array(values: darray[i32, row]) -> void
extern take_array_view(view: dview[i32]) -> usize
extern take_str(text: dstr[row]) -> void
`
	result := parseAndAnalyze(t, "backend_runtime_types.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArray__i32 = type { ptr, i64, i64 }",
		"%DynArrayView = type { ptr, i64, i64 }",
		"declare void @take_array(%DynArray__i32)",
		"declare i64 @take_array_view(%DynArrayView)",
		"declare void @take_str(ptr)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRAcceptsShapeErasingDArrayShorthand(t *testing.T) {
	src := `def keep(values: darray[i32]) -> darray[i32]:
    return values

def erase(values: darray[i32, row]) -> darray[i32]:
    return values
`
	result := parseAndAnalyze(t, "backend_darray_shorthand.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArray__i32 = type { ptr, i64, i64 }",
		"define %DynArray__i32 @keep(%DynArray__i32",
		"define %DynArray__i32 @erase(%DynArray__i32",
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
	if !looksLikeBitcodeFile(data) {
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
	return sizeof(view[i32])
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
	src := `def advance(ptr: any u8&, offset: usize) -> any u8&:
    return ptr + offset

def advance_commutative(offset: usize, ptr: any u8&) -> any u8&:
    return offset + ptr

def rewind(ptr: any u8&, offset: usize) -> any u8&:
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

func TestGenerateLLVMIRLowersManualRegions(t *testing.T) {
	src := `def region_value(seed: i32) -> i32:
	region scratch(1024u)
	value: any i32& = new[scratch] seed + 1
	return value[0u]
`
	result := parseAndAnalyze(t, "backend_manual_regions.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Arena = type { ptr, ptr, i64 }",
		"declare ptr @new_region(i64)",
		"declare ptr @arena_alloc(ptr, i64)",
		"declare void @arena_free(ptr)",
		"call ptr @new_region(i64 1024)",
		"call ptr @arena_alloc(ptr",
		"call void @arena_free(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersRegionCheckpoints(t *testing.T) {
	src := `def region_value(seed: i32) -> i32:
	region scratch(1024u)
	mark scratch as cp
	temp: any i32& = new[scratch] seed + 1
	restore scratch from cp
	reused: any i32& = new[scratch] seed + 2
	value: i32 = reused[0u]
	reset scratch
	final: any i32& = new[scratch] seed + 3
	return value + final[0u]
`
	result := parseAndAnalyze(t, "backend_region_checkpoints.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ArenaMark = type { ptr, i64 }",
		"declare %ArenaMark @arena_snapshot(ptr)",
		"declare void @arena_rewind(ptr, %ArenaMark)",
		"declare void @arena_reset(ptr)",
		"call %ArenaMark @arena_snapshot(ptr",
		"call void @arena_rewind(ptr",
		"call void @arena_reset(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersNestedRegionCheckpoints(t *testing.T) {
	src := `def region_value(seed: i32) -> i32:
	region scratch(1024u)
	mark scratch as outer
	stable: any i32& = new[scratch] seed + 1
	mark scratch as inner
	temp: any i32& = new[scratch] seed + 2
	restore scratch from inner
	kept: i32 = stable[0u]
	restore scratch from outer
	reset scratch
	fresh: any i32& = new[scratch] seed + 3
	return kept + fresh[0u]
`
	result := parseAndAnalyze(t, "backend_nested_region_checkpoints.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	if got := strings.Count(output, "call %ArenaMark @arena_snapshot(ptr"); got != 2 {
		t.Fatalf("expected 2 arena_snapshot calls, got %d\n%s", got, output)
	}
	if got := strings.Count(output, "call void @arena_rewind(ptr"); got != 2 {
		t.Fatalf("expected 2 arena_rewind calls, got %d\n%s", got, output)
	}
	if got := strings.Count(output, "call void @arena_reset(ptr"); got != 1 {
		t.Fatalf("expected 1 arena_reset call, got %d\n%s", got, output)
	}
}

func TestGenerateLLVMIRLowersEnumConstructorsAndMatch(t *testing.T) {
	src := `enum MaybeInt:
	None
	Some(int)
	Pair(int, int)

def make_pair() -> MaybeInt:
	return MaybeInt.Pair(3, 4)

def unwrap_or(value: MaybeInt, fallback: int) -> int:
	match value:
		MaybeInt.None:
			return fallback
		MaybeInt.Some(inner):
			return inner
		MaybeInt.Pair(left, right):
			return left + right
`
	result := parseAndAnalyze(t, "backend_enum_match.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%MaybeInt = type { i32, [2 x i64] }",
		"define %MaybeInt @make_pair()",
		"define i64 @unwrap_or(%MaybeInt",
		"icmp eq i32",
		"store i32 2",
		"extractvalue { i64, i64 }",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersMatchExpressionsViaPhi(t *testing.T) {
	src := `enum MaybeInt:
	None
	Some(int)
	Pair(int, int)

def unwrap_or(value: MaybeInt, fallback: int) -> int:
	return match value:
		MaybeInt.None:
			fallback
		MaybeInt.Some(inner):
			inner
		MaybeInt.Pair(left, right):
			left + right
`
	result := parseAndAnalyze(t, "backend_enum_match_expr.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%MaybeInt = type { i32, [2 x i64] }",
		"define i64 @unwrap_or(%MaybeInt",
		"phi i64",
		"icmp eq i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersNestedMatchPatterns(t *testing.T) {
	src := `enum Inner:
	A(int)
	B

enum Outer:
	Wrap(Inner)
	Empty

def nested_value(value: Outer) -> int:
	return match value:
		Outer.Wrap(Inner.A(inner)):
			inner
		Outer.Wrap(Inner.B):
			0
		Outer.Empty:
			-1
`
	result := parseAndAnalyze(t, "backend_nested_match_patterns.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Inner = type { i32, [1 x i64] }",
		"%Outer = type { i32, [2 x i64] }",
		"define i64 @nested_value(%Outer",
		"extractvalue %Outer",
		"extractvalue %Inner",
		"phi i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "icmp eq i32") < 3 {
		t.Fatalf("expected nested match lowering to compare multiple enum tags, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersNamedEnumPayloadPatterns(t *testing.T) {
	src := `enum PairOrInt:
	Just(value: int)
	Pair(left: int, right: int)

def score(value: PairOrInt) -> int:
	return match value:
		PairOrInt.Just(value: inner):
			inner
		PairOrInt.Pair(right: r, left: l):
			l + r
`
	result := parseAndAnalyze(t, "backend_enum_named_payload_patterns.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%PairOrInt = type { i32, [2 x i64] }",
		"define i64 @score(%PairOrInt",
		"extractvalue %PairOrInt",
		"phi i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "extractvalue { i64, i64 }") < 1 {
		t.Fatalf("expected named payload pattern lowering to unpack pair payloads, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersNamedEnumConstructorArgs(t *testing.T) {
	src := `enum PairOrInt:
	Pair(left: int, right: int)

def make_pair() -> PairOrInt:
	return PairOrInt.Pair(right: 4, left: 3)
`
	result := parseAndAnalyze(t, "backend_enum_named_ctor_args.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%PairOrInt = type { i32, [2 x i64] }",
		"define %PairOrInt @make_pair()",
		"store { i64, i64 } { i64 3, i64 4 }, ptr %enum.payload.ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersEnumEqualityViaTagAndPayloadWords(t *testing.T) {
	src := `enum MaybeInt:
	None
	Some(int)
	Pair(int, int)

def same_none(left: MaybeInt, right: MaybeInt) -> bool:
	return left == right

def differs(left: MaybeInt, right: MaybeInt) -> bool:
	return left != right

def compare_payload() -> bool:
	return MaybeInt.Pair(3, 4) == MaybeInt.Pair(3, 4)
`
	result := parseAndAnalyze(t, "backend_enum_equality.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%MaybeInt = type { i32, [2 x i64] }",
		"define i1 @same_none(%MaybeInt",
		"define i1 @differs(%MaybeInt",
		"define i1 @compare_payload()",
		"extractvalue %MaybeInt",
		"extractvalue [2 x i64]",
		"icmp eq i32",
		"and i1",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"icmp eq %MaybeInt", "icmp ne %MaybeInt"} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected enum comparisons to avoid aggregate icmp %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRLowersPayloadlessEnumsAsPlainTags(t *testing.T) {
	src := `enum TokenKind:
	Ident
	Region
	Destroy
	New

def is_region(kind: TokenKind) -> bool:
	return kind == TokenKind.Region

def next_kind() -> TokenKind:
	return TokenKind.Destroy
`
	result := parseAndAnalyze(t, "backend_payloadless_enum_tags.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i1 @is_region(i32",
		"define i32 @next_kind()",
		"icmp eq i32",
		"ret i32 2",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"%TokenKind = type", "[0 x i64]", "extractvalue %TokenKind"} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected payloadless enum lowering to avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRLowersPackedEnumStoresAllocationsAndMatches(t *testing.T) {
	src := `packed enum Expr:
	Lit(int)
	Add(Expr, Expr)

def fold() -> int:
	region scratch(1024u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	left: Expr = new[store] Expr.Lit(3)
	right: Expr = new[store] Expr.Lit(4)
	node: Expr = new[store] Expr.Add(left, right)
	return match node in store:
		Expr.Lit(value):
			value
		Expr.Add(lhs, rhs):
			1 if lhs != rhs else 0
`
	result := parseAndAnalyze(t, "backend_packed_enum_match.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Expr__Store = type { ptr, i64, ptr }",
		"%Expr = type { i32, [2 x i64] }",
		"define i64 @fold()",
		"call ptr @new_region(i64 1024)",
		"call ptr @arena_alloc(ptr",
		"extractvalue %Expr__Store",
		"load i32, ptr",
		"load { ptr, ptr }, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "extractvalue %Expr,") {
		t.Fatalf("expected packed enum matching to load through handles rather than extract aggregate enum values, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPackedInStoreBlockSugar(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	Add(left: Expr, right: Expr)

def fold() -> int:
	region scratch(1024u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		left: Expr = new Expr.Lit(span: 1, value: 3)
		right: Expr = new Expr.Lit(span: 2, value: 4)
		node: Expr = new Expr.Add(span: 3, left: left, right: right)
		return match node:
			Expr.Lit(value: value):
				value + node.span
			Expr.Add(left: lhs, right: rhs):
				node.span + lhs.span + rhs.span
`
	result := parseAndAnalyze(t, "backend_packed_in_store_block.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Expr = type { i32, i64, [2 x i64] }",
		"define i64 @fold()",
		"call ptr @new_region(i64 1024)",
		"call ptr @arena_alloc(ptr",
		"store %Expr { i32 1, i64 3, [2 x i64] zeroinitializer }, ptr %packed.alloc4",
		"load i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "unknown enum constructor") {
		t.Fatalf("expected in-store packed sugar to lower successfully, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPayloadlessPackedEnumsAsHandles(t *testing.T) {
	src := `packed enum Token:
	Ident
	Region

def differs(left: Token, right: Token) -> bool:
	return left != right
`
	result := parseAndAnalyze(t, "backend_packed_payloadless_enum.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i1 @differs(ptr",
		"icmp ne ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"ret i32", "icmp ne i32"} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected payloadless packed enums to stay handle-backed and avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRLowersPackedCommonFieldAccess(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def read(node: Expr) -> int:
	return node.span
`
	result := parseAndAnalyze(t, "backend_packed_common_field.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Expr = type { i32, i64, [1 x i64] }",
		"define i64 @read(ptr",
		"getelementptr inbounds",
		"%Expr, ptr",
		"load i64, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "extractvalue %Expr,") {
		t.Fatalf("expected packed common field access to lower through the row handle, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPackedCommonFieldInitialization(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def build() -> Expr:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	return new[store] Expr.Int(span: 9, value: 5)
`
	result := parseAndAnalyze(t, "backend_packed_common_field_init.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Expr = type { i32, i64, [1 x i64] }",
		"define ptr @build()",
		"store %Expr { i32 0, i64 9, [1 x i64] zeroinitializer }, ptr %packed.alloc",
		"store i64 5, ptr %enum.payload.ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "unknown enum constructor") {
		t.Fatalf("expected packed common-field initialization to lower successfully, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPayloadlessPackedCommonFieldInitialization(t *testing.T) {
	src := `packed enum Token:
	common:
		span: int
	Region

def build() -> Token:
	region scratch(256u)
	store: Token.Store[Local] = Token.Store(scratch)
	return new[store] Token.Region(span: 4)
`
	result := parseAndAnalyze(t, "backend_payloadless_packed_common_init.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Token = type { i32, i64 }",
		"define ptr @build()",
		"store %Token { i32 0, i64 4 }, ptr %packed.alloc",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPayloadlessPackedAllocationFromQualifiedConstructor(t *testing.T) {
	src := `packed enum Token:
	Ident
	Region

def build() -> Token:
	region scratch(256u)
	store: Token.Store[Local] = Token.Store(scratch)
	return new[store] Token.Region
`
	result := parseAndAnalyze(t, "backend_payloadless_packed_alloc.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Token = type { i32 }",
		"define ptr @build()",
		"call ptr @new_region(i64 256)",
		"call ptr @arena_alloc(ptr",
		"store %Token { i32 1 }, ptr %packed.alloc",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "unknown enum constructor") {
		t.Fatalf("expected payloadless packed allocation to lower successfully, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersDictSurfaceTypesViaDynDictCarrier(t *testing.T) {
	src := `extern take_runtime(values: DynDict[i32]) -> void
extern make_runtime() -> DynDict[i32]

def id[V](values: dict[dstr, V]) -> dict[dstr, V]:
	return values

def keep(values: dict[dstr, i32]) -> dict[dstr, i32]:
	return id(values)

def pass_runtime(values: dict[dstr, i32]) -> void:
	take_runtime(values)

def from_runtime() -> dict[dstr, i32]:
	return make_runtime()
`
	result := parseAndAnalyze(t, "backend_dict_runtime_bridge.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynDict__i32 = type { ptr, i64, i64, i64, ptr }",
		"declare void @take_runtime(%DynDict__i32)",
		"declare %DynDict__i32 @make_runtime()",
		"define %DynDict__i32 @id__i32(%DynDict__i32",
		"define %DynDict__i32 @keep(%DynDict__i32",
		"call %DynDict__i32 @id__i32(%DynDict__i32",
		"define void @pass_runtime(%DynDict__i32",
		"call void @take_runtime(%DynDict__i32",
		"define %DynDict__i32 @from_runtime()",
		"call %DynDict__i32 @make_runtime()",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRSpecializesDictHelperStyleFunctions(t *testing.T) {
	src := `error RuntimeError:
	OutOfMemory

def arena_dict_get[V](m: any dict[dstr, V]&, key: dstr) -> any V&?:
	return null

def arena_dict_put[V](a: any Arena&, m: any dict[dstr, V]&, key: dstr, value: V) -> any V&? error[RuntimeError]:
	raise RuntimeError.OutOfMemory

def touch(a: any Arena&, m: any dict[dstr, i32]&, key: dstr) -> bool:
	slot: any i32&? = try arena_dict_put(a, m, key, 7) else null
	if slot == null:
		return false
	maybe_slot: any i32&? = arena_dict_get(m, key)
	return maybe_slot != null
`
	result := parseAndAnalyze(t, "backend_dict_helper_calls.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__RuntimeError__any_i32 = type { i32, ptr }",
		"define ptr @arena_dict_get__i32(ptr",
		"define i32 @arena_dict_put__i32(ptr",
		"define i1 @touch(ptr %0, ptr %1, ptr %2)",
		"call i32 @arena_dict_put__i32(ptr",
		"call ptr @arena_dict_get__i32(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersFrontendStressFixture(t *testing.T) {
	src := loadFixtureSource(t, "Code", "test_programs", "frontend_stress.llcontext")
	result := parseAndAnalyze(t, "backend_frontend_stress.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%SourceSpan = type { i64, i64 }",
		"%Token = type { i32, %SourceSpan, ptr }",
		"%DynDict__Symbol = type { ptr, i64, i64, i64, ptr }",
		"%Scope = type { ptr, %DynDict__Symbol, i64 }",
		"%ParserState = type { %DynArrayView, i64, ptr }",
		"define %DynArrayView @make_tokens()",
		"define i32 @frontend_scope_stress(ptr",
		"define i64 @frontend_region_token(i64",
		"define i32 @frontend_smoke(ptr",
		"define %DynDict__Symbol @arena_dict_new__Symbol(ptr",
		"define i32 @arena_dict_put__Symbol(ptr",
		"define i1 @arena_dict_contains__Symbol(ptr",
		"call ptr @new_region(i64 2048)",
		"call ptr @arena_alloc(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersAllocatorOwnershipFixture(t *testing.T) {
	src := loadFixtureSource(t, "Code", "test_programs", "allocator_ownership.llcontext")
	result := parseAndAnalyze(t, "backend_allocator_ownership.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%FuzzPair = type { i64, i64 }",
		"%HeapPairNode = type { %FuzzPair, ptr }",
		"declare ptr @alloc_heap_pair_node()",
		"declare ptr @sfree_heap_pair_node(ptr)",
		"declare ptr @alloc_bytes(i64)",
		"declare ptr @sfree_bytes(ptr)",
		"declare i64 @snprintf(ptr, i64, ptr, ...)",
		"define i32 @recursive_pair_node_sum(ptr ",
		"define i32 @recursive_free_pair_chain(ptr ",
		"define i32 @build_pair_chain_sum(ptr ",
		"call i32 @recursive_pair_node_sum(ptr ",
		"call i32 @recursive_free_pair_chain(ptr ",
		"define i32 @alloc_and_format_heap_buffer(ptr ",
		"call i64 (ptr, i64, ptr, ...) @snprintf(",
		"@recursive_format_or_fallback(",
		"@allocator_ownership_combo(",
		"alloca [32 x i8]",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "call ptr @sfree_heap_pair_node(ptr") < 2 {
		t.Fatalf("expected multiple heap-pair frees in ownership fixture lowering, got:\n%s", output)
	}
	if strings.Count(output, "call i32 @require_heap_pair_node(ptr") < 3 {
		t.Fatalf("expected repeated heap-pair acquisition helper lowering, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersErrorUnionsTryAndRaise(t *testing.T) {
	src := `error MemoryError:
	OutOfMemory

error IoError:
	NotFound

extern alloc(size: usize) -> heap void&?
extern read_file(path: any u8&) -> dstr[file_text] error[IoError.NotFound, ...]

def checked_alloc(size: usize) -> heap void& error[MemoryError.OutOfMemory, ...]:
	ptr: heap void& = alloc(size) else raise MemoryError.OutOfMemory
	return ptr

def load_text(path: any u8&) -> dstr[file_text] error[IoError.NotFound, ...]:
	text: dstr[file_text] = try read_file(path)
	return text

def load_with_fallback(path: any u8&) -> any u8&:
	text: any u8& = try read_file(path) else "".cast[any u8&]()
	return text
`
	result := parseAndAnalyze(t, "backend_error_handling.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__IoError__dstr_file_text = type { i32, ptr }",
		"%ErrUnion__MemoryError__heap_void = type { i32, ptr }",
		"declare ptr @alloc(i64)",
		"declare i32 @read_file(ptr, ptr)",
		"define i32 @checked_alloc(ptr ",
		"define i32 @load_text(ptr ",
		"define ptr @load_with_fallback(ptr ",
		"extractvalue %ErrUnion__IoError__dstr_file_text",
		"insertvalue %ErrUnion__IoError__dstr_file_text",
		"icmp eq i32",
		"phi ptr",
		"ret i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRRemapsErrorCodesWhenWideningErrorSets(t *testing.T) {
	src := `error SourceError:
	NotFound
	PermissionDenied

error BroadError:
	PermissionDenied
	Busy
	NotFound

extern read_value() -> int error[SourceError.NotFound, ...]

def bubble() -> int error[BroadError.NotFound, ...]:
	return try read_value()

def fail_now() -> int error[BroadError.NotFound, ...]:
	raise SourceError.NotFound
`
	result := parseAndAnalyze(t, "backend_error_set_widening.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__SourceError__int = type { i32, i64 }",
		"%ErrUnion__BroadError__int = type { i32, i64 }",
		"declare i32 @read_value(ptr)",
		"define i32 @bubble(ptr ",
		"define i32 @fail_now(ptr ",
		"errmap_is_SourceError_NotFound",
		"errmap_is_SourceError_PermissionDenied",
		"ret i32 3",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRComposesMultipleErrorFamilies(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	NotFound

extern read_disk() -> int error[FileError]
extern read_network() -> int error[NetworkError.Timeout]

def bubble_disk() -> int error[FileError, NetworkError]:
	return try read_disk()

def bubble_network() -> int error[FileError, NetworkError]:
	return try read_network()
`
	result := parseAndAnalyze(t, "backend_error_multi_family.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__FileError__int = type { i32, i64 }",
		"declare i32 @read_disk(ptr)",
		"declare i32 @read_network(ptr)",
		"define i32 @bubble_disk(ptr ",
		"define i32 @bubble_network(ptr ",
		"errmap_is_FileError_NotFound",
		"errmap_is_FileError_PermissionDenied",
		"errmap_is_NetworkError_Timeout",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRExpandsMixedRowStyleFamilies(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	Disconnected

extern read_disk() -> int error[FileError]
extern read_network() -> int error[NetworkError.Timeout]

def bubble_disk() -> int error[FileError.NotFound, NetworkError.Timeout, ...]:
	return try read_disk()

def bubble_network() -> int error[FileError.NotFound, NetworkError.Timeout, ...]:
	return try read_network()

def fail_disk() -> int error[FileError.NotFound, NetworkError.Timeout, ...]:
	raise FileError.PermissionDenied
`
	result := parseAndAnalyze(t, "backend_error_mixed_row_style.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i32 @bubble_disk(ptr ",
		"define i32 @bubble_network(ptr ",
		"define i32 @fail_disk(ptr ",
		"errmap_is_FileError_NotFound",
		"errmap_is_FileError_PermissionDenied",
		"errmap_is_NetworkError_Timeout",
		"ret i32 2",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRCanonicalizesErrorUnionNames(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	Disconnected

extern read_value() -> int error[NetworkError, FileError]

def by_reverse_family_order() -> int error[NetworkError, FileError]:
	return try read_value()
`
	result := parseAndAnalyze(t, "backend_error_canonicalization.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	if !strings.Contains(output, "%ErrUnion__error_FileError__NetworkError__int = type { i32, i64 }") {
		t.Fatalf("expected canonical error union struct name, got:\n%s", output)
	}
}

func TestGenerateLLVMIRAcceptsBareFamilyErrorSetShorthand(t *testing.T) {
	src := `error IoError:
	NotFound

extern read_file(path: any u8&) -> dstr[file_text] error[IoError]

def load_text(path: any u8&) -> dstr[file_text] error[IoError, ...]:
	text: dstr[file_text] = try read_file(path)
	return text
`
	result := parseAndAnalyze(t, "backend_error_set_wildcard.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__IoError__dstr_file_text = type { i32, ptr }",
		"declare i32 @read_file(ptr, ptr)",
		"define i32 @load_text(ptr ",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRIndexesRuntimeBackedArraysAndViews(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct DynArrayView:
	items: mutable any void&?
    count: mutable usize

def read_array(values: darray[i32, row]) -> i32:
    return values[1]

def read_view(view: dview[i32]) -> i32:
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

func TestGenerateLLVMIRIndexesDStrViaRuntimeHelper(t *testing.T) {
	src := `def read_codepoint(text: dstr[row]) -> char:
    return text[1]
`
	result := parseAndAnalyze(t, "backend_runtime_dstr_index.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @read_codepoint(ptr",
		"declare i64 @ctx_string_index(ptr, i64)",
		"call i64 @ctx_string_index(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRAcceptsShapeErasingDStrShorthand(t *testing.T) {
	src := `def keep(text: dstr) -> dstr:
    return text

def erase(text: dstr[row]) -> dstr:
    return text
`
	result := parseAndAnalyze(t, "backend_dstr_shorthand.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define ptr @keep(ptr",
		"define ptr @erase(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRIndexesStringViewViaRuntimeHelper(t *testing.T) {
	src := `def read_view(view: StringView) -> char:
    return view[1]
`
	result := parseAndAnalyze(t, "backend_runtime_string_view_index.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%StringView = type { ptr, i64 }",
		"define i64 @read_view(%StringView",
		"declare i64 @ctx_string_view_index(%StringView, i64)",
		"call i64 @ctx_string_view_index(%StringView",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersRuntimeStringEqualityHelpers(t *testing.T) {
	src := `def same_text(left: dstr[row], right: dstr[col]) -> bool:
	return left == right

def same_view_text(view: StringView, text: dstr[row]) -> bool:
	return view == text

def same_text_view(text: dstr[row], view: StringView) -> bool:
	return text == view

def different_views(left: StringView, right: StringView) -> bool:
	return left != right
`
	result := parseAndAnalyze(t, "backend_runtime_string_equality.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i64 @ctx_streq(ptr, ptr)",
		"declare i64 @ctx_string_view_eq(%StringView, ptr)",
		"declare i64 @ctx_string_views_eq(%StringView, %StringView)",
		"call i64 @ctx_streq(ptr",
		"call i64 @ctx_string_view_eq(%StringView",
		"call i64 @ctx_string_views_eq(%StringView",
		"icmp ne i64",
		"icmp eq i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRSpecializesSameExtentRuntimeStringEquality(t *testing.T) {
	src := `repr(c) struct StringView:
	data: mutable any u8&
	len: mutable i64

def string_view(value: any u8&?, start: i64, end: i64) -> StringView:
	_ = value
	_ = start
	return StringView("".cast[any u8&](), end - start)

def ctx_string_view(value: dstr[shape_in], start: i64, end: i64) -> StringView:
	return string_view(value, start, end)

def ctx_string_view_prefix(view: StringView, end: i64) -> StringView:
	return string_view(view.data, 0, end)

def ctx_string_view_suffix(view: StringView, start: i64) -> StringView:
	return string_view(view.data, start, view.len)

def same_shape_text(left: dstr[row], right: dstr[row]) -> bool:
	return left == right

def same_bounds_view(left: dstr[row], right: dstr[col]) -> bool:
	left_view: StringView = ctx_string_view(left, 0, 2)
	right_view: StringView = ctx_string_view(right, 0, 2)
	return left_view == right_view

def fresh_disjoint_raw_views() -> bool:
	region scratch(1024u)
	return string_view(new[scratch] 1u8, 0, 1) == string_view(new[scratch] 2u8, 0, 1)

def split_disjoint_views(text: dstr[row]) -> bool:
	base: StringView = ctx_string_view(text, 0, 4)
	return ctx_string_view_prefix(base, 2) == ctx_string_view_suffix(base, 2)

def different_bounds_view(left: dstr[row], right: dstr[col]) -> bool:
	left_view: StringView = ctx_string_view(left, 0, 2)
	right_view: StringView = ctx_string_view(right, 0, 3)
	return left_view == right_view
`
	result := parseAndAnalyze(t, "backend_runtime_string_same_extent.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i64 @memcmp(ptr, ptr, i64)",
		"declare i64 @ctx_strlen(ptr)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}

	sameShapeBody := functionIR(output, "same_shape_text")
	if sameShapeBody == "" {
		t.Fatalf("expected to find same_shape_text body, got:\n%s", output)
	}
	for _, want := range []string{"call i64 @ctx_strlen(ptr", "call i64 @memcmp(ptr"} {
		if !strings.Contains(sameShapeBody, want) {
			t.Fatalf("expected same_shape_text to contain %q, got:\n%s", want, sameShapeBody)
		}
	}
	if strings.Contains(sameShapeBody, "call i64 @ctx_streq") {
		t.Fatalf("expected same_shape_text to avoid ctx_streq helper, got:\n%s", sameShapeBody)
	}

	sameBoundsBody := functionIR(output, "same_bounds_view")
	if sameBoundsBody == "" {
		t.Fatalf("expected to find same_bounds_view body, got:\n%s", output)
	}
	if !strings.Contains(sameBoundsBody, "call i64 @memcmp(ptr") {
		t.Fatalf("expected same_bounds_view to use memcmp fast path, got:\n%s", sameBoundsBody)
	}
	if strings.Contains(sameBoundsBody, "call i64 @ctx_string_views_eq") {
		t.Fatalf("expected same_bounds_view to avoid ctx_string_views_eq helper, got:\n%s", sameBoundsBody)
	}

	disjointBoundsBody := functionIR(output, "fresh_disjoint_raw_views")
	if disjointBoundsBody == "" {
		t.Fatalf("expected to find fresh_disjoint_raw_views body, got:\n%s", output)
	}
	if !strings.Contains(disjointBoundsBody, "call i64 @memcmp(ptr noalias") {
		t.Fatalf("expected fresh_disjoint_raw_views to mark memcmp operands noalias, got:\n%s", disjointBoundsBody)
	}

	splitBoundsBody := functionIR(output, "split_disjoint_views")
	if splitBoundsBody == "" {
		t.Fatalf("expected to find split_disjoint_views body, got:\n%s", output)
	}
	if !strings.Contains(splitBoundsBody, "call i64 @memcmp(ptr noalias") {
		t.Fatalf("expected split_disjoint_views to use disjoint memcmp fast path, got:\n%s", splitBoundsBody)
	}
	if strings.Contains(splitBoundsBody, "call i64 @ctx_string_views_eq") {
		t.Fatalf("expected split_disjoint_views to avoid ctx_string_views_eq helper, got:\n%s", splitBoundsBody)
	}

	differentBoundsBody := functionIR(output, "different_bounds_view")
	if differentBoundsBody == "" {
		t.Fatalf("expected to find different_bounds_view body, got:\n%s", output)
	}
	if !strings.Contains(differentBoundsBody, "call i64 @ctx_string_views_eq") {
		t.Fatalf("expected different_bounds_view to keep helper fallback, got:\n%s", differentBoundsBody)
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
	result := parseAndAnalyze(t, "backend_runtime_string_literal_eq_tiny.llcontext", src)
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
	src := `extern string_view_eq(view: StringView, other: any u8&?) -> int

def same_long(view: StringView) -> bool:
	return string_view_eq(view, "destroy_region".cast[any u8&]()) != 0
`
	result := parseAndAnalyze(t, "backend_runtime_string_literal_eq_long.llcontext", src)
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
	src := `repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

extern arena_memcpy(dest: any void&?, src: any void&?, n: usize) -> any void&?

def arena_da_view_prefix[T](view: dview[T], end: usize) -> dview[T]:
	_ = end
	return view

def arena_da_view_suffix[T](view: dview[T], start: usize) -> dview[T]:
	_ = start
	return view

def split_copy(view: dview[i32]) -> any void&?:
	prefix: dview[i32] = arena_da_view_prefix(view, 2u)
	suffix: dview[i32] = arena_da_view_suffix(view, 2u)
	return arena_memcpy(prefix.data, suffix.data, prefix.len * prefix.elem_size)
`
	result := parseAndAnalyze(t, "backend_dview_split_memcpy.llcontext", src)
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

func TestGenerateLLVMIRSpecializesStringViewLiteralWrapperCalls(t *testing.T) {
	src := `extern string_view_eq(view: StringView, other: any u8&?) -> int

def frontend_sv_eq_literal(view: StringView, literal: static u8&) -> bool:
	return string_view_eq(view, literal.cast[any u8&]()) != 0

def same_short(view: StringView) -> bool:
	return frontend_sv_eq_literal(view, "def")

def same_long(view: StringView) -> bool:
	return frontend_sv_eq_literal(view, "destroy_region")
`
	result := parseAndAnalyze(t, "backend_runtime_string_literal_wrapper_eq.llcontext", src)
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
	src := `def text_len(text: dstr[row]) -> i64:
    return text.len
`
	result := parseAndAnalyze(t, "backend_dstr_len.llcontext", src)
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
	return view.len > 0u and view.elem_size > 0u

def probe(view: dview[i64]) -> bool:
	return non_empty(view)
`
	result := parseAndAnalyze(t, "backend_darray_view_runtime_fields.llcontext", src)
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
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
    len: mutable usize
    elem_size: mutable usize

def head_owned(values: darray[i32, row]) -> i32:
	part: dview[i32] = values[1u:3u]
    return part[0u]

def head_view(view: dview[i32]) -> i32:
	part: dview[i32] = view[0u:1u]
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
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def slice_owned(values: i32[4]) -> view[i32]:
	return values[1u:3u]

def head_ref(values: any i32[4]&) -> i32:
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
	items: mutable any T&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
    len: mutable usize
    elem_size: mutable usize

extern make_array() -> darray[i32, row]
extern make_array_view() -> dview[i32]

def read_array_index() -> i32:
    return make_array()[1u]

def read_array_slice_index() -> i32:
    return make_array()[1u:3u][0u]

def read_array_view_index() -> i32:
    return make_array_view()[0u]
`
	result := parseAndAnalyze(t, "backend_nested_collection_access_returns.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare %DynArray__i32 @make_array()",
		"declare %DynArrayView @make_array_view()",
		"call %DynArray__i32 @make_array()",
		"call %DynArrayView @make_array_view()",
		"call %DynArrayView @arena_da_view(ptr",
		"getelementptr i32, ptr",
		"alloca %DynArray__i32",
		"alloca %DynArrayView",
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
	if strings.Contains(output, "@rt_tlist_new") || strings.Contains(output, "@rt_tlist_push") {
		t.Fatalf("expected fixed array literals not to lower through typed-list runtime helpers, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersStringSliceSyntaxViaRuntimeHelpers(t *testing.T) {
	src := `repr(c) struct StringView:
	data: mutable any u8&
    len: mutable i64

def head_codepoint(text: dstr[row]) -> char:
	view: StringView = text[1:3]
    return view[0]
`
	result := parseAndAnalyze(t, "backend_string_slice_syntax.llcontext", src)
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
