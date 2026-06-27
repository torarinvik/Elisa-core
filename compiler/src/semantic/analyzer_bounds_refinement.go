package semantic

import (
	"strconv"

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

// indexExprRefinementBounds returns the closed interval an index EXPRESSION is known to satisfy. The
// base case is a value that directly carries a refinement; an AFFINE expression over such a value
// (`idx + 1`, `idx * 2`, `base + 1`) is propagated by monotone interval arithmetic. This is what
// multi-dword / register-PAIR GCN accesses need: `sgprs[idx]` and `sgprs[idx+1]` where idx is
// InRange[0,126] both prove in bounds for an array[u32,128].
//
// Affine propagation is sound because the array-bounds caller requires the RESULT interval to fit
// `[0, ConstSize)` (a small N): a shift/scale that would overflow the machine width produces a hi far
// outside [0,N) and the access simply declines — the value never actually wraps within the proven range.
func (a *Analyzer) indexExprRefinementBounds(idx ast.Expr) (lo int64, hi int64, ok bool) {
	switch n := stripOptimizationParens(idx).(type) {
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_PLUS:
			if c, cok := a.constIntValue(n.Right); cok {
				return a.shiftInterval(n.Left, c)
			}
			if c, cok := a.constIntValue(n.Left); cok {
				return a.shiftInterval(n.Right, c)
			}
		case lexer.TOKEN_MINUS:
			if c, cok := a.constIntValue(n.Right); cok {
				return a.shiftInterval(n.Left, -c)
			}
		case lexer.TOKEN_STAR:
			if c, cok := a.constIntValue(n.Right); cok && c > 0 {
				return a.scaleInterval(n.Left, c)
			}
			if c, cok := a.constIntValue(n.Left); cok && c > 0 {
				return a.scaleInterval(n.Right, c)
			}
		}
		return 0, 0, false
	}
	return a.refinementCarriedInterval(idx)
}

// shiftInterval offsets a sub-expression's proven interval by a constant (`expr + delta`).
func (a *Analyzer) shiftInterval(expr ast.Expr, delta int64) (int64, int64, bool) {
	lo, hi, ok := a.indexExprRefinementBounds(expr)
	if !ok {
		return 0, 0, false
	}
	return lo + delta, hi + delta, true
}

// scaleInterval multiplies a sub-expression's proven interval by a positive constant (`expr * k`).
func (a *Analyzer) scaleInterval(expr ast.Expr, k int64) (int64, int64, bool) {
	lo, hi, ok := a.indexExprRefinementBounds(expr)
	if !ok {
		return 0, 0, false
	}
	return lo * k, hi * k, true
}

// objectIsImmutableDArrayParam reports whether `obj` is an IMMUTABLE dynamic-array parameter — a
// plain (readonly) `darray[T]&` / `darray[T]` param, not `mutable`. Immutability is the soundness
// precondition for trusting a `requires count >= K` length bound at an index site: a `mutable`
// darray could `pop`/`clear` and shrink its count below K inside the body, but a readonly param's
// length cannot change (it cannot be reassigned, nor mutably re-borrowed).
func (a *Analyzer) objectIsImmutableDArrayParam(obj ast.Expr) bool {
	ident, ok := stripOptimizationParens(obj).(*ast.Ident)
	if !ok || ident == nil || a.currentFuncDecl == nil {
		return false
	}
	for _, p := range a.currentFuncDecl.Params {
		if p.Name != ident.Name {
			continue
		}
		if p.Mutable {
			return false
		}
		if _, isMut := p.Type.(*ast.MutableType); isMut {
			return false
		}
		return true
	}
	return false
}

