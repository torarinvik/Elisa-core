package parsersyntax

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"testing"
)

func TestWhenLiteralValues(t *testing.T) {
	value := &ast.UnaryExpr{Op: lexer.TOKEN_MINUS, Operand: &ast.ParenExpr{Inner: &ast.IntLit{Value: "1_024"}}}
	if got, ok := WhenLiteralIntValue(value); !ok || got != -1024 {
		t.Fatalf("integer: %d %v", got, ok)
	}
	if got, ok := WhenCharValue("\\n"); !ok || got != '\n' {
		t.Fatal("newline escape")
	}
	if _, ok := WhenCharValue("\\q"); ok {
		t.Fatal("unknown escape accepted")
	}
}
