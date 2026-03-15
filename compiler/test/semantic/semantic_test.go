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

func TestAnalyzeDArrayViewUsesDynArrayViewRuntimeFields(t *testing.T) {
	src := `repr(c) struct DynArrayView:
    data: mutable void&?
    len: mutable usize
    elem_size: mutable usize

def non_empty[T](view: DArrayView[T]) -> bool:
    return view.len > 0u and view.elem_size > 0u
`
	_, errs := parseAndAnalyze(t, "darray_view_runtime_field_access.llcontext", src)
	requireNoErrors(t, errs)
}
