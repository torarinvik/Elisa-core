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
	all := strings.Join(result.Warnings(), "\n")
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
	all := strings.Join(result.Warnings(), "\n")
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
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, `alloc_value`) || strings.Contains(all, `explicit local effect grant`) {
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
	all := strings.Join(result.Warnings(), "\n")
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
	all := strings.Join(result.Warnings(), "\n")
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

func TestPointerLikeCastRequiresUnsafePointerCastGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "pointer_cast_requires_unsafe.elisa", `
def build(value: uintptr) -> heap u8&:
    return value.cast[heap u8&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "trusted_pointer_cast.elisa", `
def build(value: uintptr) -> heap u8&:
    trusted Unsafe.PointerCast:
        return value.cast[heap u8&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "pointer_arithmetic_requires_unsafe.elisa", `
def advance(ptr: u8&, offset: usize) -> u8&:
    return ptr + offset
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "commutative_pointer_arithmetic_requires_unsafe.elisa", `
def advance(offset: usize, ptr: u8&) -> u8&:
    return offset + ptr
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "trusted_pointer_arithmetic.elisa", `
def advance(ptr: u8&, offset: usize) -> u8&:
    trusted Unsafe.PointerArithmetic:
        return ptr + offset
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "pointer_cast_grant_not_arithmetic.elisa", `
def advance(ptr: u8&, offset: usize) -> u8&:
    trusted Unsafe.PointerCast:
        return ptr + offset
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "raw_extern_requires_unsafe.elisa", `
extern c_puts(text: u8&) -> int

def write(text: u8&) -> int:
    return c_puts(text)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "trusted_raw_extern.elisa", `
extern c_puts(text: u8&) -> int

def write(text: u8&) -> int:
    trusted Unsafe.RawExtern:
        return c_puts(text)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "raw_extern_preserves_declared_permissions.elisa", `
extern c_puts(text: u8&) -> int can[Console.Write]

def write(text: u8&) -> int:
    can Console.Write:
        trusted Unsafe.RawExtern:
            return c_puts(text)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "mutable_global_requires_unsafe.elisa", `
global mutable counter: int = 0

def read_counter() -> int:
    return counter
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "trusted_mutable_global.elisa", `
global mutable counter: int = 0

def read_counter() -> int:
    trusted Unsafe.MutableGlobal:
        return counter
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "mutable_global_assignment_requires_unsafe.elisa", `
global mutable counter: int = 0

def set_counter(value: int) -> void:
    counter <- value
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "ref_index_requires_unsafe.elisa", `
def read_at(ptr: u8&, index: usize) -> u8:
    return ptr[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "trusted_ref_index.elisa", `
def read_at(ptr: u8&, index: usize) -> u8:
    trusted Unsafe.UncheckedIndex:
        return ptr[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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

func TestUncheckedIndexGrantDoesNotCoverPointerArithmetic(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "unchecked_index_grant_not_arithmetic.elisa", `
def advance(ptr: u8&, offset: usize) -> u8&:
    trusted Unsafe.UncheckedIndex:
        return ptr + offset
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	all := strings.Join(result.Warnings(), "\n")
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
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "static_ref_thread_share_no_unsafe.elisa", `
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
	all := strings.Join(result.Warnings(), "\n")
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
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "numeric_cast_no_unsafe.elisa", `
def build(value: i64) -> u64:
    return value.cast[u64]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
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
	all := strings.Join(result.Warnings(), "\n")
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
	all := strings.Join(result.Warnings(), "\n")
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
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, `panic requires`) || strings.Contains(all, `explicit local effect grant`) {
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
	all := strings.Join(result.Warnings(), "\n")
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
	all := strings.Join(result.Warnings(), "\n")
	if strings.Contains(all, `redundantly`) {
		t.Fatalf("expected no redundant grant warning for grant outside surrounding scope, got:\n%s", all)
	}
}
