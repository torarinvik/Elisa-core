package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// This file bridges REFINEMENT types to the array index-bounds proof. A value whose refinement
// proves `lo <= value <= hi` is a safe index into a constant-size array of size N whenever
// `lo >= 0 && hi <= N-1` — with NO runtime bounds check. This is the systems-code payoff of the
// decoder pattern: `regs[d.sdst]` where `sdst : u32 is InRange[0,127]` indexes a `[128]u32` for free.
//
// Soundness hinges on lawRangeBounds: it concludes a numeric interval ONLY when the law body is
// literally the canonical range conjunction `self >= lo and self <= hi` (any operand order /
// strict-or-not), so the named predicate genuinely entails the interval. A law whose body is
// anything else declines — no interval is fabricated.

// lawRangeBounds returns the closed interval [lo, hi] that a refinement predicate `Law[args...]`
// guarantees for its subject, when (and only when) the law is the canonical range form. It verifies
// the law body structurally and substitutes the predicate's constant arguments for the law's bound
// parameters. ok=false whenever the shape is not exactly a lower+upper bound on `self`.
func (a *Analyzer) lawRangeBounds(lawDecl *ast.FuncDecl, args []ast.Expr) (lo int64, hi int64, ok bool) {
	if lawDecl == nil || len(lawDecl.Params) == 0 {
		return 0, 0, false
	}
	body, bok := a.lawBodyExpr(lawDecl)
	if !bok {
		return 0, 0, false
	}
	// Map each non-subject law parameter name to the constant the predicate binds it to.
	selfName := lawDecl.Params[0].Name
	paramConst := map[string]int64{}
	for i := 1; i < len(lawDecl.Params); i++ {
		j := i - 1
		if j >= len(args) {
			return 0, 0, false
		}
		v, vok := a.constIntValue(args[j])
		if !vok {
			return 0, 0, false
		}
		paramConst[lawDecl.Params[i].Name] = v
	}
	haveLo, haveHi := false, false
	for _, conj := range splitConjuncts(body) {
		bound, val, kind := a.rangeConjunctBound(conj, selfName, paramConst)
		switch kind {
		case rangeLower:
			if !haveLo || bound > lo {
				lo = bound
			}
			haveLo = true
		case rangeUpper:
			if !haveHi || bound < hi {
				hi = bound
			}
			haveHi = true
		default:
			// A conjunct that is not a pure self-vs-constant bound makes the interval imprecise;
			// declining keeps the bridge sound (we never claim a tighter range than proven).
			return 0, 0, false
		}
		_ = val
	}
	if !haveLo || !haveHi || lo > hi {
		return 0, 0, false
	}
	return lo, hi, true
}

type rangeBoundKind int

const (
	rangeNone rangeBoundKind = iota
	rangeLower
	rangeUpper
)

// splitConjuncts flattens an `and` tree into its leaf comparisons.
func splitConjuncts(expr ast.Expr) []ast.Expr {
	bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr)
	if ok && bin != nil && bin.Op == lexer.TOKEN_AND {
		return append(splitConjuncts(bin.Left), splitConjuncts(bin.Right)...)
	}
	return []ast.Expr{expr}
}

// rangeConjunctBound classifies one comparison of the law body as a lower or upper bound on `self`,
// resolving the other operand to a constant via the predicate's bound-parameter substitution (or a
// literal baked into the law). Strict comparisons are tightened to the closed integer bound.
func (a *Analyzer) rangeConjunctBound(conj ast.Expr, selfName string, paramConst map[string]int64) (bound int64, ok bool, kind rangeBoundKind) {
	bin, isBin := stripOptimizationParens(conj).(*ast.BinaryExpr)
	if !isBin || bin == nil {
		return 0, false, rangeNone
	}
	leftSelf := isIdentNamed(bin.Left, selfName)
	rightSelf := isIdentNamed(bin.Right, selfName)
	if leftSelf == rightSelf {
		return 0, false, rangeNone // need exactly one side to be `self`
	}
	// Normalize so `self OP operand` with self on the left.
	op := bin.Op
	operand := bin.Right
	if rightSelf {
		operand = bin.Left
		op = flipComparison(op)
	}
	c, cok := a.rangeOperandConst(operand, paramConst)
	if !cok {
		return 0, false, rangeNone
	}
	switch op {
	case lexer.TOKEN_GTEQ: // self >= c  -> lower bound c
		return c, true, rangeLower
	case lexer.TOKEN_GT: // self > c    -> lower bound c+1
		return c + 1, true, rangeLower
	case lexer.TOKEN_LTEQ: // self <= c -> upper bound c
		return c, true, rangeUpper
	case lexer.TOKEN_LT: // self < c    -> upper bound c-1
		return c - 1, true, rangeUpper
	}
	return 0, false, rangeNone
}

