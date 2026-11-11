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

var resolvedTypeNameOwnersBenchmarkSink map[ast.TypeExpr]resolvedTypeNameSourceOwner

func BenchmarkResolvedTypeNameSourceOwners(b *testing.B) {
	locations := make(map[ast.TypeExpr]resolvedTypeNameLocation, 32768)
	for i := 0; i < 32768; i++ {
		locations[&ast.NamedType{Name: "Box"}] = resolvedTypeNameLocation{owner: "fn:Bench::f#0", region: "body", firstPath: "$[0].Type"}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resolvedTypeNameOwnersBenchmarkSink = resolvedTypeNameSourceOwners(locations)
	}
}
