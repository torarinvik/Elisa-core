package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func BenchmarkFillMayAdoptStmtLists(b *testing.B) {
	body := make([]ast.Stmt, 256)
	for i := range body {
		body[i] = &ast.IfStmt{Cond: &ast.Ident{Name: "condition"}, Then: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "value"}}}}
	}
	root := reflect.ValueOf(body)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		lists := 0
		fillMayAdoptStmtLists(root, func([]ast.Stmt) { lists++ })
		if lists < 257 {
			b.Fatalf("missing statement lists: %d", lists)
		}
	}
}
