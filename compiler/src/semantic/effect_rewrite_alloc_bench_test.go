package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"testing"
)

func BenchmarkEffectRewriteWithoutHandlers(b *testing.B) {
	body := make([]ast.Stmt, 1024)
	for i := range body {
		body[i] = &ast.ExprStmt{Expr: &ast.BinaryExpr{
			Left: &ast.IntLit{Value: "1"}, Op: lexer.TOKEN_PLUS, Right: &ast.IntLit{Value: "2"},
		}}
	}
	a := &Analyzer{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.rewriteEffectValue(reflect.ValueOf(body), effectRewriteContext{})
	}
}
