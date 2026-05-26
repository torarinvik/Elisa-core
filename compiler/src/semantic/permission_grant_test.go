package semantic

import (
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"strings"
	"testing"
)

func analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t *testing.T, filename string, src string, options AnalyzeOptions) *Result {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	return AnalyzeWithOptions(file, options)
}

func TestDeclaredCallPermissionRequiresTopLevelGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_call_permission_local_grant.elisa", `
extern alloc_value() -> i64 can[Abort.Panic, Memory.Allocate]

def build() -> i64:
	return alloc_value()
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `call to "alloc_value" requires can[Abort, Memory] and has no explicit local effect grant; add  can[Abort.Panic, Memory.Allocate] or a surrounding can ...: block`) {
		t.Fatalf("expected missing top-level grant warning on call, got:\n%s", all)
	}
}

func TestDeclaredCallPermissionCanInheritOuterExplicitGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_call_permission_inner_local_grant.elisa", `
extern alloc_value() -> i64 can[Abort.Panic, Memory.Allocate]

def build() -> i64:
    can Abort.Panic, Memory.Allocate:
        can Memory.Allocate:
            return alloc_value()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, `alloc_value`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected outer explicit grant to satisfy nested call, got:\n%s", all)
	}
	if !strings.Contains(all, `can block grants can Memory.Allocate redundantly`) {
		t.Fatalf("expected redundant nested grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Abort.Panic, Memory.Allocate]" {
		t.Fatalf("expected inferred build permissions, got %q", got)
	}
	analysis, ok := result.FunctionAnalysisByName("build")
	if !ok || analysis == nil {
		t.Fatal("expected build function analysis")
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformRequire, FactEffects, "Abort.Panic", "requires effect authority") {
		t.Fatalf("expected function analysis to expose Abort.Panic require transform, got %#v", analysis.FactTransforms)
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformRequire, FactEffects, "Memory.Allocate", "requires effect authority") {
		t.Fatalf("expected function analysis to expose Memory.Allocate require transform, got %#v", analysis.FactTransforms)
	}
}

func TestDeclaredCallPermissionWithLocalGrantIsQuiet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_call_permission_with_local_grant.elisa", `
extern alloc_value() -> i64 can[Memory.Allocate]

def build() -> i64:
    can Memory.Allocate:
        return alloc_value()
`)
	if all := allDiagnostics(result); strings.Contains(all, `alloc_value`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected no local grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Memory.Allocate]" {
		t.Fatalf("expected inferred build permissions, got %q", got)
	}
}

func TestTrustedPermissionBlockSatisfiesLocalGrantWithoutInference(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "trusted_permission_block.elisa", `
extern raw_pointer_cast() -> i64 can[Unsafe.PointerCast]

def build() -> i64:
    trusted Unsafe.PointerCast:
        return raw_pointer_cast()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, `raw_pointer_cast`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted block to satisfy local unsafe grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted unsafe implementation detail not to infer caller permissions, got %q", got)
	}
}

func TestCanUnsafePermissionBlockStillInfersCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "unsafe_api_permission_block.elisa", `
extern raw_pointer_cast() -> i64 can[Unsafe.PointerCast]

def build() -> i64:
    can Unsafe.PointerCast:
        return raw_pointer_cast()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, `raw_pointer_cast`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected explicit unsafe API grant to satisfy local call, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.PointerCast]" {
		t.Fatalf("expected ordinary can block to infer unsafe caller permission, got %q", got)
	}
}

func TestSegmentMutationPermissionRequiresLocalGrantError(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "segment_mutation_requires_grant.elisa", `
extern load_fs(selector: u16) -> void can[Unsafe.SegmentMutation]

def run() -> void:
    load_fs(7u16)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `call to "load_fs" requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.SegmentMutation or a surrounding can ...: block`) {
		t.Fatalf("expected segment mutation to require local grant as an error, got:\n%s", all)
	}
	if len(result.Errors) == 0 {
		t.Fatalf("expected missing segment mutation grant to be an error")
	}
}

