package semantic_test

import (
	"llcontext/src/semantic"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeRejectsLegacyWildcardErrorSetShorthand(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

extern read_value() -> int error[FileError.*]
`
	_, errs := parseAndAnalyze(t, "error_set_wildcard_mixed.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "error[Set.*] is no longer supported; use error[Set] instead") {
		t.Fatalf("expected wildcard migration diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsTryPropagationWhenDestinationMissesErrorTags(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error AppError:
	NotFound

extern read_value() -> int error[FileError.NotFound, ...]

def bubble() -> int error[AppError.NotFound, ...]:
	return try read_value()
`
	_, errs := parseAndAnalyze(t, "error_set_widening_rejects_missing_tags.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot propagate FileError from a function returning AppError") {
		t.Fatalf("expected propagation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeCanonicalizesTryDestinationInDiagnostics(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout

error AppError:
	Busy

extern read_value() -> int error[AppError]

def bubble() -> int error[NetworkError, FileError]:
	return try read_value()
`
	_, errs := parseAndAnalyze(t, "error_canonical_try_diag.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot propagate AppError from a function returning error[FileError, NetworkError]") {
		t.Fatalf("expected canonical try destination diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsRaiseOutsideErrorUnionFunction(t *testing.T) {
	src := `error IoError:
	NotFound

def bad() -> int:
	raise IoError.NotFound
	return 0
`
	_, errs := parseAndAnalyze(t, "raise_outside_error_union.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "raise requires the current function to return an error union") {
		t.Fatalf("expected raise diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsLegacyPipeErrorSyntax(t *testing.T) {
	src := `error IoError:
	NotFound

extern read_file(path: u8&) -> int | IoError
`
	_, errs := parseAndAnalyze(t, "legacy_pipe_error_syntax.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected parser error, got none")
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "legacy fallible return syntax `T | ErrorSet` is no longer supported") {
		t.Fatalf("expected legacy syntax migration diagnostic, got:\n%s", joined)
	}
	if !strings.Contains(joined, "use `T error[SomeSet]` instead") {
		t.Fatalf("expected migration guidance, got:\n%s", joined)
	}
}
func TestAnalyzeRejectsTryOnNonFallibleExpression(t *testing.T) {
	src := `def bad() -> int:
	value: int = try 7
	return value
`
	_, errs := parseAndAnalyze(t, "try_on_non_fallible.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "try requires a fallible expression") {
		t.Fatalf("expected try diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsElseOnNonNullableReference(t *testing.T) {
	src := `def bad(value: u8&) -> u8&:
	return value else "" as u8&
`
	_, errs := parseAndAnalyze(t, "else_on_nonnullable_ref.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "else recovery requires an optional or nullable reference (refstate fact nullable)") {
		t.Fatalf("expected else recovery diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsRuntimeBackedArrayAndViewIndexing(t *testing.T) {
	src := `struct DynArray[T]:
	items: mutable T&?
	count: mutable usize
	capacity: mutable usize

struct DynArrayView:
	items: mutable void&?
	count: mutable usize

def read_array(values: darray[i32, row]) -> i32:
	return values[0]

def read_view(view: dview[i32]) -> i32:
	return view[0]
`
	_, errs := parseAndAnalyze(t, "runtime_backed_array_index.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsDStrIndexingAsChar(t *testing.T) {
	src := `def read_codepoint(text: cstr[row]) -> char:
	return text[0]
`
	result, errs := parseAndAnalyze(t, "runtime_backed_cstr_index.llcontext", src)
	requireNoErrors(t, errs)
	fn, ok := result.GlobalScope.Lookup("read_codepoint")
	if !ok {
		t.Fatal("expected read_codepoint symbol")
	}
	ft, ok := fn.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", fn.Type)
	}
	if ft.Return.String() != "char" {
		t.Fatalf("expected return type char, got %s", ft.Return.String())
	}
}
func TestAnalyzeRejectsAssigningToDStrIndex(t *testing.T) {
	src := `def bad(text: cstr[row]) -> void:
	text[0] <- 1
`
	_, errs := parseAndAnalyze(t, "cstr_index_assignment.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot assign to string index") {
		t.Fatalf("expected string index assignment diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsSViewIndexingAsChar(t *testing.T) {
	src := `def read_codepoint(view: sview) -> char:
	return view[0]
`
	result, errs := parseAndAnalyze(t, "ctx_string_view_index.llcontext", src)
	requireNoErrors(t, errs)
	fn, ok := result.GlobalScope.Lookup("read_codepoint")
	if !ok {
		t.Fatal("expected read_codepoint symbol")
	}
	ft, ok := fn.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", fn.Type)
	}
	if ft.Return.String() != "char" {
		t.Fatalf("expected return type char, got %s", ft.Return.String())
	}
}
func TestAnalyzeRejectsInternalRuntimeCarrierTypesInUserFiles(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "runtime_carrier_warning.llcontext")
	src := `extern take_view(view: StringView) -> void
extern take_raw[T](values: DynArray[T]) -> void
extern take_window(view: DynArrayView) -> void
`
	_, errs := parseAndAnalyze(t, fixturePath, src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	for _, want := range []string{
		`internal runtime carrier type "StringView" is not supported in user-facing code; use "sview[...]" instead`,
		`internal runtime carrier type "DynArray" is not supported in user-facing code; use "darray[T, shape]" instead`,
		`internal runtime carrier type "DynArrayView" is not supported in user-facing code; use "dview[T]" instead`,
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected runtime carrier rejection %q, got:\n%s", want, all)
		}
	}
}
func TestAnalyzeAcceptsSViewAndDStrEqualityOperators(t *testing.T) {
	src := `def same_text(left: cstr[row], right: cstr[col]) -> bool:
	return left == right

def same_view_text(view: sview, text: cstr[row]) -> bool:
	return view == text

def same_text_view(text: cstr[row], view: sview) -> bool:
	return text == view

def different_views(left: sview, right: sview) -> bool:
	return left != right

def same_literal(text: cstr[row]) -> bool:
	return text == "hello"
`
	_, errs := parseAndAnalyze(t, "runtime_string_equality.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsDStrLenField(t *testing.T) {
	src := `def text_len(text: cstr[row]) -> i64:
	return text.len
`
	_, errs := parseAndAnalyze(t, "cstr_len_field.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsViewAliasForArraySlices(t *testing.T) {
	src := `def middle(values: i32[4]) -> view[i32]:
	part: view[i32] = values[1:3]
	return part
`
	_, errs := parseAndAnalyze(t, "view_alias_and_slice.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsArrayAndArrayViewSliceSyntax(t *testing.T) {
	src := `def middle(values: darray[i32, row], view: dview[i32]) -> i32:
	part: dview[i32] = values[1:3]
	sub: dview[i32] = view[0:1]
	return part[0] + sub[0]
`
	_, errs := parseAndAnalyze(t, "array_and_array_view_slice.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsFixedArraySliceSyntax(t *testing.T) {
	src := `def middle(values: i32[4], view: i32[4]&) -> i32:
	part: view[i32] = values[1:3]
	sub: view[i32] = view[0:2]
	return part[0] + sub[1]
`
	_, errs := parseAndAnalyze(t, "fixed_array_slice.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsNestedCollectionAccessOnReturnedValues(t *testing.T) {
	src := `extern make_array() -> darray[i32, row]
extern make_array_view() -> dview[i32]

def read_array_index() -> i32:
	return make_array()[1]

def read_array_slice_index() -> i32:
	return make_array()[1:3][0]

def read_array_view_index() -> i32:
	return make_array_view()[0]
`
	_, errs := parseAndAnalyze(t, "nested_collection_access_returns.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsArrayLiteralWithInferredLocalAndViewSlice(t *testing.T) {
	src := `def middle() -> int:
	values = [1, 2, 3, 4]
	part: view[int] = values[1:3]
	return part[0]
`
	_, errs := parseAndAnalyze(t, "array_literal_inferred_local.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzePreservesStackStorageForAddressableLocalSubobjects(t *testing.T) {
	src := `struct ScratchPair:
	left: mutable int
	right: mutable int


struct ScratchHolder:
	pair: mutable ScratchPair


error ProbeError:
	Zero


def checked_pair(slot: stack ScratchPair&) -> int error[ProbeError]:
		if slot.left == 0:
			raise ProbeError.Zero
		return slot.left + slot.right


def from_local_field() -> int:
		holder: ScratchHolder = ScratchHolder(ScratchPair(1, 2))
		return try checked_pair(&holder.pair) else 0


def from_local_array_elem() -> int:
		values: ScratchPair[2] = [ScratchPair(1, 2), ScratchPair(5, 6)]
		return try checked_pair(&values[1]) else 0
`
	_, errs := parseAndAnalyze(t, "stack_storage_local_subobjects.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsAllocatorOwnershipFixturePatterns(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "Code", "test_programs", "allocator_ownership.llcontext"), map[string]bool{})
	_, errs := parseAndAnalyze(t, "allocator_ownership.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzePinsArenaBuiltinPermissionContracts(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "compiler", "runtime", "llcontext_std", "arena.llcontext"), map[string]bool{})
	result, errs := parseAndAnalyze(t, "arena.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireDeclaredFunctionPermissionRefs(t, result, "malloc", "Memory.Allocate")
	requireDeclaredFunctionPermissionRefs(t, result, "free", "Memory.Release")
	requireFunctionPermissionRefs(t, result, "assert", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "sfree", "Memory.Release")
	requireFunctionPermissionRefs(t, result, "new_region_with_owner", "Memory.Allocate", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "new_region", "Memory.Allocate", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "free_region", "Memory.Release", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "arena_alloc", "Memory.Allocate", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "arena_realloc", "Memory.Allocate", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "arena_free", "Memory.Release", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "arena_trim", "Memory.Release", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "arena_vsprintf", "Memory.Allocate", "Console.Format", "Abort.Panic")
}
func TestAnalyzePinsArenaHeapPointerContracts(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "compiler", "runtime", "llcontext_std", "arena.llcontext"), map[string]bool{})
	result, errs := parseAndAnalyze(t, "arena.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "malloc", "heap mutable void&?")
	requireFunctionReturnTypeString(t, result, "sfree", "heap T!")
	requireFunctionReturnTypeString(t, result, "new_region_with_owner", "heap mutable Region&")
	requireFunctionReturnTypeString(t, result, "new_region", "heap mutable Region&")
	requireFunctionReturnTypeString(t, result, "arena_alloc", "heap mutable void&")
	requireFunctionReturnTypeString(t, result, "arena_realloc", "heap mutable void&")
	requireFunctionReturnTypeString(t, result, "arena_strdup", "heap mutable u8&")
	requireFunctionReturnTypeString(t, result, "arena_memdup", "heap mutable void&")
	requireFunctionReturnTypeString(t, result, "arena_vsprintf", "heap mutable u8&")
}
func TestAnalyzePinsCollectionsDictContracts(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "compiler", "runtime", "llcontext_std", "collections.llcontext"), map[string]bool{})
	result, errs := parseAndAnalyze(t, "collections.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "arena_dict_new__i64", "dict[cstr[key_shape], i64]")
	requireFunctionReturnTypeString(t, result, "arena_dict_get__i64", "mutable i64&?")
}
func TestAnalyzePinsStoresHeapPointerContracts(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "compiler", "runtime", "llcontext_std", "stores.llcontext"), map[string]bool{})
	result, errs := parseAndAnalyze(t, "stores.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ctx_packed_store_state_new", "heap mutable void&")
}
func TestAnalyzePinsRuntimePreludeBuiltinExternPermissionContracts(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "compiler", "runtime", "llcontext_std", "contextlang_runtime_prelude.llcontext"), map[string]bool{})
	result, errs := parseAndAnalyze(t, "contextlang_runtime_prelude.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireDeclaredFunctionPermissionRefs(t, result, "snprintf", "Console.Format")
	requireDeclaredFunctionPermissionRefs(t, result, "puts", "Console.Write")
	requireDeclaredFunctionPermissionRefs(t, result, "fprintf", "Console")
	requireDeclaredFunctionPermissionRefs(t, result, "exit", "Abort.Exit")
}
func TestAnalyzePinsRuntimePreludeHeapPointerContracts(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "compiler", "runtime", "llcontext_std", "contextlang_runtime_prelude.llcontext"), map[string]bool{})
	result, errs := parseAndAnalyze(t, "contextlang_runtime_prelude.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "alloc_perm", "heap mutable void&")
	requireFunctionReturnTypeString(t, result, "alloc_scratch", "heap mutable void&")
	requireFunctionReturnTypeString(t, result, "intern_small_string", "heap u8&")
	requireFunctionReturnTypeString(t, result, "int_to_string_into", "heap u8&")
	requireFunctionReturnTypeString(t, result, "char_to_string_into", "heap u8&")
	requireFunctionReturnTypeString(t, result, "string_builder_new", "heap mutable StringBuilder&")
	requireFunctionReturnTypeString(t, result, "string_builder_append", "heap mutable StringBuilder&")
	requireFunctionReturnTypeString(t, result, "string_builder_finish", "heap u8&")
}
func TestAnalyzePinsRuntimeStage1BuiltinPermissionContracts(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "compiler", "runtime", "llcontext_std", "contextlang_runtime.llcontext"), map[string]bool{})
	result, errs := parseAndAnalyze(t, "contextlang_runtime.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionPermissionRefs(t, result, "int_to_string", "Memory.Allocate", "Console.Format", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "int_to_string_scratch", "Memory.Allocate", "Console.Format", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "char_to_string", "Memory.Allocate", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "char_to_string_scratch", "Memory.Allocate", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "rt_concat2", "Memory.Allocate", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "rt_string_builder_new", "Memory.Allocate", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "rt_string_builder_append", "Memory.Allocate", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "rt_string_builder_finish", "Memory.Allocate", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "rt_int_to_string", "Memory.Allocate", "Console.Format", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "rt_char_to_string", "Memory.Allocate", "Abort.Panic")
	requireFunctionPermissionRefs(t, result, "rt_puts", "Console.Write")
	requireFunctionReturnTypeString(t, result, "int_to_string", "heap u8&")
	requireFunctionReturnTypeString(t, result, "int_to_string_scratch", "heap u8&")
	requireFunctionReturnTypeString(t, result, "char_to_string", "heap u8&")
	requireFunctionReturnTypeString(t, result, "char_to_string_scratch", "heap u8&")
	requireFunctionReturnTypeString(t, result, "rt_string_builder_new", "heap mutable StringBuilder&")
	requireFunctionReturnTypeString(t, result, "rt_string_builder_append", "heap mutable StringBuilder&")
	requireFunctionReturnTypeString(t, result, "string_view_copy", "heap u8&")
}
func TestAnalyzeAcceptsValueOptionalsAndTryElse(t *testing.T) {
	src := `def maybe_value(flag: bool) -> int?:
	if flag:
		return 7
	return null


def fallback_value(flag: bool) -> int:
	value: int? = maybe_value(flag)
	return try value else 11
`
	result, errs := parseAndAnalyze(t, "value_optionals_try_else.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "maybe_value", "int?")
	requireFunctionReturnTypeString(t, result, "fallback_value", "int")
}
func TestAnalyzeAcceptsReturnQuestionWithOptionalBindings(t *testing.T) {
	src := `def maybe_value(value: i64, keep: bool) -> i64?:
	if keep:
		return value
	return null


def in_range(lower: i64?, upper: i64?, value: i64?) -> bool?:
	return? with lower_value = lower, upper_value = upper, value_int = value:
		value_int >= lower_value and value_int <= upper_value
	return null


def call_range(keep: bool) -> bool?:
	return in_range(maybe_value(1, keep), maybe_value(5, keep), maybe_value(3, keep))
`
	result, errs := parseAndAnalyze(t, "return_question_with_optional_bindings.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "in_range", "bool?")
	requireFunctionReturnTypeString(t, result, "call_range", "bool?")
}
func TestAnalyzeAcceptsErrorUnionTryDefaultShorthand(t *testing.T) {
	src := `error FileError:
	NotFound

extern read_value(flag: bool) -> int error[FileError]

def fallback_value(flag: bool) -> int:
	return try? read_value(flag) default 11
`
	result, errs := parseAndAnalyze(t, "error_union_try_default.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "fallback_value", "int")
}
func TestAnalyzeRejectsOptionalTryDefaultShorthand(t *testing.T) {
	src := `def maybe_value(flag: bool) -> int?:
	if flag:
		return 7
	return null


def bad(flag: bool) -> int:
	return try? maybe_value(flag) default 11
`
	_, errs := parseAndAnalyze(t, "optional_try_default.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "try? ... default requires an error union") {
		t.Fatalf("expected try default shorthand diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsTryOptionalWithoutElse(t *testing.T) {
	src := `def maybe_value(flag: bool) -> int?:
	if flag:
		return 7
	return null


def bad(flag: bool) -> int:
	return try maybe_value(flag)
`
	_, errs := parseAndAnalyze(t, "value_optionals_try_without_else.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "try without else requires an error union") {
		t.Fatalf("expected optional try-without-else diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsOptionalNullChecksAndSmartCastUse(t *testing.T) {
	src := `struct Box:
	value: int


def maybe_box(flag: bool) -> Box?:
	if flag:
		return Box(7)
	return null


def unwrap_or(flag: bool) -> int:
	value: Box? = maybe_box(flag)
	if value == null:
		return 11
	return value.value
`
	result, errs := parseAndAnalyze(t, "value_optionals_smart_cast.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "unwrap_or", "int")
}
func TestAnalyzeAcceptsTypedFixedArrayLiteralInitialization(t *testing.T) {
	src := `def first() -> i32:
	values: i32[3] = [1, 2, 3]
	return values[0]
`
	_, errs := parseAndAnalyze(t, "typed_array_literal_init.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsEmptyArrayLiteralWithoutContext(t *testing.T) {
	src := `def bad() -> void:
	values = []
`
	_, errs := parseAndAnalyze(t, "empty_array_literal_needs_context.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "empty list literal requires an expected array or darray type") {
		t.Fatalf("expected empty-array context diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
