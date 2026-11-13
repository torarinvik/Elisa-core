package purematch

import (
	"elisacore/src/ast"
	"fmt"
)

// MatchEqual reports whether two classifier matches (`when` or `match`) are the
// same pure function of the same scrutinee: equal scrutinee places, the same arms in the same order
// with equal literal/range/wildcard/pinned patterns, no guards and no bindings, and each arm yielding
// an equal literal or pure place. Evaluating either side therefore yields the same value, so
// `ensure result == <match>` holds by reflexivity when the body returns the other.
func MatchEqual(x, y *ast.MatchExpr) bool {
	if x == nil || y == nil || x.Store != nil || y.Store != nil || len(x.Arms) != len(y.Arms) {
		return false
	}
	if !samePath(x.Value, y.Value) {
		return false
	}
	for i := range x.Arms {
		ax, ay := x.Arms[i], y.Arms[i]
		if ax.Guard != nil || ay.Guard != nil || !matchPatternsEqual(ax.Pattern, ay.Pattern) {
			return false
		}
		vx, okx := pureArmValue(ax.Body)
		vy, oky := pureArmValue(ay.Body)
		if !okx || !oky || !LeafEqual(vx, vy) {
			return false
		}
	}
	return true
}

func pureArmValue(body []ast.Stmt) (ast.Expr, bool) {
	if len(body) != 1 {
		return nil, false
	}
	if es, ok := body[0].(*ast.ExprStmt); ok && es != nil {
		return es.Expr, true
	}
	return nil, false
}

// LeafEqual compares literals by value and pure places by path.
func LeafEqual(x, y ast.Expr) bool {
	switch a := x.(type) {
	case *ast.IntLit:
		b, ok := y.(*ast.IntLit)
		return ok && a.Value == b.Value && a.Suffix == b.Suffix
	case *ast.StringLit:
		b, ok := y.(*ast.StringLit)
		return ok && a.Value == b.Value
	case *ast.BoolLit:
		b, ok := y.(*ast.BoolLit)
		return ok && a.Value == b.Value
	case *ast.CharLit:
		b, ok := y.(*ast.CharLit)
		return ok && a.Value == b.Value
	case *ast.BinaryExpr:
		b, ok := y.(*ast.BinaryExpr)
		return ok && a.Op == b.Op && LeafEqual(a.Left, b.Left) && LeafEqual(a.Right, b.Right)
	case *ast.UnaryExpr:
		b, ok := y.(*ast.UnaryExpr)
		return ok && a.Op == b.Op && LeafEqual(a.Operand, b.Operand)
	case *ast.TernaryExpr:
		b, ok := y.(*ast.TernaryExpr)
		return ok && LeafEqual(a.Cond, b.Cond) && LeafEqual(a.Value, b.Value) && LeafEqual(a.Alt, b.Alt)
	case *ast.MatchExpr:
		b, ok := y.(*ast.MatchExpr)
		return ok && MatchEqual(a, b)
	case *ast.CallExpr:
		// The ensure side already passed the contract purity check, so an identical call
		// on identical arguments denotes the same deterministic value.
		b, ok := y.(*ast.CallExpr)
		if !ok || a.Safe || b.Safe || a.SafeReceiver != nil || b.SafeReceiver != nil ||
			a.HasArgForward || b.HasArgForward || len(a.Args) != len(b.Args) ||
			len(a.ArgNames) != len(b.ArgNames) || !LeafEqual(a.Func, b.Func) {
			return false
		}
		for i := range a.ArgNames {
			if a.ArgNames[i] != b.ArgNames[i] {
				return false
			}
		}
		for i := range a.Args {
			if !LeafEqual(a.Args[i], b.Args[i]) {
				return false
			}
		}
		return true
	case *ast.ParenExpr:
		return LeafEqual(a.Inner, y)
	}
	if p, ok := y.(*ast.ParenExpr); ok {
		return LeafEqual(x, p.Inner)
	}
	return samePath(x, y)
}

func matchPatternsEqual(x, y ast.MatchPattern) bool {
	switch a := x.(type) {
	case *ast.MatchWildcardPattern:
		_, ok := y.(*ast.MatchWildcardPattern)
		return ok
	case *ast.MatchStringLiteralPattern:
		b, ok := y.(*ast.MatchStringLiteralPattern)
		return ok && a.Value == b.Value
	case *ast.MatchLiteralPattern:
		b, ok := y.(*ast.MatchLiteralPattern)
		return ok && a.Pinned == b.Pinned && LeafEqual(a.Value, b.Value)
	case *ast.MatchRangePattern:
		b, ok := y.(*ast.MatchRangePattern)
		return ok && a.Inclusive == b.Inclusive && LeafEqual(a.Lo, b.Lo) && LeafEqual(a.Hi, b.Hi)
	case *ast.MatchOrPattern:
		b, ok := y.(*ast.MatchOrPattern)
		if !ok || len(a.Options) != len(b.Options) {
			return false
		}
		for i := range a.Options {
			if !matchPatternsEqual(a.Options[i], b.Options[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// samePath preserves the projection spelling used by the SMT checker,
// including its positional encoding for non-place subexpressions.
func samePath(x, y ast.Expr) bool {
	switch x.(type) {
	case *ast.Ident, *ast.FieldExpr, *ast.ParenExpr:
		switch y.(type) {
		case *ast.Ident, *ast.FieldExpr, *ast.ParenExpr:
			return projectionName(x) == projectionName(y)
		}
	}
	return false
}

func projectionName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.FieldExpr:
		return projectionName(n.Object) + "." + n.Field
	case *ast.ParenExpr:
		return projectionName(n.Inner)
	default:
		return "@expr." + fmt.Sprintf("%x", fmt.Sprintf("%T_%s", expr, expr.Pos().String()))
	}
}
