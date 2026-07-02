package semantic

import (
	"strings"
	"testing"
)

func TestExternABIAnnotationsPopulateFunctionType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "extern_abi_annotations.elisa", `
@intrinsic(llvm.ctpop.i64)
extern popcount64(value: u64) -> u64

@callconv(winapi)
extern winapi(value: i32) -> i32

@callconv(c)
extern c_api(value: i32) -> i32
`)

	popcountSym, ok := result.GlobalScope.Lookup("popcount64")
	if !ok {
		t.Fatal("expected popcount64 symbol")
	}
	popcountType, ok := popcountSym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected popcount64 function type, got %T", popcountSym.Type)
	}
	if popcountType.IntrinsicName != "llvm.ctpop.i64" || popcountSym.LinkName != "llvm.ctpop.i64" {
		t.Fatalf("expected intrinsic metadata and link name, got intrinsic=%q link=%q", popcountType.IntrinsicName, popcountSym.LinkName)
	}

	winapiSym, ok := result.GlobalScope.Lookup("winapi")
	if !ok {
		t.Fatal("expected winapi symbol")
	}
	winapiType, ok := winapiSym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected winapi function type, got %T", winapiSym.Type)
	}
	if winapiType.CallConv != "winapi" {
		t.Fatalf("expected winapi calling convention, got %q", winapiType.CallConv)
	}

	capiSym, ok := result.GlobalScope.Lookup("c_api")
	if !ok {
		t.Fatal("expected c_api symbol")
	}
	capiType, ok := capiSym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected c_api function type, got %T", capiSym.Type)
	}
	if capiType.CallConv != "c" {
		t.Fatalf("expected c calling convention, got %q", capiType.CallConv)
	}
}

func TestExternABIAnnotationsRejectInvalidValues(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "extern_abi_annotations_invalid.elisa", `
@intrinsic(ctpop)
extern bad_intrinsic(value: u64) -> u64

@callconv(vectorcall)
extern bad_callconv(value: i32) -> i32
`)

	allErrors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(allErrors, "expects an LLVM intrinsic name starting with \"llvm.\"") {
		t.Fatalf("expected invalid intrinsic error, got:\n%s", allErrors)
	}
	if !strings.Contains(allErrors, "unsupported calling convention \"vectorcall\"") {
		t.Fatalf("expected invalid calling convention error, got:\n%s", allErrors)
	}
}

func TestFunctionABIAnnotationsPopulateFunctionType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "function_abi_annotations.elisa", `
@callconv(winapi)
def winapi_callback(arg: void&) -> u32:
	_ = arg
	return 0

@callconv(c)
def c_callback(arg: void&) -> u32:
	_ = arg
	return 1

@callconv(stdcall)
def stdcall_callback(arg: void&) -> u32:
	_ = arg
	return 2
`)

	tests := map[string]string{
		"winapi_callback":  "winapi",
		"c_callback":       "c",
		"stdcall_callback": "stdcall",
	}
	for name, want := range tests {
		sym, ok := result.GlobalScope.Lookup(name)
		if !ok {
			t.Fatalf("expected %s symbol", name)
		}
		fnType, ok := sym.Type.(*FuncType)
		if !ok {
			t.Fatalf("expected %s function type, got %T", name, sym.Type)
		}
		if fnType.CallConv != want {
			t.Fatalf("expected %s calling convention %q, got %q", name, want, fnType.CallConv)
		}
	}
}

func TestFunctionABIAnnotationsRejectInvalidValues(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "function_abi_annotations_invalid.elisa", `
@callconv(vectorcall)
def bad_callconv(arg: void&) -> u32:
	_ = arg
	return 0
`)

	allErrors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(allErrors, "unsupported calling convention \"vectorcall\" on function \"bad_callconv\"") {
		t.Fatalf("expected invalid function calling convention error, got:\n%s", allErrors)
	}
}

// The redundant ABI aliases @c_abi and @stdcall were removed in favor of the general
// @callconv form; each now reports an actionable migration message.
func TestRemovedABIAliasAnnotationsAreRejected(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"c_abi on function": {
			src:  "@c_abi(c)\ndef f(arg: void&) -> u32:\n\t_ = arg\n\treturn 0\n",
			want: "@c_abi has been removed; use @callconv",
		},
		"stdcall on function": {
			src:  "@stdcall\ndef f(arg: void&) -> u32:\n\t_ = arg\n\treturn 0\n",
			want: "@stdcall has been removed; use @callconv(stdcall)",
		},
		"c_abi on extern": {
			src:  "@c_abi(c)\nextern f(value: i32) -> i32\n",
			want: "@c_abi has been removed; use @callconv",
		},
		"stdcall on extern": {
			src:  "@stdcall\nextern f(value: i32) -> i32\n",
			want: "@stdcall has been removed; use @callconv(stdcall)",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "removed_abi_alias.elisa", tc.src)
			allErrors := strings.Join(result.Errors(), "\n")
			if !strings.Contains(allErrors, tc.want) {
				t.Fatalf("expected removal message %q, got:\n%s", tc.want, allErrors)
			}
		})
	}
}