// liveRequiresCountLowerBound returns the largest constant K such that a live
// `requires <obj>.count >= K` (or `> K-1`) precondition of the enclosing function is in scope.
func (a *Analyzer) liveRequiresCountLowerBound(obj ast.Expr) (int64, bool) {
	if a == nil || a.currentFuncDecl == nil {
		return 0, false
	}
	base := optimizationExprString(obj)
	if base == "" {
		return 0, false
	}
	target := base + ".count"
	best, found := int64(0), false
	for _, req := range a.currentFuncDecl.Requires {
		// No assertFactLive gate is needed here: the caller (objectIsImmutableDArrayParam) has already
		// established the darray is immutable, so its count cannot change in the body and the
		// precondition holds at every access — there is no mutation that could stale this fact.
		k, ok := a.requiresClauseLowerBound(req, target)
		if !ok {
			continue
		}
		if !found || k > best {
			best, found = k, true
		}
	}
	return best, found
}

// requiresClauseLowerBound extracts a constant lower bound K on the expression whose optimization
// string is `target`, from `target >= K` / `target > K` (or the flipped `K <= target` / `K < target`).
func (a *Analyzer) requiresClauseLowerBound(expr ast.Expr, target string) (int64, bool) {
	bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr)
	if !ok || bin == nil {
		return 0, false
	}
	leftIsTarget := optimizationExprString(bin.Left) == target
	rightIsTarget := optimizationExprString(bin.Right) == target
	switch bin.Op {
	case lexer.TOKEN_GTEQ: // target >= K
		if leftIsTarget {
			if k, ok := a.constIntValue(bin.Right); ok {
				return k, true
			}
		}
	case lexer.TOKEN_GT: // target > K  ⇒  target >= K+1
		if leftIsTarget {
			if k, ok := a.constIntValue(bin.Right); ok {
				return k + 1, true
			}
		}
	case lexer.TOKEN_LTEQ: // K <= target
		if rightIsTarget {
			if k, ok := a.constIntValue(bin.Left); ok {
				return k, true
			}
		}
	case lexer.TOKEN_LT: // K < target  ⇒  target >= K+1
		if rightIsTarget {
			if k, ok := a.constIntValue(bin.Left); ok {
				return k + 1, true
			}
		}
	}
	return 0, false
}

// refinementCarriedInterval returns the interval a value directly carries via a refinement it holds.
// Three construction/contract-backed sources are honored (none depend on invalidatable local flow facts):
//   - a refined struct-field read `d.sdst` (enforced at construction),
//   - a direct call whose return type is refined `f(..) -> u32 is InRange[..]` (enforced on every exit),
//   - an IMMUTABLE refined parameter `idx: u32 is InRange[..]` (enforced at the call boundary; immutable
//     so it cannot drift out of range inside the body).
func (a *Analyzer) refinementCarriedInterval(idx ast.Expr) (lo int64, hi int64, ok bool) {
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
				return a.refinementPredsOfTypeExpr(fd.Type)
			}
		}
	case *ast.CallExpr:
		decl, ok := a.resolveDirectCallFuncDecl(n)
		if ok && decl != nil {
			return a.refinementPredsOfTypeExpr(decl.ReturnType)
		}
		// P2: a generic protocol method call (`D.decode(d, w)` / `d.decode(w)`) whose result type is
		// an unresolved associated-type projection `D.Field`. Every conforming impl that binds Field
		// to a refined type carries that refinement; the bound a generic caller may rely on is the
		// INTERSECTION across all impls (so one non-refined impl correctly forfeits elision).
		if preds, ok := a.associatedTypeProjectionRefinement(a.exprTypes[n]); ok {
			return preds
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
			return a.refinementPredsOfTypeExpr(p.Type)
		}
	}
	return nil
}

