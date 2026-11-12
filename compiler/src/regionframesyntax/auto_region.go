package regionframesyntax

import (
	"elisacore/src/ast"
	"fmt"
)

func WrapFuncBodyInLazyAutoRegion(fn *ast.FuncDecl) {
	if fn == nil || len(fn.Body) == 0 {
		return
	}
	pos := fn.Body[0].Pos()
	fn.Body = []ast.Stmt{&ast.RegionStmt{
		Position: pos,
		Name:     fmt.Sprintf("__auto_%d", pos.Offset),
		Lazy:     true,
		Body:     fn.Body,
	}}
}

func ReturnTypeIsStaticRef(typ ast.TypeExpr) bool {
	for {
		switch t := typ.(type) {
		case *ast.MutableType:
			typ = t.Elem
		case *ast.OptionalTypeExpr:
			typ = t.Value
		case *ast.RefType:
			return t.Storage == ast.RefStorageStatic
		default:
			return false
		}
	}
}
