package exprpurity

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func Termination(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.IntLit, *ast.BoolLit, *ast.StringLit, *ast.CharLit, *ast.Ident:
		return true
	case *ast.ParenExpr:
		return n != nil && Termination(n.Inner)
	case *ast.FieldExpr:
		return n != nil && !n.Safe && Termination(n.Object)
	case *ast.IndexExpr:
		return n != nil &&
			n.Index2 == nil && n.Fallback == nil && !n.LegacyElseFallback &&
			n.AsSpecialize == nil && n.AsOverlayCall == nil &&
			Termination(n.Object) && Termination(n.Index)
	case *ast.SliceExpr:
		return n != nil &&
			Termination(n.Object) &&
			(n.Start == nil || Termination(n.Start)) &&
			(n.End == nil || Termination(n.End))
	case *ast.UnaryExpr:
		return n != nil && n.Op != lexer.TOKEN_AMPERSAND && Termination(n.Operand)
	case *ast.BinaryExpr:
		return n != nil && Termination(n.Left) && Termination(n.Right)
	case *ast.CastExpr:
		return n != nil && Termination(n.Operand)
	case *ast.TernaryExpr:
		return n != nil &&
			Termination(n.Cond) &&
			Termination(n.Value) &&
			Termination(n.Alt)
	default:
		return false
	}
}
