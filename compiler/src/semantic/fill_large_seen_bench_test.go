package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func BenchmarkFillMayAdoptLargeBody(b *testing.B) {
	body := make([]ast.Stmt, 10000)
	for i := range body {
		body[i] = &ast.ReturnStmt{Value: &ast.Ident{Name: "x"}}
	}
	root := reflect.ValueOf(body)
	visit := func(any) {}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fillMayAdoptWalk(root, visit)
	}
}
