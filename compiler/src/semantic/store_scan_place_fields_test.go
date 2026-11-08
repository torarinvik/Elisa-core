package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func BenchmarkStoreScanPlaceFields(b *testing.B) {
	idx := &storeScanDeclIndex{}
	names := map[string]bool{"root": true}
	nodes := []any{&ast.Ident{Name: "root"}, &ast.ReturnStmt{Value: &ast.Ident{Name: "root"}}, &ast.AssignStmt{Target: &ast.Ident{Name: "root"}}, &ast.CallExpr{}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, node := range nodes {
			_ = storeScanNodePlaceMentions(idx, node, names)
		}
	}
}

func BenchmarkStoreScanTouchedLocals(b *testing.B) {
	idx := &storeScanDeclIndex{flat: make([]any, 256)}
	for i := range idx.flat {
		idx.flat[i] = &ast.ReturnStmt{Value: &ast.Ident{Name: "x"}}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = storeScanTouchedLocals(idx)
	}
}
