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
	}{
		{"plain", &ast.ReturnStmt{Value: &ast.Ident{Name: "items"}}, false},
		{"ordinary call", &ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.Ident{Name: "unknown"}, Args: []ast.Expr{&ast.Ident{Name: "items"}}}}, false},
		{"query", &ast.ReturnStmt{Value: query()}, true},
		{"two queries", &ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: []ast.Expr{query(), query()}}}, true},
		{"nested queries", &ast.ReturnStmt{Value: &ast.QueryExpr{Kind: ast.QueryExprAny, Name: "outer", Source: &ast.Ident{Name: "items"}, Filter: query()}}, true},
		{"ternary query", &ast.ReturnStmt{Value: &ast.TernaryExpr{Value: query(), Cond: query(), Alt: query()}}, true},
		{"receiver", &ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.FieldExpr{Object: query(), Field: "count"}}}, true},
		{"safe receiver", &ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.Ident{Name: "f"}, SafeReceiver: query()}}, true},
		{"nested statement", &ast.IfStmt{Cond: &ast.Ident{Name: "condition"}, Then: []ast.Stmt{&ast.ReturnStmt{Value: query()}}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Analyzer{}
			stmt := reflect.ValueOf(tc.stmt)
			queryScan := storeFlowQueryScan{}
			a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, nil, true, true, &queryScan)
			pairs := a.storeFlowQueryBinderPairs(stmt)
			if got := len(pairs) != 0; got != tc.wantPair {
				t.Fatalf("query pairs: %v", pairs)
			}
			got := queryScan.result(a, stmt)
			if !reflect.DeepEqual(got, pairs) {
				t.Fatalf("query pairs: got %v, want %v", got, pairs)
			}

		})
	}
}
