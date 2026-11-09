package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// collectLawConstraints walks a conjunction of `self OP <const>` comparisons, where the operand is
// a literal constant, a static param bound in paramConsts, or (brick 90-9) a static param bound to a
// direction-appropriate interval in paramRanges. Any other shape makes the whole body undecidable
// (returns false) so the prover stays sound by declining.
func (a *Analyzer) collectLawConstraints(expr ast.Expr, self string, paramConsts map[string]int64, paramRanges map[string]numRange, out *[]lawConstraint) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.collectLawConstraints(n.Inner, self, paramConsts, paramRanges, out)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			return a.collectLawConstraints(n.Left, self, paramConsts, paramRanges, out) && a.collectLawConstraints(n.Right, self, paramConsts, paramRanges, out)
		}
		// Normalize to `self OP operand` (the operand may sit on either side).
		var operand ast.Expr
		var op lexer.TokenKind
		switch {
		case isSelfIdent(n.Left, self):
			operand, op = n.Right, n.Op
		case isSelfIdent(n.Right, self):
			operand, op = n.Left, flipComparison(n.Op)
		default:
			return false
		}
		if c, ok := a.operandConst(operand, paramConsts); ok {
			*out = append(*out, lawConstraint{op: op, c: c})
			return true
		}
		// Ranged fallback: a param bound to an interval contributes the bound that matches the
		// comparison direction. `self >= param` (param ∈ [lo, hi]) ⟹ self >= lo; `self <= param` ⟹
		// self <= hi. `==` against a non-constant interval cannot become a single constraint → decline.
		if c, ok := a.operandRangeBound(operand, op, paramRanges); ok {
			*out = append(*out, lawConstraint{op: op, c: c})
			return true
		}
		return false
	default:
		return false
	}
}

// operandRangeBound resolves a law-body operand that is a static param bound to an interval, picking
// the bound that keeps `self OP operand` sound for the given direction. Returns ok=false when the
// operand is not a ranged param, the needed side of its interval is unknown, or the operator is one
// for which a single constant bound would be unsound (`==`, `!=`).
func (a *Analyzer) operandRangeBound(expr ast.Expr, op lexer.TokenKind, paramRanges map[string]numRange) (int64, bool) {
	if paramRanges == nil {
		return 0, false
	}
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return 0, false
	}
	r, ok := paramRanges[ident.Name]
	if !ok {
		return 0, false
	}
	switch op {
	case lexer.TOKEN_GTEQ, lexer.TOKEN_GT: // self >= param  ⟹  self >= param.lo
		if r.loKnown {
			return r.lo, true
		}
	case lexer.TOKEN_LTEQ, lexer.TOKEN_LT: // self <= param  ⟹  self <= param.hi
		if r.hiKnown {
			return r.hi, true
		}
	}
	return 0, false
}

// operandConst resolves a law-body comparison operand to a constant: a literal, or a static law
// param bound to a bracket-arg constant.
func (a *Analyzer) operandConst(expr ast.Expr, paramConsts map[string]int64) (int64, bool) {
	if ident, ok := expr.(*ast.Ident); ok && ident != nil {
		if c, bound := paramConsts[ident.Name]; bound {
			return c, true
		}
	}
	return a.constIntValue(expr)
}

func isSelfIdent(expr ast.Expr, self string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident != nil && ident.Name == self
}

