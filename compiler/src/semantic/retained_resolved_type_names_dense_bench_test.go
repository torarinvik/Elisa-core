package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"testing"
)

// Compiler bodies contain many calls and literal leaves between type uses.
// Keep their traversal cost visible alongside the small header-focused fixture.
func BenchmarkResolvedTypeNameCaptureDenseBodies(b *testing.B) {
	decls := make([]scopedDecl, 128)
	rows := make(map[ast.TypeExpr]string, len(decls))
	for i := range decls {
		typ := &ast.NamedType{Name: "Box"}
		fn := &ast.FuncDecl{Name: fmt.Sprintf("function_%d", i), Params: []ast.ParamDecl{{Name: "value", Type: typ}}}
		for j := 0; j < 64; j++ {
			fn.Body = append(fn.Body, &ast.ExprStmt{Expr: &ast.CallExpr{
				Func: &ast.Ident{Name: "consume"}, Args: []ast.Expr{&ast.Ident{Name: "value"}, &ast.IntLit{Value: "1"}},
			}})
		}
		decls[i] = scopedDecl{Decl: fn, Namespace: "Bench"}
		rows[typ] = "Bench.Box"
	}
	keys := retainedPreBodyFunctionKeys(decls)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		certificate, locations := captureResolvedTypeNameContributions(rows, decls, keys, true)
		if certificate.failure != "" || len(locations) != len(rows) {
			b.Fatal(certificate.failure, len(locations))
		}
	}
}
