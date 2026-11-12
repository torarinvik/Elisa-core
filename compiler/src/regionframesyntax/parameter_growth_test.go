package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestParameterGrowthRespectsRegionScopes(t *testing.T) {
	root := &ast.Ident{Name: "p"}
	field := &ast.FieldExpr{Object: root, Field: "items"}
	push := &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.FieldExpr{Object: field, Field: "push"}}}
	region := &ast.RegionStmt{Name: "auto", Body: []ast.Stmt{push}}
	isAuto := func(r *ast.RegionStmt) bool { return r.Name == "auto" }
	rootIdent := func(expr ast.Expr) *ast.Ident {
		if f, ok := expr.(*ast.FieldExpr); ok {
			id, _ := f.Object.(*ast.Ident)
			return id
		}
		return nil
	}
	growth := map[string]bool{"push": true}
	check := func() bool { return ParamFieldContainerIsGrown([]ast.Stmt{region}, "p", isAuto, growth, rootIdent) }
	if !check() {
		t.Fatal("growth in synthesized region must be visible")
	}
	region.Name = "explicit"
	if check() {
		t.Fatal("growth in explicit region must be skipped")
	}
	direct := &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.FieldExpr{Object: root, Field: "push"}}}
	if !ParamContainerIsGrown([]ast.Stmt{direct}, "p", growth) {
		t.Fatal("direct growth was missed")
	}
}

func TestContainerGrowthMatchesTargetAndArgument(t *testing.T) {
	call := &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "out"}, Field: "push"}, Args: []ast.Expr{&ast.Ident{Name: "fresh"}}}
	body := []ast.Stmt{&ast.ExprStmt{Expr: call}}
	accept := func(expr ast.Expr) bool { id, ok := expr.(*ast.Ident); return ok && id.Name == "fresh" }
	if !ContainerGrowthHasArgument(body, "out", map[string]bool{"push": true}, accept) {
		t.Fatal("matching growth was missed")
	}
	if ContainerGrowthHasArgument(body, "other", map[string]bool{"push": true}, accept) {
		t.Fatal("unrelated receiver matched")
	}
}
