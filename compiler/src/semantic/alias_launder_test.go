package semantic

import (
	"strings"
	"testing"
)

// A borrow laundered through a reference-returning call must still be caught as an alias when
// passed alongside an overlapping mutable borrow. Without provenance propagation, `r = get_ref(&x)`
// would lose x's root and `mutate_pair(r, &x)` would slip past the call-site alias checker.
func TestLaunderedRefThroughCallReturnRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "laundered_ref_call_return.elisa", `
def get_ref(p: mutable i32&) -> mutable i32&:
    return p

def mutate_pair(a: mutable i32&, b: mutable i32&) -> void:
    pass

def run(x: mutable i32&) -> void:
    r: mutable i32& = get_ref(x)
    mutate_pair(r, x)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe]`) {
		t.Fatalf("expected laundered-ref alias to require unsafe grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType := sym.Type.(*FuncType)
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected laundered call to infer alias permission, got %q", got)
	}
}

// Inline form: f(get_ref(&x), &x) — the laundering call is itself an argument.
func TestInlineLaunderedRefThroughCallReturnRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "inline_laundered_ref_call_return.elisa", `
def get_ref(p: mutable i32&) -> mutable i32&:
    return p

def mutate_pair(a: mutable i32&, b: mutable i32&) -> void:
    pass

def run(x: mutable i32&) -> void:
    mutate_pair(get_ref(x), x)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe]`) {
		t.Fatalf("expected inline laundered-ref alias to require unsafe grant, got:\n%s", all)
	}
}
