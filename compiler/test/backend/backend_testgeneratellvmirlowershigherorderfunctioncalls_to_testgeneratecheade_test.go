package backend_test

import (
	"elisacore/src/backend"
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersHigherOrderFunctionCalls(t *testing.T) {
	src := `def apply_twice(fn: func(i64) -> i64, value: i64) -> i64:
    return fn(fn(value))

def inc(value: i64) -> i64:
    return value + 1

def run() -> i64:
    return apply_twice(inc, 40)
`
	result := parseAndAnalyze(t, "backend_higher_order_call.elisa", src)
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
	fn: func(i64) -> i64 = bits.cast[func(i64) -> i64]
    return fn(value)

def run() -> i64:
	bits: uintptr = inc as uintptr
    return call_bits(bits, 41)
`
	result := parseAndAnalyze(t, "backend_function_value_erasure_casts.elisa", src)
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
	result := parseAndAnalyze(t, "backend_explicit_generic_function_specialization.elisa", src)
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
func TestGenerateLLVMIRLowersGenericBuilderStructFunctionFields(t *testing.T) {
	src := `struct Box[T]:
    value: T

struct Builder[T]:
    make: func(T) -> Box[T]

def make_i64_box(value: i64) -> Box[i64]:
    return Box[i64](value)

def wrap[T](builder: Builder[T], value: T) -> Box[T]:
    return builder.make(value)

def run() -> i64:
    builder: Builder[i64] = Builder[i64](make_i64_box)
    boxed: Box[i64] = wrap(builder, 7)
    return boxed.value
`
	result := parseAndAnalyze(t, "backend_generic_builder_struct_function_field.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define %Box__i64 @wrap__i64(",
		"define i64 @run()",
		"store %Builder__i64 { ptr @make_i64_box }",
		"call %Box__i64 %",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersPanicViaBacktraceAwareAbort(t *testing.T) {
	src := `def fail() -> void:
	panic("boom")
`
	result := parseAndAnalyze(t, "backend_panic_stmt.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define void @fail()",
		"declare i64 @printf(ptr, ...)",
		"declare i64 @backtrace(ptr, i64)",
		"declare void @backtrace_symbols_fd(ptr, i64, i64)",
		"declare void @abort()",
		"call i64 (ptr, ...) @printf(",
		"call i64 @backtrace(ptr",
		"call void @backtrace_symbols_fd(ptr",
		"call void @abort()",
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
	result := parseAndAnalyze(t, "backend_builtin_string_surface.elisa", src)
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
func TestGenerateLLVMIRCoercesStringLiteralToSView(t *testing.T) {
	src := `def literal_view() -> sview:
    view: sview = "hello"
    return view
`
	result := parseAndAnalyze(t, "backend_string_literal_sview_context.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	checks := []string{
		"%StringView = type { ptr, i64 }",
		"define %StringView @literal_view()",
		"%StringView { ptr @str, i64 5 }",
		"i64 5",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersEscapedStringLiteralBytes(t *testing.T) {
	src := `def newline_text() -> u8&:
	return "line\nbreak" as u8&

def quoted_text() -> u8&:
	return "quote: \" slash: \\ hex: \x41" as u8&

def unicode_text() -> u8&:
	return "\u263A" as u8&
`
	result := parseAndAnalyze(t, "backend_string_escapes.elisa", src)
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
	result := parseAndAnalyze(t, "backend_standalone_char_values.elisa", src)
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
	src := `struct Box:
    value: i32

extern maybe_box() -> Box&?

def is_missing() -> bool:
    return maybe_box() == null

def is_present() -> bool:
    return maybe_box() != null

def same_box(left: Box&, right: Box&) -> bool:
    return left == right
`
	result := parseAndAnalyze(t, "backend_reference_comparisons.elisa", src)
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
func TestGenerateLLVMIRLowersOptionalStructFieldNullComparisons(t *testing.T) {
	src := `tree PascalType:
	node Type:
		Name(id: u32)

struct Symbol:
	type_expr: PascalType.Type?

def has_type_expr(symbol: Symbol) -> bool:
	return symbol.type_expr != null

def lacks_type_expr(symbol: Symbol) -> bool:
	return symbol.type_expr == null
`
	result := parseAndAnalyze(t, "backend_optional_struct_field_null_compare.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	for _, check := range []string{"define i1 @has_type_expr", "define i1 @lacks_type_expr", "optional.present"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersOptionalNullChecksInTernaryConditions(t *testing.T) {
	src := `tree PascalType:
	node Type:
		Name(id: u32)

def both_present(left: PascalType.Type?, right: PascalType.Type?) -> bool:
	return true if left != null and right != null else false

def both_missing(left: PascalType.Type?, right: PascalType.Type?) -> bool:
	return true if left == null and right == null else false
`
	result := parseAndAnalyze(t, "backend_optional_ternary_condition_null_compare.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	for _, check := range []string{"define i1 @both_present", "define i1 @both_missing", "cond.and.rhs", "optionalisnull"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRTernaryUsesPhi(t *testing.T) {
	src := `def choose(flag: bool, left: i32, right: i32) -> i32:
    return left if flag else right
`
	result := parseAndAnalyze(t, "backend_ternary.elisa", src)
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
	src := `struct Pair:
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
	result := parseAndAnalyze(t, "backend_globals.elisa", src)
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
	src := `struct Pair:
	left: i32
	right: i32

struct Holder:
	pair: Pair

global base: Pair = Pair(1, 2)
global table: Pair[2] = [base, Pair(3, 4)]
global picked: Pair = table[1]
global wrapped: Holder = Holder(table[0])
global first_left: i32 = table[0].left
`
	result := parseAndAnalyze(t, "backend_global_aggregate_refs.elisa", src)
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
	result := parseAndAnalyze(t, "backend_generic_specialization.elisa", src)
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
func TestGenerateLLVMIRSpecializesRefQualifierGenericFunctions(t *testing.T) {
	src := `struct Node:
	value: mutable i32

struct Handle[refstorage Store, refstate State]:
	ptr: Store Node&[State]

def id_handle[refstorage Store, refstate State](value: Handle[Store, State]) -> Handle[Store, State]:
	return value

def use_handle(value: Handle[heap, &]) -> heap Node&:
	kept: Handle[heap, &] = id_handle(value)
	return kept.ptr
`
	result := parseAndAnalyze(t, "backend_ref_qualifier_specialization.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Handle__heap__anon = type { ptr }",
		"define %Handle__heap__anon @id_handle__heap__anon(%Handle__heap__anon",
		"define ptr @use_handle(%Handle__heap__anon",
		"call %Handle__heap__anon @id_handle__heap__anon(%Handle__heap__anon",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersExportWrappers(t *testing.T) {
	src := `struct Vec[T]:
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
	result := parseAndAnalyze(t, "backend_export_wrappers.elisa", src)
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
	src := `struct Vec[T]:
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
	result := parseAndAnalyze(t, "backend_export_header.elisa", src)
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
