package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestNestedStoreFlowSCCKeepsOuterInProgressSummary(t *testing.T) {
	call := func(name string) ast.Stmt {
		return &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: name}}}
	}
	outer := &ast.FuncDecl{Name: "outer", Body: []ast.Stmt{call("nested")}}
	nested := &ast.FuncDecl{Name: "nested", Body: []ast.Stmt{call("outer")}}
	inProgress := newStoreFlowSummary(0)
	a := &Analyzer{
		funcDeclSymbols: map[*ast.FuncDecl]*Symbol{
			outer:  {Name: "outer"},
			nested: {Name: "nested"},
		},
		storeFlowInProgress: map[*ast.FuncDecl]*storeFlowSummary{outer: inProgress},
		storeFlowActive:     map[*ast.FuncDecl]bool{outer: true},
	}
	if got := a.storeFlowSummaryFor(nested); got != nil {
		t.Fatalf("overlapping nested component should remain unknown, got %#v", got)
	}
	if a.storeFlowInProgress[outer] != inProgress || !a.storeFlowActive[outer] {
		t.Fatal("nested component query cleared the outer solver state")
	}
}
