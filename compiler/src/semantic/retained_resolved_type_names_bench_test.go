package semantic

import (
	"fmt"
	"testing"

	"elisacore/src/ast"
)

func BenchmarkResolvedTypeNameCapture(b *testing.B) {
	decls := make([]scopedDecl, 256)
	rows := make(map[ast.TypeExpr]string, len(decls))
	for i := range decls {
		typ := &ast.NamedType{Name: "Box"}
		fn := &ast.FuncDecl{Name: fmt.Sprintf("function_%d", i), Params: []ast.ParamDecl{{Name: "value", Type: typ}}, Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "value"}}}}
		decls[i] = scopedDecl{Decl: fn, Namespace: "Bench"}
		rows[typ] = "Bench.Box"
	}
	keys := retainedPreBodyFunctionKeys(decls)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		certificate, _ := captureResolvedTypeNameContributions(rows, decls, keys, true)
		if certificate.failure != "" {
			b.Fatal(certificate.failure)
		}
	}
}
