package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func BenchmarkStoreFlowIdentifierNames(b *testing.B) {
	a := &Analyzer{}
	expr := &ast.Ident{Name: "item"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if len(a.storeFlowArgNames(expr, nil)) != 1 {
			b.Fatal("bad names")
		}
	}
}
