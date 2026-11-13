package purematch

import (
	"testing"

	"elisacore/src/ast"
)

func TestLeafEqualComparesNamesAndLiterals(t *testing.T) {
	if !LeafEqual(&ast.Ident{Name: "x"}, &ast.Ident{Name: "x"}) {
		t.Fatal("identical names must match")
	}
	if LeafEqual(&ast.Ident{Name: "x"}, &ast.Ident{Name: "y"}) {
		t.Fatal("distinct names must not match")
	}
	if !LeafEqual(&ast.BoolLit{Value: true}, &ast.BoolLit{Value: true}) || LeafEqual(&ast.BoolLit{Value: true}, &ast.BoolLit{Value: false}) {
		t.Fatal("boolean values were not compared")
	}
}
