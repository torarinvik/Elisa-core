package parserdesugar

import (
	"elisacore/src/ast"
	"testing"
)

func TestNormalizeFunctionTailReturns(t *testing.T) {
	expr := &ast.Ident{Name: "x"}
	original := []ast.Stmt{&ast.ExprStmt{Expr: expr}}
	got := NormalizeFunctionTailReturns(original, &ast.NamedType{Name: "i64"})
	if ret, ok := got[0].(*ast.ReturnStmt); !ok || ret.Value != expr {
		t.Fatal("value tail was not returned")
	}
	if _, ok := original[0].(*ast.ExprStmt); !ok {
		t.Fatal("mutated original body")
	}
	if _, ok := NormalizeFunctionTailReturns(original, &ast.NamedType{Name: "void"})[0].(*ast.ExprStmt); !ok {
		t.Fatal("rewrote void tail")
	}
	panicCall := &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "panic"}}}
	if got := NormalizeFunctionTailReturns([]ast.Stmt{panicCall}, &ast.NamedType{Name: "i64"}); got[0] != panicCall {
		t.Fatal("rewrote panic")
	}
}
