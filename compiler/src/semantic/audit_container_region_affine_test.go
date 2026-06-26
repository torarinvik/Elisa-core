package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Deep audit #7/#8: `set[T] @r` is a region-carrying container exactly like
// `darray[T] @r` / `dict[K,V] @r`. typeCanCarryRegion previously omitted SetType, so a
// valid `set[i64] @r` binding was wrongly rejected with "cannot carry a region"; and
// containerTypeRegion omitted SetType, so a live `set @r` dependency went untracked. Both
// must be fixed together (a naive accept without tracking opens the missed-check direction).
func TestSetTypeCanCarryRegionAnnotation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "set_region_carry.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region r(4096):
            s: mutable set[i64] @r = {}
            s.add(1)
            s.add(2)
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "cannot carry a region") {
		t.Fatalf("set[T] @r must be accepted as a region-carrying container, got:\n%s", allDiagnostics(result))
	}
}

// Deep audit #15: cloning a default-argument expression dropped ListLitExpr.Keys, so a
// brace DICT default `= {1: 2}` was silently re-materialized as a set literal (Keys == nil),
// changing the value's type from dict to set. The clone must carry the keys through.
func TestCloneDefaultArgPreservesDictLiteralKeys(t *testing.T) {
	pos := lexer.Pos{}
	dictLit := &ast.ListLitExpr{
		Position: pos,
		Brace:    true,
		Keys:     []ast.Expr{&ast.IntLit{Position: pos, Value: "1"}},
		Elems:    []ast.Expr{&ast.IntLit{Position: pos, Value: "2"}},
	}
	cloned := cloneDefaultArgExpr(dictLit)
	clonedLit, ok := cloned.(*ast.ListLitExpr)
	if !ok || clonedLit == nil {
		t.Fatalf("expected clone to remain a *ast.ListLitExpr, got %T", cloned)
	}
	if len(clonedLit.Keys) != 1 {
		t.Fatalf("dict-literal clone dropped its keys: a `{1: 2}` dict default became a set literal (Keys=%v)", clonedLit.Keys)
	}
	if len(clonedLit.Elems) != 1 {
		t.Fatalf("dict-literal clone lost its values, Elems=%v", clonedLit.Elems)
	}
}
