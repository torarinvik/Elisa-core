package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func BenchmarkStoreScanIndexConstruction(b *testing.B) {
	decl := &ast.FuncDecl{Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "value"}}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a := &Analyzer{}
		if a.storeScanIndex(decl) == nil {
			b.Fatal("missing index")
		}
	}
}
