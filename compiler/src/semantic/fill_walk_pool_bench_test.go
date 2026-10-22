package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func BenchmarkFillWalkAfterLargeBody(b *testing.B) {
	large := make([]ast.Stmt, 5000)
	for i := range large {
		large[i] = &ast.ExprStmt{Expr: &ast.IntLit{Value: "1"}}
	}
	visit := func(any) {}
	fillMayAdoptWalk(reflect.ValueOf(large), visit)
	small := reflect.ValueOf(&ast.IntLit{Value: "1"})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fillMayAdoptWalk(small, visit)
	}
}
