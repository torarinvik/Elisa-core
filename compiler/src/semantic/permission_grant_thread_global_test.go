package semantic

import (
	"strings"
	"testing"
)

func TestThreadTransferOfNonStaticRefRequiresUnsafeThreadShareGrant(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "thread_share_requires_unsafe.elisa", `
def spawn1[A, R](fn: fn(A) -> R, arg: A) -> Thread[R, Joinable]:
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
def spawn1[A, R](fn: fn(A) -> R, arg: A) -> Thread[R, Joinable]:
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

def spawn1[A, R](fn: fn(A) -> R, arg: A) -> Thread[R, Joinable]:
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
    can Abort.Panic, Atomics.Load:
        can Atomics.Load:
            panic("boom")
`)
	all := allDiagnostics(result)
	if strings.Contains(all, `panic requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected outer explicit grant to satisfy nested panic, got:\n%s", all)
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
extern alloc_value() -> i64 can[Atomics.Load]

def build() -> i64:
    can Atomics.Load:
        return alloc_value() can Atomics.Load
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `inline can grants can Atomics.Load redundantly`) {
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

// Mutable-global grants are mandatory regardless of the legacy -Wglobals dial.
func TestGlobalPermissionsRequiredByDefault(t *testing.T) {
	const src = `
global mutable hot: i32 = 0

def bump() -> i32:
	hot <- hot + 1
	return hot

def main() -> i64:
	return 1 if bump() != 1
	return 0
`
	off := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "globals_dial_off.elisa", src, AnalyzeOptions{})
	if got := strings.Join(off.Errors(), "\n"); !strings.Contains(got, "mutable global read") || !strings.Contains(got, "mutable global write") || !strings.Contains(got, `call to "bump"`) {
		t.Fatalf("mutable Global grants must be errors without the dial, got:\n%s", got)
	}

	on := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "globals_dial_on.elisa", src, AnalyzeOptions{EnforceGlobalPermissions: true})
	if got := allDiagnostics(on); !strings.Contains(got, `call to "bump" requires can[Global] and has no explicit local effect grant; add can[Global{Read,Write}]`) {
		t.Fatalf("expected the write to require Global.Read and Global.Write, got:\n%s", got)
	}
}

// Reading demands Read alone; a `const` is not a global and stays pure. The two
// members are what make the family worth having over a single "touches a global"
// bit -- a reader and a writer are different obligations.
func TestGlobalReadDemandsReadOnlyAndConstStaysPure(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "globals_read_only.elisa", `
const LIMIT: i32 = 10
global mutable hot: i32 = 0

def peek() -> i32:
	return hot

def scale(width: i32) -> i32:
	return width * LIMIT

def main() -> i64:
	return 1 if peek() != 0
	return 2 if scale(2) != 20
	return 0
`, AnalyzeOptions{EnforceGlobalPermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `call to "peek" requires can[Global] and has no explicit local effect grant; add can Global.Read`) {
		t.Fatalf("expected a read to require Global.Read alone, got:\n%s", all)
	}
	if strings.Contains(all, `call to "scale"`) {
		t.Fatalf("reading a const is pure and must carry no Global effect, got:\n%s", all)
	}
}

// The member-brace sugar is the ergonomic half: `Global{Read, Write}` is exactly
// `Global.Read, Global.Write`, and a surrounding grant written that way silences
// the requirement.
func TestGlobalBraceSugarGrantsBothMembers(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "globals_brace_grant.elisa", `
global mutable hot: i32 = 0

def bump() -> i32:
	can Global{Read, Write}:
		hot <- hot + 1
		return hot

def main() -> i64:
	can Global{Read, Write}:
		return 1 if bump() != 1
	return 0
`, AnalyzeOptions{EnforceGlobalPermissions: true})
	if got := allDiagnostics(result); strings.Contains(got, "can[Global]") {
		t.Fatalf("Global{Read, Write} must grant both members, got:\n%s", got)
	}
}

// A parameter named after a global is that PARAMETER, not the global. The permission
// collector runs over a bare body with no scope pushed, so before it carried its own
// shadow set this function inferred Global.Read (and, under strict, Unsafe.MutableGlobal)
// from a name that never touches the global. The analyzer's scope-aware paths always got
// this right -- `shadowed`'s own fact_snapshot reports no required effect -- which is why
// the collector's disagreement with them went unnoticed until stage1 grew the same family.
func TestGlobalPermissionsRespectShadowingByParamsAndLocals(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "globals_shadowed.elisa", `
global mutable hot: i32 = 0

def shadowed(hot: i32) -> i32:
	return hot

def local_shadowed() -> i32:
	hot: i32 = 3
	return hot

def genuinely_reads() -> i32:
	return hot

def main() -> i64:
	return 1 if shadowed(1) != 1
	return 2 if local_shadowed() != 3
	return 3 if genuinely_reads() != 0
	return 0
`, AnalyzeOptions{EnforceGlobalPermissions: true})

	got := allDiagnostics(result)
	if strings.Contains(got, `call to "shadowed"`) {
		t.Fatalf("a parameter named after a global must not infer Global, got:\n%s", got)
	}
	if strings.Contains(got, `call to "local_shadowed"`) {
		t.Fatalf("a local named after a global must not infer Global, got:\n%s", got)
	}
	// The control: the SAME name, unshadowed, still carries the effect. Without this the
	// test would pass just as well if the family stopped being inferred at all.
	if !strings.Contains(got, `call to "genuinely_reads" requires can[Global] and has no explicit local effect grant; add can Global.Read`) {
		t.Fatalf("an unshadowed read must still require Global.Read, got:\n%s", got)
	}
}

func TestExplicitUnsafeTrackingIndependentOfSourceIdentity(t *testing.T) {
	for _, filename := range []string{"ordinary.elisa", "/tmp/project/elisacore_std/probe.elisa"} {
		for _, strict := range []bool{false, true} {
			mode := "permissive"
			if strict {
				mode = "strict"
			}
			t.Run(filename+"/"+mode, func(t *testing.T) {
				result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, filename, `
def tracked() -> i32:
    can Unsafe.PointerCast:
        return 0

def hidden() -> i32:
    trusted Unsafe.PointerCast:
        can Unsafe.PointerCast:
            return 0

def selective() -> i32:
    trusted Unsafe.PointerCast:
        can Unsafe.MutableGlobal:
            return 0
`, AnalyzeOptions{EnforceUnsafePermissions: strict})
				for name, want := range map[string]string{"tracked": " can[Unsafe.PointerCast]", "hidden": "", "selective": " can[Unsafe.MutableGlobal]"} {
					sym, ok := result.GlobalScope.Lookup(name)
					if !ok {
						t.Fatalf("missing function %s", name)
					}
					fn, ok := sym.Type.(*FuncType)
					if !ok {
						t.Fatalf("unexpected function type %T", sym.Type)
					}
					if got := PermissionRefsString(fn.PermissionRefs); got != want {
						t.Errorf("%s row = %q, want %q", name, got, want)
					}
				}
			})
		}
	}
}

func TestGlobalHeaderContractDoesNotAuthorizeBody(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "header_global.elisa", `
global mutable value: i32 = 0

def read() -> i32 can[Global.Read]:
    return value
`, AnalyzeOptions{})
	if all := allDiagnostics(result); !strings.Contains(all, "explicit local effect grant") {
		t.Fatalf("expected missing body-local grant despite declared caller contract, got:\n%s", all)
	}
}

func TestGroupedGlobalGrantSemanticEquivalence(t *testing.T) {
	for _, grant := range []string{"Global.Read, Global.Write", "Global{Read,Write}"} {
		result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "grouped_global.elisa", `
global mutable value: i32 = 0

def increment():
    can `+grant+`:
        value <- value + 1
`, AnalyzeOptions{})
		if all := allDiagnostics(result); strings.Contains(all, "explicit local effect grant") {
			t.Fatalf("grant %s did not authorize both axes: %s", grant, all)
		}
		sym, _ := result.GlobalScope.Lookup("increment")
		if got := PermissionRefsString(sym.Type.(*FuncType).PermissionRefs); got != " can[Global.Read, Global.Write]" {
			t.Fatalf("grant %s inferred %q", grant, got)
		}
	}
}

func TestRuntimeSourceRequiresExplicitUnsafeGrant(t *testing.T) {
	for _, filename := range []string{"pointer_grant.elisa", "/tmp/project/elisacore_std/probe.elisa"} {
		result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, filename, `
def build(value: uintptr) -> heap u8&:
    return value.cast[heap u8&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
		if all := allDiagnostics(result); !strings.Contains(all, "add can Unsafe.PointerCast") || len(result.Errors()) == 0 {
			t.Fatalf("%s must reject ungranted pointer cast, got:\n%s", filename, all)
		}
	}
}
