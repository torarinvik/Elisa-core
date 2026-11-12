package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestReturnViewRegionRetainsConservativeAlias(t *testing.T) {
	view := &ast.BuiltinTypeExpr{Name: "view"}
	slice := &ast.SliceExpr{Object: &ast.Ident{Name: "param"}}
	fn := &ast.FuncDecl{ReturnType: view, Body: []ast.Stmt{
		&ast.VarDeclStmt{Name: "first", Value: slice},
		&ast.VarDeclStmt{Name: "second", Value: &ast.Ident{Name: "first"}},
		&ast.AssignStmt{Target: &ast.Ident{Name: "second"}, Value: &ast.Ident{Name: "other"}},
		&ast.ReturnStmt{Value: &ast.Ident{Name: "second"}},
	}}
	InferReturnViewRegion(fn, map[string]string{"param": "r"})
	if view.Region != "r" {
		t.Fatal("potentially parameter-backed return must retain its region")
	}
	view.Region = "explicit"
	InferReturnViewRegion(fn, map[string]string{"param": "other"})
	if view.Region != "explicit" {
		t.Fatal("explicit return region was overwritten")
	}
}
