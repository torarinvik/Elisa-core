package exprpurity

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"testing"
)

func TestGuardShape(t *testing.T) {
	pure := func(e ast.Expr) bool { _, ok := e.(*ast.IntLit); return ok }
	compare := &ast.BinaryExpr{Op: lexer.TOKEN_LT, Left: &ast.IntLit{Value: "1"}, Right: &ast.IntLit{Value: "2"}}
	if !GuardShape(compare, pure) {
		t.Fatal("rejected pure comparison")
	}
	compare.Right = &ast.CallExpr{}
	if GuardShape(compare, pure) {
		t.Fatal("accepted impure operand")
	}
	field := &ast.FieldExpr{Object: &ast.Ident{Name: "state"}, Field: "ready", Safe: true}
	if GuardShape(field, pure) {
		t.Fatal("accepted safe field access")
	}
}
