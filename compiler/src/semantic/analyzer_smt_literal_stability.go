package semantic

import "elisacore/src/ast"

// A struct/tuple literal's components are evaluated left to right, once. Translating a component
// expression AGAIN at the proof point (`result.f` ↦ the `f` argument of the returned literal, or a
// literal projection in a goal) yields the captured value exactly when nothing evaluated after it
// could have changed what it reads. Side effects only come from calls (including overloaded
// operators), so a component is re-evaluable when every LATER component is side-effect free: the
// state at the proof point is then the state right after the component ran. (The component's own
// internal evaluation carries exactly the exposure of a direct `return expr` substitution, which the
// call-result model already governs.) Constants and immutable scalar bindings
// (constructorProjectionStable) are re-evaluable regardless.

// sideEffectFreeExpr is a conservative whitelist: literals, names, field reads, casts and
// primitive (non-overloaded) unary/binary operators over them, and nested literals of the same.
// Index expressions are excluded (a user type may lower indexing to a call).
func sideEffectFreeExpr(expr ast.Expr, depth int) bool {
	if expr == nil || depth > 64 {
		return false
	}
	switch n := expr.(type) {
	case *ast.Ident, *ast.IntLit, *ast.BoolLit, *ast.FloatLit, *ast.CharLit, *ast.StringLit, *ast.NullLit:
		return true
	case *ast.ParenExpr:
		return sideEffectFreeExpr(n.Inner, depth+1)
	case *ast.UnaryExpr:
		return n.LoweredCall == nil && sideEffectFreeExpr(n.Operand, depth+1)
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && sideEffectFreeExpr(n.Left, depth+1) && sideEffectFreeExpr(n.Right, depth+1)
	case *ast.CastExpr:
		return sideEffectFreeExpr(n.Operand, depth+1)
	case *ast.FieldExpr:
		return sideEffectFreeExpr(n.Object, depth+1)
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			if !sideEffectFreeExpr(elem, depth+1) {
				return false
			}
		}
		return true
	case *ast.StructLitExpr:
		if n.CopyBase != nil || len(n.Spreads) != 0 {
			return false
		}
		for _, arg := range n.Args {
			if !sideEffectFreeExpr(arg, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

// literalComponentReevaluable reports, for components in EVALUATION (source) order, which ones may be
// translated again at the proof point.
func (a *Analyzer) literalComponentReevaluable(components []ast.Expr) []bool {
	out := make([]bool, len(components))
	laterPure := true
	for i := len(components) - 1; i >= 0; i-- {
		c := components[i]
		out[i] = c != nil && (laterPure || a.constructorProjectionStable(c))
		if c == nil || !sideEffectFreeExpr(c, 0) {
			laterPure = false
		}
	}
	return out
}

// literalComponentReevaluableAt is literalComponentReevaluable for one index, without allocating
// (single-field projections are on a hot path).
func (a *Analyzer) literalComponentReevaluableAt(components []ast.Expr, index int) bool {
	if index < 0 || index >= len(components) || components[index] == nil {
		return false
	}
	laterPure := true
	for j := index + 1; laterPure && j < len(components); j++ {
		if components[j] == nil || !sideEffectFreeExpr(components[j], 0) {
			laterPure = false
		}
	}
	return laterPure || a.constructorProjectionStable(components[index])
}

// structLiteralReevaluable maps each source argument of a struct literal to whether it may be
// re-evaluated (see literalComponentReevaluable).
func (a *Analyzer) structLiteralReevaluable(sl *ast.StructLitExpr) map[ast.Expr]bool {
	if sl == nil {
		return nil
	}
	flags := a.literalComponentReevaluable(sl.Args)
	out := make(map[ast.Expr]bool, len(sl.Args))
	for i, arg := range sl.Args {
		if arg != nil && flags[i] {
			out[arg] = true
		}
	}
	return out
}
