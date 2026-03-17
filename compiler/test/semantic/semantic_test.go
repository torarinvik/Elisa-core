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

extern make_box() -> any Box&?

def read_box() -> int:
	box: mutable any Box&? = make_box()
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
    box: any Box& = null
`
	_, errs := parseAndAnalyze(t, "nonnull_ref_rejects_null.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "expects any Box&, got null") {
		t.Fatalf("expected non-null ref rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzePointerTypestateBranches(t *testing.T) {
	src := `repr(c) struct Box:
    value: mutable int

extern alloc_box() -> any Box&?
extern sfree_box(box: any Box&) -> any Box!

def release_box() -> void:
	box: mutable any Box&? = alloc_box()
    if box != null:
        box as ! <- sfree_box(box)

def missing_box() -> any Box!:
    return null
`
	_, errs := parseAndAnalyze(t, "pointer_typestate.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsNullableFieldAccessWithoutProof(t *testing.T) {
	src := `repr(c) struct Box:
    value: mutable int

extern maybe_box() -> any Box&?

def bad() -> int:
	box: any Box&? = maybe_box()
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

extern maybe_box() -> any Box&?

def read_box() -> int:
	box: any Box&? = maybe_box()
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

func TestAnalyzeAcceptsExportedConcreteWrappers(t *testing.T) {
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
	result, errs := parseAndAnalyze(t, "export_wrappers.llcontext", src)
	requireNoErrors(t, errs)
	if len(result.ExportedTypes) != 1 {
		t.Fatalf("expected 1 exported type, got %d", len(result.ExportedTypes))
	}
	if len(result.ExportedFuncs) != 2 {
		t.Fatalf("expected 2 exported funcs, got %d", len(result.ExportedFuncs))
	}
	if len(result.ExportedGlobals) != 1 {
		t.Fatalf("expected 1 exported global, got %d", len(result.ExportedGlobals))
	}
	if _, ok := result.NamedTypes["Vec2i"]; !ok {
		t.Fatal("expected Vec2i type alias to be available")
	}
	if result.ExportedGlobals[0].PublicName != "ctx_seed" {
		t.Fatalf("expected exported global name ctx_seed, got %s", result.ExportedGlobals[0].PublicName)
	}
	if result.ExportedFuncs[1].TargetGenericDecl == nil {
		t.Fatal("expected generic export target metadata for vec2i_keep_left")
	}
	if result.ExportedFuncs[1].TargetBindings["T"] == nil {
		t.Fatal("expected generic export binding for keep_left")
	}
	if result.ExportedFuncs[0].Signature.Return.String() != "Vec[i32]" {
		t.Fatalf("expected exported wrapper return to resolve concretely, got %s", result.ExportedFuncs[0].Signature.Return.String())
	}
}

