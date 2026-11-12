package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestObjectMayEscape(t *testing.T) {
	name := func() *ast.Ident { return &ast.Ident{Name: "buf"} }
	cases := []struct {
		expr ast.Expr
		want bool
	}{
		{&ast.FieldExpr{Object: name(), Field: "count"}, false},
		{&ast.IndexExpr{Object: name(), Index: &ast.IntLit{Value: "0"}}, false},
		{name(), true},
		{&ast.FieldExpr{Object: &ast.ParenExpr{Inner: name()}, Field: "count"}, false},
		{&ast.AddrOfExpr{Operand: name()}, true},
		{&ast.Ident{Name: "other"}, false},
		{&ast.AddrOfExpr{Operand: &ast.IndexExpr{Object: name(), Index: &ast.IntLit{Value: "0"}}}, true},
	}
	for _, tc := range cases {
		got := ObjectMayEscape([]ast.Stmt{&ast.ExprStmt{Expr: tc.expr}}, "buf")
		if got != tc.want {
			t.Errorf("%T: got %v, want %v", tc.expr, got, tc.want)
		}
	}
}
