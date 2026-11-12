package taskgroupsyntax

import (
	"elisacore/src/ast"
	"elisacore/src/astpointerscan"
	"reflect"
	"testing"
)

func TestCollectFactsPreservesConservativeSignals(t *testing.T) {
	call := &ast.CallExpr{Func: &ast.Ident{Name: "pool_submit1"}, SafeConcurrencySugar: true, Args: []ast.Expr{&ast.Ident{Name: "group"}}}
	node := []ast.Stmt{&ast.VarDeclStmt{Name: "task_group_add"}, &ast.ExprStmt{Expr: call}, &ast.ReturnStmt{}}
	facts := CollectFacts(node, "group", func(value any, visit func(any)) {
		astpointerscan.WalkPointersReflect(reflect.ValueOf(value), visit, map[uintptr]bool{})
	})
	if !facts.GroupRef || !facts.GroupAPI || !facts.UntrustedCall || facts.SafeSubmitCount != 1 || !facts.EarlyExit || !facts.ApiShadow {
		t.Fatalf("missing conservative signal: %+v", facts)
	}
}

func TestWindowStmtSafeTracksOnlyRecognizedSubmission(t *testing.T) {
	walk := func(value any, visit func(any)) {
		astpointerscan.WalkPointersReflect(reflect.ValueOf(value), visit, map[uintptr]bool{})
	}
	submission := &ast.CallExpr{Func: &ast.Ident{Name: "pool_submit1"}, SafeConcurrencySugar: true, Args: []ast.Expr{&ast.Ident{Name: "pool"}, &ast.Ident{Name: "worker"}, &ast.IntLit{Value: "1"}}}
	stmt := &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "task_group_add"}, Args: []ast.Expr{&ast.AddrOfExpr{Operand: &ast.Ident{Name: "group"}}, submission}}}
	var got *ast.CallExpr
	if !WindowStmtSafe(stmt, "group", walk, func(call *ast.CallExpr) { got = call }) || got != submission {
		t.Fatal("recognized submission was not retained")
	}
	submission.SafeConcurrencySugar = false
	if WindowStmtSafe(stmt, "group", walk, func(*ast.CallExpr) {}) {
		t.Fatal("unrecognized submission must be rejected")
	}
}
