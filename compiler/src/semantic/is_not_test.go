package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeIsNotExprUsesBool(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "is_not_expr.elisa", `const enum TokenKind of u32:
    IDENT
    NUMBER

def keep(kind: TokenKind) -> bool:
    return kind is not .IDENT
`)

	decl := result.File.Decls[1].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	notExpr, ok := ret.Value.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected is-not to parse as unary not expr, got %T", ret.Value)
	}
	if got := result.ExprTypes[notExpr].String(); got != "bool" {
		t.Fatalf("expected is-not expr type bool, got %s", got)
	}
	isExpr, ok := notExpr.Operand.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected is-not operand to be is-expression, got %T", notExpr.Operand)
	}
	if got := result.ExprTypes[isExpr].String(); got != "bool" {
		t.Fatalf("expected is operand type bool, got %s", got)
	}
}