// rangeOperandConst resolves a law-body operand to a constant: either a law bound-parameter (mapped
// to its predicate constant) or a literal integer.
func (a *Analyzer) rangeOperandConst(operand ast.Expr, paramConst map[string]int64) (int64, bool) {
	if id, ok := stripOptimizationParens(operand).(*ast.Ident); ok && id != nil {
		if v, known := paramConst[id.Name]; known {
			return v, true
		}
	}
	return a.constIntValue(operand)
}

// indexExprRefinementBounds returns the closed interval an index EXPRESSION is known to satisfy by a
// refinement it carries. Three construction/contract-backed sources are honored (none depend on
// invalidatable local flow facts):
//   - a refined struct-field read `d.sdst` (enforced at construction),
//   - a direct call whose return type is refined `f(..) -> u32 is InRange[..]` (enforced on every exit),
//   - an IMMUTABLE refined parameter `idx: u32 is InRange[..]` (enforced at the call boundary; immutable
//     so it cannot drift out of range inside the body).
func (a *Analyzer) indexExprRefinementBounds(idx ast.Expr) (lo int64, hi int64, ok bool) {
	preds := a.refinementPredsForIndexExpr(idx)
	for _, pred := range preds {
		lawDecl, _, lok := a.lookupLaw(pred.Name)
		if !lok || lawDecl == nil {
			continue
		}
		l, h, rok := a.lawRangeBounds(lawDecl, pred.Args)
		if !rok {
			continue
		}
		if !ok {
			lo, hi, ok = l, h, true
			continue
		}
		// Intersect when several refinements apply — the tightest interval is still sound.
		if l > lo {
			lo = l
		}
		if h < hi {
			hi = h
		}
	}
	return lo, hi, ok
}

// refinementPredsForIndexExpr collects the refinement predicates an index expression provably carries.
func (a *Analyzer) refinementPredsForIndexExpr(idx ast.Expr) []ast.RefinementPredExpr {
	switch n := stripOptimizationParens(idx).(type) {
	case *ast.FieldExpr:
		st, ok := stripRefForBounds(a.exprTypes[n.Object]).(*StructType)
		if !ok || st == nil || st.Decl == nil {
			return nil
		}
		for _, fd := range st.Decl.Fields {
			if fd.Name == n.Field {
				return refinementPredsOfTypeExpr(fd.Type)
			}
		}
	case *ast.CallExpr:
		decl, ok := a.resolveDirectCallFuncDecl(n)
		if ok && decl != nil {
			return refinementPredsOfTypeExpr(decl.ReturnType)
		}
	case *ast.Ident:
		if a.currentFuncDecl == nil {
			return nil
		}
		for _, p := range a.currentFuncDecl.Params {
			if p.Name != n.Name {
				continue
			}
			// Only an IMMUTABLE refined param keeps its boundary guarantee body-wide. A `mutable`
			// param could be reassigned out of range, so it carries no static interval here.
			if p.Mutable {
				return nil
			}
			if _, isMut := p.Type.(*ast.MutableType); isMut {
				return nil
			}
			return refinementPredsOfTypeExpr(p.Type)
		}
	}
	return nil
}

// refinementPredsOfTypeExpr extracts the refinement predicates of a (possibly mutable-wrapped) type expr.
func refinementPredsOfTypeExpr(te ast.TypeExpr) []ast.RefinementPredExpr {
	if mt, ok := te.(*ast.MutableType); ok && mt != nil {
		te = mt.Elem
	}
	if rt, ok := te.(*ast.RefinementTypeExpr); ok && rt != nil {
		return rt.Preds
	}
	return nil
}
