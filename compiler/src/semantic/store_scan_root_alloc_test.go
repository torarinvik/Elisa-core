package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func BenchmarkStoreScanTypedRoot(b *testing.B) {
	stmts := []ast.Stmt{&ast.ExprStmt{Expr: &ast.Ident{Name: "value"}}}
	visit := func(any) {}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		walkStoreScanNodes(stmts, visit)
	}
}
