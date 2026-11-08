package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func BenchmarkWalkParamStoresScopes(b *testing.B) {
	body := []ast.Stmt{&ast.ReturnStmt{}}
	for i := 0; i < 128; i++ {
		body = []ast.Stmt{&ast.IfStmt{Cond: &ast.BoolLit{Value: true}, Then: body}}
	}
	a := &Analyzer{}
	add := func(string, string) {}
	global := func(string) {}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.walkParamStores(body, nil, nil, add, global, nil)
	}
}
