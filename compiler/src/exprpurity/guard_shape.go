package exprpurity

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// GuardShape accepts the conservative grammar used for loop termination guards.
func GuardShape(cond ast.Expr, pureArith func(ast.Expr) bool) bool {
	switch n := cond.(type) {
	case *ast.ParenExpr:
		return n != nil && GuardShape(n.Inner, pureArith)
	case *ast.BoolLit:
		return true
	case *ast.Ident:
		return true // a boolean variable; boolTerm models it as a bool constant
	case *ast.FieldExpr:
		// A boolean field read like `self.done`; the object must be a pure field path (no call/index).
		return n != nil && !n.Safe && pureFieldPath(n.Object)
	case *ast.UnaryExpr:
		if n == nil {
			return false
		}
		if n.Op == lexer.TOKEN_NOT {
			return GuardShape(n.Operand, pureArith)
		}
		return false
	case *ast.BinaryExpr:
		if n == nil {
			return false
		}
		switch n.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR:
			return GuardShape(n.Left, pureArith) && GuardShape(n.Right, pureArith)
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_GT, lexer.TOKEN_GTEQ:
			return pureArith(n.Left) && pureArith(n.Right)
		}
		return false
	default:
		return false
	}
}

func pureFieldPath(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		return n != nil
	case *ast.ParenExpr:
		return n != nil && pureFieldPath(n.Inner)
	case *ast.FieldExpr:
		return n != nil && !n.Safe && pureFieldPath(n.Object)
	default:
		return false
	}
}
