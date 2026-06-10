package backend_test

import (
	"elisacore/src/backend"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateLLVMIRAcceptsShapeErasingDArrayShorthand(t *testing.T) {
	src := `def keep(values: darray[i32]) -> darray[i32]:
    return values

def erase(values: darray[i32, row]) -> darray[i32]:
    return values
`
	result := parseAndAnalyze(t, "backend_darray_shorthand.elisa", src)
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
	result := parseAndAnalyze(t, "backend_bitcode.elisa", src)
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
	result := parseAndAnalyze(t, "backend_object.elisa", src)
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
	src := `struct Padded:
    tag: i8
    value: i32

def padded_size() -> usize:
    return size_of(Padded)

def array_view_size() -> usize:
	return size_of(view[i32])
`
	result := parseAndAnalyze(t, "backend_sizeof.elisa", src)
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
	value: mutable u32 = 10
	value %= 4
    return value
`
	result := parseAndAnalyze(t, "backend_modulo.elisa", src)
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
	result := parseAndAnalyze(t, "backend_pointer_arithmetic.elisa", src)
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
	region scratch(1024)
	value: i32& = new[scratch] seed + 1
	return value[0]
`
	result := parseAndAnalyze(t, "backend_manual_regions.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Arena = type { ptr, ptr, i64, i64 }",
		"declare ptr @new_region_backend(i64, i64)",
		"declare ptr @arena_alloc(ptr, i64)",
		"declare void @arena_free(ptr)",
		// Every region lowers through the strategy-dispatching allocator; a plain
		// region passes ARENA_STRATEGY_CHAINED (0), which new_region_backend routes to new_region.
		"call ptr @new_region_backend(i64 1024, i64 0)",
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
	region scratch(1024)
	mark scratch as cp
	temp: i32& = new[scratch] seed + 1
	restore scratch from cp
	reused: i32& = new[scratch] seed + 2
	value: i32 = reused[0]
	reset scratch
	final: i32& = new[scratch] seed + 3
	return value + final[0]
`
	result := parseAndAnalyze(t, "backend_region_checkpoints.elisa", src)
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
	region scratch(1024)
	mark scratch as outer
	stable: i32& = new[scratch] seed + 1
	mark scratch as inner
	temp: i32& = new[scratch] seed + 2
	restore scratch from inner
	kept: i32 = stable[0]
	restore scratch from outer
	reset scratch
	fresh: i32& = new[scratch] seed + 3
	return kept + fresh[0]
`
	result := parseAndAnalyze(t, "backend_nested_region_checkpoints.elisa", src)
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
	return fallback
`
	result := parseAndAnalyze(t, "backend_enum_match.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%MaybeInt = type { i32, [2 x i64] }",
		"define %MaybeInt @make_pair()",
		"define i64 @unwrap_or(%MaybeInt",
		"switch i32 %match.tag.value",
		"store i32 2",
		"extractvalue { i64, i64 }",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersMatchStatementsWithEnumPayloads(t *testing.T) {
	src := `enum MaybeInt:
	None
	Some(int)
	Pair(int, int)

def unwrap_or(value: MaybeInt, fallback: int) -> int:
	match value:
		MaybeInt.None:
			return fallback
		MaybeInt.Some(inner):
			return inner
		MaybeInt.Pair(left, right):
			return left + right
	return fallback
`
	result := parseAndAnalyze(t, "backend_enum_match_stmt_payloads.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%MaybeInt = type { i32, [2 x i64] }",
		"define i64 @unwrap_or(%MaybeInt",
		"switch i32 %match.tag.value",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersStringMatchStatementsWithTinyLiteralFastPath(t *testing.T) {
	src := `def classify(text: StringView) -> int:
	match text:
		"if":
			return 1
		"local":
			return 2
		_:
			return 0
	return 0
`
	result := parseAndAnalyze(t, "backend_string_match_stmt_fast_path.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	body := functionIR(output, "classify")
	if body == "" {
		t.Fatalf("expected to find classify body, got:\n%s", output)
	}
	checks := []string{
		"define i64 @classify(%StringView",
		"extractvalue %StringView",
		"load i8, ptr",
		"getelementptr i8, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"ctx_string_view_eq", "ctx_string_views_eq", "@memcmp("} {
		if strings.Contains(body, bad) {
			t.Fatalf("expected tiny string match literals to avoid %q, got:\n%s", bad, body)
		}
	}
	if strings.Count(body, "icmp eq i8") < 2 {
		t.Fatalf("expected classify to compare literal bytes directly for both string arms, got:\n%s", body)
	}
}
func TestGenerateLLVMIRLowersStringMatchStatementsWithoutPhi(t *testing.T) {
	src := `def classify(text: StringView) -> int:
	match text:
		"do":
			return 1
		"end":
			return 2
		_:
			return 0
`
	result := parseAndAnalyze(t, "backend_string_match_stmt.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	body := functionIR(output, "classify")
	if body == "" {
		t.Fatalf("expected to find classify body, got:\n%s", output)
	}
	checks := []string{
		"define i64 @classify(%StringView",
		"extractvalue %StringView",
		"load i8, ptr",
		"getelementptr i8, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"phi i64", "ctx_string_view_eq", "ctx_string_views_eq", "@memcmp("} {
		if strings.Contains(body, bad) {
			t.Fatalf("expected statement-form tiny string match to avoid %q, got:\n%s", bad, body)
		}
	}
	if strings.Count(body, "icmp eq i8") < 2 {
		t.Fatalf("expected classify to compare literal bytes directly for both string arms, got:\n%s", body)
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
	match value:
		Outer.Wrap(Inner.A(inner)):
			return inner
		Outer.Wrap(Inner.B):
			return 0
		Outer.Empty:
			return -1
	return 0
`
	result := parseAndAnalyze(t, "backend_nested_match_patterns.elisa", src)
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
