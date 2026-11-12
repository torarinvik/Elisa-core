package regionframesyntax

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"testing"
)

func TestWrapFuncBodyInLazyAutoRegion(t *testing.T) {
	stmt := &ast.ReturnStmt{Position: lexer.Pos{Offset: 42}}
	fn := &ast.FuncDecl{Body: []ast.Stmt{stmt}}
	WrapFuncBodyInLazyAutoRegion(fn)
	region, ok := fn.Body[0].(*ast.RegionStmt)
	if !ok || !region.Lazy || region.Name != "__auto_42" || len(region.Body) != 1 || region.Body[0] != stmt {
		t.Fatalf("incorrect region: %#v", fn.Body)
	}
}

func TestReturnTypeIsStaticRefThroughWrappers(t *testing.T) {
	ref := &ast.MutableType{Elem: &ast.OptionalTypeExpr{Value: &ast.RefType{Storage: ast.RefStorageStatic}}}
	if !ReturnTypeIsStaticRef(ref) || ReturnTypeIsStaticRef(&ast.RefType{}) {
		t.Fatal("incorrect static reference classification")
	}
}
