package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestInteriorRefNames(t *testing.T) {
	body := []ast.Stmt{&ast.ExprStmt{Expr: &ast.AddrOfExpr{Operand: &ast.IndexExpr{Object: &ast.ParenExpr{Inner: &ast.Ident{Name: "items"}}, Index: &ast.IntLit{Value: "0"}}}}}
	if !InteriorRefNames(body, map[string]bool{"items": true})["items"] {
		t.Fatal("missed tracked interior reference")
	}
	if len(InteriorRefNames(body, map[string]bool{"other": true})) != 0 {
		t.Fatal("included untracked name")
	}
}
