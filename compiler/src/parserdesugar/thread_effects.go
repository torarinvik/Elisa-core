package parserdesugar

import "elisacore/src/ast"

func MarkThreadEffect(e ast.Expr) {
	for {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return
		}
		call.LmutThreadEffect = true
		field, ok := call.Func.(*ast.FieldExpr)
		if !ok {
			return
		}
		e = field.Object
	}
}

func ExprRootIdentName(e ast.Expr) (string, bool) {
	for {
		switch n := e.(type) {
		case *ast.Ident:
			return n.Name, true
		case *ast.ParenExpr:
			e = n.Inner
		case *ast.AddrOfExpr:
			e = n.Operand
		case *ast.FieldExpr:
			e = n.Object
		case *ast.IndexExpr:
			e = n.Object
		case *ast.CallExpr:
			field, ok := n.Func.(*ast.FieldExpr)
			if !ok {
				return "", false
			}
			e = field.Object
		default:
			return "", false
		}
	}
}
