package regionframesyntax

import (
	"elisacore/src/ast"
	"elisacore/src/astscan"
	"testing"
)

func TestParamStoresBodyLocalTracksReachableStores(t *testing.T) {
	local := &ast.VarDeclStmt{Name: "local", Value: &ast.ListLitExpr{}}
	assignment := &ast.AssignStmt{Target: &ast.FieldExpr{Object: &ast.Ident{Name: "param"}, Field: "items"}, Value: &ast.Ident{Name: "local"}}
	fn := &ast.FuncDecl{Body: []ast.Stmt{local, assignment}}
	walk := func(stmts []ast.Stmt, visit func(any)) { astscan.WalkStatements(stmts, visit, map[uintptr]bool{}) }
	owns := func(*ast.VarDeclStmt) bool { return true }
	root := func(expr ast.Expr) *ast.Ident {
		if f, ok := expr.(*ast.FieldExpr); ok {
			id, _ := f.Object.(*ast.Ident)
			return id
		}
		return nil
	}
	mentions := func(expr any, names map[string]bool) bool {
		id, ok := expr.(*ast.Ident)
		return ok && names[id.Name]
	}
	if !ParamStoresBodyLocal(fn, "param", walk, owns, root, mentions) {
		t.Fatal("store into parameter was missed")
	}
	if ParamStoresBodyLocal(fn, "other", walk, owns, root, mentions) {
		t.Fatal("unrelated parameter was marked")
	}
}
