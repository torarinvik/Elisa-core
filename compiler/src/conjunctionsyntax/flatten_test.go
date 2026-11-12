package conjunctionsyntax

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"testing"
)

func TestFlattenPreservesOrderAndLeafParentheses(t *testing.T) {
	a, b, c := &ast.Ident{Name: "a"}, &ast.Ident{Name: "b"}, &ast.Ident{Name: "c"}
	leaf := &ast.ParenExpr{Inner: b}
	tree := &ast.BinaryExpr{Op: lexer.TOKEN_AND, Left: &ast.ParenExpr{Inner: &ast.BinaryExpr{Op: lexer.TOKEN_AND, Left: a, Right: leaf}}, Right: c}
	got := Flatten(tree)
	if len(got) != 3 || got[0] != a || got[1] != leaf || got[2] != c {
		t.Fatalf("incorrect leaves: %#v", got)
	}
}

func TestFlattenDeepLeftTree(t *testing.T) {
	var tree ast.Expr = &ast.Ident{Name: "first"}
	for i := 0; i < 10000; i++ {
		tree = &ast.BinaryExpr{Op: lexer.TOKEN_AND, Left: tree, Right: &ast.Ident{Name: "next"}}
	}
	if got := len(Flatten(tree)); got != 10001 {
		t.Fatalf("got %d leaves", got)
	}
}

func BenchmarkFlattenDeep(b *testing.B) {
	var tree ast.Expr = &ast.Ident{Name: "first"}
	for i := 0; i < 2000; i++ {
		tree = &ast.BinaryExpr{Op: lexer.TOKEN_AND, Left: tree, Right: &ast.Ident{Name: "next"}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Flatten(tree)
	}
}
