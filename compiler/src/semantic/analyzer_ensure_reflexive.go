package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// ensureGoalReflexivelyValid reports whether a postcondition, with `result` replaced by the
// returned expression, is valid by reflexivity and boolean folding alone: every `E == E` (`E != E`)
// with structurally identical operands is true (false), boolean literals fold, and the clause
// reduces to true. The SMT lane misses these whenever an operand is outside its fragment (a string
// comparison in the guard, a call returning a view, an enum payload), e.g.
// `ensure op == "cpuid" implies result == cpuid_mask()` at `return cpuid_mask()`.
//
// Soundness. An ensure clause is checked pure when it is analyzed (no IO, allocation, mutation,
// time, randomness or mutable-global reads), so an operand text that occurs in the clause names
// pure calls; an occurrence produced by substituting `result` denotes the single returned value.
// Both sides are evaluated at the same return point, so identical text denotes the same value as
// long as every name means what it meant in the clause: the check declines while any binding
// shadows another, and when a name resolves to a body local that hides a module-level symbol.
// Float operands are excluded (NaN != NaN).
func (a *Analyzer) ensureGoalReflexivelyValid(clause ast.Expr, subst map[string]ast.Expr) bool {
	if a == nil || clause == nil || a.currentFuncDecl == nil || a.currentScope == nil {
		return false
	}
	if a.shadowingActive() || a.mentionsShadowedParam(clause) {
		return false
	}
	goal := clause
	if len(subst) > 0 {
		goal = substituteIdents(clause, subst)
	}
	v, known := a.reflexiveFold(goal)
	return known && v
}

// reflexiveFold evaluates the boolean skeleton of e; known is false when e's value is not
// determined by reflexivity and literals alone.
func (a *Analyzer) reflexiveFold(e ast.Expr) (value bool, known bool) {
	switch n := e.(type) {
	case *ast.ParenExpr:
		return a.reflexiveFold(n.Inner)
	case *ast.BoolLit:
		return n.Value, true
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT && n.LoweredCall == nil {
			v, ok := a.reflexiveFold(n.Operand)
			return !v, ok
		}
	case *ast.BinaryExpr:
		if n.LoweredCall != nil {
			return false, false
		}
		switch n.Op {
		case lexer.TOKEN_OR:
			l, lok := a.reflexiveFold(n.Left)
			if lok && l {
				return true, true
			}
			r, rok := a.reflexiveFold(n.Right)
			if rok && r {
				return true, true
			}
			return false, lok && rok
		case lexer.TOKEN_AND:
			l, lok := a.reflexiveFold(n.Left)
			if lok && !l {
				return false, true
			}
			r, rok := a.reflexiveFold(n.Right)
			if rok && !r {
				return false, true
			}
			return true, lok && rok
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
			eq := n.Op == lexer.TOKEN_EQEQ
			if a.reflexiveOperandsEqual(n.Left, n.Right) {
				return eq, true
			}
			l, lok := a.reflexiveFold(n.Left)
			r, rok := a.reflexiveFold(n.Right)
			if lok && rok {
				return (l == r) == eq, true
			}
		}
	}
	return false, false
}

// reflexiveOperandsEqual: structurally identical operands of a non-float type whose names all
// denote their clause meaning.
func (a *Analyzer) reflexiveOperandsEqual(l, r ast.Expr) bool {
	for _, side := range []ast.Expr{l, r} {
		if t := a.exprType(stripOptimizationParens(side)); t != nil && IsFloatType(stripRefForBounds(t)) {
			return false
		}
	}
	if t := a.exprType(stripOptimizationParens(l)); t == nil {
		if rt := a.exprType(stripOptimizationParens(r)); rt == nil {
			// Result substitution clones field nodes, including unchanged pointer projections on
			// the other side. Recover their caller-scope types before declining: a stable pointer
			// read is reflexive, whereas an unknown or floating-point read must remain excluded.
			tr := &smtTranslator{a: a}
			if !a.exprIsPointerLike(tr.pointerIdentityOperandType(l)) || !a.exprIsPointerLike(tr.pointerIdentityOperandType(r)) ||
				!tr.pointerIdentityReadStable(l) || !tr.pointerIdentityReadStable(r) {
				return false
			}
		}
	}
	return a.sameExprText(l, r)
}

