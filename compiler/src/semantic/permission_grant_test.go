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

func TestConcreteParametricImplCallKeepsLocalPermissionRequirement(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "parametric_impl_permission.elisa", `
struct Box[T]:
    item: T

protocol Read:
    type Elem
    def read(s: Self&, fail: bool) -> Elem can[Abort.Panic]

impl[T] Read for Box[T]:
    type Elem = T
    def read(s: Box[T]&, fail: bool) -> T:
        can Abort.Panic:
            if fail:
                panic("requested failure")
        return s.item

def missing_grant(b: Box[i64]&) -> i64:
    return b.read(false)
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, "requires can[Abort] and has no explicit local effect grant") {
		t.Fatalf("expected concrete dispatch to preserve the impl's Abort.Panic requirement, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("missing_grant")
	if !ok {
		t.Fatal("expected missing_grant symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || !strings.Contains(PermissionRefsString(fnType.PermissionRefs), "Abort.Panic") {
		t.Fatalf("expected Abort.Panic to propagate into missing_grant, got %v", sym.Type)
	}
}

func TestParametricImplSignatureDoesNotGrantItsBody(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "parametric_impl_signature_is_not_body_grant.elisa", `
struct Box[T]:
    item: T

protocol Read:
    type Elem
    def read(s: Self&, fail: bool) -> Elem can[Abort.Panic]

impl[T] Read for Box[T]:
    type Elem = T
    def read(s: Box[T]&, fail: bool) -> T can[Abort.Panic]:
        if fail:
            panic("requested failure")
        return s.item

def caller(b: Box[i64]&) -> i64:
    can Abort.Panic:
        return b.read(false)
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `panic requires can[Abort] and has no explicit local effect grant; add can Abort.Panic or a surrounding can ...: block`) {
		t.Fatalf("expected an impl signature effect to remain a caller contract rather than authorize panic in its body, got:\n%s", all)
	}
}

func TestDeclaredCallPermissionRequiresTopLevelGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_call_permission_local_grant.elisa", `
extern alloc_value() -> i64 can[Abort.Panic, Atomics.Load]

def build() -> i64:
	return alloc_value()
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `call to "alloc_value" requires can[Abort, Atomics] and has no explicit local effect grant; add can[Abort.Panic, Atomics.Load] or a surrounding can ...: block`) {
		t.Fatalf("expected missing top-level grant warning on call, got:\n%s", all)
	}
}

func TestDeclaredCallPermissionCanInheritOuterExplicitGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_call_permission_inner_local_grant.elisa", `
extern alloc_value() -> i64 can[Abort.Panic, Atomics.Load]

def build() -> i64:
    can Abort.Panic, Atomics.Load:
        can Atomics.Load:
            return alloc_value()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, `alloc_value`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected outer explicit grant to satisfy nested call, got:\n%s", all)
	}
	if !strings.Contains(all, `can block grants can Atomics.Load redundantly`) {
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
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Abort.Panic, Atomics.Load]" {
		t.Fatalf("expected inferred build permissions, got %q", got)
	}
	analysis, ok := result.FunctionAnalysisByName("build")
	if !ok || analysis == nil {
		t.Fatal("expected build function analysis")
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformRequire, FactEffects, "Abort.Panic", "requires effect authority") {
		t.Fatalf("expected function analysis to expose Abort.Panic require transform, got %#v", analysis.FactTransforms)
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformRequire, FactEffects, "Atomics.Load", "requires effect authority") {
		t.Fatalf("expected function analysis to expose Atomics.Load require transform, got %#v", analysis.FactTransforms)
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

func TestDeclaredMemoryAllocatePermissionRequiresLocalGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_memory_allocate_permission_requires_grant.elisa", `
extern malloc_like() -> i64 can[Memory.Allocate]

def build() -> i64:
    return malloc_like()
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `call to "malloc_like" requires can[Memory] and has no explicit local effect grant; add can Memory.Allocate or a surrounding can ...: block`) {
		t.Fatalf("expected explicit allocation API to require a local grant, got:\n%s", all)
	}
}

