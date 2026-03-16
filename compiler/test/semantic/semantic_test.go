package semantic_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"llcontext/src/lexer"
	"llcontext/src/parser"
	"llcontext/src/semantic"
)

func parseAndAnalyze(t *testing.T, filename string, src string) (*semantic.Result, []string) {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) > 0 {
		return nil, errs
	}
	result := semantic.Analyze(file)
	return result, result.Errors()
}

func requireNoErrors(t *testing.T, errs []string) {
	t.Helper()
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got:\n%s", strings.Join(errs, "\n"))
	}
}

func repoRootFromTestFile(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

func loadSourceWithIncludes(t *testing.T, filename string, seen map[string]bool) string {
	t.Helper()
	abs, err := filepath.Abs(filename)
	if err != nil {
		t.Fatalf("failed to resolve %s: %v", filename, err)
	}
	if seen[abs] {
		t.Fatalf("cyclic include detected for %s", abs)
	}
	seen[abs] = true
	defer delete(seen, abs)

	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("failed to read %s: %v", abs, err)
	}

	lines := strings.Split(string(raw), "\n")
	var out strings.Builder
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if includePath, ok := parseIncludeDirective(trimmed); ok {
			out.WriteString(loadSourceWithIncludes(t, filepath.Join(filepath.Dir(abs), includePath), seen))
			if out.Len() == 0 || !strings.HasSuffix(out.String(), "\n") {
				out.WriteByte('\n')
			}
			continue
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func parseIncludeDirective(line string) (string, bool) {
	if !strings.HasPrefix(line, "# include ") {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, "# include "))
	if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
		return "", false
	}
	return rest[1 : len(rest)-1], true
}

