package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestSharedCodegenMapCopiesIsolateAndRestore(t *testing.T) {
	node := &ast.Ident{Name: "value"}
	oldType := &BuiltinType{Name: "i64"}
	original := map[ast.Expr]Type{node: oldType}
	private := map[string]bool{"private": true}
	a := &Analyzer{exprTypes: original, programRegions: private}
	r := &Result{ExprTypes: original, analyzer: a}
	r.BeginSharedCodegen()
	replacement := &BuiltinType{Name: "bool"}
	a.exprTypes[node] = replacement
	added := &ast.Ident{Name: "added"}
	a.exprTypes[added] = replacement
	if r.ExprTypes[node] != oldType || r.ExprTypes[added] != nil {
		t.Fatal("analyzer writes leaked to shared result")
	}
	a.programRegions["same-private-map"] = true
	if !private["same-private-map"] {
		t.Fatal("unshared map was copied")
	}
	r.EndSharedCodegen()
	if a.exprTypes[node] != oldType || a.exprTypes[added] != nil {
		t.Fatal("original analyzer map was not restored")
	}
	a.exprTypes[added] = oldType
	if r.ExprTypes[added] != oldType {
		t.Fatal("restored map lost identity")
	}
}

func BenchmarkSharedCodegenMapCopies(b *testing.B) {
	original := make(map[ast.Expr]Type, 10000)
	typ := &BuiltinType{Name: "i64"}
	for i := 0; i < 10000; i++ {
		original[&ast.Ident{Name: "value"}] = typ
	}
	a := &Analyzer{exprTypes: original}
	r := &Result{ExprTypes: original, analyzer: a}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.BeginSharedCodegen()
		r.EndSharedCodegen()
	}
}
