package semantic_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeRejectsMismatchedFixedArrayLiteralLength(t *testing.T) {
	src := `def bad() -> void:
	values: i32[2] = [1, 2, 3]
`
	_, errs := parseAndAnalyze(t, "fixed_array_literal_length_mismatch.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "array literal expects 2 elements, got 3") {
		t.Fatalf("expected array-length diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsStringSliceSyntax(t *testing.T) {
	src := `def middle(text: cstr[row]) -> sview:
	return text[1:3]
`
	_, errs := parseAndAnalyze(t, "string_slice_syntax.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsAssigningToDStrLenField(t *testing.T) {
	src := `def bad(text: cstr[row]) -> void:
	text.len <- 1
`
	_, errs := parseAndAnalyze(t, "cstr_len_assign.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "field \"len\" is immutable") {
		t.Fatalf("expected immutable len-field diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsAssigningToSViewIndex(t *testing.T) {
	src := `def bad(view: sview) -> void:
	view[0] <- 1
`
	_, errs := parseAndAnalyze(t, "ctx_string_view_index_assignment.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot assign to string view index") {
		t.Fatalf("expected string view index assignment diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsImplicitDArrayShapeParams(t *testing.T) {
	src := `def identity[T](array: darray[T, shape_in]) -> darray[T, shape_in]:
	return array

def keep(array: darray[i32, row]) -> darray[i32, row]:
	return identity(array)
`
	_, errs := parseAndAnalyze(t, "implicit_darray_shape_params.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsShapeErasingDArrayShorthand(t *testing.T) {
	src := `def keep_surface(values: darray[i32]) -> darray[i32]:
	return values

def erase_explicit(values: darray[i32, row]) -> darray[i32]:
	return values
`
	_, errs := parseAndAnalyze(t, "darray_shorthand_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsRecoveringExplicitShapeFromShorthand(t *testing.T) {
	src := `def bad(values: darray[i32]) -> darray[i32, row]:
	return values
`
	_, errs := parseAndAnalyze(t, "darray_shorthand_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects darray[i32, row], got darray[i32]") {
		t.Fatalf("expected omitted-shape to explicit-shape rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeDArrayUsesDynArrayRuntimeFields(t *testing.T) {
	src := `def needs_grow[T](array: darray[T, row]&) -> bool:
	return array.count >= array.capacity
`
	_, errs := parseAndAnalyze(t, "darray_runtime_field_access.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeDynArrayRuntimeBridgeWorksBothDirections(t *testing.T) {
	fixturePath := filepath.Join(repoRootFromTestFile(t), "compiler", "runtime", "elisacore_std", "dynarray_runtime_bridge_roundtrip.elisa")
	src := `def take_raw[T](values: DynArray[T]) -> void:
	pass

def take_logical[T](values: darray[T, shape_in]) -> void:
	pass

def roundtrip(values: darray[i32, row], raw: DynArray[i32]) -> darray[i32, row]:
	take_raw(values)
	take_logical(raw)
	bridged: DynArray[i32] = values
	return bridged
`
	_, errs := parseAndAnalyze(t, fixturePath, src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsMismatchedDArrayShapes(t *testing.T) {
	src := `def bad(array: darray[i32, row]) -> darray[i32, col]:
	return array
`
	_, errs := parseAndAnalyze(t, "mismatched_darray_shapes.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects darray[i32, col], got darray[i32, row]") {
		t.Fatalf("expected dynamic shape mismatch diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsImplicitDStrShapeParams(t *testing.T) {
	src := `def echo(text: cstr[shape_text]) -> cstr[shape_text]:
	return text

def keep(text: cstr[row]) -> cstr[row]:
	return echo(text)
`
	_, errs := parseAndAnalyze(t, "implicit_cstr_shape_params.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsShapeErasingDStrShorthand(t *testing.T) {
	src := `def keep_surface(text: cstr) -> cstr:
	return text

def erase_explicit(text: cstr[row]) -> cstr:
	return text
`
	_, errs := parseAndAnalyze(t, "cstr_shorthand_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsShapeErasingSViewShorthand(t *testing.T) {
	src := `def keep_surface(text: sview) -> sview:
	return text

def slice_prefix(text: cstr[row]) -> sview:
	prefix: sview = text[0:1]
	return prefix
`
	_, errs := parseAndAnalyze(t, "sview_shorthand_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsRecoveringExplicitShapeFromDStrShorthand(t *testing.T) {
	src := `def bad(text: cstr) -> cstr[row]:
	return text
`
	_, errs := parseAndAnalyze(t, "cstr_shorthand_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects cstr[row], got cstr") {
		t.Fatalf("expected omitted-shape DStr to explicit-shape rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeFormatsBareSViewShorthandInDiagnostics(t *testing.T) {
	src := `def bad() -> void:
	text: sview = 1
`
	_, errs := parseAndAnalyze(t, "sview_shorthand_mismatch.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, `variable "text" expects sview, got int`) {
		t.Fatalf("expected bare sview diagnostic, got:\n%s", all)
	}
	if strings.Contains(all, "sview[") {
		t.Fatalf("expected erased sview diagnostic spelling, got:\n%s", all)
	}
}
func TestAnalyzeFormatsWhileConditionSViewUsingSurfaceNames(t *testing.T) {
	src := `def bad(text: cstr[row]) -> void:
	while text[0:1]:
		pass
`
	_, errs := parseAndAnalyze(t, "while_sview_surface_diagnostic.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "while condition must be bool, got sview[0, 1]") {
		t.Fatalf("expected surface sview while diagnostic, got:\n%s", all)
	}
	if strings.Contains(all, "StringView") {
		t.Fatalf("expected StringView to stay out of user-facing flow diagnostics, got:\n%s", all)
	}
}
func TestAnalyzeFormatsMatchDViewUsingSurfaceNames(t *testing.T) {
	src := `def bad(values: view[i32]) -> int:
	match values:
		Token.Region:
			return 0
	return 0
`
	_, errs := parseAndAnalyze(t, "match_dview_surface_diagnostic.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "unsupported top-level sequence match pattern *ast.MatchVariantPattern") {
		t.Fatalf("expected surface view match diagnostic, got:\n%s", all)
	}
	if strings.Contains(all, "DynArrayView") {
		t.Fatalf("expected DynArrayView to stay out of user-facing flow diagnostics, got:\n%s", all)
	}
}
func TestAnalyzeFormatsCompareSViewUsingSurfaceNames(t *testing.T) {
	src := `def bad(text: cstr[row]) -> bool:
	return text[0:1] == 1
`
	_, errs := parseAndAnalyze(t, "compare_sview_surface_diagnostic.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "cannot compare sview[0, 1] and int") {
		t.Fatalf("expected surface sview compare diagnostic, got:\n%s", all)
	}
	if strings.Contains(all, "StringView") {
		t.Fatalf("expected StringView to stay out of user-facing expression diagnostics, got:\n%s", all)
	}
}
func TestAnalyzeFormatsIsVariantSViewUsingSurfaceNames(t *testing.T) {
	src := `enum Flag:
	On

def bad(text: cstr[row]) -> bool:
	return text[0:1] is Flag.On
`
	_, errs := parseAndAnalyze(t, "is_variant_sview_surface_diagnostic.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "is requires an enum value for variant tests, got sview[0, 1]") {
		t.Fatalf("expected surface sview is-variant diagnostic, got:\n%s", all)
	}
	if strings.Contains(all, "StringView") {
		t.Fatalf("expected StringView to stay out of user-facing expression diagnostics, got:\n%s", all)
	}
}
func TestAnalyzeFormatsSafeChainDViewUsingSurfaceNames(t *testing.T) {
	src := `def bad(values: darray[i32, 4]) -> void:
	_ = values[0:4]?.count
`
	_, errs := parseAndAnalyze(t, "safe_chain_dview_surface_diagnostic.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "optional chaining receiver requires an optional or nullable reference (refstate fact nullable), got view[i32]") {
		t.Fatalf("expected surface view safe-chain diagnostic, got:\n%s", all)
	}
	if strings.Contains(all, "DynArrayView") {
		t.Fatalf("expected DynArrayView to stay out of user-facing expression diagnostics, got:\n%s", all)
	}
}
func TestAnalyzeFormatsConstEnumStorageSViewUsingSurfaceNames(t *testing.T) {
	src := `const enum Tok of sview:
	Value = 1
`
	_, errs := parseAndAnalyze(t, "const_enum_sview_storage_surface_diagnostic.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "const enum \"Tok\" storage type must be an explicit integer type, got sview") {
		t.Fatalf("expected surface sview const-enum storage diagnostic, got:\n%s", all)
	}
	if strings.Contains(all, "StringView") {
		t.Fatalf("expected StringView to stay out of user-facing analyzer diagnostics, got:\n%s", all)
	}
}
func TestAnalyzeFormatsGuardNonnullSViewUsingSurfaceNames(t *testing.T) {
	src := `@guard_nonnull(text)
def has_text(text: sview) -> bool:
	return true
`
	_, errs := parseAndAnalyze(t, "guard_nonnull_sview_surface_diagnostic.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "@guard_nonnull on function \"has_text\" requires a nullable reference or optional parameter, got sview") {
		t.Fatalf("expected surface sview guard_nonnull diagnostic, got:\n%s", all)
	}
	if strings.Contains(all, "StringView") {
		t.Fatalf("expected StringView to stay out of user-facing annotation diagnostics, got:\n%s", all)
	}
}
func TestAnalyzeFormatsInterfaceBoundTypeArgSViewUsingSurfaceNames(t *testing.T) {
	src := `struct BuilderTag:
	tag: int

protocol Builder:
	type State
	def state() -> State

impl Builder for BuilderTag:
	type State = int

	def state() -> int:
		return 1

def build[B: Builder]() -> B.State:
	return B.state()

def bad() -> int:
	return build[sview]()
`
	_, errs := parseAndAnalyze(t, "interface_bound_sview_type_arg_surface_diagnostic.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "type \"sview\" does not satisfy required interface fact \"Builder\" for type argument") {
		t.Fatalf("expected surface sview interface-bound diagnostic, got:\n%s", all)
	}
	if strings.Contains(all, "StringView") {
		t.Fatalf("expected StringView to stay out of user-facing generic diagnostics, got:\n%s", all)
	}
}
func TestAnalyzeDStrRuntimeBridgeWorksBothDirections(t *testing.T) {
	src := `def take_raw(text: u8&) -> void:
	pass

def take_logical(text: cstr[shape_text]) -> void:
	pass

def roundtrip(text: cstr[row], raw: u8&) -> cstr[row]:
	take_raw(text)
	take_logical(raw)
	bridged: cstr[row] = raw
	raw_value: u8& = text
	return raw_value
`
	_, errs := parseAndAnalyze(t, "cstr_runtime_bridge_roundtrip.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsLegacyUppercaseBuiltinTypes(t *testing.T) {
	src := `def bad_array(values: DArray[i32]) -> void:
	pass

def bad_array_view(values: DArrayView[i32]) -> void:
	pass

def bad_str(text: DStr[row]) -> void:
	pass

def bad_dict(values: Dict[cstr, i32]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "dynamic_shape_arity.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	for _, want := range []string{
		"legacy built-in \"DArray\" has been replaced; use \"darray\" instead",
		"legacy built-in \"DArrayView\" has been replaced; use \"view\" instead",
		"legacy built-in \"DStr\" has been replaced; use \"cstr\" instead",
		"legacy built-in \"Dict\" has been replaced; use \"dict\" instead",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected legacy-builtin diagnostic containing %q, got:\n%s", want, all)
		}
	}
}
func TestAnalyzeRejectsLegacyStringBuiltinAliases(t *testing.T) {
	src := `def bad_fixed(text: string[5]) -> void:
	pass

def bad_dynamic(text: cstring[row]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "legacy_string_aliases.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "legacy built-in \"string\" has been replaced; use \"str\" instead") || !strings.Contains(all, "legacy built-in \"cstring\" has been replaced; use \"cstr\" instead") {
		t.Fatalf("expected legacy string alias diagnostics, got:\n%s", all)
	}
}
func TestAnalyzeRejectsRemovedDListTypes(t *testing.T) {
	src := `def bad_list(values: DList[i32, row]) -> void:
	pass

def bad_list_view(view: DListView[i32]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "removed_dlist_surface.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "DList has been removed from the language") || !strings.Contains(all, "DListView has been removed from the language") {
		t.Fatalf("expected removed-DList diagnostics, got:\n%s", all)
	}
}
func TestAnalyzeDArrayViewUsesDynArrayViewRuntimeFields(t *testing.T) {
	src := `def non_empty[T](view: view[T]) -> bool:
	return view.len > 0 and view.elem_size > 0
`
	_, errs := parseAndAnalyze(t, "darray_view_runtime_field_access.elisa", src)
	requireNoErrors(t, errs)
}
