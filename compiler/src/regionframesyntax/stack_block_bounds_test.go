package regionframesyntax

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"testing"
)

func TestStackBlockBoundsUseOnlyEntryAvailableValues(t *testing.T) {
	params := map[string]bool{"count": true}
	cases := []struct {
		name string
		expr ast.Expr
		want bool
	}{
		{"parameter arithmetic", &ast.BinaryExpr{Op: lexer.TOKEN_STAR, Left: &ast.Ident{Name: "count"}, Right: &ast.IntLit{Value: "2"}}, true},
		{"local", &ast.Ident{Name: "local"}, false},
		{"division", &ast.BinaryExpr{Op: lexer.TOKEN_SLASH, Left: &ast.Ident{Name: "count"}, Right: &ast.IntLit{Value: "2"}}, false},
		{"integer conversion", &ast.CastExpr{Operand: &ast.Ident{Name: "count"}, Target: &ast.NamedType{Name: "usize"}}, true},
		{"noninteger conversion", &ast.CastExpr{Operand: &ast.Ident{Name: "count"}, Target: &ast.NamedType{Name: "f64"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StackBlockBoundEvaluable(tc.expr, params); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
