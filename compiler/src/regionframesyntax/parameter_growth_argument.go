package regionframesyntax

import (
	"elisacore/src/ast"
	"elisacore/src/astprunedscan"
)

func ContainerGrowthHasArgument(body []ast.Stmt, name string, growthMethods map[string]bool, acceptArg func(ast.Expr) bool) bool {
	found := false
	astprunedscan.WalkPruned(body, func(node any) bool {
		if found {
			return false
		}
		if call, ok := node.(*ast.CallExpr); ok && call != nil {
			if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && growthMethods[field.Field] {
				if id, ok := field.Object.(*ast.Ident); ok && id != nil && id.Name == name {
					for _, arg := range call.Args {
						if acceptArg(arg) {
							found = true
							return false
						}
					}
				}
			}
		}
		return true
	})
	return found
}