// associatedTypeProjectionRefinement returns the refinement predicates a generic protocol-method call
// result of associated-type type `D.Field` provably carries (P2). For an abstract type param `D: P`,
// the concrete impl is unknown at this site, so the only bound a caller may soundly rely on is one
// that EVERY conforming impl of P guarantees. We therefore:
//   - require that EVERY impl of P binds `Field` to a refined type (any non-refined impl ⇒ no bound,
//     because that impl's value could be anything — correctly forfeiting elision), and
//   - take the convex HULL (widest lo..hi) of the impls' intervals: the concrete value lies in some
//     one impl's interval, hence in the hull. The hull is re-expressed as a single InRange-style pred
//     reusing the impls' law name so the existing lawRangeBounds path computes the interval.
func (a *Analyzer) associatedTypeProjectionRefinement(t Type) ([]ast.RefinementPredExpr, bool) {
	proj, ok := t.(*AssociatedTypeProjection)
	if !ok || proj == nil || proj.InterfaceName == "" || proj.Name == "" {
		return nil, false
	}
	var lawName string
	var hullPred ast.RefinementPredExpr
	var loV, hiV int64
	count := 0
	for _, impl := range a.staticImpls {
		if impl == nil || impl.InterfaceName != proj.InterfaceName {
			continue
		}
		count++
		preds := impl.AssociatedTypeRefinements[proj.Name]
		if len(preds) == 0 {
			// An impl with no refinement on this associated type: a generic caller can prove nothing.
			return nil, false
		}
		l, h, rok := a.predsInterval(preds)
		if !rok {
			return nil, false
		}
		if lawName == "" {
			lawName = preds[0].Name
			hullPred = preds[0]
			loV, hiV = l, h
			continue
		}
		if l < loV {
			loV = l
		}
		if h > hiV {
			hiV = h
		}
	}
	if count == 0 || lawName == "" {
		return nil, false
	}
	// Re-express the hull as a single InRange-style predicate carrying the widened [loV,hiV] bounds.
	hullPred.Args = []ast.Expr{intLiteralExprForBound(loV), intLiteralExprForBound(hiV)}
	return []ast.RefinementPredExpr{hullPred}, true
}

// associatedTypeProjectionBase returns the concrete (representation-erased) type that every impl of
// proj's protocol binds proj's associated type to, when they all agree. Used so a generic index of
// associated-type type can be validated/elided against its real base (e.g. u32).
func (a *Analyzer) associatedTypeProjectionBase(proj *AssociatedTypeProjection) (Type, bool) {
	if proj == nil || proj.InterfaceName == "" || proj.Name == "" {
		return nil, false
	}
	var base Type
	for _, impl := range a.staticImpls {
		if impl == nil || impl.InterfaceName != proj.InterfaceName {
			continue
		}
		t, ok := impl.AssociatedTypes[proj.Name]
		if !ok || t == nil {
			return nil, false
		}
		if base == nil {
			base = t
			continue
		}
		if !SameType(base, t) {
			return nil, false
		}
	}
	return base, base != nil
}

// intLiteralExprForBound builds an AST integer literal carrying the given value, for synthesizing the
// hull predicate's bracket arguments (negative values are not produced by InRange-style bounds here).
func intLiteralExprForBound(v int64) ast.Expr {
	return &ast.IntLit{Value: strconv.FormatInt(v, 10)}
}

// predsInterval reduces a refinement pred list to the tightest interval its laws prove, mirroring
// refinementCarriedInterval's reduction but over an explicit pred list.
func (a *Analyzer) predsInterval(preds []ast.RefinementPredExpr) (lo int64, hi int64, ok bool) {
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
		if l > lo {
			lo = l
		}
		if h < hi {
			hi = h
		}
	}
	return lo, hi, ok
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

// refinementPredsOfTypeExpr is the alias-aware analyzer variant used by index-bounds elision. It
// mirrors paramRefinementTypeExpr so shader-style aliases such as
// `type GcnScalarRegisterIndex = u32 is InRange[0, 127]` carry their interval at index sites.
func (a *Analyzer) refinementPredsOfTypeExpr(te ast.TypeExpr) []ast.RefinementPredExpr {
	if mt, ok := te.(*ast.MutableType); ok && mt != nil {
		te = mt.Elem
	}
	if rt, ok := a.paramRefinementTypeExpr(te); ok && rt != nil {
		return rt.Preds
	}
	return nil
}
