package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestStructFactoryChecksFreshnessWithoutAutoRegion(t *testing.T) {
	decl := &ast.VarDeclStmt{Name: "out", Value: &ast.StructLitExpr{}}
	ret := &ast.ReturnStmt{Value: &ast.Ident{Name: "out"}}
	fn := &ast.FuncDecl{Body: []ast.Stmt{decl, ret}}
	unwrap := func(e ast.Expr) ast.Expr { return e }
	auto := func(r *ast.RegionStmt) bool { return r.Name == "auto" }
	fresh := false
	check := func() bool {
		return BuildsAndReturnsLocalStruct(fn, unwrap, auto, func(_ []ast.Stmt, names map[string]bool) bool { return fresh && names["out"] })
	}
	if check() {
		t.Fatal("nonfresh fields must decline inference")
	}
	fresh = true
	if !check() {
		t.Fatal("fresh returned struct should qualify")
	}
	fn.Body = []ast.Stmt{&ast.RegionStmt{Name: "auto", Body: []ast.Stmt{decl, ret}}}
	fresh = false
	if !check() {
		t.Fatal("auto-region factory should qualify without the no-region fallback")
	}
	fn.Body = []ast.Stmt{&ast.RegionStmt{Name: "explicit", Body: []ast.Stmt{decl, ret}}}
	if check() {
		t.Fatal("explicit region must not be inferred as auto")
	}
}
