package semantic

import (
	"strings"
	"testing"
)

// `get arr[i]` is a bounds-checked container access: with an `else` fallback it
// is total and requires no Unsafe.UncheckedIndex grant.
func TestGetIndexWithElseFallbackIsChecked(t *testing.T) {
	analyzeFunctionAnalysisTestSourceWithOptions(t, "get_index_else.elisa", `def f(xs: darray[i32]&, i: usize) -> i32:
    return get xs[i] else 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
}

// `get opt` without else propagates absence; the enclosing function must return
// an optional.
func TestGetPropagationRequiresOptionalReturn(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "get_prop_bad.elisa", `def f(o: i32?) -> i32:
    return get o
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "get without else") || !strings.Contains(all, "optional") {
		t.Fatalf("expected get-propagation to require optional return; got: %s", all)
	}
}

// `get opt` is legal when the enclosing function returns an optional.
func TestGetPropagationCleanWithOptionalReturn(t *testing.T) {
	analyzeTreeTestSource(t, "get_prop_ok.elisa", `def f(o: i32?) -> i32?:
    x: i32 = get o
    return x
`)
}

// `get value else fallback_value` unwraps an optional with a value fallback.
func TestGetOptionalElseValue(t *testing.T) {
	analyzeTreeTestSource(t, "get_opt_else.elisa", `def f(o: i32?) -> i32:
    return get o else 7
`)
}

// `get arr[i] else return null` attaches the recovery to the `get`, not the
// index, and is a checked access requiring no Unsafe.UncheckedIndex grant.
func TestGetIndexElseReturn(t *testing.T) {
	analyzeFunctionAnalysisTestSourceWithOptions(t, "get_index_return.elisa", `def f(xs: darray[i32]&, i: usize) -> i32?:
    v: i32 = get xs[i] else return null
    return v
`, AnalyzeOptions{EnforceUnsafePermissions: true})
}

// `get arr[i]` with no else propagates absence; legal when the function returns
// an optional, and a checked access (no Unsafe.UncheckedIndex grant needed).
func TestGetIndexPropagation(t *testing.T) {
	analyzeFunctionAnalysisTestSourceWithOptions(t, "get_index_prop.elisa", `def f(xs: darray[i32]&, i: usize) -> i32?:
    v: i32 = get xs[i]
    return v
`, AnalyzeOptions{EnforceUnsafePermissions: true})
}

// No-laundering regression: a permission-requiring operation buried INSIDE a
// `get` operand must still be seen by the permission-inference walker. Reading a
// mutable global requires Unsafe.MutableGlobal; wrapping it in `get` must not
// launder that obligation away. If the walker failed to recurse into GetExpr,
// the inferred permission set would be empty and this assertion would fail.
func TestGetDoesNotLaunderPermissionThroughWalker(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "get_no_launder_perm.elisa", `
global mutable counter: int? = null

def read_counter() -> int:
    return get counter else 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	sym, ok := result.GlobalScope.Lookup("read_counter")
	if !ok {
		t.Fatal("expected read_counter symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_counter function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); !strings.Contains(got, "Unsafe.MutableGlobal") {
		t.Fatalf("get laundered the mutable-global permission; expected Unsafe.MutableGlobal in inferred set, got %q", got)
	}
}
