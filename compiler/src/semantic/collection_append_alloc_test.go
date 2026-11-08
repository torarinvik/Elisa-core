package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func BenchmarkPrepareCollectionAppendScopes(b *testing.B) {
	body := []ast.Stmt{&ast.ReturnStmt{}}
	for i := 0; i < 128; i++ {
		body = []ast.Stmt{&ast.IfStmt{Cond: &ast.BoolLit{Value: true}, Then: body}}
	}
	fn := &ast.FuncDecl{Body: body}
	a := newAnalyzerWithScope(NewScope(nil))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.prepareCollectionAppends(fn)
	}
}
