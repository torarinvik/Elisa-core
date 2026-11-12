package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestContainerFactoryRespectsArenaAndRegionOwnership(t *testing.T) {
	declaration := &ast.VarDeclStmt{Name: "out", Value: &ast.ListLitExpr{}}
	ret := &ast.ReturnStmt{Value: &ast.Ident{Name: "out"}}
	region := &ast.RegionStmt{Name: "auto", Body: []ast.Stmt{declaration, ret}}
	fn := &ast.FuncDecl{Body: []ast.Stmt{region}}
	owned := func(ast.TypeExpr) bool { return true }
	regionless := func(ast.TypeExpr) bool { return true }
	unwrap := func(e ast.Expr) ast.Expr { return e }
	auto := func(r *ast.RegionStmt) bool { return r.Name == "auto" }
	hasArena := false
	check := func() bool {
		return BuildsAndReturnsLocalContainer(fn, owned, func(*ast.FuncDecl) bool { return hasArena }, regionless, unwrap, auto)
	}
	if !check() {
		t.Fatal("auto-region factory was missed")
	}
	region.Name = "explicit"
	if check() {
		t.Fatal("explicit-region factory must not adopt caller storage")
	}
	region.Name = "auto"
	hasArena = true
	if check() {
		t.Fatal("explicit arena parameter must decline inference")
	}
}
