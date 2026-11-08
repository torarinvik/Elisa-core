package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestRecordConjunctionFactsPreservesOrderAndScope(t *testing.T) {
	a, b, c := &ast.BoolLit{Value: true}, &ast.BoolLit{Value: false}, &ast.BoolLit{Value: true}
	expr := &ast.BinaryExpr{Op: lexer.TOKEN_AND, Left: &ast.ParenExpr{Inner: &ast.BinaryExpr{Op: lexer.TOKEN_AND, Left: a, Right: b}}, Right: c}
	parent := &Scope{smtAssertFacts: []smtFact{{Expr: b}}}
	scope := &Scope{Parent: parent, smtAssertFacts: []smtFact{{Expr: c}}}
	(&Analyzer{}).recordSMTAssertFactInScope(scope, expr)
	got := make([]ast.Expr, 0, len(scope.smtAssertFacts))
	for _, fact := range scope.smtAssertFacts {
		got = append(got, fact.Expr)
	}
	if !reflect.DeepEqual(got, []ast.Expr{c, a, b, c}) || len(parent.smtAssertFacts) != 1 || parent.smtAssertFacts[0].Expr != b {
		t.Fatal("conjunction facts changed order or leaked into the parent scope")
	}
}

func BenchmarkRecordConjunctionFacts(b *testing.B) {
	var expr ast.Expr = &ast.BoolLit{Value: true}
	for i := 1; i < 32; i++ {
		expr = &ast.BinaryExpr{Op: lexer.TOKEN_AND, Left: expr, Right: &ast.BoolLit{Value: true}}
	}
	a := &Analyzer{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		scope := &Scope{}
		a.recordSMTAssertFactInScope(scope, expr)
		if len(scope.smtAssertFacts) != 32 {
			b.Fatal("lost conjunction facts")
		}
	}
}
