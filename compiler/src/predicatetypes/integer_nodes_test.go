package predicatetypes

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestIntegerExpressionNodesChecksSafetyAndBudgets(t *testing.T) {
	x := &ast.Ident{Name: "x"}
	expr := &ast.BinaryExpr{Left: x, Op: lexer.TOKEN_PLUS, Right: &ast.IntLit{Value: "3"}}
	nodes, bytes, ok := IntegerExpressionNodes(expr, map[string]bool{"x": true}, 0, 100, true, nil, nil)
	if !ok || len(nodes) != 3 || bytes != 2 {
		t.Fatalf("valid: nodes=%d bytes=%d ok=%v", len(nodes), bytes, ok)
	}
	budget, exhausted := 1, false
	if _, _, ok := IntegerExpressionNodes(expr, map[string]bool{"x": true}, 0, 100, true, &budget, &exhausted); ok || !exhausted {
		t.Fatal("node budget not enforced")
	}
	expr.Right = x
	if _, _, ok := IntegerExpressionNodes(expr, map[string]bool{"x": true}, 0, 100, true, nil, nil); ok {
		t.Fatal("shared expression node accepted")
	}
}
