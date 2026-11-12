package parserdesugar

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"testing"
)

func TestPinnedFoldDeclinesAccumulatorDependentTransform(t *testing.T) {
	body := &ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: &ast.Ident{Name: "acc"}, Right: &ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: &ast.Ident{Name: "item"}, Right: &ast.Ident{Name: "acc"}}}
	source := &ast.Ident{Name: "values"}
	if _, ok := BuildPinnedFoldReduce(lexer.Pos{}, nil, body, "item", "acc", nil, source, nil, nil, &ast.IntLit{Value: "1"}); ok {
		t.Fatal("accumulator-dependent transform cannot use independent lanes")
	}
	body.Right = &ast.Ident{Name: "item"}
	if _, ok := BuildPinnedFoldReduce(lexer.Pos{}, nil, body, "item", "acc", nil, source, nil, nil, &ast.IntLit{Value: "1"}); !ok {
		t.Fatal("independent transform should use pinned lanes")
	}
}