// The @borrows_return_* variants collapsed into flags on @borrows_return, and
// @c_bind_prefix into a trailing `prefix` flag on @c_bind; each removed name reports a
// migration message.
func TestRemovedBorrowsReturnAndCBindVariantsAreRejected(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"borrows_return_field": {
			src:  "@borrows_return_field(node, node)\nextern f(node: void&) -> void&\n",
			want: "@borrows_return_field has been removed; use @borrows_return with a leading `field` flag",
		},
		"borrows_return_rebased": {
			src:  "@borrows_return_rebased(node)\nextern f(node: void&) -> void&\n",
			want: "@borrows_return_rebased has been removed; use @borrows_return with a leading `rebased` flag",
		},
		"borrows_return_field_rebased": {
			src:  "@borrows_return_field_rebased(node, node)\nextern f(node: void&) -> void&\n",
			want: "@borrows_return_field_rebased has been removed; use @borrows_return with leading `field, rebased` flags",
		},
		"c_bind_prefix": {
			src:  "@c_bind_prefix(\"header.h\", \"Foo\")\nstruct Foo layout(c):\n    value: i64\n",
			want: "@c_bind_prefix has been removed; use @c_bind with a trailing `prefix` flag",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "removed_borrow_cbind_variant.elisa", tc.src)
			allErrors := strings.Join(result.Errors(), "\n")
			if !strings.Contains(allErrors, tc.want) {
				t.Fatalf("expected removal message %q, got:\n%s", tc.want, allErrors)
			}
		})
	}
}

func TestExternFunctionCanBeSatisfiedByLaterElisaDefinition(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "extern_satisfied_later.elisa", `
@callconv(c)
extern bridge(value: i32) -> i32

def bridge(value: i32) -> i32:
	return value + 1
`)

	sym, ok := result.GlobalScope.Lookup("bridge")
	if !ok {
		t.Fatal("expected bridge symbol")
	}
	if sym.Kind != SymbolFunc {
		t.Fatalf("expected bridge implementation to be canonical function, got %s", sym.Kind)
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected bridge function type, got %T", sym.Type)
	}
	if fnType.CallConv != "c" {
		t.Fatalf("expected extern c calling convention to carry onto implementation, got %q", fnType.CallConv)
	}
}

func TestExternFunctionCanBeSatisfiedByEarlierElisaDefinition(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "extern_satisfied_earlier.elisa", `
def bridge(value: i32) -> i32:
	return value + 1

@link_name(native_bridge)
@callconv(c)
extern bridge(value: i32) -> i32
`)

	sym, ok := result.GlobalScope.Lookup("bridge")
	if !ok {
		t.Fatal("expected bridge symbol")
	}
	if sym.Kind != SymbolFunc {
		t.Fatalf("expected bridge implementation to remain canonical function, got %s", sym.Kind)
	}
	if sym.LinkName != "native_bridge" {
		t.Fatalf("expected extern link name to carry onto implementation, got %q", sym.LinkName)
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected bridge function type, got %T", sym.Type)
	}
	if fnType.CallConv != "c" {
		t.Fatalf("expected extern c calling convention to carry onto implementation, got %q", fnType.CallConv)
	}
}

func TestExternFunctionSatisfiedByElisaDefinitionRequiresMatchingSignature(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "extern_satisfied_mismatch.elisa", `
extern bridge(value: i32) -> i32

def bridge(value: u32) -> i32:
	return 1
`)

	allErrors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(allErrors, "extern function \"bridge\" declaration does not match implementation") {
		t.Fatalf("expected extern implementation mismatch error, got:\n%s", allErrors)
	}
}

func TestExternLinkNameAllowsMatchingAliasDeclarations(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "extern_link_name_matching_aliases.elisa", `
@link_name(native_bridge)
extern bridge_one(value: uintptr) -> int

@link_name(native_bridge)
extern bridge_two(value: uintptr) -> int
`)

	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected matching @link_name aliases to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestExternLinkNameRejectsConflictingAliasDeclarations(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "extern_link_name_conflict.elisa", `
@link_name(native_bridge)
extern bridge_one(value: void&) -> int

@link_name(native_bridge)
extern bridge_two(value: uintptr) -> int
`)

	allErrors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(allErrors, "extern link_name \"native_bridge\"") {
		t.Fatalf("expected conflicting @link_name diagnostic, got:\n%s", allErrors)
	}
	if !strings.Contains(allErrors, "avoid native symbol splitting") {
		t.Fatalf("expected native symbol splitting guidance, got:\n%s", allErrors)
	}
}

