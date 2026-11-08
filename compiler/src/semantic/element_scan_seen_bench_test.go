package semantic

import (
	"fmt"
	"testing"

	"elisacore/src/ast"
)

func TestElementScanNamesReset(t *testing.T) {
	n := &elementScanNames{ineligible: map[string]bool{"a": true}, multiDecl: map[string]bool{"b": true}, mentioned: map[string]bool{"c": true}}
	if !n.reset() || len(n.ineligible)+len(n.multiDecl)+len(n.mentioned) != 0 {
		t.Fatal("pooled names were not cleared")
	}
	for i := 0; i <= 1<<14; i++ {
		n.mentioned[fmt.Sprint(i)] = true
	}
	if n.reset() {
		t.Fatal("oversized name table retained")
	}
}

func BenchmarkElementScanVisitedSet(b *testing.B) {
	body := make([]ast.Stmt, 256)
	for i := range body {
		body[i] = &ast.ReturnStmt{Value: &ast.IntLit{Value: "1"}}
	}
	a := &Analyzer{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.scanLocalContainerElementReads(body)
	}
}

func BenchmarkElementScanNames(b *testing.B) {
	body := make([]ast.Stmt, 256)
	for i := range body {
		body[i] = &ast.ReturnStmt{Value: &ast.Ident{Name: fmt.Sprintf("name%d", i)}}
	}
	a := &Analyzer{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.scanLocalContainerElementReads(body)
	}
}
