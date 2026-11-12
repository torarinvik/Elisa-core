package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestParameterAndLocalOwnershipShapes(t *testing.T) {
	mutable := &ast.ParamDecl{Type: &ast.MutableType{Elem: &ast.RefType{Elem: &ast.NamedType{Name: "Record"}}}}
	readonly := &ast.ParamDecl{Type: &ast.RefType{Elem: &ast.NamedType{Name: "Record"}}}
	if !ParamIsWritableStructRef(mutable) || ParamIsWritableStructRef(readonly) {
		t.Fatal("reference mutability classification changed")
	}
	unwrap := func(e ast.Expr) ast.Expr { return e }
	if !LocalMayOwnContainer(&ast.VarDeclStmt{Value: &ast.ListLitExpr{}}, unwrap) {
		t.Fatal("untyped list can own storage")
	}
	if LocalMayOwnContainer(&ast.VarDeclStmt{Type: &ast.NamedType{Name: "i64"}}, unwrap) {
		t.Fatal("scalar cannot own container storage")
	}
}