func TestPointerLikeCastRequiresUnsafePointerCastGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "pointer_cast_requires_unsafe.elisa", `
def build(value: uintptr) -> heap u8&:
    return value.cast[heap u8&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `pointer cast requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.PointerCast or a surrounding can ...: block`) {
		t.Fatalf("expected missing unsafe pointer cast grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.PointerCast]" {
		t.Fatalf("expected unsafe pointer cast to infer caller permission, got %q", got)
	}
}

func TestTrustedPointerLikeCastDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_pointer_cast.elisa", `
def build(value: uintptr) -> heap u8&:
    trusted Unsafe.PointerCast:
        return value.cast[heap u8&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `pointer cast requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted block to satisfy pointer cast grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted pointer cast not to infer caller permission, got %q", got)
	}
}

func TestPointerArithmeticRequiresUnsafePointerArithmeticGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "pointer_arithmetic_requires_unsafe.elisa", `
def advance(ptr: u8&, offset: usize) -> u8&:
    return ptr + offset
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `pointer arithmetic requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.PointerArithmetic or a surrounding can ...: block`) {
		t.Fatalf("expected missing unsafe pointer arithmetic grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("advance")
	if !ok {
		t.Fatal("expected advance symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected advance function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.PointerArithmetic]" {
		t.Fatalf("expected unsafe pointer arithmetic to infer caller permission, got %q", got)
	}
}

func TestCommutativePointerArithmeticRequiresUnsafePointerArithmeticGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "commutative_pointer_arithmetic_requires_unsafe.elisa", `
def advance(offset: usize, ptr: u8&) -> u8&:
    return offset + ptr
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `pointer arithmetic requires can[Unsafe]`) {
		t.Fatalf("expected missing unsafe pointer arithmetic grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("advance")
	if !ok {
		t.Fatal("expected advance symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected advance function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.PointerArithmetic]" {
		t.Fatalf("expected commutative pointer arithmetic to infer caller permission, got %q", got)
	}
}

func TestTrustedPointerArithmeticDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_pointer_arithmetic.elisa", `
def advance(ptr: u8&, offset: usize) -> u8&:
    trusted Unsafe.PointerArithmetic:
        return ptr + offset
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `pointer arithmetic requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted block to satisfy pointer arithmetic grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("advance")
	if !ok {
		t.Fatal("expected advance symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected advance function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted pointer arithmetic not to infer caller permission, got %q", got)
	}
}

func TestPointerCastGrantDoesNotCoverPointerArithmetic(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "pointer_cast_grant_not_arithmetic.elisa", `
def advance(ptr: u8&, offset: usize) -> u8&:
    trusted Unsafe.PointerCast:
        return ptr + offset
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `pointer arithmetic requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.PointerArithmetic or a surrounding can ...: block`) {
		t.Fatalf("expected pointer cast grant not to satisfy pointer arithmetic, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("advance")
	if !ok {
		t.Fatal("expected advance symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected advance function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.PointerArithmetic]" {
		t.Fatalf("expected unsatisfied pointer arithmetic to infer caller permission, got %q", got)
	}
}

func TestRawExternCallRequiresUnsafeRawExternGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "raw_extern_requires_unsafe.elisa", `
extern c_puts(text: u8&) -> int

def write(text: u8&) -> int:
    return c_puts(text)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `call to "c_puts" requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.RawExtern or a surrounding can ...: block`) {
		t.Fatalf("expected missing raw extern grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("write")
	if !ok {
		t.Fatal("expected write symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected write function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.RawExtern]" {
		t.Fatalf("expected raw extern to infer caller permission, got %q", got)
	}
}

func TestTrustedRawExternCallDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_raw_extern.elisa", `
extern c_puts(text: u8&) -> int

def write(text: u8&) -> int:
    trusted Unsafe.RawExtern:
        return c_puts(text)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `c_puts`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted raw extern grant to satisfy call, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("write")
	if !ok {
		t.Fatal("expected write symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected write function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted raw extern not to infer caller permission, got %q", got)
	}
}

func TestRawExternPreservesDeclaredPermissions(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "raw_extern_preserves_declared_permissions.elisa", `
extern c_puts(text: u8&) -> int can[Console.Write]

def write(text: u8&) -> int:
    can Console.Write:
        trusted Unsafe.RawExtern:
            return c_puts(text)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `c_puts`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected raw extern grant to be satisfied by trusted block, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("write")
	if !ok {
		t.Fatal("expected write symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected write function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Console.Write]" {
		t.Fatalf("expected declared extern permission to remain visible, got %q", got)
	}
}

func TestMutableGlobalAccessRequiresUnsafeMutableGlobalGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "mutable_global_requires_unsafe.elisa", `
global mutable counter: int = 0

def read_counter() -> int:
    return counter
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable global access requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.MutableGlobal or a surrounding can ...: block`) {
		t.Fatalf("expected missing mutable global grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_counter")
	if !ok {
		t.Fatal("expected read_counter symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_counter function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.MutableGlobal]" {
		t.Fatalf("expected mutable global access to infer caller permission, got %q", got)
	}
}

func TestTrustedMutableGlobalAccessDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_mutable_global.elisa", `
global mutable counter: int = 0

def read_counter() -> int:
    trusted Unsafe.MutableGlobal:
        return counter
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable global access requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted mutable global grant to satisfy access, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_counter")
	if !ok {
		t.Fatal("expected read_counter symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_counter function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted mutable global access not to infer caller permission, got %q", got)
	}
}

func TestMutableGlobalAssignmentRequiresUnsafeMutableGlobalGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "mutable_global_assignment_requires_unsafe.elisa", `
global mutable counter: int = 0

def set_counter(value: int) -> void:
    counter <- value
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable global access requires can[Unsafe]`) {
		t.Fatalf("expected missing mutable global grant warning for assignment, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("set_counter")
	if !ok {
		t.Fatal("expected set_counter symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected set_counter function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.MutableGlobal]" {
		t.Fatalf("expected mutable global assignment to infer caller permission, got %q", got)
	}
}

func TestRefIndexRequiresUnsafeUncheckedIndexGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ref_index_requires_unsafe.elisa", `
def read_at(ptr: u8&, index: usize) -> u8:
    return ptr[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `unchecked index requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.UncheckedIndex or a surrounding can ...: block`) {
		t.Fatalf("expected missing unchecked index grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_at")
	if !ok {
		t.Fatal("expected read_at symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_at function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.UncheckedIndex]" {
		t.Fatalf("expected ref index to infer caller permission, got %q", got)
	}
}

func TestTrustedRefIndexDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_ref_index.elisa", `
def read_at(ptr: u8&, index: usize) -> u8:
    trusted Unsafe.UncheckedIndex:
        return ptr[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `unchecked index requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted unchecked index grant to satisfy access, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_at")
	if !ok {
		t.Fatal("expected read_at symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_at function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted ref index not to infer caller permission, got %q", got)
	}
}

func TestStaleViewRequiresUnsafeStaleRefGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "stale_view_requires_unsafe.elisa", `
def read_stale(items: mutable darray[i32]&) -> i32:
    view: dview[i32] = items[0:items.count]
    items.clear()
    trusted Unsafe.UncheckedIndex:
        return view[0u]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `stale reference requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.StaleRef or a surrounding can ...: block`) {
		t.Fatalf("expected missing stale ref grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_stale")
	if !ok {
		t.Fatal("expected read_stale symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_stale function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.StaleRef]" {
		t.Fatalf("expected stale view use to infer caller permission, got %q", got)
	}
}

func TestTrustedStaleViewDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_stale_view.elisa", `
def read_stale(items: mutable darray[i32]&) -> i32:
    view: dview[i32] = items[0:items.count]
    items.clear()
    trusted Unsafe.StaleRef:
        trusted Unsafe.UncheckedIndex:
            return view[0u]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `stale reference requires`) || strings.Contains(all, `unchecked index requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted stale ref and unchecked index grants to satisfy access, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_stale")
	if !ok {
		t.Fatal("expected read_stale symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_stale function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted stale ref not to infer caller permission, got %q", got)
	}
}

func TestDuplicateMutableCallArgRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "duplicate_mutable_call_arg_requires_alias.elisa", `
struct Node:
    value: mutable i32

def update_pair(left: mutable Node&, right: mutable Node&) -> void:
    pass

def run(node: mutable Node&) -> void:
    update_pair(node, node)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected missing unsafe alias grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected duplicate mutable call arg to infer alias permission, got %q", got)
	}
}

func TestTrustedDuplicateMutableCallArgDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_duplicate_mutable_call_arg.elisa", `
struct Node:
    value: mutable i32

def update_pair(left: mutable Node&, right: mutable Node&) -> void:
    pass

def run(node: mutable Node&) -> void:
    trusted Unsafe.Alias:
        update_pair(node, node)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted alias grant to satisfy duplicate mutable call, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted alias not to infer caller permission, got %q", got)
	}
}

func TestSharedAndMutableCallArgsRequireUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "shared_and_mutable_call_args_require_alias.elisa", `
struct Node:
    value: mutable i32

def inspect_and_update(shared: Node&, writable: mutable Node&) -> void:
    pass

def run(node: mutable Node&) -> void:
    inspect_and_update(node, node)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected shared+mutable call alias warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected shared+mutable call to infer alias permission, got %q", got)
	}
}

func TestDuplicateSharedCallArgsDoNotRequireUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "duplicate_shared_call_args_no_alias.elisa", `
struct Node:
    value: mutable i32

def inspect_pair(left: Node&, right: Node&) -> void:
    pass

def run(node: Node&) -> void:
    inspect_pair(node, node)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected duplicate shared refs to stay safe, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected duplicate shared refs not to infer alias permission, got %q", got)
	}
}

func TestLocalMutableAliasRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "local_mutable_alias_requires_alias.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    first: mutable stack Node& = &node
    second: mutable stack Node& = &node
    _ = first
    _ = second
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected missing unsafe alias grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected local mutable alias to infer alias permission, got %q", got)
	}
}

func TestTrustedLocalMutableAliasDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_local_mutable_alias.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    trusted Unsafe.Alias:
        first: mutable stack Node& = &node
        second: mutable stack Node& = &node
        _ = first
        _ = second
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted alias grant to satisfy local mutable alias, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted local alias not to infer caller permission, got %q", got)
	}
}

func TestLocalSharedThenMutableAliasRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "local_shared_then_mutable_alias_requires_alias.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    shared: stack Node& = &node
    writable: mutable stack Node& = &node
    _ = shared
    _ = writable
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected shared+mutable local alias warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected shared+mutable local alias to infer alias permission, got %q", got)
	}
}

func TestDuplicateLocalSharedAliasesDoNotRequireUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "duplicate_local_shared_aliases_no_alias.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    left: stack Node& = &node
    right: stack Node& = &node
    _ = left
    _ = right
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected duplicate local shared refs to stay safe, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected duplicate local shared refs not to infer alias permission, got %q", got)
	}
}

func TestInnerBlockSharedAliasDoesNotLeakPastScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "inner_block_shared_alias_no_leak.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    if true:
        shared: stack Node& = &node
        _ = shared
    writable: mutable stack Node& = &node
    _ = writable
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected inner shared alias not to leak past scope, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected scoped local alias not to infer alias permission, got %q", got)
	}
}

func TestOuterSharedAliasConflictsInsideNestedScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "outer_shared_alias_conflicts_in_nested_scope.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    shared: stack Node& = &node
    if true:
        writable: mutable stack Node& = &node
        _ = writable
    _ = shared
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected outer shared alias to conflict with nested mutable alias, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected nested local alias conflict to infer alias permission, got %q", got)
	}
}

