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

func TestStoreFlowDeclaredCallQueryScan(t *testing.T) {
	query := func(name string) ast.Expr {
		return &ast.QueryExpr{Kind: ast.QueryExprAny, Name: name, Source: &ast.Ident{Name: "items"}}
	}
	for _, tc := range []struct {
		name     string
		call     *ast.CallExpr
		fallback bool
	}{
		{"arguments", &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: []ast.Expr{query("arg")}}, false},
		{"resolved", &ast.CallExpr{Func: &ast.Ident{Name: "f"}, ResolvedArgsValid: true, ResolvedArgs: []ast.Expr{query("resolved")}}, false},
		{"unresolved", &ast.CallExpr{Func: &ast.Ident{Name: "f"}, ResolvedArgs: []ast.Expr{query("unresolved")}}, true},
		{"receiver", &ast.CallExpr{Func: &ast.Ident{Name: "f"}, SafeReceiver: query("receiver")}, true},
		{"implicit", &ast.CallExpr{Func: &ast.Ident{Name: "f"}, ResolvedImplicitArgs: []ast.Expr{query("implicit")}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Analyzer{declaredFuncNames: map[string]bool{"f": true}}
			stmt := reflect.ValueOf(&ast.ReturnStmt{Value: tc.call})
			scan := storeFlowQueryScan{}
			a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, nil, false, true, &scan)
			if scan.incomplete != tc.fallback {
				t.Fatalf("fallback = %v, want %v", scan.incomplete, tc.fallback)
			}
			if got, want := scan.result(a, stmt), a.storeFlowQueryBinderPairs(stmt); !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

func TestStoreFlowReadOnlyCallQueryScan(t *testing.T) {
	query := func(name string) ast.Expr {
		return &ast.QueryExpr{Kind: ast.QueryExprAny, Name: name, Source: &ast.Ident{Name: "items"}}
	}
	for _, tc := range []struct {
		name     string
		call     *ast.CallExpr
		fallback bool
	}{
		{"arguments", &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "items"}, Field: "contains"}, Args: []ast.Expr{query("arg")}}, false},
		{"receiver", &ast.CallExpr{Func: &ast.FieldExpr{Object: query("receiver"), Field: "contains"}}, true},
		{"safe receiver", &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "items"}, Field: "contains"}, SafeReceiver: query("safe")}, true},
		{"resolved", &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "items"}, Field: "contains"}, ResolvedArgs: []ast.Expr{query("resolved")}}, true},
		{"implicit", &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "items"}, Field: "contains"}, ResolvedImplicitArgs: []ast.Expr{query("implicit")}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Analyzer{}
			stmt := reflect.ValueOf(&ast.ReturnStmt{Value: tc.call})
			scan := storeFlowQueryScan{}
			a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, nil, true, true, &scan)
			if scan.incomplete != tc.fallback {
				t.Fatalf("fallback = %v, want %v", scan.incomplete, tc.fallback)
			}
			if got, want := scan.result(a, stmt), a.storeFlowQueryBinderPairs(stmt); !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

func TestStoreFlowQueryBinderPairsIdentifierFastPath(t *testing.T) {
	if !storeFlowIdentFast {
		t.Skip("identifier schema changed")
	}
	stmt := reflect.ValueOf(&ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: []ast.Expr{&ast.Ident{Name: "x"}, &ast.QueryExpr{Kind: ast.QueryExprAny, Name: "item", Source: &ast.Ident{Name: "items"}}}}})
	a := &Analyzer{}
	original := storeFlowIdentFast
	defer func() { storeFlowIdentFast = original }()
	storeFlowIdentFast = false
	want := a.storeFlowQueryBinderPairs(stmt)
	storeFlowIdentFast = true
	got := a.storeFlowQueryBinderPairs(stmt)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestStoreFlowBinaryFastMatchesReflective(t *testing.T) {
	if !storeFlowBinaryFast {
		t.Skip("binary syntax schema changed")
	}
	old := storeFlowBinaryFast
	defer func() { storeFlowBinaryFast = old }()
	query := &ast.QueryExpr{Kind: ast.QueryExprAny, Name: "item", Source: &ast.Ident{Name: "items"}}
	binary := &ast.BinaryExpr{Left: query, Right: &ast.Ident{Name: "x"}, LoweredCall: &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: []ast.Expr{query}}}
	stmt := reflect.ValueOf(&ast.ReturnStmt{Value: binary})
	run := func(fast bool) ([]string, [][]string, []*ast.CallExpr, bool) {
		storeFlowBinaryFast = fast
		a := &Analyzer{}
		var calls []*ast.CallExpr
		scan := storeFlowQueryScan{}
		names := a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, &calls, false, true, &scan)
		return names, scan.pairs, calls, a.storeFlowBinds
	}
	want, wp, wc, wb := run(false)
	got, gp, gc, gb := run(true)
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gp, wp) || !reflect.DeepEqual(gc, wc) || gb != wb {
		t.Fatalf("binary walk differs: %v %v %v %v; want %v %v %v %v", got, gp, gc, gb, want, wp, wc, wb)
	}
}

