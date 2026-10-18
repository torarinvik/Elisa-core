package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestStatementListWalkReachesLambdaReserveRemedy(t *testing.T) {
	loop := &ast.IterForStmt{}
	reserve := &ast.ExprStmt{Expr: &ast.CallExpr{
		Func: &ast.FieldExpr{Object: &ast.Ident{Name: "values"}, Field: "reserve"},
		Args: []ast.Expr{&ast.IntLit{Value: "100"}},
	}}
	// The nested list is reached through ReturnStmt.Value's Expr interface.
	stmt := &ast.ReturnStmt{Value: &ast.LambdaExpr{Body: []ast.Stmt{reserve, loop}}}
	analyzer := &Analyzer{pendingIterFillLints: map[*ast.IterForStmt]string{loop: "values"}}
	analyzer.descendStmtLists(stmt, nil)
	if len(analyzer.pendingIterFillLints) != 0 || !analyzer.iterFillLintDecided[loop] {
		t.Fatal("nested lambda loop was skipped rather than checking its reserve remedy")
	}
	if !analyzer.iterFillSuppressedAt[iterFillPosKey(loop)] {
		t.Fatal("nested loop's explicit reserve did not suppress the pending warning")
	}
}