// rangeEntailsConstraint reports whether the known range provably satisfies one law constraint.
//
// Sound cases handled:
//   - Interval-implies-comparison: [lo,hi] ⊆ {x | x OP c} for the basic comparison ops.
//   - Equality (degenerate interval): [k,k] entails any comparison that k satisfies, including
//     strict forms (k > k-1, k < k+1) already handled by the lo/hi cases below.
//   - Strict/non-strict duality via off-by-one: [lo,hi] entails x >= lo (already: r.lo >= lo)
//     and also x > lo-1 (already: r.lo > lo-1). Both directions are covered by the GT/GTEQ cases.
//   - Non-equality: [lo,hi] entails x != c when c is strictly outside [lo,hi] — i.e. c < lo
//     (every value in the range is > c, hence != c) or c > hi (every value is < c, hence != c).
func rangeEntailsConstraint(r numRange, k lawConstraint) bool {
	switch k.op {
	case lexer.TOKEN_GTEQ: // self >= c
		return r.loKnown && r.lo >= k.c
	case lexer.TOKEN_GT: // self > c
		return r.loKnown && r.lo > k.c
	case lexer.TOKEN_LTEQ: // self <= c
		return r.hiKnown && r.hi <= k.c
	case lexer.TOKEN_LT: // self < c
		return r.hiKnown && r.hi < k.c
	case lexer.TOKEN_EQEQ: // self == c  (degenerate range [c,c] is the only proof)
		return r.loKnown && r.hiKnown && r.lo == k.c && r.hi == k.c
	case lexer.TOKEN_BANGEQ: // self != c
		// Sound: the range is entirely below c (every value < c, hence != c), or entirely above c.
		// A one-sided open range is insufficient: we need to rule out c being in-range on BOTH sides.
		belowC := r.hiKnown && r.hi < k.c
		aboveC := r.loKnown && r.lo > k.c
		return belowC || aboveC
	default:
		return false
	}
}

// paramRefinementTypeExpr returns the refinement on a param's declared type expr — directly
// (`tx: i32 is Bounded[..]`) or through a type alias (`tx: TileX`, `type TileX = i32 is Bounded[..]`).
// The alias channel is aliasRefinements (namedTypes erases the refinement), keyed by the SAME
// canonical name resolveType uses, so the seed is never confused with a like-named alias elsewhere.
func (a *Analyzer) paramRefinementTypeExpr(te ast.TypeExpr) (*ast.RefinementTypeExpr, bool) {
	switch n := te.(type) {
	case *ast.RefinementTypeExpr:
		return n, n != nil
	case *ast.NamedType:
		if _, canonical, ok := a.lookupVisibleType(n.Name); ok && canonical != "" {
			if rt, found := a.aliasRefinements[canonical]; found {
				return rt, true
			}
		}
		if rt, found := a.aliasRefinements[n.Name]; found {
			return rt, true
		}
	case *ast.GenericType:
		// Parametric refinement alias use site: `x: SlotIndex[128]`. Look up the template
		// and instantiate by substituting the use-site args for the declared type params.
		if rt := a.lookupParametricAliasRefinement(n.Name, n.Args); rt != nil {
			return rt, true
		}
	}
	return nil, false
}

func (a *Analyzer) paramRefinementRangeFacts(params []ast.ParamDecl) map[int]numRange {
	var out map[int]numRange
	for i, param := range params {
		fact, any := a.paramRefinementRangeFact(param.Type, param.Name)
		if !any {
			continue
		}
		if out == nil {
			out = map[int]numRange{}
		}
		out[i] = fact
	}
	return out
}

func (a *Analyzer) paramRefinementRangeFact(te ast.TypeExpr, subjectName string) (numRange, bool) {
	switch n := te.(type) {
	case *ast.RefType:
		if n == nil {
			return numRange{}, false
		}
		return a.paramRefinementRangeFact(n.Elem, subjectName)
	case *ast.MutableType:
		if n == nil {
			return numRange{}, false
		}
		return a.paramRefinementRangeFact(n.Elem, subjectName)
	case *ast.OwnedType:
		if n == nil {
			return numRange{}, false
		}
		return a.paramRefinementRangeFact(n.Elem, subjectName)
	}
	if lo, hi, ok := a.refinementIntervalOfTypeExpr(te, subjectName); ok {
		return numRange{loKnown: true, lo: lo, hiKnown: true, hi: hi}, true
	}
	rt, ok := a.paramRefinementTypeExpr(te)
	if !ok {
		return numRange{}, false
	}
	return a.rangeFromRefinementTypeExpr(rt, nil)
}