// Dual to the darray/dict no-ceremony rule: an *explicit* `panic(...)` in user code is
// the case the user reserves Abort.Panic for, so it still requires a local grant. (Safe
// container growth, which may panic on overflow internally, does not surface this.)
func TestExplicitPanicRequiresLocalAbortGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "explicit_panic_requires_grant.elisa", `def risky(x: i64) -> i64:
    if x < 0:
        panic("negative")
    return x
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `panic requires can[Abort] and has no explicit local effect grant; add can Abort.Panic or a surrounding can ...: block`) {
		t.Fatalf("expected explicit panic to require a local Abort.Panic grant, got:\n%s", all)
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
def caller() -> i64:
    return build()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, `call to "raw_pointer_cast"`) {
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
	if !strings.Contains(all, `call to "build" requires can[Unsafe]`) {
		t.Fatalf("expected caller to need the propagated Unsafe grant, got:\n%s", all)
	}
}

func TestSegmentMutationPermissionRequiresLocalGrantError(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "segment_mutation_requires_grant.elisa", `
extern load_fs(selector: u16) -> void can[Unsafe.SegmentMutation]

def run() -> void:
    load_fs(7)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `call to "load_fs" requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.SegmentMutation or a surrounding can ...: block`) {
		t.Fatalf("expected segment mutation to require local grant as an error, got:\n%s", all)
	}
	if len(result.Errors()) == 0 {
		t.Fatalf("expected missing segment mutation grant to be an error")
	}
}

func TestGuestSegmentInstallPermissionRequiresLocalGrantError(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "guest_segment_install_requires_grant.elisa", `
extern install_guest_fs() -> void can[Unsafe.GuestSegmentInstall]

def run() -> void:
    install_guest_fs()
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `call to "install_guest_fs" requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.GuestSegmentInstall or a surrounding can ...: block`) {
		t.Fatalf("expected guest segment install to require local grant as an error, got:\n%s", all)
	}
	if len(result.Errors()) == 0 {
		t.Fatalf("expected missing guest segment install grant to be an error")
	}
}

func TestSegmentHostPermissionRequiresLocalGrantError(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "segment_host_requires_grant.elisa", `
def host_libc_call() -> void can[Segment.Host]:
    return

def run() -> void:
    host_libc_call()
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `call to "host_libc_call" requires can[Segment] and has no explicit local effect grant; add can Segment.Host or a surrounding can ...: block`) {
		t.Fatalf("expected Segment.Host to require local grant as an error, got:\n%s", all)
	}
	if len(result.Errors()) == 0 {
		t.Fatalf("expected missing Segment.Host grant to be an error")
	}
}

func TestSegmentHostPermissionWithLocalGrantIsQuiet(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "segment_host_with_grant.elisa", `
def host_libc_call() -> void can[Segment.Host]:
    return

def run() -> void:
    can Segment.Host:
        host_libc_call()
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `host_libc_call`) || strings.Contains(all, `Segment.Host`) {
		t.Fatalf("expected Segment.Host local grant to satisfy call, got:\n%s", all)
	}
}

func TestBoundaryPointerArgsRejectsHostReferenceBoundaryShape(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "boundary_pointer_arg_host_ref.elisa", `
@boundary_pointer_args(mutex)
def posix_pthread_mutex_lock(mutex: mutable void&?&?) -> int:
    _ = mutex
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `@boundary_pointer_args on function "posix_pthread_mutex_lock" marks "mutex" as an address-space pointer, but its type is mutable void&?&?; use a typed address-space carrier such as GuestVAddr[T], HostPtr[T], or NativeMappedGuestPtr[T] at the unsafe boundary and resolve before host dereference`) {
		t.Fatalf("expected boundary pointer arg shape diagnostic, got:\n%s", all)
	}
	if len(result.Errors()) == 0 {
		t.Fatalf("expected host-reference boundary pointer arg to be an error")
	}
}