func TestExternLinkNameRejectsFunctionAndVariableCollision(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "extern_link_name_func_var_conflict.elisa", `
@link_name(native_bridge)
extern bridge_value: uintptr

@link_name(native_bridge)
extern bridge_call(value: uintptr) -> int
`)

	allErrors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(allErrors, "extern link_name \"native_bridge\"") {
		t.Fatalf("expected function/variable @link_name collision diagnostic, got:\n%s", allErrors)
	}
	if !strings.Contains(allErrors, "var uintptr") {
		t.Fatalf("expected variable signature in diagnostic, got:\n%s", allErrors)
	}
}

func TestWindowsFFIModelCanBeGuardedByTargetStaticIf(t *testing.T) {
	src := `
static if ELISA_TARGET_OS_WINDOWS:
	@c_opaque(windows.h, CRITICAL_SECTION)
	extern Win32CriticalSection

	@callconv(winapi)
	extern EnterCriticalSection(section: mutable Win32CriticalSection&) -> void

	def lock(section: mutable Win32CriticalSection&):
		EnterCriticalSection(section)
`

	windows := analyzeFunctionAnalysisTestSourceWithOptions(t, "windows_ffi_model.elisa", src, AnalyzeOptions{TargetTriple: "x86_64-pc-windows-msvc"})
	if errs := windows.Errors(); len(errs) != 0 {
		t.Fatalf("expected no Windows-target errors, got:\n%s", strings.Join(errs, "\n"))
	}

	sym, ok := windows.GlobalScope.Lookup("EnterCriticalSection")
	if !ok {
		t.Fatal("expected guarded Win32 extern on Windows target")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected function type for Win32 extern, got %T", sym.Type)
	}
	if fnType.CallConv != "winapi" {
		t.Fatalf("expected winapi callconv, got %q", fnType.CallConv)
	}
	opaque, ok := windows.NamedTypes["Win32CriticalSection"].(*OpaqueType)
	if !ok {
		t.Fatalf("expected Win32CriticalSection opaque type, got %T", windows.NamedTypes["Win32CriticalSection"])
	}
	if opaque.CHeader != "windows.h" || opaque.CType != "CRITICAL_SECTION" {
		t.Fatalf("expected c_opaque metadata, got header=%q type=%q", opaque.CHeader, opaque.CType)
	}

	posix := analyzeFunctionAnalysisTestSourceWithOptions(t, "windows_ffi_model_posix.elisa", src, AnalyzeOptions{TargetTriple: "x86_64-unknown-linux-gnu"})
	if errs := posix.Errors(); len(errs) != 0 {
		t.Fatalf("expected no POSIX-target errors, got:\n%s", strings.Join(errs, "\n"))
	}
	if _, ok := posix.GlobalScope.Lookup("EnterCriticalSection"); ok {
		t.Fatal("did not expect guarded Win32 extern on POSIX target")
	}
}

func TestDuplicateIdenticalExternFunctionDeclarationsCoalesce(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "duplicate_identical_extern.elisa", `
extern puts(text: u8&) -> int
extern puts(text: u8&) -> int
`)
	errors := strings.Join(result.Errors(), "\n")
	if strings.Contains(errors, DuplicateDeclarationMessage("puts", SymbolExternFunc)) {
		t.Fatalf("did not expect duplicate extern diagnostic for identical declaration, got:\n%s", errors)
	}
}

func TestDuplicateConflictingExternFunctionDeclarationsError(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "duplicate_conflicting_extern.elisa", `
extern puts(text: u8&) -> int
extern puts(text: u8&) -> u64
`)
	errors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errors, "extern function \"puts\" declaration does not match implementation") {
		t.Fatalf("expected conflicting extern diagnostic, got:\n%s", errors)
	}
}

func TestPascalCaseFunctionCallCanFallbackFromStructLiteralSyntax(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "pascal_case_function_call.elisa", `
def DivCeil(value: usize, divisor: usize) -> usize:
    return (value + divisor - 1) / divisor

def main() -> usize:
    return DivCeil(9, 4)
`)

	sym, ok := result.GlobalScope.Lookup("main")
	if !ok {
		t.Fatal("expected main symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType.Return == nil {
		t.Fatalf("expected main function type, got %T", sym.Type)
	}
}

// An `extern` forward declaration that matches an Elisa `def` of the same name
// must reconcile even though the extern carries an implicit can[Unsafe] effect
// the implementation lacks. This covers a regression where, in large units, the
// effect difference defeated SameType-based reconciliation: the first symbol in
// a cluster reported "does not match implementation" and the rest (especially
// those whose first parameter is a reference, mistaken for an overload receiver)
// reported "duplicate declaration".
func TestExternForwardDeclarationReconcilesWithImplementation(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "extern_forward_decl_reconcile.elisa", `
@callconv(c)
def k_create(type_: int, out_error: mutable int&?) -> void&?:
    return null
@callconv(c)
def k_destroy(handle: void&?) -> int:
    return 0

extern k_create(type_: int, out_error: mutable int&?) -> void&?
extern k_destroy(handle: void&?) -> int

def use_them() -> int:
    e: mutable int = 0
    h: void&? = k_create(0, &e)
    return k_destroy(h)
`)
}