// lookupParametricAliasRefinement resolves and instantiates a parametric alias use site,
// e.g. `SlotIndex[128]` → concrete `RefinementTypeExpr{u32, [InRange[0,128]]}`.
// Returns nil when the name is not a parametric alias or instantiation fails.
func (a *Analyzer) lookupParametricAliasRefinement(name string, args []ast.TypeExpr) *ast.RefinementTypeExpr {
	if a == nil || a.parametricAliasRefinements == nil {
		return nil
	}
	entry, found := a.parametricAliasRefinements[name]
	if !found {
		// Try namespace-qualified candidates (mirrors lookupRefineAlias).
		for _, candidate := range a.qualifiedNameCandidates(name) {
			if e, ok := a.parametricAliasRefinements[candidate]; ok {
				entry = e
				found = true
				break
			}
		}
	}
	if !found {
		return nil
	}
	return instantiateParametricAlias(entry, args)
}

// seedParamRefinementFacts records, on the function entry scope, the integer range implied by each
// IMMUTABLE integer param's declared refinement (docs/86 brick 86-2). This is what lets the docs/85
// §13 form `def tile_index(tx: TileX, ty: TileY) -> usize is Bounded[..]` prove with NO body guard:
// the params carry their bounds on entry, and tier-1/tier-2 read them from rangeFacts as usual.
// Mutable params are skipped (a fact could be invalidated mid-body) — sound, just no seed.
func (a *Analyzer) seedParamRefinementFacts(params []ast.ParamDecl) {
	if a.currentScope == nil {
		return
	}
	for _, param := range params {
		if a.paramIsMutable(param) {
			continue
		}
		rt, ok := a.paramRefinementTypeExpr(param.Type)
		if !ok {
			continue
		}
		sym, ok := a.currentScope.Lookup(param.Name)
		if !ok || sym == nil || sym.Mutable || !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
			continue
		}
		fact, any := a.rangeFromRefinementTypeExpr(rt, nil)
		if !any {
			continue
		}
		if a.currentScope.rangeFacts == nil {
			a.currentScope.rangeFacts = map[string]numRange{}
		}
		a.currentScope.rangeFacts[param.Name] = a.currentScope.rangeFacts[param.Name].intersect(fact)
	}
}

func (a *Analyzer) seedMutableRefParamRefinementRangeFact(arg ast.Expr, fact numRange) {
	a.seedMutableRefParamRefinementRangeFactInScope(a.currentScope, arg, fact)
}

func (a *Analyzer) seedMutableRefParamRefinementRangeFactInScope(scope *Scope, arg ast.Expr, fact numRange) {
	if scope == nil || arg == nil {
		return
	}
	name, ok := directIdentName(arg)
	if !ok {
		return
	}
	sym, ok := scope.Lookup(name)
	if !ok || sym == nil || !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
		return
	}
	if scope.rangeFacts == nil {
		scope.rangeFacts = map[string]numRange{}
	}
	scope.rangeFacts[name] = scope.rangeFacts[name].intersect(fact)
}

func directIdentName(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil {
			return "", false
		}
		return n.Name, true
	case *ast.ParenExpr:
		if n == nil {
			return "", false
		}
		return directIdentName(n.Inner)
	case *ast.UnaryExpr:
		if n == nil || n.Op != lexer.TOKEN_AMPERSAND {
			return "", false
		}
		return directIdentName(n.Operand)
	case *ast.AddrOfExpr:
		if n == nil {
			return "", false
		}
		return directIdentName(n.Operand)
	default:
		return "", false
	}
}

// rangeFromRefinementTypeExpr computes the integer interval implied by a refinement type expression
// whose law predicates reduce to compile-time-constant arguments (e.g. `i64 is Bounded[0, 100]`). It
// is the shared kernel behind both the param-entry seed (seedParamRefinementFacts) and the
// caller-side return-refinement seed (seedReturnRefinementFacts): a refinement on a function's return
// type IS its postcondition, so binding its result lets the caller assume the bound.
//
// `subst` (nil for the param-entry case) maps callee parameter names to the CALLER's argument
// expressions, so a parametric return refinement like `-> i64 is Bounded[0, n]` called as `f(100)`
// substitutes `n` → `100` and yields `[0, 100]` in the caller. A law arg that does not reduce to a
// constant after substitution drops that predicate — fails closed, never widens.
func (a *Analyzer) rangeFromRefinementTypeExpr(rt *ast.RefinementTypeExpr, subst map[string]ast.Expr) (numRange, bool) {
	if rt == nil {
		return numRange{}, false
	}
	return a.rangeFromRefinementPreds(rt.Preds, subst)
}

