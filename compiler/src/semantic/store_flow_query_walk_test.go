package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestStoreFlowQueryScanConservative(t *testing.T) {
	query := func() *ast.QueryExpr {
		return &ast.QueryExpr{Kind: ast.QueryExprAny, Name: "item", Source: &ast.Ident{Name: "items"}}
	}
	tests := []struct {
		name     string
		stmt     ast.Stmt
		wantPair bool
		wantScan bool
	}{
		{"plain", &ast.ReturnStmt{Value: &ast.Ident{Name: "items"}}, false, false},
		{"query", &ast.ReturnStmt{Value: query()}, true, true},
		{"receiver", &ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.FieldExpr{Object: query(), Field: "count"}}}, true, true},
		{"safe receiver", &ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.Ident{Name: "f"}, SafeReceiver: query()}}, true, true},
		{"nested statement", &ast.IfStmt{Cond: &ast.Ident{Name: "condition"}, Then: []ast.Stmt{&ast.ReturnStmt{Value: query()}}}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Analyzer{}
			stmt := reflect.ValueOf(tc.stmt)
			mayHaveQuery := false
			a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, nil, true, true, &mayHaveQuery)
			pairs := a.storeFlowQueryBinderPairs(stmt)
			if got := len(pairs) != 0; got != tc.wantPair {
				t.Fatalf("query pairs: %v", pairs)
			}
			if mayHaveQuery != tc.wantScan {
				t.Fatalf("query scan = %v, want %v", mayHaveQuery, tc.wantScan)
			}
			if len(pairs) != 0 && !mayHaveQuery {
				t.Fatal("query pairs would be skipped")
			}
		})
	}
}
