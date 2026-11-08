package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func BenchmarkScalarTupleProvenance(b *testing.B) {
	elems := make([]ast.Expr, 128)
	for i := range elems {
		elems[i] = &ast.IntLit{Value: "1"}
	}
	value := &ast.TupleExpr{Elems: elems}
	a := &Analyzer{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := a.regionRefStateForExpr(value); ok {
			b.Fatal("scalar tuple unexpectedly has region provenance")
		}
	}
}
