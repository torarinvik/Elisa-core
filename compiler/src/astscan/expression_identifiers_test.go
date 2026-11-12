package astscan

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestExprIdentNamesIncludeStructCopiesAndSpreads(t *testing.T) {
	value := &ast.StructLitExpr{
		CopyBase: &ast.Ident{Name: "base"},
		Spreads:  []ast.Expr{&ast.Ident{Name: "spread"}},
		Args:     []ast.Expr{&ast.Ident{Name: "field"}, &ast.Ident{Name: "base"}},
	}
	if got, want := ExprIdentNames(value), []string{"base", "spread", "field"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExprIdentNamesFollowValueWrappers(t *testing.T) {
	value := &ast.CanExpr{Expr: &ast.MoveExpr{Operand: &ast.AllocExpr{
		Owner: &ast.Ident{Name: "arena"},
		Value: &ast.TryExpr{Value: &ast.Ident{Name: "input"}, Fallback: &ast.Ident{Name: "fallback"}},
	}}}
	if got, want := ExprIdentNames(value), []string{"arena", "input", "fallback"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExprIdentNamesIncludeSafeCallReceiver(t *testing.T) {
	call := &ast.CallExpr{Func: &ast.Ident{Name: "method"}, SafeReceiver: &ast.Ident{Name: "receiver"}, Args: []ast.Expr{&ast.Ident{Name: "argument"}}}
	if got, want := ExprIdentNames(call), []string{"method", "receiver", "argument"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExprIdentNamesIncludeRecoveryValues(t *testing.T) {
	try := &ast.TryExpr{Value: &ast.Ident{Name: "input"}, Recovery: &ast.RecoveryClause{Value: &ast.Ident{Name: "recovery"}}}
	if got, want := ExprIdentNames(try), []string{"input", "recovery"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExprIdentNamesIncludeRecoveryStatements(t *testing.T) {
	value := &ast.GetExpr{Value: &ast.Ident{Name: "input"}, Recovery: &ast.RecoveryClause{
		Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "recovery"}}},
	}}
	if got, want := ExprIdentNames(value), []string{"input", "recovery"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