func TestStoreFlowFieldFastMatchesReflective(t *testing.T) {
	if !storeFlowSimpleFieldQueryFast {
		t.Skip("field syntax schema changed")
	}
	saved := storeFlowSimpleFieldQueryFast
	defer func() { storeFlowSimpleFieldQueryFast = saved }()
	q := &ast.QueryExpr{Kind: ast.QueryExprAny, Name: "item", Source: &ast.Ident{Name: "items"}}
	stmt := reflect.ValueOf(&ast.ReturnStmt{Value: &ast.FieldExpr{Object: q, Field: "value"}})
	run := func(fast bool) ([]string, [][]string, bool) {
		storeFlowSimpleFieldQueryFast = fast
		a := &Analyzer{}
		scan := storeFlowQueryScan{}
		names := a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, nil, false, true, &scan)
		return names, scan.pairs, a.storeFlowBinds
	}
	want, wp, wb := run(false)
	got, gp, gb := run(true)
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gp, wp) || gb != wb {
		t.Fatalf("got %v %v %v, want %v %v %v", got, gp, gb, want, wp, wb)
	}
}

func TestStoreFlowParenFastMatchesReflective(t *testing.T) {
	if !storeFlowParenFast {
		t.Skip("parenthesis schema changed")
	}
	saved := storeFlowParenFast
	defer func() { storeFlowParenFast = saved }()
	q := &ast.QueryExpr{Kind: ast.QueryExprAny, Name: "item", Source: &ast.Ident{Name: "items"}}
	stmt := reflect.ValueOf(&ast.ReturnStmt{Value: &ast.ParenExpr{Inner: q}})
	run := func(fast bool) ([]string, [][]string, bool) {
		storeFlowParenFast = fast
		a := &Analyzer{}
		scan := storeFlowQueryScan{}
		names := a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, nil, false, true, &scan)
		return names, scan.pairs, a.storeFlowBinds
	}
	want, wp, wb := run(false)
	got, gp, gb := run(true)
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gp, wp) || gb != wb {
		t.Fatalf("got %v %v %v, want %v %v %v", got, gp, gb, want, wp, wb)
	}
}

func TestStoreFlowIndexFastMatchesReflective(t *testing.T) {
	if !storeFlowIndexFast {
		t.Skip("index syntax schema changed")
	}
	saved := storeFlowIndexFast
	defer func() { storeFlowIndexFast = saved }()
	q := &ast.QueryExpr{Kind: ast.QueryExprAny, Name: "item", Source: &ast.Ident{Name: "items"}}
	expr := &ast.IndexExpr{Object: &ast.Ident{Name: "a"}, Index: q, Index2: &ast.Ident{Name: "b"}, Fallback: q, AsOverlayCall: &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: []ast.Expr{q}}}
	stmt := reflect.ValueOf(&ast.ReturnStmt{Value: expr})
	run := func(fast bool) ([]string, [][]string, []*ast.CallExpr, bool) {
		storeFlowIndexFast = fast
		a := &Analyzer{}
		scan := storeFlowQueryScan{}
		var calls []*ast.CallExpr
		names := a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, &calls, false, true, &scan)
		return names, scan.pairs, calls, a.storeFlowBinds
	}
	want, wp, wc, wb := run(false)
	got, gp, gc, gb := run(true)
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gp, wp) || !reflect.DeepEqual(gc, wc) || gb != wb {
		t.Fatalf("index walk differs: %v %v %v %v; want %v %v %v %v", got, gp, gc, gb, want, wp, wc, wb)
	}
}
