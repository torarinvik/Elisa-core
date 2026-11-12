package lemmasubstitution

import (
	"elisacore/src/ast"
	"elisacore/src/purereturnshape"
)

// MaxDepth bounds recursive substitutions.
const MaxDepth = 128

func WithBudget(expr ast.Expr, subst map[string]ast.Expr, work *int, depth int, contains func(ast.Expr, string) bool) (ast.Expr, bool) {
	if expr == nil || work == nil || depth >= MaxDepth || *work <= 0 {
		return nil, false
	}
	(*work)--
	switch n := expr.(type) {
	case *ast.Ident:
		if arg, ok := subst[n.Name]; ok {
			return arg, true
		}
		// Not a parameter: a global/const reference — same meaning in the caller. Keep it.
		return n, true
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit, *ast.StringLit, *ast.NullLit:
		return n, true
	case *ast.ParenExpr:
		inner, ok := WithBudget(n.Inner, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		return &ast.ParenExpr{Position: n.Position, Inner: inner}, true
	case *ast.UnaryExpr:
		operand, ok := WithBudget(n.Operand, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: operand}, true
	case *ast.BinaryExpr:
		left, ok := WithBudget(n.Left, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		right, ok := WithBudget(n.Right, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: left, Right: right}, true
	case *ast.QuantifierExpr:
		// Capture-avoiding: a bound name that is substituted, or that occurs in a substituted
		// argument, would change meaning under the binder; decline.
		for _, name := range n.Vars {
			if _, shadowed := subst[name]; shadowed {
				return nil, false
			}
			for _, arg := range subst {
				if contains(arg, name) {
					return nil, false
				}
			}
		}
		var in ast.Expr
		if n.In != nil {
			var ok bool
			if in, ok = WithBudget(n.In, subst, work, depth+1, contains); !ok {
				return nil, false
			}
		}
		body, ok := WithBudget(n.Body, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		return &ast.QuantifierExpr{Position: n.Position, Exists: n.Exists, Vars: n.Vars, In: in, Body: body}, true
	case *ast.TernaryExpr:
		cond, ok := WithBudget(n.Cond, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		value, ok := WithBudget(n.Value, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		alt, ok := WithBudget(n.Alt, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		return &ast.TernaryExpr{Position: n.Position, Cond: cond, Value: value, Alt: alt}, true
	case *ast.MatchExpr:
		if n.Store != nil {
			return nil, false
		}
		value, ok := WithBudget(n.Value, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		arms := make([]ast.MatchArm, len(n.Arms))
		for i, arm := range n.Arms {
			// Payload binders need capture-avoiding substitution; decline until supported.
			if !purereturnshape.ScalarMatchPattern(arm.Pattern) || arm.Guard != nil || len(arm.Body) != 1 {
				return nil, false
			}
			tail, ok := arm.Body[0].(*ast.ExprStmt)
			if !ok {
				return nil, false
			}
			body, ok := WithBudget(tail.Expr, subst, work, depth+1, contains)
			if !ok {
				return nil, false
			}
			arms[i] = arm
			arms[i].Body = []ast.Stmt{&ast.ExprStmt{Position: tail.Position, Expr: body}}
		}
		return &ast.MatchExpr{Position: n.Position, Value: value, Arms: arms}, true
	case *ast.FieldExpr:
		object, ok := WithBudget(n.Object, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		return &ast.FieldExpr{Position: n.Position, Object: object, Field: n.Field}, true
	case *ast.IndexExpr:
		object, ok := WithBudget(n.Object, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		index, ok := WithBudget(n.Index, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		return &ast.IndexExpr{Position: n.Position, Object: object, Index: index}, true
	case *ast.CastExpr:
		// A value-form numeric conversion (`(i - 1).usize()`) substituted into a loop measure. Rebuild
		// the cast over the substituted operand, preserving Origin/Target so the SMT term translator's
		// CastExpr handler (which keys off exprTypes of operand and result) models it value-exactly. Only
		// the operand is rewritten; the target type is unchanged.
		operand, ok := WithBudget(n.Operand, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		return &ast.CastExpr{Position: n.Position, Operand: operand, Target: n.Target, Origin: n.Origin, RefShorthand: n.RefShorthand}, true
	case *ast.CallExpr:
		fn, ok := WithBudget(n.Func, subst, work, depth+1, contains)
		if !ok {
			return nil, false
		}
		newArgs := make([]ast.Expr, len(n.Args))
		for i, arg := range n.Args {
			ra, ok := WithBudget(arg, subst, work, depth+1, contains)
			if !ok {
				return nil, false
			}
			newArgs[i] = ra
		}
		return &ast.CallExpr{Position: n.Position, Func: fn, Args: newArgs}, true
	case *ast.TupleExpr:
		elems := make([]ast.Expr, len(n.Elems))
		for i, elem := range n.Elems {
			re, ok := WithBudget(elem, subst, work, depth+1, contains)
			if !ok {
				return nil, false
			}
			elems[i] = re
		}
		return &ast.TupleExpr{Position: n.Position, Elems: elems}, true
	case *ast.SliceExpr:
		parts := [3]ast.Expr{n.Object, n.Start, n.End}
		for i, part := range parts {
			if part == nil {
				continue
			}
			rp, ok := WithBudget(part, subst, work, depth+1, contains)
			if !ok {
				return nil, false
			}
			parts[i] = rp
		}
		return &ast.SliceExpr{Position: n.Position, Object: parts[0], Start: parts[1], End: parts[2]}, true
	case *ast.StructLitExpr:
		if n.CopyBase != nil || len(n.Spreads) != 0 {
			return nil, false
		}
		args := make([]ast.Expr, len(n.Args))
		for i, arg := range n.Args {
			ra, ok := WithBudget(arg, subst, work, depth+1, contains)
			if !ok {
				return nil, false
			}
			args[i] = ra
		}
		lit := *n
		lit.Args = args
		return &lit, true
	default:
		return nil, false
	}
}
