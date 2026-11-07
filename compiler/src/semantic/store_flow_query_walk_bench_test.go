package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"testing"
)

func BenchmarkStoreFlowStatementScans(b *testing.B) {
	var expr ast.Expr = &ast.Ident{Name: "source"}
	for i := 0; i < 64; i++ {
		expr = &ast.BinaryExpr{Left: expr, Op: lexer.TokenKind(0), Right: &ast.Ident{Name: "value"}}
	}
	stmt := reflect.ValueOf(&ast.ReturnStmt{Value: expr})
	a := &Analyzer{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		mayHaveQuery := false
		a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, nil, false, true, &mayHaveQuery)
		if mayHaveQuery {
			a.storeFlowQueryBinderPairs(stmt)
		}
	}
}
