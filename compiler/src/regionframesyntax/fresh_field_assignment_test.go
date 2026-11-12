package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestFreshFieldAssignmentRespectsExplicitRegions(t *testing.T) {
	local := &ast.VarDeclStmt{Name: "local", Value: &ast.ListLitExpr{}}
	assignment := &ast.AssignStmt{Target: &ast.FieldExpr{Object: &ast.Ident{Name: "param"}, Field: "items"}, Value: &ast.Ident{Name: "local"}}
	region := &ast.RegionStmt{Name: "auto", Body: []ast.Stmt{local, assignment}}
	isAuto := func(r *ast.RegionStmt) bool { return r.Name == "auto" }
	fresh := func(e ast.Expr) bool { _, ok := e.(*ast.ListLitExpr); return ok }
	strip := func(e ast.Expr) ast.Expr { return e }
	root := func(e ast.Expr) *ast.Ident {
		if f, ok := e.(*ast.FieldExpr); ok {
			id, _ := f.Object.(*ast.Ident)
			return id
		}
		return nil
	}
	scalar := func(ast.TypeExpr) bool { return false }
	check := func() bool {
		return ParamFieldAssignedFreshContainer([]ast.Stmt{region}, "param", nil, isAuto, fresh, strip, root, scalar)
	}
	if !check() {
		t.Fatal("fresh assignment in auto region was missed")
	}
	region.Name = "explicit"
	if check() {
		t.Fatal("explicit region should not trigger inference")
	}
}
