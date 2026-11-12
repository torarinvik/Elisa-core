package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestFieldPathLValue(t *testing.T) {
	root := &ast.Ident{Name: "record"}
	path := &ast.FieldExpr{Object: &ast.FieldExpr{Object: root, Field: "part"}, Field: "items"}
	if got, ok := FieldPathLValue(path, "."); !ok || got != "record.part.items" {
		t.Fatalf("path: %q, %v", got, ok)
	}
	if _, ok := FieldPathLValue(&ast.FieldExpr{Object: root, Field: "items", Safe: true}, "."); ok {
		t.Fatal("accepted safe navigation")
	}
	if _, ok := FieldPathLValue(&ast.IndexExpr{Object: root}, "."); ok {
		t.Fatal("accepted indexed receiver")
	}
}