func TestAnalyzeRejectsExportedNonGlobalSymbol(t *testing.T) {
	src := `const MAGIC = 1337

export global MAGIC as ctx_magic
`
	_, errs := parseAndAnalyze(t, "export_non_global_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "must target a global") {
		t.Fatalf("expected exported-global target rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsExportedArrayBoundaryTypes(t *testing.T) {
	src := `def pass_array(value: i32[4]) -> i32[4]:
	return value

export func pass_array_c(value: i32[4]) -> i32[4] = pass_array
`
	_, errs := parseAndAnalyze(t, "export_array_boundary_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "export func \"pass_array_c\" is not C-ABI-compatible") {
		t.Fatalf("expected export array boundary rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeTernaryRefinesNullablePointerBranch(t *testing.T) {
	src := `def choose_text(value: any u8&?) -> any u8&:
	return value if value != null else "".cast[any u8&]()
`
	_, errs := parseAndAnalyze(t, "ternary_refinement.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsNullableToNonNullCastWithoutProof(t *testing.T) {
	src := `repr(c) struct Box:
    value: int

extern maybe_box() -> any Box&?

def bad() -> any Box&:
    box: any Box&? = maybe_box()
    return box.cast[any Box&]()
`
	_, errs := parseAndAnalyze(t, "nonnull_cast_rejection.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "invalid cast from any Box&? to any Box&") {
		t.Fatalf("expected invalid cast diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsReferenceComparisons(t *testing.T) {
	src := `repr(c) struct Box:
	value: int

extern maybe_box() -> any Box&?

def is_missing() -> bool:
	return maybe_box() == null

def is_present() -> bool:
	return maybe_box() != null

def same_box(left: any Box&, right: any Box&) -> bool:
	return left == right
`
	_, errs := parseAndAnalyze(t, "reference_comparisons.llcontext", src)
	requireNoErrors(t, errs)
}

func TestParseRejectsBareReferenceTypeSyntax(t *testing.T) {
	src := `repr(c) struct Box:
	value: int

def read(box: Box&) -> int:
	return box.value
`
	_, errs := parseAndAnalyze(t, "bare_reference_type_parse_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected parse error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference types require an explicit storage qualifier") {
		t.Fatalf("expected explicit-storage parse diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestParseRejectsLegacyDotReferenceCastSyntax(t *testing.T) {
	src := `def bits_ptr(bits: uintptr) -> any u8&:
	return bits.u8&()
`
	_, errs := parseAndAnalyze(t, "legacy_reference_cast_parse_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected parse error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "legacy reference cast syntax is no longer supported") {
		t.Fatalf("expected legacy reference cast parse diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsStorageQualifiedPointersAndCastSyntax(t *testing.T) {
	src := `repr(c) struct Box:
	value: int

extern maybe_heap_box() -> heap Box&?

def widen(box: heap Box&?) -> any Box&?:
	return box.cast[any Box&?]()

def keep_heap(box: heap Box&?) -> heap Box&?:
	return box.cast[heap Box&?]()

def coerce_text() -> any u8&:
	return "hello".cast[any u8&]()

def use_source() -> any Box&?:
	return maybe_heap_box().cast[any Box&?]()
`
	_, errs := parseAndAnalyze(t, "storage_cast_syntax.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsImplicitStorageMismatchWithoutCast(t *testing.T) {
	src := `repr(c) struct Box:
	value: int

def bad(box: heap Box&) -> any Box&:
	return box
`
	_, errs := parseAndAnalyze(t, "storage_mismatch_without_cast.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects any Box&, got heap Box&") {
		t.Fatalf("expected storage-mismatch diagnostic, got:\n%s", all)
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

func TestAnalyzeAcceptsPointerArithmetic(t *testing.T) {
	src := `def advance(ptr: any u8&, offset: usize) -> any u8&:
	return ptr + offset

def advance_commutative(offset: usize, ptr: any u8&) -> any u8&:
	return offset + ptr

def rewind(ptr: any u8&, offset: usize) -> any u8&:
	return ptr - offset
`
	_, errs := parseAndAnalyze(t, "pointer_arithmetic.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsManualRegions(t *testing.T) {
	src := `def sum_region(seed: i32) -> i32:
	region scratch(1024u)
	value: any i32& = new[scratch] seed + 1
	return value[0u]
`
	_, errs := parseAndAnalyze(t, "manual_regions_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsEnumConstructorsAndMatch(t *testing.T) {
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

def make_pair() -> MaybeInt:
	return MaybeInt.Pair(3, 4)

def make_none() -> MaybeInt:
	return MaybeInt.None
`
	_, errs := parseAndAnalyze(t, "enum_match_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsDictSurfaceTypesAndRuntimeBridge(t *testing.T) {
	src := `extern take_runtime(values: DynDict[i32]) -> void
extern make_runtime() -> DynDict[i32]

def id[V](values: dict[dstr, V]) -> dict[dstr, V]:
	return values

def keep(values: dict[dstr, i32]) -> dict[dstr, i32]:
	return id(values)

def use(values: dict[dstr[row], i32]) -> dict[dstr, i32]:
	take_runtime(values)
	return make_runtime()
`
	_, errs := parseAndAnalyze(t, "dict_surface_and_bridge_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsUnsupportedDictKeyTypes(t *testing.T) {
	src := `def bad(values: dict[i32, i32]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "dict_bad_key.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "dict currently only supports dstr keys") {
		t.Fatalf("expected dict-key diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsAllocatingFromDestroyedRegion(t *testing.T) {
	src := `def bad() -> void:
	region scratch
	destroy scratch
	value: any i32& = new[scratch] 1
`
	_, errs := parseAndAnalyze(t, "manual_regions_destroyed_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot allocate from destroyed region \"scratch\"") {
		t.Fatalf("expected destroyed-region diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsErrorDeclarationsAndTryRecovery(t *testing.T) {
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
	_, errs := parseAndAnalyze(t, "error_handling_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsErrorSetWideningAndTagRaise(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error AppError:
	PermissionDenied
	Busy
	NotFound

error OneTagError:
	NotFound

extern read_value() -> int error[FileError.NotFound, ...]

def bubble() -> int error[AppError.NotFound, ...]:
	return try read_value()

def fail_now() -> int error[OneTagError.NotFound]:
	raise FileError.NotFound
`
	_, errs := parseAndAnalyze(t, "error_set_widening_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsWildcardErrorSetShorthand(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

extern read_value() -> int error[FileError]

def bubble() -> int error[FileError, ...]:
	return try read_value()
`
	_, errs := parseAndAnalyze(t, "error_set_wildcard_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsMultiFamilyErrorComposition(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	NotFound

extern read_disk() -> int error[FileError]
extern read_network() -> int error[NetworkError.Timeout]

def load_any(use_disk: bool) -> int error[FileError, NetworkError]:
	if use_disk:
		return try read_disk()
	return try read_network()
`
	_, errs := parseAndAnalyze(t, "error_multi_family_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsMixedRowStyleFamilyExpansion(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	Disconnected

extern read_disk() -> int error[FileError]
extern read_network() -> int error[NetworkError.Timeout]

def load_any(use_disk: bool) -> int error[FileError.NotFound, NetworkError.Timeout, ...]:
	if use_disk:
		return try read_disk()
	return try read_network()

def fail_disk() -> int error[FileError.NotFound, NetworkError.Timeout, ...]:
	raise FileError.PermissionDenied
`
	_, errs := parseAndAnalyze(t, "error_mixed_row_expansion_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeCanonicalizesEquivalentErrorSetSpellings(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	Disconnected

def by_full_subset() -> int error[FileError.NotFound, FileError.PermissionDenied]:
	return 1

def by_reverse_family_order() -> int error[NetworkError, FileError]:
	return 2
`
	result, errs := parseAndAnalyze(t, "error_canonicalization.llcontext", src)
	requireNoErrors(t, errs)

	fullSubset, ok := result.GlobalScope.Lookup("by_full_subset")
	if !ok {
		t.Fatal("expected by_full_subset symbol")
	}
	fullSubsetFn, ok := fullSubset.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", fullSubset.Type)
	}
	if fullSubsetFn.Return.String() != "int | FileError" {
		t.Fatalf("expected canonical single-family return type, got %s", fullSubsetFn.Return.String())
	}

	reversed, ok := result.GlobalScope.Lookup("by_reverse_family_order")
	if !ok {
		t.Fatal("expected by_reverse_family_order symbol")
	}
	reversedFn, ok := reversed.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", reversed.Type)
	}
	if reversedFn.Return.String() != "int | error[FileError, NetworkError]" {
		t.Fatalf("expected canonical multi-family return type, got %s", reversedFn.Return.String())
	}
}

func TestAnalyzeRejectsAmbiguousCrossFamilyRaiseIntoMultiFamilySet(t *testing.T) {
	src := `error LegacyError:
	NotFound

error FileError:
	NotFound

error NetworkError:
	NotFound

def fail() -> int error[FileError, NetworkError]:
	raise LegacyError.NotFound
`
	_, errs := parseAndAnalyze(t, "error_multi_family_ambiguous_raise.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "raise cannot propagate tag \"LegacyError.NotFound\" into error[FileError, NetworkError]") {
		t.Fatalf("expected ambiguous multi-family raise diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeCanonicalizesRaiseDestinationInDiagnostics(t *testing.T) {
	src := `error LegacyError:
	Busy

error FileError:
	NotFound

error NetworkError:
	Disconnected

def fail() -> int error[NetworkError, FileError]:
	raise LegacyError.Busy
`
	_, errs := parseAndAnalyze(t, "error_canonical_raise_diag.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "raise cannot propagate tag \"LegacyError.Busy\" into error[FileError, NetworkError]") {
		t.Fatalf("expected canonical raise destination diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

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

extern read_file(path: any u8&) -> int | IoError
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
	src := `def bad(value: any u8&) -> any u8&:
	return value else "".cast[any u8&]()
`
	_, errs := parseAndAnalyze(t, "else_on_nonnullable_ref.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "else recovery requires a nullable reference") {
		t.Fatalf("expected else recovery diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsRuntimeBackedArrayAndViewIndexing(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	items: mutable any void&?
	count: mutable usize

def read_array(values: darray[i32, row]) -> i32:
	return values[0]

def read_view(view: view[i32]) -> i32:
	return view[0]
`
	_, errs := parseAndAnalyze(t, "runtime_backed_array_index.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsDStrIndexingAsChar(t *testing.T) {
	src := `def read_codepoint(text: dstr[row]) -> char:
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
	src := `def bad(text: dstr[row]) -> void:
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

func TestAnalyzeAcceptsStringViewIndexingAsChar(t *testing.T) {
	src := `def read_codepoint(view: StringView) -> char:
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
	src := `def same_text(left: dstr[row], right: dstr[col]) -> bool:
	return left == right

def same_view_text(view: StringView, text: dstr[row]) -> bool:
	return view == text

def same_text_view(text: dstr[row], view: StringView) -> bool:
	return text == view

def different_views(left: StringView, right: StringView) -> bool:
	return left != right

def same_literal(text: dstr[row]) -> bool:
	return text == "hello"
`
	_, errs := parseAndAnalyze(t, "runtime_string_equality.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsDStrLenField(t *testing.T) {
	src := `def text_len(text: dstr[row]) -> i64:
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

func TestAnalyzeAcceptsArrayAndArrayViewSliceSyntax(t *testing.T) {
	src := `def middle(values: darray[i32, row], view: view[i32]) -> i32:
	part: view[i32] = values[1u:3u]
	sub: view[i32] = view[0u:1u]
	return part[0u] + sub[0u]
`
	_, errs := parseAndAnalyze(t, "array_and_array_view_slice.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsFixedArraySliceSyntax(t *testing.T) {
	src := `def middle(values: i32[4], view: any i32[4]&) -> i32:
	part: view[i32] = values[1u:3u]
	sub: view[i32] = view[0u:2u]
	return part[0u] + sub[1u]
`
	_, errs := parseAndAnalyze(t, "fixed_array_slice.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsNestedCollectionAccessOnReturnedValues(t *testing.T) {
	src := `extern make_array() -> darray[i32, row]
extern make_array_view() -> view[i32]

def read_array_index() -> i32:
	return make_array()[1u]

def read_array_slice_index() -> i32:
	return make_array()[1u:3u][0u]

def read_array_view_index() -> i32:
	return make_array_view()[0u]
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
	src := `repr(c) struct ScratchPair:
	left: mutable int
	right: mutable int


repr(c) struct ScratchHolder:
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
		return try checked_pair(&values[1u]) else 0
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
	src := `def middle(text: dstr[row]) -> StringView:
	return text[1:3]
`
	_, errs := parseAndAnalyze(t, "string_slice_syntax.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsAssigningToDStrLenField(t *testing.T) {
	src := `def bad(text: dstr[row]) -> void:
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

func TestAnalyzeRejectsAssigningToStringViewIndex(t *testing.T) {
	src := `def bad(view: StringView) -> void:
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
	src := `def identity[T](array: darray[T, shape_in]) -> darray[T, shape_in]:
	return array

def keep(array: darray[i32, row]) -> darray[i32, row]:
	return identity(array)
`
	_, errs := parseAndAnalyze(t, "implicit_darray_shape_params.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsShapeErasingDArrayShorthand(t *testing.T) {
	src := `def keep_surface(values: darray[i32]) -> darray[i32]:
	return values

def erase_explicit(values: darray[i32, row]) -> darray[i32]:
	return values
`
	_, errs := parseAndAnalyze(t, "darray_shorthand_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsRecoveringExplicitShapeFromShorthand(t *testing.T) {
	src := `def bad(values: darray[i32]) -> darray[i32, row]:
	return values
`
	_, errs := parseAndAnalyze(t, "darray_shorthand_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects darray[i32, row], got darray[i32]") {
		t.Fatalf("expected omitted-shape to explicit-shape rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeDArrayUsesDynArrayRuntimeFields(t *testing.T) {
	src := `def needs_grow[T](array: any darray[T, row]&) -> bool:
	return array.count >= array.capacity
`
	_, errs := parseAndAnalyze(t, "darray_runtime_field_access.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeDynArrayRuntimeBridgeWorksBothDirections(t *testing.T) {
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
	_, errs := parseAndAnalyze(t, "dynarray_runtime_bridge_roundtrip.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsMismatchedDArrayShapes(t *testing.T) {
	src := `def bad(array: darray[i32, row]) -> darray[i32, col]:
	return array
`
	_, errs := parseAndAnalyze(t, "mismatched_darray_shapes.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects darray[i32, col], got darray[i32, row]") {
		t.Fatalf("expected dynamic shape mismatch diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsImplicitDStrShapeParams(t *testing.T) {
	src := `def echo(text: dstr[shape_text]) -> dstr[shape_text]:
	return text

def keep(text: dstr[row]) -> dstr[row]:
	return echo(text)
`
	_, errs := parseAndAnalyze(t, "implicit_dstr_shape_params.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeAcceptsShapeErasingDStrShorthand(t *testing.T) {
	src := `def keep_surface(text: dstr) -> dstr:
	return text

def erase_explicit(text: dstr[row]) -> dstr:
	return text
`
	_, errs := parseAndAnalyze(t, "dstr_shorthand_ok.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsRecoveringExplicitShapeFromDStrShorthand(t *testing.T) {
	src := `def bad(text: dstr) -> dstr[row]:
	return text
`
	_, errs := parseAndAnalyze(t, "dstr_shorthand_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects dstr[row], got dstr") {
		t.Fatalf("expected omitted-shape DStr to explicit-shape rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeDStrRuntimeBridgeWorksBothDirections(t *testing.T) {
	src := `def take_raw(text: any u8&) -> void:
	pass

def take_logical(text: dstr[shape_text]) -> void:
	pass

def roundtrip(text: dstr[row], raw: any u8&) -> dstr[row]:
	take_raw(text)
	take_logical(raw)
	bridged: dstr[row] = raw
	raw_value: any u8& = text
	return raw_value
`
	_, errs := parseAndAnalyze(t, "dstr_runtime_bridge_roundtrip.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeRejectsLegacyUppercaseBuiltinTypes(t *testing.T) {
	src := `def bad_array(values: DArray[i32]) -> void:
	pass

def bad_array_view(values: DArrayView[i32]) -> void:
	pass

def bad_str(text: DStr[row]) -> void:
	pass

def bad_dict(values: Dict[dstr, i32]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "dynamic_shape_arity.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	for _, want := range []string{
		"legacy built-in \"DArray\" has been replaced; use \"darray\" instead",
		"legacy built-in \"DArrayView\" has been replaced; use \"view\" instead",
		"legacy built-in \"DStr\" has been replaced; use \"dstr\" instead",
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

def bad_dynamic(text: dstring[row]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "legacy_string_aliases.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "legacy built-in \"string\" has been replaced; use \"str\" instead") || !strings.Contains(all, "legacy built-in \"dstring\" has been replaced; use \"dstr\" instead") {
		t.Fatalf("expected legacy string alias diagnostics, got:\n%s", all)
	}
}

func TestAnalyzeRejectsRemovedDListTypes(t *testing.T) {
	src := `def bad_list(values: DList[i32, row]) -> void:
	pass

def bad_list_view(view: DListView[i32]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "removed_dlist_surface.llcontext", src)
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
	return view.len > 0u and view.elem_size > 0u
`
	_, errs := parseAndAnalyze(t, "darray_view_runtime_field_access.llcontext", src)
	requireNoErrors(t, errs)
}
