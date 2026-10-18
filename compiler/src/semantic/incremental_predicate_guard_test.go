//go:build cgo

package semantic

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestPredicateUnaryMinusIntrinsicGuard(t *testing.T) {
	parameters := map[string]string{"integer": "i32", "boolean": "bool", "character": "char"}
	cases := []struct {
		name string
		expr ast.Expr
		want bool
	}{
		{"unsuffixed integer literal", &ast.IntLit{Value: "1"}, true},
		{"character literal", &ast.CharLit{Value: "x"}, true},
		{"integer parameter", &ast.Ident{Name: "integer"}, true},
		{"boolean parameter", &ast.Ident{Name: "boolean"}, false},
		{"character parameter", &ast.Ident{Name: "character"}, true},
		{"parenthesized integer", &ast.ParenExpr{Inner: &ast.Ident{Name: "integer"}}, true},
		{"nested numeric negation", &ast.UnaryExpr{Op: lexer.TOKEN_MINUS, Operand: &ast.Ident{Name: "integer"}}, true},
		{"comparison operand", &ast.BinaryExpr{Op: lexer.TOKEN_LT, Left: &ast.Ident{Name: "integer"}, Right: &ast.IntLit{Value: "0"}}, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := predicateUnaryMinusIsIntrinsic(test.expr, parameters); got != test.want {
				t.Fatalf("intrinsic guard = %v, want %v", got, test.want)
			}
		})
	}
}
