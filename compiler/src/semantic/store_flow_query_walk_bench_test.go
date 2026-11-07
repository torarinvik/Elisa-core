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
		queryScan := storeFlowQueryScan{}
		a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, nil, false, true, &queryScan)
		_ = queryScan.result(a, stmt)
	}
}

func BenchmarkStoreFlowPositions(b *testing.B) {
	var expr ast.Expr = &ast.Ident{Name: "source"}
	for i := 0; i < 64; i++ {
		expr = &ast.BinaryExpr{Position: lexer.Pos{File: "file.elisa", Line: i}, Left: expr, Right: &ast.IntLit{Position: lexer.Pos{File: "file.elisa", Line: i}, Value: "1"}}
	}
	stmt := reflect.ValueOf(&ast.ReturnStmt{Value: expr})
	for _, fast := range []bool{false, true} {
		name := "reflective"
		if fast {
			name = "direct"
		}
		b.Run(name, func(b *testing.B) {
			saved := storeFlowPositionFast
			storeFlowPositionFast = fast
			defer func() { storeFlowPositionFast = saved }()
			a := &Analyzer{}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				scan := storeFlowQueryScan{}
				a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, nil, false, true, &scan)
				_ = scan.result(a, stmt)
			}
		})
	}
}

func BenchmarkStoreFlowCallQueryScans(b *testing.B) {
	args := make([]ast.Expr, 64)
	for i := range args {
		args[i] = &ast.Ident{Name: "value"}
	}
	stmt := reflect.ValueOf(&ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.Ident{Name: "unknown"}, Args: args}})
	a := &Analyzer{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		queryScan := storeFlowQueryScan{}
		a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, nil, false, true, &queryScan)
		_ = queryScan.result(a, stmt)
	}
}

func BenchmarkStoreFlowDeclaredCallQueryScans(b *testing.B) {
	args := make([]ast.Expr, 64)
	for i := range args {
		args[i] = &ast.Ident{Name: "value"}
	}
	stmt := reflect.ValueOf(&ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: args}})
	for _, fast := range []bool{false, true} {
		name := "fallback"
		if fast {
			name = "combined"
		}
		b.Run(name, func(b *testing.B) {
			saved := storeFlowDeclaredCallQueryFast
			storeFlowDeclaredCallQueryFast = fast
			defer func() { storeFlowDeclaredCallQueryFast = saved }()
			a := &Analyzer{declaredFuncNames: map[string]bool{"f": true}}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				scan := storeFlowQueryScan{}
				a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, nil, false, true, &scan)
				_ = scan.result(a, stmt)
			}
		})
	}
}

func BenchmarkStoreFlowScalarQueries(b *testing.B) {
	args := make([]ast.Expr, 32)
	for i := range args {
		args[i] = &ast.QueryExpr{Kind: ast.QueryExprAny, Name: "item", Source: &ast.Ident{Name: "items"}}
	}
	stmt := reflect.ValueOf(&ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.Ident{Name: "unknown"}, Args: args}})
	a := &Analyzer{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		queryScan := storeFlowQueryScan{}
		a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, nil, false, true, &queryScan)
		_ = queryScan.result(a, stmt)
	}
}

func BenchmarkStoreFlowQueryBinderPairsIdentifiers(b *testing.B) {
	args := make([]ast.Expr, 64)
	for i := range args {
		args[i] = &ast.Ident{Name: "value"}
	}
	stmt := reflect.ValueOf(&ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: args}})
	for _, fast := range []bool{false, true} {
		name := "reflective"
		if fast {
			name = "identifier"
		}
		b.Run(name, func(b *testing.B) {
			old := storeFlowIdentFast
			storeFlowIdentFast = fast
			defer func() { storeFlowIdentFast = old }()
			a := &Analyzer{}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = a.storeFlowQueryBinderPairs(stmt)
			}
		})
	}
}

func BenchmarkStoreFlowIntegerLiterals(b *testing.B) {
	var expr ast.Expr = &ast.IntLit{Value: "1"}
	for i := 0; i < 64; i++ {
		expr = &ast.BinaryExpr{Left: expr, Right: &ast.IntLit{Value: "1"}}
	}
	stmt := reflect.ValueOf(&ast.ReturnStmt{Value: expr})
	for _, fast := range []bool{false, true} {
		label := "reflective"
		if fast {
			label = "direct"
		}
		b.Run(label, func(b *testing.B) {
			saved := storeFlowIntLiteralFast
			storeFlowIntLiteralFast = fast
			defer func() { storeFlowIntLiteralFast = saved }()
			a := &Analyzer{}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				a.storeFlowCollectMentionsWithQuery(stmt.Elem(), stmt.Pointer(), nil, nil, nil, false, true, nil)
			}
		})
	}
}