func TestBoundaryPointerArgsRejectsRawIntegerCarrier(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "boundary_pointer_arg_vaddr.elisa", `
type VAddr = uintptr

@boundary_pointer_args(mutex)
def posix_pthread_mutex_lock(mutex: VAddr) -> int:
    _ = mutex
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `@boundary_pointer_args on function "posix_pthread_mutex_lock" marks "mutex" as an address-space pointer, but its type is uintptr`) {
		t.Fatalf("expected raw integer alias boundary pointer arg to be rejected, got:\n%s", all)
	}
	if len(result.Errors()) == 0 {
		t.Fatalf("expected raw integer alias boundary pointer arg to be an error")
	}
}

func TestGuestVAddrIsDistinctFromRawInteger(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "guest_vaddr_distinct.elisa", `
def bad(raw: uintptr) -> GuestVAddr[void]:
    return raw
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "return type expects GuestVAddr[void], got uintptr") {
		t.Fatalf("expected raw integer to guest vaddr assignment rejection, got:\n%s", all)
	}
}

func TestGuestVAddrCastsToStorageAndBack(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "guest_vaddr_cast_storage.elisa", `
def wrap(raw: uintptr) -> GuestVAddr[void]:
    return raw.cast[GuestVAddr[void]]

def unwrap(addr: GuestVAddr[void]) -> uintptr:
    return addr.cast[uintptr]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, "invalid cast") || strings.Contains(all, "GuestHostPointerCast") {
		t.Fatalf("expected explicit storage casts for GuestVAddr, got:\n%s", all)
	}
}

func TestBoundaryPointerArgsAcceptsGuestVAddrCarrier(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "boundary_pointer_arg_guest_vaddr.elisa", `
@boundary_pointer_args(mutex)
def posix_pthread_mutex_lock(mutex: GuestVAddr[void]) -> int:
    _ = mutex
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, "@boundary_pointer_args") {
		t.Fatalf("expected GuestVAddr boundary pointer arg to satisfy annotation, got:\n%s", all)
	}
}

func TestBoundaryPointerArgCastToHostRefRequiresGuestHostPointerCastGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "boundary_pointer_arg_host_cast_requires_grant.elisa", `
@boundary_pointer_args(mutex)
def posix_pthread_mutex_lock_hle(mutex: GuestVAddr[void]) -> int:
    slot: mutable void&?&? = mutex.cast[mutable void&?&?]
    _ = slot
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `guest-host pointer cast requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.GuestHostPointerCast or a surrounding can ...: block`) {
		t.Fatalf("expected guest-host pointer cast diagnostic, got:\n%s", all)
	}
	if len(result.Errors()) == 0 {
		t.Fatalf("expected guest-host pointer cast to require an explicit grant")
	}
}

func TestBoundaryPointerArgCastToHostRefAcceptsGuestHostPointerCastGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "boundary_pointer_arg_host_cast_with_grant.elisa", `
@boundary_pointer_args(mutex)
def posix_pthread_mutex_lock_hle(mutex: GuestVAddr[void]) -> int:
    trusted Unsafe.GuestHostPointerCast:
        slot: mutable void&?&? = mutex.cast[mutable void&?&?]
        _ = slot
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `guest-host pointer cast requires`) {
		t.Fatalf("expected explicit guest-host pointer grant to satisfy cast, got:\n%s", all)
	}
}

func TestPointerLikeCastRequiresUnsafePointerCastGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "pointer_cast_requires_unsafe.elisa", `
def build(value: uintptr) -> heap u8&:
    return value.cast[heap u8&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `forging a reference from an integer (`+"`.cast`"+` of a number to a reference type) requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.PointerCast or a surrounding can ...: block`) {
		t.Fatalf("expected missing unsafe pointer cast grant error, got:\n%s", all)
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
