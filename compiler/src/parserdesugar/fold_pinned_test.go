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

func TestPinnedFoldDeclinesGeneratedNameCollisions(t *testing.T) {
	for _, internal := range []string{"__fold_n", "__fold_i", "__fold_a0", "__fold_a7"} {
		t.Run(internal, func(t *testing.T) {
			body := &ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: &ast.Ident{Name: "acc"}, Right: &ast.Ident{Name: internal}}
			if _, ok := BuildPinnedFoldReduce(lexer.Pos{}, nil, body, "item", "acc", nil, &ast.Ident{Name: "values"}, nil, nil, &ast.IntLit{Value: "0"}); ok {
				t.Fatal("transform name must not be shadowed by an internal binding")
			}
			body.Right = &ast.Ident{Name: "item"}
			if _, ok := BuildPinnedFoldReduce(lexer.Pos{}, nil, body, "item", "acc", nil, &ast.Ident{Name: internal}, nil, nil, &ast.IntLit{Value: "0"}); ok {
				t.Fatal("source name must not be shadowed by an internal binding")
			}
			if _, ok := BuildPinnedFoldReduce(lexer.Pos{}, nil, body, internal, "acc", nil, &ast.Ident{Name: "values"}, nil, nil, &ast.IntLit{Value: "0"}); ok {
				t.Fatal("element name must not collide with an internal binding")
			}
		})
	}
}
