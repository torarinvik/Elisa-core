package purereturnshape

import (
	"testing"

	"elisacore/src/ast"
)

func TestCasesFromStmtsPreservesReturnAndBudget(t *testing.T) {
	value := &ast.IntLit{Value: "1"}
	rewrite := func(expr ast.Expr, _ map[string]ast.Expr, _ *int) (ast.Expr, bool) { return expr, true }
	pure := func(ast.Expr) bool { return true }
	work := 2
	cases, ok := CasesFromStmts([]ast.Stmt{&ast.ReturnStmt{Value: value}}, nil, nil, &work, 0, rewrite, pure)
	if !ok || len(cases) != 1 || cases[0].Expr != value {
		t.Fatalf("cases=%v ok=%v", cases, ok)
	}
	work = 0
	if _, ok := CasesFromStmts([]ast.Stmt{&ast.ReturnStmt{Value: value}}, nil, nil, &work, 0, rewrite, pure); ok {
		t.Fatal("exhausted budget accepted")
	}
}
