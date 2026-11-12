package regionframesyntax

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestAssignmentPathPreservesFieldOrderAndIndex(t *testing.T) {
	root := &ast.Ident{Name: "value"}
	expr := &ast.FieldExpr{Object: &ast.IndexExpr{Object: &ast.FieldExpr{Object: root, Field: "items"}}, Field: "name"}
	gotRoot, path := AssignmentPath(expr, func(e ast.Expr) ast.Expr { return e })
	if gotRoot != root || !reflect.DeepEqual(path, []string{"items", "", "name"}) {
		t.Fatalf("root=%v path=%v", gotRoot, path)
	}
}
