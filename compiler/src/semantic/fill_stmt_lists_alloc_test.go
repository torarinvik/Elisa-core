package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func BenchmarkFillMayAdoptStatementLists(b *testing.B) {
	body := []ast.Stmt{&ast.ReturnStmt{}}
	for i := 0; i < 128; i++ {
		body = []ast.Stmt{&ast.IfStmt{Cond: &ast.BoolLit{Value: true}, Then: body}}
	}
	root := reflect.ValueOf(body)
	visit := func([]ast.Stmt) {}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		fillMayAdoptStmtLists(root, visit)
	}
}
