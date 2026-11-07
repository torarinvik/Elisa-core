package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func TestStoreScanAliasClosureNodes(t *testing.T) {
	decl, call, assign, ret := &ast.VarDeclStmt{Name: "x"}, &ast.CallExpr{}, &ast.AssignStmt{}, &ast.ReturnStmt{}
	idx := &storeScanDeclIndex{flat: []any{&ast.Ident{Name: "x"}, decl, &ast.IntLit{Value: "1"}, call, &ast.ForStmt{}, assign, ret}}
	want := []any{decl, call, assign, ret}
	for i := 0; i < 2; i++ {
		if got := idx.aliasClosureNodes(); !reflect.DeepEqual(got, want) {
			t.Fatalf("nodes = %v, want %v", got, want)
		}
	}
}
