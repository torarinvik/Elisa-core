package semantic

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// A flat `1 + 1 + …` chain of 200,000 terms parses left-leaning, so folding it by recursing on
// Left exceeded the Go stack. The fold now walks the left spine iteratively.
func TestEvalConstExprFoldsVeryLongBinaryChainIteratively(t *testing.T) {
	const terms = 200000
	var expr ast.Expr = &ast.IntLit{Value: "1"}
	for i := 1; i < terms; i++ {
		expr = &ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: expr, Right: &ast.IntLit{Value: "1"}}
	}
	a := &Analyzer{}
	value, ok := a.evalConstExpr(expr)
	if !ok || value.Kind != ConstInt || value.Int != terms {
		t.Fatalf("expected %d, got ok=%v value=%+v", terms, ok, value)
	}
	// Mixed operators keep left-to-right evaluation: (((2 - 1) * 3) + 4) == 7.
	mixed := &ast.BinaryExpr{Op: lexer.TOKEN_PLUS,
		Left: &ast.BinaryExpr{Op: lexer.TOKEN_STAR,
			Left:  &ast.BinaryExpr{Op: lexer.TOKEN_MINUS, Left: &ast.IntLit{Value: "2"}, Right: &ast.IntLit{Value: "1"}},
			Right: &ast.IntLit{Value: "3"}},
		Right: &ast.IntLit{Value: "4"}}
	value, ok = a.evalConstExpr(mixed)
	if !ok || value.Int != 7 {
		t.Fatalf("expected 7, got ok=%v value=%+v", ok, value)
	}
}