func TestLiveSharedAliasConflictsWithMutableCallArg(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "live_shared_alias_conflicts_with_mutable_call_arg.elisa", `
struct Node:
    value: mutable i32

def update(value: mutable Node&) -> void:
    pass

def run() -> void:
    node: mutable Node = zeroed
    shared: stack Node& = &node
    update(node)
    _ = shared
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected live shared alias to conflict with mutable call arg, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected live shared alias call conflict to infer alias permission, got %q", got)
	}
}

func TestLiveMutableAliasConflictsWithFreshMutableCallArg(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "live_mutable_alias_conflicts_with_fresh_mutable_call_arg.elisa", `
struct Node:
    value: mutable i32

def update(value: mutable Node&) -> void:
    pass

def run() -> void:
    node: mutable Node = zeroed
    writable: mutable stack Node& = &node
    update(node)
    _ = writable
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected live mutable alias to conflict with fresh mutable call arg, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected live mutable alias call conflict to infer alias permission, got %q", got)
	}
}

func TestPassingLiveMutableAliasItselfDoesNotRequireUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "passing_live_mutable_alias_itself_no_alias.elisa", `
struct Node:
    value: mutable i32

def update(value: mutable Node&) -> void:
    pass

def run() -> void:
    node: mutable Node = zeroed
    writable: mutable stack Node& = &node
    update(writable)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected passing live mutable alias itself to stay safe, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected passing live mutable alias itself not to infer alias permission, got %q", got)
	}
}

func TestAssignedMutableAliasRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "assigned_mutable_alias_requires_alias.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    other: mutable Node = zeroed
    shared: stack Node& = &node
    slot: mutable stack Node& = &other
    slot <- &node
    _ = shared
    _ = slot
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected assigned mutable alias to require alias grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected assigned mutable alias to infer alias permission, got %q", got)
	}
}

func TestAliasAssignmentReleasesPreviousRoot(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "alias_assignment_releases_previous_root.elisa", `
struct Node:
    value: mutable i32

