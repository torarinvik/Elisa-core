package regionframesyntax

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestSubtreeHasIdentFindsNestedAndHandlesNil(t *testing.T) {
	fields := func(typ reflect.Type) []int {
		var out []int
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).IsExported() {
				out = append(out, i)
			}
		}
		return out
	}
	tree := &ast.BinaryExpr{Left: &ast.Ident{Name: "left"}, Right: &ast.ParenExpr{Inner: &ast.Ident{Name: "right"}}}
	has := func(name string) bool {
		return SubtreeHasIdent(reflect.ValueOf(tree), func(candidate string) bool { return candidate == name }, fields)
	}
	if !has("right") || has("missing") {
		t.Fatal("identifier traversal returned the wrong result")
	}
	if SubtreeHasIdent(reflect.ValueOf(nil), func(string) bool { return true }, fields) {
		t.Fatal("nil tree cannot contain an identifier")
	}
}