func (a *Analyzer) rangeFromRefinementPreds(preds []ast.RefinementPredExpr, subst map[string]ast.Expr) (numRange, bool) {
	fact := numRange{}
	any := false
	for _, pred := range preds {
		r, ok := a.rangeFromLawApplication(pred.Name, pred.Args, subst)
		if !ok {
			continue
		}
		fact = fact.intersect(r)
		any = true
	}
	return fact, any
}

// rangeFromLawApplication computes the integer interval implied by ONE law application `Law[args...]`
// (the subject being the value the law constrains). It is the per-predicate kernel shared by the
// return-type seed (rangeFromRefinementTypeExpr) and the `ensures <param> is Law` post-call seed
// (seedEnsuresParamRangeFacts, brick 90-11). `subst` maps callee params to caller arguments for parametric bounds
// (nil = exact-constant args). Returns ok=false when the args/law leave the decidable fragment.
func (a *Analyzer) rangeFromLawApplication(lawName string, lawArgs []ast.Expr, subst map[string]ast.Expr) (numRange, bool) {
	decl, _, ok := a.lookupLaw(lawName)
	if !ok || decl == nil || len(lawArgs) != len(decl.Params)-1 {
		return numRange{}, false
	}
	paramConsts := map[string]int64{}
	var paramRanges map[string]numRange
	for i, arg := range lawArgs {
		name := decl.Params[i+1].Name
		if c, ok := a.substConstInt(arg, subst); ok {
			paramConsts[name] = c
			continue
		}
		// Not an exact constant: try a known interval for the (substituted) argument, used
		// direction-aware in collectLawConstraints (brick 90-9). nil subst (param-entry seed) never
		// reaches here with a range, so that path is unchanged.
		if r, ok := a.substArgRange(arg, subst); ok {
			if paramRanges == nil {
				paramRanges = map[string]numRange{}
			}
			paramRanges[name] = r
		}
		// Either way leave the param out of paramConsts; collectLawConstraints declines any
		// constraint whose operand it cannot resolve, dropping the whole predicate (sound).
	}
	constraints, ok := a.lawConstraintsRanged(decl, paramConsts, paramRanges)
	if !ok || len(constraints) == 0 {
		return numRange{}, false
	}
	fact := numRange{}
	for _, k := range constraints {
		fact = fact.intersect(constraintToRange(k))
	}
	return fact, true
}

// substConstInt const-evaluates an integer expression, first replacing any identifier named in
// `subst` (a callee parameter) with the caller's argument expression (evaluated in the caller scope).
// With subst==nil it is exactly constIntValue. Covers the small arithmetic fragment a refinement's
// bracket arguments can use (`Bounded[0, n]`, `Bounded[0, n - 1]`, `Bounded[0, n * 2]`). Anything
// outside the fragment, or a substituted argument that is not itself constant, returns ok=false.
func (a *Analyzer) substConstInt(expr ast.Expr, subst map[string]ast.Expr) (int64, bool) {
	if subst == nil {
		return a.constIntValue(expr)
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.substConstInt(n.Inner, subst)
	case *ast.Ident:
		if arg, ok := subst[n.Name]; ok {
			// The callee parameter is bound to the caller's argument; that argument must const-fold in
			// the caller scope for the bracket value to be statically known.
			return a.constIntValue(arg)
		}
		return a.constIntValue(n)
	case *ast.UnaryExpr:
		if n.Op != lexer.TOKEN_MINUS {
			return 0, false
		}
		v, ok := a.substConstInt(n.Operand, subst)
		if !ok {
			return 0, false
		}
		return -v, true
	case *ast.BinaryExpr:
		l, ok := a.substConstInt(n.Left, subst)
		if !ok {
			return 0, false
		}
		r, ok := a.substConstInt(n.Right, subst)
		if !ok {
			return 0, false
		}
		switch n.Op {
		case lexer.TOKEN_PLUS:
			return l + r, true
		case lexer.TOKEN_MINUS:
			return l - r, true
		case lexer.TOKEN_STAR:
			return l * r, true
		case lexer.TOKEN_SLASH:
			if r == 0 {
				return 0, false
			}
			return l / r, true
		default:
			return 0, false
		}
	default:
		return a.constIntValue(expr)
	}
}