func TestAnalyzeValidInlineProgram(t *testing.T) {
	src := `repr(c) struct Box:
    value: mutable int

extern make_box() -> Box&?

def read_box() -> int:
    box: mutable Box&? = make_box()
    if box == null:
        return 0
    box.value <- 7
    return box.value
`
	_, errs := parseAndAnalyze(t, "inline_valid.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeUndefinedIdentifier(t *testing.T) {
	src := `def bad() -> int:
    return missing
`
	_, errs := parseAndAnalyze(t, "undefined_ident.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(errs[0], "undefined identifier") {
		t.Fatalf("expected undefined identifier error, got %q", errs[0])
	}
}

func TestAnalyzeWrongCallArity(t *testing.T) {
	src := `extern alloc(size: usize) -> int

def use_alloc() -> int:
    return alloc()
`
	_, errs := parseAndAnalyze(t, "wrong_arity.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "expects 1 arguments, got 0") {
		t.Fatalf("expected arity diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeTypeMismatchAssignment(t *testing.T) {
	src := `def mismatch() -> int:
    value: mutable int = true
    return value
`
	_, errs := parseAndAnalyze(t, "type_mismatch.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "expects int, got bool") {
		t.Fatalf("expected assignment mismatch diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsNullIntoNonNullRef(t *testing.T) {
	src := `repr(c) struct Box:
    value: int

def bad() -> void:
    box: Box& = null
`
	_, errs := parseAndAnalyze(t, "nonnull_ref_rejects_null.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "expects Box&, got null") {
		t.Fatalf("expected non-null ref rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzePointerTypestateBranches(t *testing.T) {
	src := `repr(c) struct Box:
    value: mutable int

extern alloc_box() -> Box&?
extern sfree_box(box: Box&) -> Box!

def release_box() -> void:
    box: mutable Box&? = alloc_box()
    if box != null:
        box as ! <- sfree_box(box)

def missing_box() -> Box!:
    return null
`
	_, errs := parseAndAnalyze(t, "pointer_typestate.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsNullableFieldAccessWithoutProof(t *testing.T) {
	src := `repr(c) struct Box:
    value: mutable int

extern maybe_box() -> Box&?

def bad() -> int:
    box: Box&? = maybe_box()
    return box.value
`
	_, errs := parseAndAnalyze(t, "nullable_field_access.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "field access requires proven non-null reference") {
		t.Fatalf("expected nullable field access rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeGuardClauseRefinesAfterReturn(t *testing.T) {
	src := `repr(c) struct Box:
    value: mutable int

extern maybe_box() -> Box&?

def read_box() -> int:
    box: Box&? = maybe_box()
    if box == null:
        return 0
    return box.value
`
	_, errs := parseAndAnalyze(t, "guard_clause_refinement.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsBuiltinSurfaceCollectionTypes(t *testing.T) {
	src := `extern take_array(values: array[i32, 4]) -> void
extern take_darray(values: darray[i32, row]) -> void
extern take_dstr(text: dstr[row]) -> void
extern take_view(values: view[i32, 0u, 2u]) -> void
extern take_sview(text: sview[1, 4]) -> void

def use(values: array[i32, 4], dyn: darray[i32, row], text: str[5], dyn_text: dstr[row]) -> char:
	sub_array: view[i32, 0u, 2u] = values[0u:2u]
	sub_text: sview[1, 4] = text[1:4]
	dyn_sub: sview[0, 1] = dyn_text[0:1]
	take_array(values)
	take_darray(dyn)
	take_dstr(dyn_text)
	take_view(sub_array)
	take_sview(sub_text)
	take_sview(dyn_sub)
	return text[0]
`
	_, errs := parseAndAnalyze(t, "builtin_surface_types.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsCharCastAndComparisonFromStringIndexing(t *testing.T) {
	src := `def first_code(text: str[4]) -> i64:
	ch: char = text[0]
	return ch.i64()

def same_head(left: dstr[row], right: dstr[col]) -> bool:
	return left[0] == right[0]
`
	_, errs := parseAndAnalyze(t, "char_cast_and_compare.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsStandaloneCharLocalsParamsAndCasts(t *testing.T) {
	src := `def normalize(code: i64) -> char:
	ch: char = code.char()
	if ch == 0.char():
		return 65.char()
	return ch

def bump(ch: char) -> i64:
	return (ch + 1).i64()
`
	_, errs := parseAndAnalyze(t, "standalone_char_values.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeTernaryRefinesNullablePointerBranch(t *testing.T) {
	src := `def choose_text(value: u8&?) -> u8&:
    return value if value != null else ""
`
	_, errs := parseAndAnalyze(t, "ternary_refinement.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsNullableToNonNullCastWithoutProof(t *testing.T) {
	src := `repr(c) struct Box:
    value: int

extern maybe_box() -> Box&?

def bad() -> Box&:
    box: Box&? = maybe_box()
    return box.Box&()
`
	_, errs := parseAndAnalyze(t, "nonnull_cast_rejection.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "invalid cast from Box&? to Box&") {
		t.Fatalf("expected invalid cast diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsReferenceComparisons(t *testing.T) {
	src := `repr(c) struct Box:
	value: int

extern maybe_box() -> Box&?

def is_missing() -> bool:
	return maybe_box() == null

def is_present() -> bool:
	return maybe_box() != null

def same_box(left: Box&, right: Box&) -> bool:
	return left == right
`
	_, errs := parseAndAnalyze(t, "reference_comparisons.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsEquivalentConstArrayShapes(t *testing.T) {
	src := `const N: usize = 4

def same_shape(buf: u8[N]) -> u8[2 + 2]:
    return buf
`
	_, errs := parseAndAnalyze(t, "equivalent_const_array_shapes.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsMismatchedFixedArrayShapes(t *testing.T) {
	src := `def bad(buf: u8[4]) -> u8[5]:
    return buf
`
	_, errs := parseAndAnalyze(t, "mismatched_fixed_array_shapes.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects u8[5], got u8[4]") {
		t.Fatalf("expected fixed-array mismatch diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsRuntimeArraySizeExpression(t *testing.T) {
	src := `def bad(n: usize) -> void:
    buf: u8[n] = zeroed
`
	_, errs := parseAndAnalyze(t, "runtime_array_size.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "array size must be a compile-time integer") {
		t.Fatalf("expected compile-time array size diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsConstantOutOfBoundsArrayIndex(t *testing.T) {
	src := `const IDX: usize = 4

def bad() -> u8:
    buf: u8[4] = zeroed
    return buf[IDX]
`
	_, errs := parseAndAnalyze(t, "constant_oob_array_index.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "constant index 4 out of bounds for u8[4]") {
		t.Fatalf("expected out-of-bounds diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsConstantInBoundsArrayIndex(t *testing.T) {
	src := `const IDX: usize = 3

def read_last() -> u8:
    buf: u8[4] = zeroed
    return buf[IDX]
`
	_, errs := parseAndAnalyze(t, "constant_in_bounds_array_index.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsModuloExpressionsAndConstModulo(t *testing.T) {
	src := `const IDX: usize = 10 % 3

def remainder(left: i32, right: i32) -> i32:
    return left % right

def read_second() -> u8:
    buf: u8[4] = zeroed
    return buf[IDX]
`
	_, errs := parseAndAnalyze(t, "modulo_expr_and_const.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsPointerArithmetic(t *testing.T) {
	src := `def advance(ptr: u8&, offset: usize) -> u8&:
	return ptr + offset

def advance_commutative(offset: usize, ptr: u8&) -> u8&:
	return offset + ptr

def rewind(ptr: u8&, offset: usize) -> u8&:
	return ptr - offset
`
	_, errs := parseAndAnalyze(t, "pointer_arithmetic.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsRuntimeBackedArrayAndViewIndexing(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	items: mutable void&?
	count: mutable usize

def read_array(values: DArray[i32, row]) -> i32:
	return values[0]

def read_view(view: DArrayView[i32]) -> i32:
	return view[0]
`
	_, errs := parseAndAnalyze(t, "runtime_backed_array_index.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsRuntimeBackedListAndViewIndexing(t *testing.T) {
	src := `repr(c) struct CtxList:
	items: mutable void&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct CtxListView:
	items: mutable void&?
	count: mutable usize

def read_list(values: DList[i32, row]) -> i32:
	return values[0]

def read_view(view: DListView[i32]) -> i32:
	return view[0]
`
	_, errs := parseAndAnalyze(t, "runtime_backed_list_index.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsRuntimeBackedListRefIndexing(t *testing.T) {
	src := `repr(c) struct CtxList:
	items: mutable void&?
	count: mutable usize
	capacity: mutable usize

def read_list_ref(values: DList[i32, row]&) -> i32:
	return values[0]
`
	_, errs := parseAndAnalyze(t, "runtime_backed_list_ref_index.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsDStrIndexingAsChar(t *testing.T) {
	src := `def read_codepoint(text: DStr[row]) -> char:
	return text[0]
`
	result, errs := parseAndAnalyze(t, "runtime_backed_dstr_index.llcontext", src)
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
	src := `def bad(text: DStr[row]) -> void:
	text[0] <- 1
`
	_, errs := parseAndAnalyze(t, "dstr_index_assignment.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot assign to string index") {
		t.Fatalf("expected string index assignment diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsCtxStringViewIndexingAsChar(t *testing.T) {
	src := `def read_codepoint(view: CtxStringView) -> char:
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

func TestAnalyzeAcceptsRuntimeStringEqualityOperators(t *testing.T) {
	src := `def same_text(left: DStr[row], right: DStr[col]) -> bool:
	return left == right

def same_view_text(view: CtxStringView, text: DStr[row]) -> bool:
	return view == text

def same_text_view(text: DStr[row], view: CtxStringView) -> bool:
	return text == view

def different_views(left: CtxStringView, right: CtxStringView) -> bool:
	return left != right

def same_literal(text: DStr[row]) -> bool:
	return text == "hello"
`
	_, errs := parseAndAnalyze(t, "runtime_string_equality.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsDStrLenField(t *testing.T) {
	src := `def text_len(text: DStr[row]) -> i64:
	return text.len
`
	_, errs := parseAndAnalyze(t, "dstr_len_field.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsViewAliasForArraySlices(t *testing.T) {
	src := `def middle(values: i32[4]) -> view[i32]:
	part: view[i32] = values[1u:3u]
	return part
`
	_, errs := parseAndAnalyze(t, "view_alias_and_slice.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsExplicitListViewSliceSyntax(t *testing.T) {
	src := `def middle(values: DList[i32, row]) -> DListView[i32]:
	part: DListView[i32] = values[1:3]
	return part
`
	_, errs := parseAndAnalyze(t, "explicit_list_view_slice.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsArrayAndArrayViewSliceSyntax(t *testing.T) {
	src := `def middle(values: DArray[i32, row], view: DArrayView[i32]) -> i32:
	part: DArrayView[i32] = values[1u:3u]
	sub: DArrayView[i32] = view[0u:1u]
	return part[0u] + sub[0u]
`
	_, errs := parseAndAnalyze(t, "array_and_array_view_slice.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsFixedArraySliceSyntax(t *testing.T) {
	src := `def middle(values: i32[4], view: i32[4]&) -> i32:
	part: DArrayView[i32] = values[1u:3u]
	sub: DArrayView[i32] = view[0u:2u]
	return part[0u] + sub[1u]
`
	_, errs := parseAndAnalyze(t, "fixed_array_slice.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsNestedCollectionAccessOnReturnedValues(t *testing.T) {
	src := `extern make_array() -> DArray[i32, row]
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
	if !strings.Contains(strings.Join(errs, "\n"), "empty array literal requires an expected array type") {
		t.Fatalf("expected empty-array context diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsMismatchedFixedArrayLiteralLength(t *testing.T) {
	src := `def bad() -> void:
	values: i32[2] = [1, 2, 3]
`
	_, errs := parseAndAnalyze(t, "fixed_array_literal_length_mismatch.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "array literal expects 2 elements, got 3") {
		t.Fatalf("expected array-length diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsStringSliceSyntax(t *testing.T) {
	src := `def middle(text: DStr[row]) -> CtxStringView:
	return text[1:3]
`
	_, errs := parseAndAnalyze(t, "string_slice_syntax.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsAssigningToDStrLenField(t *testing.T) {
	src := `def bad(text: DStr[row]) -> void:
	text.len <- 1
`
	_, errs := parseAndAnalyze(t, "dstr_len_assign.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "field \"len\" is immutable") {
		t.Fatalf("expected immutable len-field diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsAssigningToCtxStringViewIndex(t *testing.T) {
	src := `def bad(view: CtxStringView) -> void:
	view[0] <- 1
`
	_, errs := parseAndAnalyze(t, "ctx_string_view_index_assignment.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot assign to string view index") {
		t.Fatalf("expected string view index assignment diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsImplicitDArrayShapeParams(t *testing.T) {
	src := `def identity[T](array: DArray[T, shape_in]) -> DArray[T, shape_in]:
    return array

def keep(array: DArray[i32, row]) -> DArray[i32, row]:
    return identity(array)
`
	_, errs := parseAndAnalyze(t, "implicit_darray_shape_params.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeDArrayUsesDynArrayRuntimeFields(t *testing.T) {
	src := `def needs_grow[T](array: DArray[T, row]&) -> bool:
    return array.count >= array.capacity
`
	_, errs := parseAndAnalyze(t, "darray_runtime_field_access.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeDynArrayRuntimeBridgeWorksBothDirections(t *testing.T) {
	src := `def take_raw[T](values: DynArray[T]) -> void:
	pass

def take_logical[T](values: DArray[T, shape_in]) -> void:
	pass

def roundtrip(values: DArray[i32, row], raw: DynArray[i32]) -> DArray[i32, row]:
	take_raw(values)
	take_logical(raw)
	bridged: DynArray[i32] = values
	return bridged
`
	_, errs := parseAndAnalyze(t, "dynarray_runtime_bridge_roundtrip.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsMismatchedDArrayShapes(t *testing.T) {
	src := `def bad(array: DArray[i32, row]) -> DArray[i32, col]:
    return array
`
	_, errs := parseAndAnalyze(t, "mismatched_darray_shapes.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects DArray[i32, col], got DArray[i32, row]") {
		t.Fatalf("expected dynamic shape mismatch diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsImplicitDStrShapeParams(t *testing.T) {
	src := `def echo(text: DStr[shape_text]) -> DStr[shape_text]:
    return text

def keep(text: DStr[row]) -> DStr[row]:
    return echo(text)
`
	_, errs := parseAndAnalyze(t, "implicit_dstr_shape_params.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsImplicitDListShapeParams(t *testing.T) {
	src := `def identity[T](values: DList[T, shape_in]) -> DList[T, shape_in]:
    return values

def keep(values: DList[i32, row]) -> DList[i32, row]:
    return identity(values)
`
	_, errs := parseAndAnalyze(t, "implicit_dlist_shape_params.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeDStrRuntimeBridgeWorksBothDirections(t *testing.T) {
	src := `def take_raw(text: u8&) -> void:
	pass

def take_logical(text: DStr[shape_text]) -> void:
	pass

def roundtrip(text: DStr[row], raw: u8&) -> DStr[row]:
	take_raw(text)
	take_logical(raw)
	bridged: DStr[row] = raw
	raw_value: u8& = text
	return raw_value
`
	_, errs := parseAndAnalyze(t, "dstr_runtime_bridge_roundtrip.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsWrongDynamicShapeArity(t *testing.T) {
	src := `def bad_array(x: DArray[i32]) -> void:
    pass

def bad_array_view(x: DArrayView[i32, row]) -> void:
	pass

def bad_list(x: DList[i32]) -> void:
	pass

def bad_list_view(x: DListView[i32, row]) -> void:
	pass

def bad_str(x: DStr[row, col]) -> void:
    pass
`
	_, errs := parseAndAnalyze(t, "dynamic_shape_arity.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "DArray expects 2 arguments, got 1") || !strings.Contains(all, "DArrayView expects 1 argument, got 2") || !strings.Contains(all, "DList expects 2 arguments, got 1") || !strings.Contains(all, "DListView expects 1 argument, got 2") || !strings.Contains(all, "DStr expects 1 argument, got 2") {
		t.Fatalf("expected dynamic shape arity diagnostics, got:\n%s", all)
	}
}

func TestAnalyzeDListUsesCtxListRuntimeFields(t *testing.T) {
	src := `def has_room[T](values: DList[T, row]&) -> bool:
    return values.len < values.cap
`
	_, errs := parseAndAnalyze(t, "dlist_runtime_field_access.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeDListViewUsesCtxListViewRuntimeFields(t *testing.T) {
	src := `def non_empty[T](view: DListView[T]) -> bool:
    return view.len > 0 and view.elem_size > 0
`
	_, errs := parseAndAnalyze(t, "dlist_view_runtime_field_access.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeDArrayViewUsesDynArrayViewRuntimeFields(t *testing.T) {
	src := `def non_empty[T](view: DArrayView[T]) -> bool:
    return view.len > 0u and view.elem_size > 0u
`
	_, errs := parseAndAnalyze(t, "darray_view_runtime_field_access.llcontext", src)
	requireNoErrors(t, errs)
}
