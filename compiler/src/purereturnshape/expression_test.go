package purereturnshape

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"testing"
)

func TestExpressionBudgetAndMatchPatterns(t *testing.T) {
	var deep ast.Expr = &ast.IntLit{Value: "1"}
	for i := 0; i < maxDepth; i++ {
		deep = &ast.ParenExpr{Inner: deep}
	}
	work := 4096
	if ExpressionWithinBudget(deep, &work, 0) {
		t.Fatal("deep expressions must be declined")
	}
	work = 1
	if ExpressionWithinBudget(&ast.BinaryExpr{Left: &ast.IntLit{}, Right: &ast.IntLit{}}, &work, 0) {
		t.Fatal("work budget must be enforced")
	}
	negative := &ast.MatchLiteralPattern{Value: &ast.UnaryExpr{Op: lexer.TOKEN_MINUS, Operand: &ast.IntLit{Value: "1"}}}
	if !ScalarMatchPattern(negative) {
		t.Fatal("negative integer pattern should be accepted")
	}
	negative.Pinned = true
	if ScalarMatchPattern(negative) {
		t.Fatal("pinned pattern must be declined")
	}
	if ScalarMatchPattern(&ast.MatchOrPattern{}) {
		t.Fatal("empty alternatives must be declined")
	}
}

func TestExpressionShapeChecksEveryListKeyAndStructInput(t *testing.T) {
	accepted := &ast.ListLitExpr{Elems: []ast.Expr{&ast.IntLit{Value: "1"}}, Keys: []ast.Expr{&ast.IntLit{Value: "2"}}}
	work := 3
	if !ExpressionWithinBudget(accepted, &work, 0) || work != 0 {
		t.Fatalf("expected list and both children to consume the budget, remaining %d", work)
	}
	work = 2
	if ExpressionWithinBudget(accepted, &work, 0) {
		t.Fatal("dictionary key must count against the work budget")
	}
	for _, literal := range []*ast.StructLitExpr{
		{CopyBase: &ast.Ident{Name: "base"}},
		{Spreads: []ast.Expr{&ast.Ident{Name: "base"}}},
	} {
		work = 10
		if ExpressionWithinBudget(literal, &work, 0) {
			t.Fatal("struct copy and spread literals must be declined")
		}
	}
}