// substArgRange bounds a refinement bracket argument as an interval after substituting callee params
// with caller arguments (docs/90 brick 90-9). It reuses the bounded-linear machinery: the substituted
// expression's affine form, bounded over the caller's range facts. Used only as the fallback when the
// argument is not an exact constant (e.g. `cap_to(k)` with `k ∈ [0, 10]` makes the bracket arg `n`
// resolve to `[0, 10]`). Returns ok=false outside the linear fragment.
func (a *Analyzer) substArgRange(arg ast.Expr, subst map[string]ast.Expr) (numRange, bool) {
	if subst == nil {
		return numRange{}, false
	}
	af, ok := a.substitutedAffine(arg, subst)
	if !ok {
		return numRange{}, false
	}
	r := a.boundAffine(af, a.currentScope)
	if !r.loKnown && !r.hiKnown {
		return numRange{}, false
	}
	return r, true
}

// seedReturnRefinementFacts records the integer range implied by a callee's REFINED return type onto
// an immutable integer binding `name = f(args)` (docs/90 brick 90-7). The return refinement is the
// function's postcondition; a caller that binds the result may assume it. This closes the modular
// loop: a function PROVES its return refinement (dischargeReturnRefinements), and every caller then
// USES it as a fact — without re-deriving it from the body.
//
// Sound and conservative:
//   - Immutable bindings only (the caller passes n.Mutable==false); a mutable binding could be
//     reassigned, invalidating the fact.
//   - Only direct calls to a resolvable FuncDecl with a constant-argument return refinement; anything
//     else simply seeds nothing.
//   - The seed only NARROWS (intersect), so it can never widen an existing fact unsoundly.
//   - Like all SMT/refinement VALUE facts, this never drives bounds-check elision (that is the
//     separate syntactic indexBoundsProven system), so even a buggy callee contract cannot create
//     memory unsafety — it is garbage-in-garbage-out at worst.
func (a *Analyzer) seedReturnRefinementFacts(name string, value ast.Expr, bindingType Type) {
	if a.currentScope == nil || value == nil {
		return
	}
	if !IsNumericType(bindingType) || IsFloatType(bindingType) {
		return
	}
	call, ok := a.proofCallExpr(value)
	if !ok {
		return
	}
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok || decl == nil {
		return
	}
	// Bind each callee parameter to the caller's argument, so a parametric postcondition
	// (`-> i64 is Bounded[0, n]`, `ensure result <= n`) is resolved in the caller's terms. Absent args
	// (variadic/defaulted) are simply not bound — the dependent clause then drops, never widens.
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(call.Args) || call.Args[i] == nil {
			continue
		}
		subst[param.Name] = call.Args[i]
	}
	fact := numRange{}
	any := false
	// (1) The refined return type (`-> i64 is Bounded[..]`) — bricks 90-7/8/9.
	if s, ok := a.valueRefinementSchemeFromTypeExpr(decl.ReturnType); ok {
		if r, found := a.rangeFromRefinementPreds(s.Preds, subst); found {
			fact = fact.intersect(r)
			any = true
		}
	}
	// (2) Value-contract postconditions over `result` (`ensure result >= 0`, `ensure result <= n`) —
	// brick 90-10. These constrain the returned value, which IS this (immutable) binding, so the caller
	// may assume them just like the return refinement. Reuses the same direction-aware bracket machinery
	// with `result` as the subject.
	if r, found := a.rangeFromEnsureResult(decl.EnsureValues, subst); found {
		fact = fact.intersect(r)
		any = true
	}
	if !any {
		return
	}
	if a.currentScope.rangeFacts == nil {
		a.currentScope.rangeFacts = map[string]numRange{}
	}
	a.currentScope.rangeFacts[name] = a.currentScope.rangeFacts[name].intersect(fact)
}
