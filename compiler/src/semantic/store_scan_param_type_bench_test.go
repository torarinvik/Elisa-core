package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func BenchmarkStoreScanWritableRefType(b *testing.B) {
	leaf := &EnumType{Name: "Leaf"}
	var t Type = leaf
	for i := 0; i < 12; i++ {
		t = &TupleType{Fields: []TupleField{{Type: t}, {Type: t}}}
	}
	decl := &ast.FuncDecl{Name: "work"}
	a := &Analyzer{funcDeclSymbols: map[*ast.FuncDecl]*Symbol{decl: {Type: &FuncType{Params: []Type{&RefType{Elem: t, Mutable: true}}}}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if a.declParamIsViewOrScalar(decl, 0) {
			b.Fatal("reference considered scalar")
		}
	}
}
