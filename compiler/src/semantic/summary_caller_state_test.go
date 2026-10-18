package semantic

import (
	"elisacore/src/ast"
	"testing"
)

// Lazy callee inference runs inside an active caller. Its local region-taint
// inventory must not replace the caller's inventory on return.
func TestLazySummaryPreservesCallerInteriorRegionTaint(t *testing.T) {
	for _, borrowed := range []bool{false, true} {
		a := newSemanticHardeningTestAnalyzer()
		a.globalScope = NewScope(nil)
		a.currentScope = NewScope(a.globalScope)
		a.returnProvenanceInProgress = map[*ast.FuncDecl]bool{}
		a.returnBorrowedOwnerRefInProgress = map[*ast.FuncDecl]bool{}
		root := &Symbol{Name: "caller_root"}
		a.currentStructInteriorRegionTaint = map[*Symbol]string{root: "caller_region"}
		fn := &ast.FuncDecl{Name: "callee"}
		typ := &FuncType{Name: "callee", Return: &RefType{Elem: &BuiltinType{Name: "i64"}}}
		if borrowed {
			a.inferFuncReturnBorrowedOwnerRefs(fn, typ)
		} else {
			a.inferFuncReturnProvenance(fn, typ)
		}
		if a.currentStructInteriorRegionTaint[root] != "caller_region" {
			t.Fatalf("borrowed=%t: lost caller region taint", borrowed)
		}
	}
}
