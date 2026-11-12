package exprpurity

import "elisacore/src/ast"

func Reevaluatable(expr ast.Expr, depth int) bool {
	if expr == nil || depth > 64 {
		return false
	}
	switch n := expr.(type) {
	case *ast.Ident, *ast.IntLit, *ast.BoolLit, *ast.FloatLit, *ast.CharLit, *ast.StringLit, *ast.NullLit:
		return true
	case *ast.ParenExpr:
		return Reevaluatable(n.Inner, depth+1)
	case *ast.UnaryExpr:
		return n.LoweredCall == nil && Reevaluatable(n.Operand, depth+1)
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && Reevaluatable(n.Left, depth+1) && Reevaluatable(n.Right, depth+1)
	case *ast.TernaryExpr:
		return Reevaluatable(n.Cond, depth+1) &&
			Reevaluatable(n.Value, depth+1) && Reevaluatable(n.Alt, depth+1)
	case *ast.CastExpr:
		return Reevaluatable(n.Operand, depth+1)
	case *ast.FieldExpr:
		return Reevaluatable(n.Object, depth+1)
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			if !Reevaluatable(elem, depth+1) {
				return false
			}
		}
		return true
	case *ast.StructLitExpr:
		if n.CopyBase != nil || len(n.Spreads) != 0 {
			return false
		}
		for _, arg := range n.Args {
			if !Reevaluatable(arg, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}
