package semantic

import (
	"strings"
	"testing"
)

// The `value.call_as[func(...)->T](args)` indirect-call primitive is gated by
// Unsafe.IndirectCall (a hard error when missing under enforcement). The
// synthetic reinterpret cast it produces must NOT additionally require
// Unsafe.PointerCast — the single IndirectCall grant covers the whole op.

func TestIndirectCallRequiresUnsafeIndirectCallGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "indirect_call_requires_unsafe.elisa", `
def run(p: void&?) -> int:
    return p.call_as[func(int) -> int](7)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `indirect call requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.IndirectCall or a surrounding can ...: block`) {
		t.Fatalf("expected missing unsafe indirect-call grant error, got:\n%s", all)
	}
	if strings.Contains(all, `pointer cast requires`) {
		t.Fatalf("call_as cast must not also require Unsafe.PointerCast, got:\n%s", all)
	}
}

func TestCanIndirectCallInfersCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "can_indirect_call.elisa", `
def run(p: void&?) -> int:
    can Unsafe.IndirectCall:
        return p.call_as[func(int) -> int](7)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `indirect call requires`) || strings.Contains(all, `pointer cast requires`) {
		t.Fatalf("expected can block to satisfy indirect-call grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.IndirectCall]" {
		t.Fatalf("expected indirect call to infer caller permission, got %q", got)
	}
}

func TestTrustedIndirectCallDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_indirect_call.elisa", `
def run(p: void&?) -> int:
    trusted Unsafe.IndirectCall:
        return p.call_as[func(int) -> int](7)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `indirect call requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted block to satisfy indirect-call grant, got:\n%s", all)
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
		t.Fatalf("expected trusted indirect call not to infer caller permission, got %q", got)
	}
}
