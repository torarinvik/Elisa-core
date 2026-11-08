package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func BenchmarkStoreFlowStatementSeen(b *testing.B) {
	decl := &ast.FuncDecl{Body: make([]ast.Stmt, 256)}
	for i := range decl.Body {
		decl.Body[i] = &ast.ReturnStmt{}
	}
	a := &Analyzer{}
	unit := func(_ reflect.Value, nested []reflect.Value) []reflect.Value { return nested }
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.storeFlowNodes2(decl, unit)
	}
}
