package exprkey

import (
	"testing"

	"elisacore/src/ast"
)

func TestExpressionKeysDistinguishDictionaryAndIndexOperands(t *testing.T) {
	ident := func(name string) ast.Expr { return &ast.Ident{Name: name} }
	value := ident("value")
	dictA := &ast.ListLitExpr{Brace: true, Keys: []ast.Expr{ident("a")}, Elems: []ast.Expr{value}}
	dictB := &ast.ListLitExpr{Brace: true, Keys: []ast.Expr{ident("b")}, Elems: []ast.Expr{value}}
	array := &ast.ListLitExpr{Elems: []ast.Expr{value}}
	set := &ast.ListLitExpr{Brace: true, Elems: []ast.Expr{value}}
	indexA := &ast.IndexExpr{Object: ident("items"), Index: ident("i"), Index2: ident("a")}
	indexB := &ast.IndexExpr{Object: ident("items"), Index: ident("i"), Index2: ident("b")}
	for _, pair := range [][2]ast.Expr{{dictA, dictB}, {array, set}, {indexA, indexB}} {
		if String(pair[0]) == String(pair[1]) {
			t.Fatalf("distinct expressions have key %q", String(pair[0]))
		}
	}
}

func TestFieldMatchDeclinesUnsupportedReceivers(t *testing.T) {
	field := &ast.FieldExpr{Object: &ast.MatchExpr{}, Field: "value"}
	if FieldMatches(field, &ast.MatchExpr{}, "value") {
		t.Fatal("unknown receiver shapes must not match")
	}
	if !FieldMatches(&ast.FieldExpr{Object: &ast.Ident{Name: "x"}, Field: "value"}, &ast.Ident{Name: "x"}, "value") {
		t.Fatal("known receiver did not match")
	}
}
