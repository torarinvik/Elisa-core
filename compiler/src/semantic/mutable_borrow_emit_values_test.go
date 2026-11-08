package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestMutableBorrowEmitValuesNestedScopes(t *testing.T) {
	first := &ast.Ident{Name: "first"}
	last := &ast.Ident{Name: "last"}
	hidden := &ast.Ident{Name: "hidden"}
	body := []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.EmitExpr{Value: first}},
		&ast.ExprStmt{Expr: &ast.FoldExpr{Arms: []ast.VisitArm{{Body: []ast.Stmt{&ast.ExprStmt{Expr: &ast.EmitExpr{Value: hidden}}}}}}},
		&ast.ExprStmt{Expr: &ast.LambdaExpr{BodyExpr: &ast.EmitExpr{Value: hidden}}},
		&ast.ExprStmt{Expr: &ast.EmitExpr{Value: last}},
	}
	got := mutableBorrowEmitValues(body)
	if len(got) != 2 || got[0] != first || got[1] != last {
		t.Fatalf("got %v, want first and last", got)
	}
}
