package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestStoreScanSimpleStatementsHaveNoBinders(t *testing.T) {
	expr := &ast.QueryExpr{Kind: ast.QueryExprAny, Name: "item", Source: &ast.Ident{Name: "source"}}
	for _, stmt := range []ast.Stmt{
		&ast.ExprStmt{Position: lexer.Pos{File: "file"}, Expr: expr},
		&ast.DiscardStmt{Position: lexer.Pos{File: "file"}, Value: expr},
		&ast.ReturnStmt{Position: lexer.Pos{File: "file"}, Value: expr},
		(*ast.ExprStmt)(nil), (*ast.DiscardStmt)(nil), (*ast.ReturnStmt)(nil),
	} {
		oldExpr, oldDiscard, oldReturn := storeScanExprStmtNoBinders, storeScanDiscardStmtNoBinders, storeScanReturnStmtNoBinders
		storeScanExprStmtNoBinders, storeScanDiscardStmtNoBinders, storeScanReturnStmtNoBinders = false, false, false
		want := stmtBoundNames(stmt)
		storeScanExprStmtNoBinders, storeScanDiscardStmtNoBinders, storeScanReturnStmtNoBinders = oldExpr, oldDiscard, oldReturn
		if got := stmtBoundNames(stmt); !reflect.DeepEqual(got, want) {
			t.Fatalf("%T: got %v want %v", stmt, got, want)
		}
	}
}

func BenchmarkStoreScanEmptyBinder(b *testing.B) {
	stmt := &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "f"}}}
	for _, fast := range []bool{false, true} {
		name := "reflective"
		if fast {
			name = "schema"
		}
		b.Run(name, func(b *testing.B) {
			old := storeScanExprStmtNoBinders
			storeScanExprStmtNoBinders = fast
			defer func() { storeScanExprStmtNoBinders = old }()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if len(stmtBoundNames(stmt)) != 0 {
					b.Fatal("unexpected binder")
				}
			}
		})
	}
}
