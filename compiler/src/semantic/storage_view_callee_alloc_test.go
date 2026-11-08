package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func BenchmarkStorageViewUnresolvedMethod(b *testing.B) {
	a := &Analyzer{}
	call := &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "receiver"}, Field: "missing"}, Args: []ast.Expr{&ast.Ident{Name: "value"}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, ok := a.storageViewOriginCallee(call)
		if ok {
			b.Fatal("unexpected resolution")
		}
	}
}
func BenchmarkStorageViewWriteCalleesOverloads(b *testing.B) {
	a := &Analyzer{storageViewWriteDeclsByName: map[string][]*ast.FuncDecl{}}
	for i := 0; i < 12; i++ {
		a.storageViewWriteDeclsByName["write"] = append(a.storageViewWriteDeclsByName["write"], &ast.FuncDecl{Name: "write", Params: []ast.ParamDecl{{}, {}}})
	}
	call := &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "value"}, Field: "write"}, Args: []ast.Expr{&ast.Ident{Name: "arg"}}}
	locals := map[string]bool{"value": true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		pairs, ok := a.storageViewWriteCallees(call, locals)
		if !ok || len(pairs) != 12 {
			b.Fatal("bad candidates")
		}
	}
}
