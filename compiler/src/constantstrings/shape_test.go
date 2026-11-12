package constantstrings

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"testing"
)

func TestResultRequiresStableConditionsAndStrings(t *testing.T) {
	literal := &ast.StringLit{Value: "ok"}
	if !Result(literal, 0) || Result(&ast.StringLit{Value: "bad\x00string"}, 0) {
		t.Fatal("literal validation failed")
	}
	expr := &ast.TernaryExpr{Cond: &ast.BinaryExpr{Op: lexer.TOKEN_EQEQ, Left: &ast.Ident{Name: "x"}, Right: &ast.IntLit{Value: "1"}}, Value: literal, Alt: &ast.StringLit{Value: "other"}}
	if !Result(expr, 0) {
		t.Fatal("pure conditional strings should be accepted")
	}
	expr.Cond.(*ast.BinaryExpr).LoweredCall = &ast.CallExpr{}
	if Result(expr, 0) {
		t.Fatal("overloaded operator may have effects")
	}
	expr.Cond = &ast.CallExpr{Func: &ast.Ident{Name: "f"}}
	if Result(expr, 0) {
		t.Fatal("call condition may have effects")
	}
	if Result(literal, 33) {
		t.Fatal("depth budget must be enforced")
	}
}
