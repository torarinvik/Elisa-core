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
	src := `repr(c) struct DynArray[T]:
    items: mutable T&?
    count: mutable usize
    capacity: mutable usize

def needs_grow[T](array: DArray[T, row]&) -> bool:
    return array.count >= array.capacity
`
	_, errs := parseAndAnalyze(t, "darray_runtime_field_access.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeDynArrayRuntimeBridgeWorksBothDirections(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable T&?
	count: mutable usize
	capacity: mutable usize

def take_raw[T](values: DynArray[T]) -> void:
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
	if !strings.Contains(all, "DArray expects 2 arguments, got 1") || !strings.Contains(all, "DList expects 2 arguments, got 1") || !strings.Contains(all, "DListView expects 1 argument, got 2") || !strings.Contains(all, "DStr expects 1 argument, got 2") {
		t.Fatalf("expected dynamic shape arity diagnostics, got:\n%s", all)
	}
}

func TestAnalyzeDListUsesCtxListRuntimeFields(t *testing.T) {
	src := `repr(c) struct CtxList:
    len: mutable i64
    cap: mutable i64
    elem_size: mutable i64

def has_room[T](values: DList[T, row]&) -> bool:
    return values.len < values.cap
`
	_, errs := parseAndAnalyze(t, "dlist_runtime_field_access.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeDListViewUsesCtxListViewRuntimeFields(t *testing.T) {
	src := `repr(c) struct CtxListView:
    data: mutable void&&?
    len: mutable i64
    elem_size: mutable i64

def non_empty[T](view: DListView[T]) -> bool:
    return view.len > 0 and view.elem_size > 0
`
	_, errs := parseAndAnalyze(t, "dlist_view_runtime_field_access.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeShapeChangingResizeReturnsFreshShape(t *testing.T) {
	src := `extern resize(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]

def bad(array: DArray[i32, row]) -> DArray[i32, row]:
    return resize(array, 8)
`
	_, errs := parseAndAnalyze(t, "resize_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects DArray[i32, row], got DArray[i32, shape_out#") {
		t.Fatalf("expected fresh resize witness diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeShapeChangingPushReturnsFreshShape(t *testing.T) {
	src := `extern push(array: DArray[i32, shape_in], element: i32) -> DArray[i32, shape_out]

def bad(array: DArray[i32, row]) -> DArray[i32, row]:
    return push(array, 1)
`
	_, errs := parseAndAnalyze(t, "push_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects DArray[i32, row], got DArray[i32, shape_out#") {
		t.Fatalf("expected fresh push witness diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeShapeChangingConcatReturnsFreshShape(t *testing.T) {
	src := `extern concat(array1: DArray[i32, shape_left], array2: DArray[i32, shape_right]) -> DArray[i32, shape_result]

def bad(left: DArray[i32, row], right: DArray[i32, col]) -> DArray[i32, row]:
    return concat(left, right)
`
	_, errs := parseAndAnalyze(t, "concat_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects DArray[i32, row], got DArray[i32, shape_result#") {
		t.Fatalf("expected fresh concat witness diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeFreshShapeMismatchAddsHelpfulNote(t *testing.T) {
	src := `extern resize(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]

def bad(array: DArray[i32, row]) -> DArray[i32, row]:
    return resize(array, 8)
`
	_, errs := parseAndAnalyze(t, "fresh_shape_note.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "note: resize returns a fresh logical shape for shape_out") {
		t.Fatalf("expected fresh shape note, got:\n%s", all)
	}
}

func TestAnalyzeShapeChangingCallCanBeDiscarded(t *testing.T) {
	src := `extern resize(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]

def grow(array: DArray[i32, row]) -> void:
    _ = resize(array, 8)
`
	_, errs := parseAndAnalyze(t, "discard_shape_changing_call.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeShapeChangingStringConcatReturnsFreshShape(t *testing.T) {
	src := `extern concat(left: DStr[shape_left], right: DStr[shape_right]) -> DStr[shape_result]

def bad(left: DStr[row], right: DStr[col]) -> DStr[row]:
    return concat(left, right)
`
	_, errs := parseAndAnalyze(t, "concat_dstr_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects DStr[row], got DStr[shape_result#") {
		t.Fatalf("expected fresh DStr concat witness diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeWrapperCanReturnFreshResizeShape(t *testing.T) {
	src := `extern resize(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]

def grow(array: DArray[i32, row]) -> DArray[i32, shape_after]:
    return resize(array, 8)
`
	_, errs := parseAndAnalyze(t, "wrapper_returns_fresh_resize_shape.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeWrapperCanReturnFreshStringShape(t *testing.T) {
	src := `extern strcat(left: DStr[shape_left], right: DStr[shape_right]) -> DStr[shape_result]

def merge(left: DStr[row], right: DStr[col]) -> DStr[shape_after]:
    return strcat(left, right)
`
	_, errs := parseAndAnalyze(t, "wrapper_returns_fresh_string_shape.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeWrapperCallPropagatesFreshArrayShape(t *testing.T) {
	src := `extern resize(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]

def grow(array: DArray[i32, row]) -> DArray[i32, shape_after]:
    return resize(array, 8)

def same(left: DArray[i32, shape_pair], right: DArray[i32, shape_pair]) -> void:
    pass

def bad(array: DArray[i32, row]) -> void:
    same(grow(array), grow(array))
`
	_, errs := parseAndAnalyze(t, "wrapper_call_propagates_fresh_array_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument 2 to \"same\" expects DArray[i32, shape_after#") || !strings.Contains(all, "got DArray[i32, shape_after#") {
		t.Fatalf("expected fresh wrapper array shape diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeWrapperCallPropagatesFreshStringShape(t *testing.T) {
	src := `extern strcat(left: DStr[shape_left], right: DStr[shape_right]) -> DStr[shape_result]

def merge(left: DStr[row], right: DStr[col]) -> DStr[shape_after]:
    return strcat(left, right)

def same(left: DStr[shape_pair], right: DStr[shape_pair]) -> void:
    pass

def bad(left: DStr[row], right: DStr[col]) -> void:
    same(merge(left, right), merge(left, right))
`
	_, errs := parseAndAnalyze(t, "wrapper_call_propagates_fresh_string_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument 2 to \"same\" expects DStr[shape_after#") || !strings.Contains(all, "got DStr[shape_after#") {
		t.Fatalf("expected fresh wrapper string shape diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeWrapperFreshShapeMismatchExplainsSeparateCalls(t *testing.T) {
	src := `extern resize(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]

def grow(array: DArray[i32, row]) -> DArray[i32, shape_after]:
    return resize(array, 8)

def same(left: DArray[i32, shape_pair], right: DArray[i32, shape_pair]) -> void:
    pass

def bad(array: DArray[i32, row]) -> void:
    same(grow(array), grow(array))
`
	_, errs := parseAndAnalyze(t, "fresh_wrapper_note.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "note: grow returns a fresh logical shape for shape_after") || !strings.Contains(all, "note: separate calls that produce fresh shapes do not share the same logical shape identity") {
		t.Fatalf("expected wrapper fresh shape notes, got:\n%s", all)
	}
}

func TestAnalyzeAdditionalShapeChangingBuiltinsReturnFreshShapes(t *testing.T) {
	src := `extern append_many(array: DArray[i32, shape_in], items: DArray[i32, chunk]) -> DArray[i32, shape_out]
extern truncate(array: DArray[i32, shape_in], size: usize) -> DArray[i32, shape_out]
extern clear(array: DArray[i32, shape_in]) -> DArray[i32, shape_out]

def bad_append(array: DArray[i32, row], items: DArray[i32, col]) -> DArray[i32, row]:
    return append_many(array, items)

def bad_truncate(array: DArray[i32, row]) -> DArray[i32, row]:
    return truncate(array, 2)

def bad_clear(array: DArray[i32, row]) -> DArray[i32, row]:
    return clear(array)
`
	_, errs := parseAndAnalyze(t, "additional_shape_builtins.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	for _, want := range []string{
		"return type expects DArray[i32, row], got DArray[i32, shape_out#",
		"note: append_many returns a fresh logical shape for shape_out",
		"note: truncate returns a fresh logical shape for shape_out",
		"note: clear returns a fresh logical shape for shape_out",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected diagnostic containing %q, got:\n%s", want, all)
		}
	}
}

func TestAnalyzeArenaAppendReturnsFreshShape(t *testing.T) {
	src := `repr(c) struct Arena:
    dummy: usize

repr(c) struct DynArray[T]:
    items: mutable T&?
    count: mutable usize
    capacity: mutable usize

def arena_da_append[T](a: Arena&, da: DArray[T, shape_in]&, item: T) -> DArray[T, shape_out]&:
    if da.count >= da.capacity:
        pass
    return da

def bad(a: Arena&, da: DArray[i32, row]&) -> DArray[i32, row]&:
    return arena_da_append(a, da, 1)
`
	_, errs := parseAndAnalyze(t, "arena_append_returns_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DArray[i32, row]&, got DArray[i32, shape_out#") || !strings.Contains(all, "note: arena_da_append returns a fresh logical shape for shape_out") {
		t.Fatalf("expected fresh arena append diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStage1StringConcatWrapperReturnsFreshShape(t *testing.T) {
	src := `def ctx_stage0_concat2(lhs: u8&?, rhs: u8&?) -> u8&:
    return lhs if lhs != null else rhs.u8&()

def ctx_stage1rt_concat2(lhs: DStr[shape_left], rhs: DStr[shape_right]) -> DStr[shape_result]:
    return ctx_stage0_concat2(lhs, rhs)

def bad(left: DStr[row], right: DStr[col]) -> DStr[row]:
    return ctx_stage1rt_concat2(left, right)
`
	_, errs := parseAndAnalyze(t, "stage1_string_concat_wrapper.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DStr[row], got DStr[shape_result#") || !strings.Contains(all, "note: ctx_stage1rt_concat2 returns a fresh logical shape for shape_result") {
		t.Fatalf("expected fresh stage1 string concat diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStage1ListPushWrapperReturnsFreshShape(t *testing.T) {
	src := `repr(c) struct CtxList:
    len: mutable i64

extern make_list() -> CtxList&

def ctx_stage0_list_push(values: CtxList&?, elem: void&?, elem_size: i64) -> CtxList&?:
    return values

def ctx_stage1rt_list_push(values: DArray[void&, shape_in], elem: void&?, elem_size: i64) -> DArray[void&, shape_out]:
    return ctx_stage0_list_push(values, elem, elem_size)

def bad(values: DArray[void&, row], elem: void&) -> DArray[void&, row]:
    return ctx_stage1rt_list_push(values, elem, 8)
`
	_, errs := parseAndAnalyze(t, "stage1_list_push_wrapper.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DArray[void&, row], got DArray[void&, shape_out#") || !strings.Contains(all, "note: ctx_stage1rt_list_push returns a fresh logical shape for shape_out") || !strings.Contains(all, "note: CtxList-backed list wrappers keep the same runtime layout; this mismatch is about the logical shape witness") {
		t.Fatalf("expected fresh stage1 list push diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStage1TypedListWrappersReduceVoidBottleneck(t *testing.T) {
	src := `repr(c) struct CtxList:
	len: mutable i64
	cap: mutable i64
	elem_size: mutable i64
	data: mutable void&&?
	inline_boxes: mutable u8&?
	inline_box_stride: mutable i64

repr(c) struct CtxListView:
	data: mutable void&&?
	len: mutable i64
	elem_size: mutable i64

extern ctx_stage0_list_new_reserve(cap: i64, elem_size: i64) -> CtxList&

def ctx_stage0_list_push(values: CtxList&?, elem: void&?, elem_size: i64) -> CtxList&?:
	return values

def ctx_stage0_list_view(values: CtxList&?, start: i64, end: i64) -> CtxListView:
	return CtxListView(null, 0, 0)

def ctx_stage0_list_view_len(view: CtxListView) -> i64:
	return view.len

def ctx_stage0_list_view_slice(view: CtxListView, start: i64, end: i64) -> CtxListView:
	return view

def ctx_stage0_list_get(values: CtxList&?, index: i64, elem_size: i64) -> void&:
	return 0.void&()

def ctx_stage0_list_view_get(view: CtxListView, index: i64, elem_size: i64) -> void&:
	return 0.void&()

extern ctx_stage0_list_view_copy(view: CtxListView) -> CtxList&

def ctx_stage1rt_tlist_new[T](type_hint: T&) -> DList[T, shape_out]:
	_ = type_hint
	return ctx_stage0_list_new_reserve(0, sizeof(T).i64())

def ctx_stage1rt_tlist_push[T](values: DList[T, shape_in], elem: T&) -> DList[T, shape_out]:
	return ctx_stage0_list_push(values, elem.void&(), sizeof(T).i64())

def ctx_stage1rt_tlist_view[T](values: DList[T, shape_in], start: i64, end: i64) -> DListView[T]:
	return ctx_stage0_list_view(values, start, end)

def ctx_stage1rt_tlist_view_len[T](view: DListView[T]) -> i64:
	return ctx_stage0_list_view_len(view)

def ctx_stage1rt_tlist_view_slice[T](view: DListView[T], start: i64, end: i64) -> DListView[T]:
	return ctx_stage0_list_view_slice(view, start, end)

def ctx_stage1rt_tlist_get[T](values: DList[T, shape_in], index: i64) -> T&:
	return ctx_stage0_list_get(values, index, sizeof(T).i64()).T&()

def ctx_stage1rt_tlist_view_get[T](view: DListView[T], index: i64) -> T&:
	return ctx_stage0_list_view_get(view, index, sizeof(T).i64()).T&()

def ctx_stage1rt_tlist_from_view[T](view: DListView[T]) -> DList[T, shape_out]:
	return ctx_stage0_list_view_copy(view)

def use(seed: i64&) -> i64&:
	view: DListView[i64] = ctx_stage1rt_tlist_view(ctx_stage1rt_tlist_push(ctx_stage1rt_tlist_new(seed), seed), 0, 2)
	sub: DListView[i64] = ctx_stage1rt_tlist_view_slice(view, 0, 1)
	if ctx_stage1rt_tlist_view_len(sub) > 0:
		_ = ctx_stage1rt_tlist_view_get(sub, 0)
	return ctx_stage1rt_tlist_get(ctx_stage1rt_tlist_from_view(sub), 0)
`
	_, errs := parseAndAnalyze(t, "stage1_typed_list_wrappers.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeStage1TypedListPushReturnsFreshShape(t *testing.T) {
	src := `repr(c) struct CtxList:
	len: mutable i64
	cap: mutable i64
	elem_size: mutable i64
	data: mutable void&&?
	inline_boxes: mutable u8&?
	inline_box_stride: mutable i64

extern ctx_stage0_list_new_reserve(cap: i64, elem_size: i64) -> CtxList&

def ctx_stage0_list_push(values: CtxList&?, elem: void&?, elem_size: i64) -> CtxList&?:
	return values

def ctx_stage1rt_tlist_new[T](type_hint: T&) -> DList[T, shape_out]:
	_ = type_hint
	return ctx_stage0_list_new_reserve(0, sizeof(T).i64())

def ctx_stage1rt_tlist_push[T](values: DList[T, shape_in], elem: T&) -> DList[T, shape_out]:
	return ctx_stage0_list_push(values, elem.void&(), sizeof(T).i64())

def bad(seed: i64&) -> DList[i64, row]:
	return ctx_stage1rt_tlist_push(ctx_stage1rt_tlist_new(seed), seed)
`
	_, errs := parseAndAnalyze(t, "stage1_typed_list_push_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DList[i64, row], got DList[i64, shape_out#") || !strings.Contains(all, "note: ctx_stage1rt_tlist_push returns a fresh logical shape for shape_out") || !strings.Contains(all, "note: CtxList-backed list wrappers keep the same runtime layout; this mismatch is about the logical shape witness") {
		t.Fatalf("expected fresh typed-list push diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStage1TypedListFromViewReturnsFreshShape(t *testing.T) {
	src := `repr(c) struct CtxList:
	len: mutable i64
	cap: mutable i64
	elem_size: mutable i64
	data: mutable void&&?
	inline_boxes: mutable u8&?
	inline_box_stride: mutable i64

repr(c) struct CtxListView:
	data: mutable void&&?
	len: mutable i64
	elem_size: mutable i64

def ctx_stage0_list_view(values: CtxList&?, start: i64, end: i64) -> CtxListView:
	return CtxListView(null, 0, 0)

extern ctx_stage0_list_view_copy(view: CtxListView) -> CtxList&

def ctx_stage1rt_tlist_view[T](values: DList[T, shape_in], start: i64, end: i64) -> DListView[T]:
	return ctx_stage0_list_view(values, start, end)

def ctx_stage1rt_tlist_from_view[T](view: DListView[T]) -> DList[T, shape_out]:
	return ctx_stage0_list_view_copy(view)

def bad(values: DList[i64, row]) -> DList[i64, row]:
	view: DListView[i64] = ctx_stage1rt_tlist_view(values, 0, 1)
	return ctx_stage1rt_tlist_from_view(view)
`
	_, errs := parseAndAnalyze(t, "stage1_typed_list_from_view_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DList[i64, row], got DList[i64, shape_out#") || !strings.Contains(all, "note: ctx_stage1rt_tlist_from_view returns a fresh logical shape for shape_out") || !strings.Contains(all, "note: CtxList-backed list wrappers keep the same runtime layout; this mismatch is about the logical shape witness") {
		t.Fatalf("expected fresh typed-list from-view diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeAdditionalStage1ListWrappersReturnFreshShapes(t *testing.T) {
	src := `repr(c) struct CtxList:
    len: mutable i64
    cap: mutable i64

def ctx_stage0_list_reserve(values: CtxList&?, cap: i64, elem_size: i64) -> CtxList&?:
    return values

def ctx_stage0_list_truncate(values: CtxList&?, size: i64) -> CtxList&?:
    return values

def ctx_stage0_list_clear(values: CtxList&?) -> CtxList&?:
    return values

def ctx_stage1rt_list_reserve(values: DArray[void&, shape_in], cap: i64, elem_size: i64) -> DArray[void&, shape_out]:
    return ctx_stage0_list_reserve(values, cap, elem_size)

def ctx_stage1rt_list_truncate(values: DArray[void&, shape_in], size: i64) -> DArray[void&, shape_out]:
    return ctx_stage0_list_truncate(values, size)

def ctx_stage1rt_list_clear(values: DArray[void&, shape_in]) -> DArray[void&, shape_out]:
    return ctx_stage0_list_clear(values)

def bad_reserve(values: DArray[void&, row]) -> DArray[void&, row]:
    return ctx_stage1rt_list_reserve(values, 16, 8)

def bad_truncate(values: DArray[void&, row]) -> DArray[void&, row]:
    return ctx_stage1rt_list_truncate(values, 2)

def bad_clear(values: DArray[void&, row]) -> DArray[void&, row]:
    return ctx_stage1rt_list_clear(values)
`
	_, errs := parseAndAnalyze(t, "stage1_list_extra_wrappers.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	for _, want := range []string{
		"note: ctx_stage1rt_list_reserve returns a fresh logical shape for shape_out",
		"note: ctx_stage1rt_list_truncate returns a fresh logical shape for shape_out",
		"note: ctx_stage1rt_list_clear returns a fresh logical shape for shape_out",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected diagnostic containing %q, got:\n%s", want, all)
		}
	}
	if strings.Count(all, "return type expects DArray[void&, row], got DArray[void&, shape_out#") != 3 {
		t.Fatalf("expected 3 fresh list wrapper mismatch diagnostics, got:\n%s", all)
	}
}

func TestAnalyzeStage1StringViewWrappersSupportBoundedViews(t *testing.T) {
	src := `repr(c) struct CtxStringView:
	data: mutable u8&
	len: mutable i64

def ctx_stage0_string_view(value: u8&?, start: i64, end: i64) -> CtxStringView:
	return CtxStringView("", 0)

def ctx_stage0_string_view_len(view: CtxStringView) -> i64:
	return view.len

def ctx_stage0_string_view_index(view: CtxStringView, index: i64) -> i64:
	return -1

def ctx_stage0_string_view_copy(view: CtxStringView) -> u8&:
	return view.data

def ctx_stage1rt_string_view(value: DStr[shape_in], start: i64, end: i64) -> CtxStringView:
	return ctx_stage0_string_view(value, start, end)

def ctx_stage1rt_string_view_len(view: CtxStringView) -> i64:
	return ctx_stage0_string_view_len(view)

def ctx_stage1rt_string_view_index(view: CtxStringView, index: i64) -> i64:
	return ctx_stage0_string_view_index(view, index)

def ctx_stage1rt_string_from_view(view: CtxStringView) -> DStr[shape_out]:
	return ctx_stage0_string_view_copy(view)

def probe(text: DStr[row]) -> i64:
	view: CtxStringView = ctx_stage1rt_string_view(text, 0, 2)
	_ = ctx_stage1rt_string_view_index(view, 0)
	return ctx_stage1rt_string_view_len(view)

def bad(text: DStr[row]) -> DStr[row]:
	view: CtxStringView = ctx_stage1rt_string_view(text, 0, 2)
	return ctx_stage1rt_string_from_view(view)
`
	_, errs := parseAndAnalyze(t, "stage1_string_view_wrappers.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DStr[row], got DStr[shape_out#") || !strings.Contains(all, "note: ctx_stage1rt_string_from_view returns a fresh logical shape for shape_out") {
		t.Fatalf("expected bounded string view fresh-shape diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStage1StringViewHelpersAcceptSubviewAndEquality(t *testing.T) {
	src := `repr(c) struct CtxStringView:
	data: mutable u8&
	len: mutable i64

def ctx_stage0_string_view(value: u8&?, start: i64, end: i64) -> CtxStringView:
	return CtxStringView("", 0)

def ctx_stage0_string_view_len(view: CtxStringView) -> i64:
	return view.len

def ctx_stage0_string_view_slice(view: CtxStringView, start: i64, end: i64) -> CtxStringView:
	return view

def ctx_stage0_string_view_eq(view: CtxStringView, other: u8&?) -> int:
	return 1

def ctx_stage0_string_views_eq(lhs: CtxStringView, rhs: CtxStringView) -> int:
	return 1

def ctx_stage1rt_string_view(value: DStr[shape_in], start: i64, end: i64) -> CtxStringView:
	return ctx_stage0_string_view(value, start, end)

def ctx_stage1rt_string_view_len(view: CtxStringView) -> i64:
	return ctx_stage0_string_view_len(view)

def ctx_stage1rt_string_view_slice(view: CtxStringView, start: i64, end: i64) -> CtxStringView:
	return ctx_stage0_string_view_slice(view, start, end)

def ctx_stage1rt_string_view_eq(view: CtxStringView, other: DStr[shape_other]) -> int:
	return ctx_stage0_string_view_eq(view, other)

def ctx_stage1rt_string_views_eq(lhs: CtxStringView, rhs: CtxStringView) -> int:
	return ctx_stage0_string_views_eq(lhs, rhs)

def probe(text: DStr[row], other: DStr[col]) -> int:
	view: CtxStringView = ctx_stage1rt_string_view(text, 0, 4)
	sub: CtxStringView = ctx_stage1rt_string_view_slice(view, 1, 3)
	if ctx_stage1rt_string_view_eq(sub, other) != 0:
		return ctx_stage1rt_string_views_eq(sub, view)
	return ctx_stage1rt_string_view_len(sub)
`
	_, errs := parseAndAnalyze(t, "stage1_string_view_helpers.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeStage1ListViewWrappersSupportBoundedViews(t *testing.T) {
	src := `repr(c) struct CtxList:
	len: mutable i64
	cap: mutable i64
	elem_size: mutable i64
	data: mutable void&&?
	inline_boxes: mutable u8&?
	inline_box_stride: mutable i64

repr(c) struct CtxListView:
	data: mutable void&&?
	len: mutable i64
	elem_size: mutable i64

def ctx_stage0_list_view(values: CtxList&?, start: i64, end: i64) -> CtxListView:
	return CtxListView(null, 0, 0)

def ctx_stage0_list_view_len(view: CtxListView) -> i64:
	return view.len

def ctx_stage0_list_view_get(view: CtxListView, index: i64, elem_size: i64) -> void&:
	return 0.void&()

def ctx_stage0_list_view_copy(view: CtxListView) -> CtxList&:
	return CtxList(0, 0, 0, null, null, 0)

def ctx_stage1rt_list_view(values: DArray[void&, shape_in], start: i64, end: i64) -> CtxListView:
	return ctx_stage0_list_view(values, start, end)

def ctx_stage1rt_list_view_len(view: CtxListView) -> i64:
	return ctx_stage0_list_view_len(view)

def ctx_stage1rt_list_view_get(view: CtxListView, index: i64, elem_size: i64) -> void&:
	return ctx_stage0_list_view_get(view, index, elem_size)

def ctx_stage1rt_list_from_view(view: CtxListView) -> DArray[void&, shape_out]:
	return ctx_stage0_list_view_copy(view)

def probe(values: DArray[void&, row]) -> i64:
	view: CtxListView = ctx_stage1rt_list_view(values, 0, 1)
	_ = ctx_stage1rt_list_view_get(view, 0, 8)
	return ctx_stage1rt_list_view_len(view)

def bad(values: DArray[void&, row]) -> DArray[void&, row]:
	view: CtxListView = ctx_stage1rt_list_view(values, 0, 1)
	return ctx_stage1rt_list_from_view(view)
`
	_, errs := parseAndAnalyze(t, "stage1_list_view_wrappers.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects DArray[void&, row], got DArray[void&, shape_out#") || !strings.Contains(all, "note: ctx_stage1rt_list_from_view returns a fresh logical shape for shape_out") || !strings.Contains(all, "note: CtxList-backed list wrappers keep the same runtime layout; this mismatch is about the logical shape witness") {
		t.Fatalf("expected bounded list view fresh-shape diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStage1ListViewHelpersAcceptNestedSubview(t *testing.T) {
	src := `repr(c) struct CtxList:
	len: mutable i64
	cap: mutable i64
	elem_size: mutable i64
	data: mutable void&&?
	inline_boxes: mutable u8&?
	inline_box_stride: mutable i64

repr(c) struct CtxListView:
	data: mutable void&&?
	len: mutable i64
	elem_size: mutable i64

def ctx_stage0_list_view(values: CtxList&?, start: i64, end: i64) -> CtxListView:
	return CtxListView(null, 0, 0)

def ctx_stage0_list_view_len(view: CtxListView) -> i64:
	return view.len

def ctx_stage0_list_view_slice(view: CtxListView, start: i64, end: i64) -> CtxListView:
	return view

def ctx_stage0_list_view_get(view: CtxListView, index: i64, elem_size: i64) -> void&:
	return 0.void&()

def ctx_stage1rt_list_view(values: DArray[void&, shape_in], start: i64, end: i64) -> CtxListView:
	return ctx_stage0_list_view(values, start, end)

def ctx_stage1rt_list_view_len(view: CtxListView) -> i64:
	return ctx_stage0_list_view_len(view)

def ctx_stage1rt_list_view_slice(view: CtxListView, start: i64, end: i64) -> CtxListView:
	return ctx_stage0_list_view_slice(view, start, end)

def ctx_stage1rt_list_view_get(view: CtxListView, index: i64, elem_size: i64) -> void&:
	return ctx_stage0_list_view_get(view, index, elem_size)

def probe(values: DArray[void&, row]) -> i64:
	view: CtxListView = ctx_stage1rt_list_view(values, 0, 4)
	sub: CtxListView = ctx_stage1rt_list_view_slice(view, 1, 3)
	_ = ctx_stage1rt_list_view_get(sub, 0, 8)
	return ctx_stage1rt_list_view_len(sub)
`
	_, errs := parseAndAnalyze(t, "stage1_list_view_helpers.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeCtxListRuntimeBridgeWorksBothDirections(t *testing.T) {
	src := `repr(c) struct CtxList:
	len: mutable i64

def take_raw(values: CtxList&) -> void:
	pass

def take_logical(values: DArray[void&, shape_in]) -> void:
	pass

def roundtrip(values: DArray[void&, row], raw: CtxList&) -> DArray[void&, row]:
	take_raw(values)
	take_logical(raw)
	logical: DArray[void&, row] = raw
	bridged: CtxList& = logical
	return bridged
`
	_, errs := parseAndAnalyze(t, "ctxlist_runtime_bridge_roundtrip.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeStage1RuntimeFileAcceptsShapeTypedWrappers(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "contextlang_runtime.llcontext")
	src := loadSourceWithIncludes(t, fixture, map[string]bool{})
	_, errs := parseAndAnalyze(t, fixture, src)
	requireNoErrors(t, errs)
}

func TestAnalyzePointerFixture(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "test_programs", "pointer_alloc.llcontext")
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	_, errs := parseAndAnalyze(t, fixture, string(src))
	requireNoErrors(t, errs)
}

func TestAnalyzeShapeOpsFixture(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "test_programs", "shape_ops.llcontext")
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("failed to read shape ops fixture: %v", err)
	}
	_, errs := parseAndAnalyze(t, fixture, string(src))
	requireNoErrors(t, errs)
}

func TestAnalyzeArenaRuntimeFile(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "arena.llcontext")
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("failed to read arena runtime fixture: %v", err)
	}
	_, errs := parseAndAnalyze(t, fixture, string(src))
	requireNoErrors(t, errs)
}

func TestAnalyzeContextRuntimeFile(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "contextlang_runtime.llcontext")
	src := loadSourceWithIncludes(t, fixture, map[string]bool{})
	_, errs := parseAndAnalyze(t, fixture, src)
	requireNoErrors(t, errs)
}
