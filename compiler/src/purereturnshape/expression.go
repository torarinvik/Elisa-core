package purereturnshape

import (
	"elisacore/src/ast"
	"elisacore/src/exprkey"
	"elisacore/src/lexer"
)

const maxDepth = 64

// ExpressionWithinBudget preflights the expression forms admitted as local aliases
// before calling the recursive purity predicate. That predicate predates hostile-source budgets,
// so this bounded walk prevents deeply nested initializers from overflowing the Go stack.
func ExpressionWithinBudget(expr ast.Expr, work *int, depth int) bool {
	if expr == nil {
		return true
	}
	if work == nil || depth >= maxDepth || *work <= 0 {
		return false
	}
	(*work)--
	switch n := expr.(type) {
	case *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.CharLit, *ast.NullLit,
		*ast.SizeofExpr, *ast.AlignofExpr, *ast.OffsetofExpr:
		return true
	case *ast.ParenExpr:
		return ExpressionWithinBudget(n.Inner, work, depth+1)
	case *ast.UnaryExpr:
		return ExpressionWithinBudget(n.Operand, work, depth+1)
	case *ast.BinaryExpr:
		return ExpressionWithinBudget(n.Left, work, depth+1) && ExpressionWithinBudget(n.Right, work, depth+1)
	case *ast.FieldExpr:
		return ExpressionWithinBudget(n.Object, work, depth+1)
	case *ast.IndexExpr:
		return ExpressionWithinBudget(n.Object, work, depth+1) && ExpressionWithinBudget(n.Index, work, depth+1) &&
			ExpressionWithinBudget(n.Index2, work, depth+1) && ExpressionWithinBudget(n.Fallback, work, depth+1)
	case *ast.SliceExpr:
		return ExpressionWithinBudget(n.Object, work, depth+1) && ExpressionWithinBudget(n.Start, work, depth+1) && ExpressionWithinBudget(n.End, work, depth+1)
	case *ast.TernaryExpr:
		return ExpressionWithinBudget(n.Cond, work, depth+1) && ExpressionWithinBudget(n.Value, work, depth+1) && ExpressionWithinBudget(n.Alt, work, depth+1)
	case *ast.CallExpr:
		if !ExpressionWithinBudget(n.Func, work, depth+1) {
			return false
		}
		for _, arg := range n.Args {
			if !ExpressionWithinBudget(arg, work, depth+1) {
				return false
			}
		}
		return true
	case *ast.MatchExpr:
		if n.Store != nil || !ExpressionWithinBudget(n.Value, work, depth+1) {
			return false
		}
		for _, arm := range n.Arms {
			if !ScalarMatchPattern(arm.Pattern) || arm.Guard != nil || len(arm.Body) != 1 {
				return false
			}
			tail, ok := arm.Body[0].(*ast.ExprStmt)
			if !ok || !ExpressionWithinBudget(tail.Expr, work, depth+1) {
				return false
			}
		}
		return true
	case *ast.StructLitExpr:
		// A plain struct literal builds a value from its field values.
		if n.CopyBase != nil || len(n.Spreads) != 0 {
			return false
		}
		for _, arg := range n.Args {
			if !ExpressionWithinBudget(arg, work, depth+1) {
				return false
			}
		}
		return true
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			if !ExpressionWithinBudget(elem, work, depth+1) {
				return false
			}
		}
		return true
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			if !ExpressionWithinBudget(elem, work, depth+1) {
				return false
			}
		}
		for _, key := range n.Keys {
			if !ExpressionWithinBudget(key, work, depth+1) {
				return false
			}
		}
		return true
	case *ast.CastExpr:
		return ExpressionWithinBudget(n.Operand, work, depth+1)
	case *ast.AddrOfExpr:
		return ExpressionWithinBudget(n.Operand, work, depth+1)
	case *ast.SpecializeExpr:
		return ExpressionWithinBudget(n.Operand, work, depth+1)
	case *ast.QuantifierExpr:
		return ExpressionWithinBudget(n.In, work, depth+1) && ExpressionWithinBudget(n.Body, work, depth+1)
	default:
		return false
	}
}

func ScalarMatchPattern(pattern ast.MatchPattern) bool {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern:
		return true
	case *ast.MatchLiteralPattern:
		if p.Pinned {
			return false
		}
		switch exprkey.StripParens(p.Value).(type) {
		case *ast.IntLit, *ast.BoolLit:
			return true
		case *ast.UnaryExpr:
			unary := exprkey.StripParens(p.Value).(*ast.UnaryExpr)
			_, integer := exprkey.StripParens(unary.Operand).(*ast.IntLit)
			return unary.Op == lexer.TOKEN_MINUS && integer
		}
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			if !ScalarMatchPattern(option) {
				return false
			}
		}
		return len(p.Options) > 0
	}
	return false
}