// sameExprText is exact structural equality over the expression forms a contract operand uses;
// any other form compares unequal (declines).
func (a *Analyzer) sameExprText(l, r ast.Expr) bool {
	l, r = stripOptimizationParens(l), stripOptimizationParens(r)
	if l == nil || r == nil {
		return false
	}
	switch x := l.(type) {
	case *ast.Ident:
		y, ok := r.(*ast.Ident)
		return ok && x.Name == y.Name && a.identMeansClauseName(x.Name)
	case *ast.IntLit:
		y, ok := r.(*ast.IntLit)
		return ok && x.Value == y.Value && x.Suffix == y.Suffix
	case *ast.BoolLit:
		y, ok := r.(*ast.BoolLit)
		return ok && x.Value == y.Value
	case *ast.StringLit:
		y, ok := r.(*ast.StringLit)
		return ok && x.Value == y.Value
	case *ast.FieldExpr:
		y, ok := r.(*ast.FieldExpr)
		return ok && x.Field == y.Field && a.sameExprText(x.Object, y.Object)
	case *ast.IndexExpr:
		y, ok := r.(*ast.IndexExpr)
		if !ok || x.Index2 != nil || y.Index2 != nil || x.Fallback != nil || y.Fallback != nil ||
			x.AsSpecialize != nil || y.AsSpecialize != nil || x.AsOverlayCall != nil || y.AsOverlayCall != nil ||
			x.AsBuiltinDictGet != nil || y.AsBuiltinDictGet != nil {
			return false
		}
		return a.sameExprText(x.Object, y.Object) && a.sameExprText(x.Index, y.Index)
	case *ast.SliceExpr:
		y, ok := r.(*ast.SliceExpr)
		return ok && a.sameExprText(x.Object, y.Object) && a.sameOptionalExprText(x.Start, y.Start) && a.sameOptionalExprText(x.End, y.End)
	case *ast.UnaryExpr:
		y, ok := r.(*ast.UnaryExpr)
		return ok && x.Op == y.Op && x.Op != lexer.TOKEN_AMPERSAND && a.sameExprText(x.Operand, y.Operand)
	case *ast.BinaryExpr:
		y, ok := r.(*ast.BinaryExpr)
		return ok && x.Op == y.Op && a.sameExprText(x.Left, y.Left) && a.sameExprText(x.Right, y.Right)
	case *ast.TernaryExpr:
		y, ok := r.(*ast.TernaryExpr)
		return ok && a.sameExprText(x.Cond, y.Cond) && a.sameExprText(x.Value, y.Value) && a.sameExprText(x.Alt, y.Alt)
	case *ast.CastExpr:
		y, ok := r.(*ast.CastExpr)
		if !ok || !a.sameExprText(x.Operand, y.Operand) {
			return false
		}
		// Same target: compare the analyzed result types (the target syntax has several spellings).
		xt, yt := a.exprType(x), a.exprType(y)
		return xt != nil && yt != nil && SameType(xt, yt)
	case *ast.CallExpr:
		y, ok := r.(*ast.CallExpr)
		if !ok || len(x.Args) != len(y.Args) || !a.sameExprText(x.Func, y.Func) {
			return false
		}
		for i := range x.Args {
			if x.ArgName(i) != y.ArgName(i) || !a.sameExprText(x.Args[i], y.Args[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func (a *Analyzer) sameOptionalExprText(l, r ast.Expr) bool {
	if l == nil || r == nil {
		return l == nil && r == nil
	}
	return a.sameExprText(l, r)
}

// identMeansClauseName: a name resolving to a body local that hides a module-level symbol of the
// same name would read the local in the body text but the global in the clause.
func (a *Analyzer) identMeansClauseName(name string) bool {
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil || sym.Kind != SymbolLocal {
		return true
	}
	if a.globalScope != nil {
		if _, global := a.globalScope.Lookup(name); global {
			return false
		}
	}
	return true
}