def update(value: mutable Node&) -> void:
    pass

def run() -> void:
    left: mutable Node = zeroed
    right: mutable Node = zeroed
    slot: mutable stack Node& = &left
    slot <- &right
    update(left)
    _ = slot
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected reassigned alias to release previous root, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected reassigned alias not to infer alias permission, got %q", got)
	}
}

func TestAssignedMutableAliasItselfCanBePassedSafely(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "assigned_mutable_alias_itself_can_be_passed.elisa", `
struct Node:
    value: mutable i32

def update(value: mutable Node&) -> void:
    pass

def run() -> void:
    left: mutable Node = zeroed
    right: mutable Node = zeroed
    slot: mutable stack Node& = &left
    slot <- &right
    update(slot)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected assigned alias itself to be passable, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected assigned alias itself not to infer alias permission, got %q", got)
	}
}

func TestUnprovenArrayIndexRequiresUnsafeUncheckedIndexGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "array_index_requires_bounds.elisa", `
def read_at(items: i32[4], index: usize) -> i32:
    return items[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `unchecked index requires can[Unsafe]`) {
		t.Fatalf("expected unproven array index to require unchecked index grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_at")
	if !ok {
		t.Fatal("expected read_at symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_at function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.UncheckedIndex]" {
		t.Fatalf("expected unproven array index to infer caller permission, got %q", got)
	}
}

func TestRangeLoopProvesFixedArrayIndexBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "range_loop_proves_array_index.elisa", `
def sum4(items: i32[4]) -> i32:
    total: mutable i32 = 0
    for i in 0..<4:
        total <- total + items[i]
    return total
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `unchecked index requires`) {
		t.Fatalf("expected range loop bound to prove array indexing, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("sum4")
	if !ok {
		t.Fatal("expected sum4 symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected sum4 function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected proven array index not to infer caller permission, got %q", got)
	}
}

func TestUnprovenDArrayIndexRequiresUnsafeUncheckedIndexGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "darray_index_requires_bounds.elisa", `
def read_at(items: darray[i32], index: usize) -> i32:
    return items[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `unchecked index requires can[Unsafe]`) {
		t.Fatalf("expected unproven darray index to require unchecked index grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_at")
	if !ok {
		t.Fatal("expected read_at symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_at function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.UncheckedIndex]" {
		t.Fatalf("expected unproven darray index to infer caller permission, got %q", got)
	}
}

func TestAssertProvesDArrayIndexBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "assert_proves_darray_index.elisa", `
def read_at(items: darray[i32], index: usize) -> i32:
    can Abort.Panic:
        assert index < items.count
    return items[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `unchecked index requires`) {
		t.Fatalf("expected assert bound to prove darray indexing, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_at")
	if !ok {
		t.Fatal("expected read_at symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_at function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Abort.Panic]" {
		t.Fatalf("expected only assert panic permission, got %q", got)
	}
}

func TestIfBranchProvesDArrayIndexBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "if_branch_proves_darray_index.elisa", `
def read_or_zero(items: darray[i32], index: usize) -> i32:
    if index < items.count:
        return items[index]
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `unchecked index requires`) {
		t.Fatalf("expected if branch bound to prove darray indexing, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_or_zero")
	if !ok {
		t.Fatal("expected read_or_zero symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_or_zero function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected proven darray index not to infer caller permission, got %q", got)
	}
}

func TestEarlyReturnGuardProvesDArrayIndexBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "early_return_proves_darray_index.elisa", `
def read_or_zero(items: darray[i32], index: usize) -> i32:
    if index >= items.count:
        return 0
    return items[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `unchecked index requires`) {
		t.Fatalf("expected early-return guard to prove darray indexing, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_or_zero")
	if !ok {
		t.Fatal("expected read_or_zero symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_or_zero function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected guarded darray index not to infer caller permission, got %q", got)
	}
}

func TestUncheckedIndexGrantDoesNotCoverPointerArithmetic(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "unchecked_index_grant_not_arithmetic.elisa", `
def advance(ptr: u8&, offset: usize) -> u8&:
    trusted Unsafe.UncheckedIndex:
        return ptr + offset
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `pointer arithmetic requires can[Unsafe]`) {
		t.Fatalf("expected unchecked index grant not to satisfy pointer arithmetic, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("advance")
	if !ok {
		t.Fatal("expected advance symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected advance function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.PointerArithmetic]" {
		t.Fatalf("expected pointer arithmetic to infer caller permission, got %q", got)
	}
}

func TestThreadTransferOfNonStaticRefRequiresUnsafeThreadShareGrant(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "thread_share_requires_unsafe.elisa", `
def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
    return zeroed

def worker(cell: i32&) -> i64:
    trusted Unsafe.UncheckedIndex:
        return cell[0].i64()

def start(cell: i32&) -> Thread[i64, Joinable]:
    return spawn1(worker, cell)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `thread share requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.ThreadShare or a surrounding can ...: block`) {
		t.Fatalf("expected missing thread share grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("start")
	if !ok {
		t.Fatal("expected start symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected start function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.ThreadShare]" {
		t.Fatalf("expected thread transfer to infer caller permission, got %q", got)
	}
}

func TestTrustedThreadShareDoesNotInferCallerPermission(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "trusted_thread_share.elisa", `
def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
    return zeroed

def worker(cell: i32&) -> i64:
    trusted Unsafe.UncheckedIndex:
        return cell[0].i64()

def start(cell: i32&) -> Thread[i64, Joinable]:
    trusted Unsafe.ThreadShare:
        return spawn1(worker, cell)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `thread share requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted thread share grant to satisfy transfer, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("start")
	if !ok {
		t.Fatal("expected start symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected start function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted thread share not to infer caller permission, got %q", got)
	}
}

func TestThreadTransferOfStaticRefDoesNotRequireUnsafeThreadShareGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "static_ref_thread_share_no_unsafe.elisa", `
extern shared_cell() -> static i32&

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
    return zeroed

def worker(cell: static i32&) -> i64:
    trusted Unsafe.UncheckedIndex:
        return cell[0].i64()

def start() -> Thread[i64, Joinable]:
    trusted Unsafe.RawExtern:
        return spawn1(worker, shared_cell())
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `Unsafe.ThreadShare`) || strings.Contains(all, `thread share requires`) {
		t.Fatalf("expected static ref transfer not to require unsafe thread share, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("start")
	if !ok {
		t.Fatal("expected start symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected start function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected static ref transfer not to infer caller permission, got %q", got)
	}
}

func TestNumericCastDoesNotRequireUnsafePointerCastGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "numeric_cast_no_unsafe.elisa", `
def build(value: i64) -> u64:
    return value.cast[u64]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `Unsafe.PointerCast`) || strings.Contains(all, `pointer cast requires`) {
		t.Fatalf("expected numeric cast not to require unsafe pointer permission, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected numeric cast not to infer caller permission, got %q", got)
	}
}

func TestDeclaredPanicPermissionRequiresTopLevelGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_panic_permission_top_level_grant.elisa", `
def build() -> void:
    panic("boom")
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `panic requires can[Abort] and has no explicit local effect grant; add can Abort.Panic or a surrounding can ...: block`) {
		t.Fatalf("expected missing top-level Abort grant warning, got:\n%s", all)
	}
}

func TestDeclaredPanicPermissionCanInheritOuterExplicitGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_panic_permission_local_grant.elisa", `
def build() -> void:
    can Abort.Panic, Memory.Allocate:
        can Memory.Allocate:
            panic("boom")
`)
	all := allDiagnostics(result)
	if strings.Contains(all, `panic requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected outer explicit grant to satisfy nested panic, got:\n%s", all)
	}
	if !strings.Contains(all, `can block grants can Memory.Allocate redundantly`) {
		t.Fatalf("expected redundant nested grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Abort.Panic, Memory.Allocate]" {
		t.Fatalf("expected inferred build permissions, got %q", got)
	}
}

func TestDeclaredPanicPermissionWithLocalGrantIsQuiet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_panic_permission_with_local_grant.elisa", `
def build() -> void:
    can Abort.Panic:
        panic("boom")
`)
	if all := allDiagnostics(result); strings.Contains(all, `panic requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected no panic local grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Abort.Panic]" {
		t.Fatalf("expected inferred build permissions, got %q", got)
	}
}

func TestRedundantInlinePermissionGrantWarnsInsideGrantedScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "redundant_inline_permission_grant.elisa", `
extern alloc_value() -> i64 can[Memory.Allocate]

def build() -> i64:
    can Memory.Allocate:
        return alloc_value() can Memory.Allocate
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `inline can grants can Memory.Allocate redundantly`) {
		t.Fatalf("expected redundant inline grant warning, got:\n%s", all)
	}
	if strings.Contains(all, `call to "alloc_value" requires`) {
		t.Fatalf("expected surrounding grant to satisfy call, got:\n%s", all)
	}
}

func TestPartialNestedPermissionGrantDoesNotWarnAsRedundant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "partial_nested_permission_grant.elisa", `
extern write_value() -> i64 can[Console.Write]

def build() -> i64:
    can Memory.Allocate:
        return write_value() can Console.Write
`)
	all := allDiagnostics(result)
	if strings.Contains(all, `redundantly`) {
		t.Fatalf("expected no redundant grant warning for grant outside surrounding scope, got:\n%s", all)
	}
}
