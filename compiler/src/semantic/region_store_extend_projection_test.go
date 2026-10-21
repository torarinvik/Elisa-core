package semantic

import (
	"testing"

	"elisacore/src/ast"
)

// Querying before the callee body is typed must still recognize deep element copies.
func TestStoreScanExtendProjectionBeforeBodyTyping(t *testing.T) {
	for _, cold := range []bool{false, true} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "copy_elements_review.elisa", `global region source
struct Row:
    safe: darray[sview @source]
    unsafe: darray[sview]
    nested: darray[darray[sview @source]]
struct Source:
    rows: darray[Row]
struct Target:
    values: mutable darray[sview] = []
    nested: mutable darray[darray[sview @source]] = []
def copy_safe(target: mutable Target&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        target.values.extend(state.rows[0].safe)
def forward_safe(target: mutable Target&, state: Source&) -> void:
    copy_safe(target, state)
def copy_unsafe(target: mutable Target&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        target.values.extend(state.rows[0].unsafe)
def copy_nested_headers(target: mutable Target&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        target.nested.extend(state.rows[0].nested)
`, AnalyzeOptions{})
		a := result.analyzer
		a.currentScope = a.globalScope
		if cold {
			for _, name := range []string{"copy_safe", "forward_safe", "copy_unsafe", "copy_nested_headers"} {
				decl := storeScanFindFunc(t, result, name)
				for _, node := range a.storeScanIndex(decl).flat {
					if expr, ok := node.(ast.Expr); ok {
						delete(a.exprTypes, expr)
					}
				}
			}
			a.storeScanCache = nil
			a.storeScanTaintCache = nil
			a.storeScanViewCache = nil
			a.storeScanScalarCache = nil
			a.storeScanFilledCache = nil
		}
		for _, name := range []string{"copy_safe", "forward_safe", "copy_unsafe", "copy_nested_headers"} {
			decl := storeScanFindFunc(t, result, name)
			mode := a.calleeStoreModeInto(decl, 1, 0)
			proof := a.calleeStoresProgramLivedElements(decl, 1, 0, map[storeScanKey]bool{})
			t.Logf("cold=%v %s: mode=%d proof=%v", cold, name, mode, proof)
			retains := mode != storeNone && !proof
			wantRetains := name == "copy_unsafe" || name == "copy_nested_headers"
			if retains != wantRetains {
				t.Errorf("cold=%v %s: retains source storage=%v, want %v", cold, name, retains, wantRetains)
			}
		}
	}
}
