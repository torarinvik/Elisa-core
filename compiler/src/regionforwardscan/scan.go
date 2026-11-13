// Package regionforwardscan collects syntactic forwarding facts used by region inference.
package regionforwardscan

import (
	"elisacore/src/ast"
	"elisacore/src/regionframesyntax"
)

// Scan reports function references, parameter growth, and parameter forwarding
// in the order supplied by walk. The caller owns traversal and visited-node policy.
func Scan(fn *ast.FuncDecl, funcByName map[string]*ast.FuncDecl, growthMethods map[string]bool, walk func([]ast.Stmt, func(any)), trackReferences bool, noteCaller func(*ast.FuncDecl), noteGrowth func(string), noteForward func(string, bool, *ast.FuncDecl, int)) {
	walk(fn.Body, func(node any) {
		var expr ast.Expr
		switch n := node.(type) {
		case *ast.Ident:
			if trackReferences {
				noteCaller(funcByName[n.Name])
			}
			return
		case *ast.CallExpr:
			if n != nil {
				if field, ok := n.Func.(*ast.FieldExpr); ok && field != nil && growthMethods[field.Field] {
					if id, ok := field.Object.(*ast.Ident); ok && id != nil {
						for _, param := range fn.Params {
							if param.Name == id.Name {
								noteGrowth(id.Name)
								break
							}
						}
					}
				}
			}
			expr = n
		case *ast.StructLitExpr:
			expr = n
		default:
			return
		}
		calleeName, args, isCall := ast.PrepassCallShape(expr)
		callee := funcByName[calleeName]
		if !isCall || callee == nil {
			return
		}
		noteCaller(callee)
		if len(fn.Params) == 0 {
			return
		}
		for argPos, arg := range args {
			name, isField, ok := regionframesyntax.RegionForwardedParamPath(arg)
			if !ok {
				continue
			}
			for _, param := range fn.Params {
				if param.Name == name {
					noteForward(name, isField, callee, argPos)
					break
				}
			}
		}
	})
}
