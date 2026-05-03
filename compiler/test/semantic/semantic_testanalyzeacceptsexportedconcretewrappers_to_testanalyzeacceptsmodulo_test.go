package semantic_test

import (
	"llcontext/src/semantic"
	"strings"
	"testing"
)

func TestAnalyzeAcceptsExportedConcreteWrappers(t *testing.T) {
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
func TestAnalyzeAcceptsConcreteRefQualifierExports(t *testing.T) {
	src := `struct Node:
	value: mutable i32

struct Handle[refstorage Store, refstate State]:
	ptr: Store Node&[State]

export type Node as CtxNode
export type Handle[heap, &] as HeapHandle

def keep_handle[refstorage Store, refstate State](value: Handle[Store, State]) -> Handle[Store, State]:
	return value

export func keep_heap_handle(value: HeapHandle) -> HeapHandle = keep_handle[heap, &]
`
	result, errs := parseAndAnalyze(t, "export_ref_qualifier_wrappers.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	if len(result.ExportedTypes) != 2 {
		t.Fatalf("expected 2 exported types, got %d", len(result.ExportedTypes))
	}
	if len(result.ExportedFuncs) != 1 {
		t.Fatalf("expected 1 exported func, got %d", len(result.ExportedFuncs))
	}
	if got := result.ExportedTypes[1].Type.String(); got != "Handle[heap, &]" {
		t.Fatalf("expected concrete exported handle type, got %s", got)
	}
	bindings := result.ExportedFuncs[0].TargetBindings
	storageBinding, ok := bindings["Store"].(*semantic.RefStorageValueType)
	if !ok {
		t.Fatalf("expected concrete refstorage binding, got %#v", bindings["Store"])
	}
	if storageBinding.Storage != semantic.RefStorageHeap {
		t.Fatalf("expected heap refstorage binding, got %v", storageBinding.Storage)
	}
	stateBinding, ok := bindings["State"].(*semantic.RefStateValueType)
	if !ok {
		t.Fatalf("expected concrete refstate binding, got %#v", bindings["State"])
	}
	if stateBinding.State != semantic.RefStateNonNull {
		t.Fatalf("expected non-null refstate binding, got %v", stateBinding.State)
	}
	if got := result.ExportedFuncs[0].Signature.Return.String(); got != "Handle[heap, &]" {
		t.Fatalf("expected concrete exported handle return type, got %s", got)
	}
}
func TestAnalyzeRejectsInvalidRefQualifierExportTypeArgs(t *testing.T) {
	src := `struct Node:
	value: mutable i32

struct Handle[refstorage Store, refstate State]:
	ptr: Store Node&[State]

export type Handle[i32, &] as BadHandle
`
	_, errs := parseAndAnalyze(t, "export_ref_qualifier_non_concrete_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "generic argument \"i32\" for refstorage parameter \"Store\" must be a refstorage literal or parameter") {
		t.Fatalf("expected refstorage export-type argument diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeCollectsKnownFunctionAnnotations(t *testing.T) {
	src := `@test
def sample_case() -> void:
	pass

@fixture
def shared_seed() -> int:
	return 7

@bench
def hot_loop() -> void:
	pass
`
	result, errs := parseAndAnalyze(t, "function_annotations_ok.llcontext", src)
	requireNoErrors(t, errs)
	if len(result.AnnotatedFuncs) != 3 {
		t.Fatalf("expected 3 annotated funcs, got %d", len(result.AnnotatedFuncs))
	}
	if result.AnnotatedFuncs[0].Name != "sample_case" {
		t.Fatalf("expected first annotated func to be sample_case, got %q", result.AnnotatedFuncs[0].Name)
	}
	if len(result.AnnotatedFuncs[0].Annotations) != 1 {
		t.Fatalf("expected sample_case to carry 1 annotation, got %d", len(result.AnnotatedFuncs[0].Annotations))
	}
	if got := result.AnnotatedFuncs[0].Annotations[0].Name; got != "test" {
		t.Fatalf("expected first annotation to be test, got %q", got)
	}
	if result.AnnotatedFuncs[0].Signature == nil || result.AnnotatedFuncs[0].Signature.Return.String() != "void" {
		t.Fatalf("expected sample_case signature to resolve to void return, got %#v", result.AnnotatedFuncs[0].Signature)
	}
	if result.AnnotatedFuncs[1].Name != "shared_seed" {
		t.Fatalf("expected second annotated func to be shared_seed, got %q", result.AnnotatedFuncs[1].Name)
	}
	if len(result.AnnotatedFuncs[1].Annotations) != 1 || result.AnnotatedFuncs[1].Annotations[0].Name != "fixture" {
		t.Fatalf("expected shared_seed to carry a single fixture annotation, got %#v", result.AnnotatedFuncs[1].Annotations)
	}
	if result.AnnotatedFuncs[1].Signature == nil || result.AnnotatedFuncs[1].Signature.Return.String() != "int" {
		t.Fatalf("expected shared_seed signature to resolve to int return, got %#v", result.AnnotatedFuncs[1].Signature)
	}
	if result.AnnotatedFuncs[2].Name != "hot_loop" {
		t.Fatalf("expected third annotated func to be hot_loop, got %q", result.AnnotatedFuncs[2].Name)
	}
	if len(result.AnnotatedFuncs[2].Annotations) != 1 || result.AnnotatedFuncs[2].Annotations[0].Name != "bench" {
		t.Fatalf("expected hot_loop to carry a single bench annotation, got %#v", result.AnnotatedFuncs[2].Annotations)
	}
}
func TestAnalyzeRejectsUnknownFunctionAnnotation(t *testing.T) {
	src := `@smoke
def sample_case() -> int:
	return 7
`
	_, errs := parseAndAnalyze(t, "function_annotations_unknown.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "unknown function annotation @smoke") {
		t.Fatalf("expected unknown-annotation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUnknownExternFunctionAnnotation(t *testing.T) {
	src := `@smoke
extern borrow_value(holder: i32&) -> i32&
`
	_, errs := parseAndAnalyze(t, "extern_function_annotations_unknown.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "unknown extern function annotation @smoke on \"borrow_value\"") {
		t.Fatalf("expected unknown-extern-annotation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsExternBorrowsReturnUnknownParam(t *testing.T) {
	src := `@borrows_return(missing)
extern borrow_value(holder: i32&) -> i32&
`
	_, errs := parseAndAnalyze(t, "extern_function_annotations_unknown_param.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@borrows_return on extern function \"borrow_value\" references unknown parameter \"missing\"") {
		t.Fatalf("expected borrows_return unknown-param diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsExternBorrowsReturnOnNonProvenanceParam(t *testing.T) {
	src := `@borrows_return(count)
extern borrow_value(count: i32) -> i32&
`
	_, errs := parseAndAnalyze(t, "extern_function_annotations_bad_param.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@borrows_return on extern function \"borrow_value\" cannot borrow from parameter \"count\" of type i32") {
		t.Fatalf("expected borrows_return bad-param diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsDuplicateFunctionAnnotation(t *testing.T) {
	src := `@test
@test
def sample_case() -> int:
	return 7
`
	_, errs := parseAndAnalyze(t, "function_annotations_duplicate.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "duplicate @test annotation on function \"sample_case\"") {
		t.Fatalf("expected duplicate-annotation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsTestFunctionParameters(t *testing.T) {
	src := `@test
def sample_case(value: int) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "function_annotations_test_params.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@test function \"sample_case\" must not take parameters") {
		t.Fatalf("expected test-parameter diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAllowsTestFunctionAbortPermission(t *testing.T) {
	src := `@test
def sample_case() -> void can[Abort.Panic]:
	assert(true)
`
	result, errs := parseAndAnalyze(t, "function_annotations_test_abort.llcontext", src)
	if len(errs) != 0 {
		t.Fatalf("expected no semantic errors, got:\n%s", strings.Join(errs, "\n"))
	}
	if result == nil || len(result.AnnotatedFuncs) != 1 {
		t.Fatalf("expected one discovered annotated function, got %#v", result)
	}
	refs := result.AnnotatedFuncs[0].Signature.PermissionRefs
	if len(refs) != 1 || refs[0].Name != "Abort" || refs[0].Member != "Panic" {
		t.Fatalf("expected discovered @test signature to keep Abort.Panic permission ref, got %#v", refs)
	}
}
func TestAnalyzeAcceptsBareAssertCondition(t *testing.T) {
	src := `def sample_case(left: int, right: int) -> void can[Abort.Panic]:
	assert left != right
`
	_, errs := parseAndAnalyze(t, "bare_assert_condition.llcontext", src)
	if len(errs) != 0 {
		t.Fatalf("expected no semantic errors, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAllowsTestFunctionNonAbortPermission(t *testing.T) {
	src := `@test
def sample_case() -> void can[Console.Write]:
	pass
`
	result, errs := parseAndAnalyze(t, "function_annotations_test_console_permission.llcontext", src)
	if len(errs) != 0 {
		t.Fatalf("expected no semantic errors, got:\n%s", strings.Join(errs, "\n"))
	}
	if result == nil || len(result.AnnotatedFuncs) != 1 {
		t.Fatalf("expected one discovered annotated function, got %#v", result)
	}
	refs := result.AnnotatedFuncs[0].Signature.PermissionRefs
	if len(refs) != 1 || refs[0].Name != "Console" || refs[0].Member != "Write" {
		t.Fatalf("expected discovered @test signature to keep Console.Write permission ref, got %#v", refs)
	}
}
func TestAnalyzeAllowsTestFunctionLocalMemoryGrantWithoutImpossiblePermissionWarning(t *testing.T) {
	src := `extern alloc_i32() -> mutable heap i32&? can[Memory.Allocate]

@test
def sample_case() -> void:
	can Memory.Allocate:
		_ = alloc_i32()
`
	result, errs := parseAndAnalyze(t, "function_annotations_test_local_memory_grant_ok.llcontext", src)
	if len(errs) != 0 {
		t.Fatalf("expected no semantic errors, got:\n%s", strings.Join(errs, "\n"))
	}
	requireNoWarnings(t, result)
}
func TestAnalyzeRejectsBenchFunctionNonVoidReturn(t *testing.T) {
	src := `@bench
def hot_loop() -> int:
	return 7
`
	_, errs := parseAndAnalyze(t, "function_annotations_bench_return.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@bench function \"hot_loop\" must return void, got int") {
		t.Fatalf("expected bench-return diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsGenericFixtureFunction(t *testing.T) {
	src := `@fixture
def shared_seed[T]() -> int:
	return 7
`
	_, errs := parseAndAnalyze(t, "function_annotations_fixture_generic.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@fixture function \"shared_seed\" must not have type or shape parameters") {
		t.Fatalf("expected fixture-generic diagnostic, got:\n%s", strings.Join(errs, "\n"))
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
	src := `def choose_text(value: u8&?) -> u8&:
	return value if value != null else "" as u8&
`
	_, errs := parseAndAnalyze(t, "ternary_refinement.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsNullableToNonNullCastWithoutProof(t *testing.T) {
	src := `struct Box:
    value: int

extern maybe_box() -> Box&?

def bad() -> Box&:
    box: Box&? = maybe_box()
	return box.cast[Box&]
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
	src := `struct Box:
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
func TestAnalyzeAcceptsBareReferenceTypeSyntax(t *testing.T) {
	src := `struct Box:
	value: int

def read(box: Box&) -> int:
	ptr: u8& = "hello" as u8&
	return box.value + ptr[0].int()
`
	_, errs := parseAndAnalyze(t, "bare_reference_type_accept.llcontext", src)
	requireNoErrors(t, errs)
}
func TestParseRejectsLegacyDotReferenceCastSyntax(t *testing.T) {
	src := `def bits_ptr(bits: uintptr) -> u8&:
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
	src := `struct Box:
	value: int

extern maybe_heap_box() -> heap Box&?

def widen(box: heap Box&?) -> Box&?:
	return box.cast[Box&?]

def keep_heap(box: heap Box&?) -> heap Box&?:
	return box.cast[Box&?].cast[heap Box&?]

def coerce_text() -> u8&:
	return "hello" as u8&

def use_source() -> Box&?:
	return maybe_heap_box().cast[Box&?]

def explicit_any_still_works(box: heap Box&?) -> Box&?:
	return box.cast[Box&?]
`
	result, errs := parseAndAnalyze(t, "storage_cast_syntax.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeRefSugarAddressCast(t *testing.T) {
	src := `struct Box:
	value: int

def widen_local() -> Box&:
	box: Box = zeroed
	return box.ref[Box&]

def bytes_local() -> u8&:
	buf: u8[4] = zeroed
	return buf.ref[u8&]

def keep_explicit_stack() -> stack Box&:
	box: Box = zeroed
	return box.ref[stack Box&]
`
	result, errs := parseAndAnalyze(t, "ref_sugar_address_cast.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeRejectsLegacyCastSyntax(t *testing.T) {
	src := `struct Box:
	value: int

def widen(box: heap Box&?) -> Box&?:
	return box.cast[Box&?]()
`
	_, errs := parseAndAnalyze(t, "legacy_cast_error.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected parser error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "legacy cast syntax `.cast[T]()` is no longer supported") {
		t.Fatalf("expected legacy cast parser diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsImplicitAnyStorageWithoutCast(t *testing.T) {
	src := `struct Box:
	value: int

def ok(box: heap Box&) -> Box&:
	return box
`
	_, errs := parseAndAnalyze(t, "implicit_any_storage_without_cast.llcontext", src)
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
