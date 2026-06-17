package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// numRange is a closed integer interval fact: lo <= value <= hi, with either bound optionally
// unknown (open). It is the flow-prover's abstraction of what a branch condition tells us about an
// immutable integer variable (docs/85 1d-2).
type numRange struct {
	loKnown bool
	lo      int64
	hiKnown bool
	hi      int64
}

// intersect tightens a range with another fact about the same variable (conjunction of conditions).
func (r numRange) intersect(o numRange) numRange {
	out := r
	if o.loKnown && (!out.loKnown || o.lo > out.lo) {
		out.loKnown, out.lo = true, o.lo
	}
	if o.hiKnown && (!out.hiKnown || o.hi < out.hi) {
		out.hiKnown, out.hi = true, o.hi
	}
	return out
}

// gatherNumericRangeRefinement records integer-bound facts for an IMMUTABLE identifier compared
// against a compile-time-constant in a (truthy) branch condition: `a > 5`, `a >= 0`, `a < n`,
// `5 < a`, `a == k`, etc. Immutable-only so the fact holds for the whole branch with no
// invalidation. Called from applyConditionRefinementsInternal for comparison operators.
func (a *Analyzer) gatherNumericRangeRefinement(scope *Scope, n *ast.BinaryExpr, truthy bool) {
	if !truthy || scope == nil || n == nil {
		return
	}
	// Normalize to `ident OP const`. If the constant is on the left (`5 < a`), flip the operator.
	op := n.Op
	name, ok := immutableIntIdentName(a, scope, n.Left)
	var c int64
	var cok bool
	if ok {
		c, cok = a.constIntValue(n.Right)
	} else if name, ok = immutableIntIdentName(a, scope, n.Right); ok {
		c, cok = a.constIntValue(n.Left)
		op = flipComparison(op)
	}
	if !ok || !cok {
		return
	}
	var fact numRange
	switch op {
	case lexer.TOKEN_GT: // a > c  ⇒ a >= c+1
		fact = numRange{loKnown: true, lo: c + 1}
	case lexer.TOKEN_GTEQ: // a >= c
		fact = numRange{loKnown: true, lo: c}
	case lexer.TOKEN_LT: // a < c  ⇒ a <= c-1
		fact = numRange{hiKnown: true, hi: c - 1}
	case lexer.TOKEN_LTEQ: // a <= c
		fact = numRange{hiKnown: true, hi: c}
	case lexer.TOKEN_EQEQ: // a == c
		fact = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}
	default:
		return
	}
	if scope.rangeFacts == nil {
		scope.rangeFacts = map[string]numRange{}
	}
	scope.rangeFacts[name] = scope.rangeFacts[name].intersect(fact)
}

// gatherLawIsRangeRefinement narrows an immutable integer variable by a law inside the truthy branch
// of `if x is Law:` (docs/85). When the law body is a decidable conjunction of `self OP const`, its
// constraints become an integer range fact on x, so a later refinement obligation on x (another `x
// is OtherLaw`, an `x`-initialized refinement binding, or passing x to a refinement param)
// discharges statically. Handles both bare laws (`is Positive`) and parametric laws with
// compile-time-constant args (`is Bounded[0, 500]`).
func (a *Analyzer) gatherLawIsRangeRefinement(scope *Scope, n *ast.BinaryExpr, truthy bool) {
	if !truthy || scope == nil || n == nil {
		return
	}
	name, ok := immutableIntIdentName(a, scope, n.Left)
	if !ok {
		return
	}
	targets := flattenIsTargetExprs(n.Right)
	if len(targets) != 1 {
		return
	}
	lawName, lawArgs, ok := a.resolveLawIsTarget(targets[0])
	if !ok {
		return
	}
	decl, _, ok := a.lookupLaw(lawName)
	if !ok || decl == nil || len(lawArgs) != len(decl.Params)-1 {
		return
	}
	// Bind the law's static params (decl.Params[1:]) to the constant bracket args, so a body like
	// `self >= lo and self <= hi` is interpreted against the actual bounds.
	paramConsts := map[string]int64{}
	for i, arg := range lawArgs {
		c, ok := a.constIntValue(arg)
		if !ok {
			return // a non-constant arg is not statically interpretable
		}
		paramConsts[decl.Params[i+1].Name] = c
	}
	constraints, ok := a.lawConstraints(decl, paramConsts)
	if !ok || len(constraints) == 0 {
		return
	}
	fact := numRange{}
	for _, k := range constraints {
		fact = fact.intersect(constraintToRange(k))
	}
	if scope.rangeFacts == nil {
		scope.rangeFacts = map[string]numRange{}
	}
	scope.rangeFacts[name] = scope.rangeFacts[name].intersect(fact)
}

// constraintToRange converts one decidable `self OP const` law constraint into the integer range it
// implies.
func constraintToRange(k lawConstraint) numRange {
	switch k.op {
	case lexer.TOKEN_GTEQ:
		return numRange{loKnown: true, lo: k.c}
	case lexer.TOKEN_GT:
		return numRange{loKnown: true, lo: k.c + 1}
	case lexer.TOKEN_LTEQ:
		return numRange{hiKnown: true, hi: k.c}
	case lexer.TOKEN_LT:
		return numRange{hiKnown: true, hi: k.c - 1}
	case lexer.TOKEN_EQEQ:
		return numRange{loKnown: true, lo: k.c, hiKnown: true, hi: k.c}
	default:
		return numRange{}
	}
}

// immutableIntIdentName returns the name of `expr` when it is a bare identifier bound to an
// IMMUTABLE integer-typed variable (so a branch-condition fact about it cannot be invalidated by a
// later mutation). Mutable bindings return false — their facts would be unsound to carry.
func immutableIntIdentName(a *Analyzer, scope *Scope, expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return "", false
	}
	sym, ok := scope.Lookup(ident.Name)
	if !ok || sym == nil || sym.Mutable {
		return "", false
	}
	if !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
		return "", false
	}
	return ident.Name, true
}

// constIntValue extracts a compile-time integer constant from an expression.
func (a *Analyzer) constIntValue(expr ast.Expr) (int64, bool) {
	cv, ok := a.evalConstExpr(expr)
	if !ok || cv.Kind != ConstInt {
		return 0, false
	}
	return cv.Int, true
}

func flipComparison(op lexer.TokenKind) lexer.TokenKind {
	switch op {
	case lexer.TOKEN_GT:
		return lexer.TOKEN_LT
	case lexer.TOKEN_GTEQ:
		return lexer.TOKEN_LTEQ
	case lexer.TOKEN_LT:
		return lexer.TOKEN_GT
	case lexer.TOKEN_LTEQ:
		return lexer.TOKEN_GTEQ
	default:
		return op
	}
}

// lookupRangeFact walks the current scope chain for a known integer range about `name`.
func (a *Analyzer) lookupRangeFact(name string) (numRange, bool) {
	acc := numRange{}
	found := false
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.rangeFacts != nil {
			if r, ok := scope.rangeFacts[name]; ok {
				acc = acc.intersect(r)
				found = true
			}
		}
	}
	return acc, found
}

// invalidateRangeFacts drops the known integer range fact about `name` across the active scope chain.
// Called at every mutation site for `name`, mirroring invalidatePredFacts. Unlike predFacts there is
// NO dependent-fact cascade: a range fact is a concrete interval snapshot (even one seeded from another
// variable's range captured that variable's bound at seed time), so it has no live symbolic dependence
// on other variables — only a write to the SUBJECT itself can stale its interval (docs/90 brick 90-11).
func (a *Analyzer) invalidateRangeFacts(name string) {
	if name == "" {
		return
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.rangeFacts != nil {
			delete(scope.rangeFacts, name)
		}
	}
}

// invalidateRangeFactsForTarget drops the range fact about the root variable of a mutation target
// expression (an identifier, or a field/index path rooted at one), mirroring invalidatePredFactsForTarget.
func (a *Analyzer) invalidateRangeFactsForTarget(target ast.Expr) {
	if name, ok := rootIdentName(target); ok {
		a.invalidateRangeFacts(name)
	}
}

// writtenConstInt returns the exact integer value of a variable when a live written-constant fact
// pins it to a compile-time integer (an immutable local or a `<- const` write). The bridge between
// the written-const tracker and the interval prover.
func (a *Analyzer) writtenConstInt(name string) (int64, bool) {
	v, ok := a.lookupWrittenConst(name)
	if !ok || v == nil {
		return 0, false
	}
	return a.constIntValue(v)
}

// lawConstraint is one `self OP const` clause of a law body in the decidable fragment.
type lawConstraint struct {
	op lexer.TokenKind
	c  int64
}

// lawConstraints interprets a law body as a conjunction of `self OP const` constraints, or returns
// false if the body is outside the decidable fragment (then the flow prover declines and discharge
// falls back to a runtime check). `self` is the law's first parameter name; `paramConsts` binds the
// law's remaining (static `[..]`) params to the refinement's bracket-arg constants, so a body like
// `self >= lo and self <= hi` is interpreted against the actual bounds.
func (a *Analyzer) lawConstraints(decl *ast.FuncDecl, paramConsts map[string]int64) ([]lawConstraint, bool) {
	return a.lawConstraintsRanged(decl, paramConsts, nil)
}

// lawConstraintsRanged is lawConstraints with an extra `paramRanges` channel (docs/90 brick 90-9):
// a static law param bound to a known INTERVAL (rather than an exact constant) is resolved
// direction-aware — its lower bound for a `self >= param` constraint, its upper bound for a
// `self <= param` constraint — which is the only sound way to use a non-constant bracket argument
// (e.g. `cap_to(k)` with `k ∈ [0, 10]` yields `result <= 10`). paramConsts is consulted first;
// paramRanges (nil for the exact-constant callers) is the fallback.
func (a *Analyzer) lawConstraintsRanged(decl *ast.FuncDecl, paramConsts map[string]int64, paramRanges map[string]numRange) ([]lawConstraint, bool) {
	if decl == nil || len(decl.Params) == 0 || len(decl.Body) != 1 {
		return nil, false
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok || ret == nil || ret.Value == nil {
		return nil, false
	}
	self := decl.Params[0].Name
	var out []lawConstraint
	if !a.collectLawConstraints(ret.Value, self, paramConsts, paramRanges, &out) {
		return nil, false
	}
	return out, true
}

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
	case lexer.TOKEN_EQEQ: // self == c
		return r.loKnown && r.hiKnown && r.lo == k.c && r.hi == k.c
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
	}
	return nil, false
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
	fact := numRange{}
	any := false
	for _, pred := range rt.Preds {
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
	call, ok := value.(*ast.CallExpr)
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
	if rt, ok := decl.ReturnType.(*ast.RefinementTypeExpr); ok && rt != nil {
		if r, found := a.rangeFromRefinementTypeExpr(rt, subst); found {
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

// seedEnsuresParamRangeFacts records, at a call site, the integer interval implied by a callee
// postcondition `ensures <param> is Law` onto the (mutable) caller variable bound to that argument
// (docs/90 brick 90-11). It complements the predicate-fact gain in the same call loop: where the
// predFact lets a later `x is Law` discharge by factset identity, this lets the flow/interval prover
// use `x`'s numeric bound directly (as a bracket argument, an array index, a comparison operand).
//
// Caller is responsible for restricting this to MUTABLE-REF params, where the postcondition genuinely
// constrains the caller's variable (for a by-value or immutable-ref param the postcondition is about
// the callee's local copy and says nothing about the caller's binding). Sound and conservative:
//   - Applied AFTER the call-site mutable-ref invalidation, and `x`'s range fact is dropped again at the
//     next mutation of `x` (invalidateRangeFactsForTarget at every assignment + every ref-arg call), so
//     the snapshot interval can never outlive a write — the same envelope that makes the predFact gain
//     sound, extended to the interval store.
//   - The interval is a concrete snapshot (the callee guaranteed `x ∈ [..]` at return) with no live
//     dependence on other variables, so no dependent-fact cascade is needed.
//   - `ensures` law args are validated compile-time constants (resolveRefinementEnsures), so no
//     caller-substitution is required; a non-decidable law simply seeds nothing.
//   - Like all refinement value facts it only NARROWS (intersect) and never drives bounds-check elision,
//     so even a buggy callee contract is garbage-in-garbage-out, never memory unsafety.
func (a *Analyzer) seedEnsuresParamRangeFacts(arg ast.Expr, lawName string, lawArgs []ast.Expr) {
	if a.currentScope == nil {
		return
	}
	name, ok := rootIdentName(arg)
	if !ok {
		return
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil || !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
		return
	}
	fact, found := a.rangeFromLawApplication(lawName, lawArgs, nil)
	if !found {
		return
	}
	if a.currentScope.rangeFacts == nil {
		a.currentScope.rangeFacts = map[string]numRange{}
	}
	a.currentScope.rangeFacts[name] = a.currentScope.rangeFacts[name].intersect(fact)
}

// rangeFromEnsureResult computes the integer interval that a callee's value-contract postconditions
// place on its returned value (docs/90 brick 90-10). Each `ensure` clause is an independent boolean;
// this collects every `result OP operand` comparison appearing in conjunction position across all
// clauses (a clause or sub-term outside the fragment is simply skipped — unlike a law body, partial
// information is sound here because the clauses are independently true). The operand is resolved with
// the same caller-substitution + direction-aware bounding used for refinement bracket args:
// a constant after substitution, or a known interval (`>=`/`>` uses its lower bound, `<=`/`<` its
// upper). `result` is the subject keyword bound in analyzeEnsureClauses.
func (a *Analyzer) rangeFromEnsureResult(clauses []ast.Expr, subst map[string]ast.Expr) (numRange, bool) {
	var constraints []lawConstraint
	for _, clause := range clauses {
		if clause == nil {
			continue
		}
		a.collectResultConstraints(clause, subst, &constraints)
	}
	if len(constraints) == 0 {
		return numRange{}, false
	}
	fact := numRange{}
	for _, k := range constraints {
		fact = fact.intersect(constraintToRange(k))
	}
	return fact, true
}

// collectResultConstraints gathers `result OP operand` comparisons from an `ensure` clause, recursing
// through parentheses and `and`. Unlike collectLawConstraints it never fails: a comparison it cannot
// resolve (operand not constant/ranged, or not about `result`) is skipped, since each conjunct that
// IS resolvable is an independent sound fact about the result.
func (a *Analyzer) collectResultConstraints(expr ast.Expr, subst map[string]ast.Expr, out *[]lawConstraint) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.collectResultConstraints(n.Inner, subst, out)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			a.collectResultConstraints(n.Left, subst, out)
			a.collectResultConstraints(n.Right, subst, out)
			return
		}
		// Normalize to `result OP operand`.
		var operand ast.Expr
		var op lexer.TokenKind
		switch {
		case isSelfIdent(n.Left, "result"):
			operand, op = n.Right, n.Op
		case isSelfIdent(n.Right, "result"):
			operand, op = n.Left, flipComparison(n.Op)
		default:
			return
		}
		switch op {
		case lexer.TOKEN_GT, lexer.TOKEN_GTEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_EQEQ:
		default:
			return // `!=` gives no interval bound; skip
		}
		if c, ok := a.substConstInt(operand, subst); ok {
			*out = append(*out, lawConstraint{op: op, c: c})
			return
		}
		// Ranged operand (`ensure result <= n` with n ∈ [.., hi]): direction-aware, like bracket args.
		if r, ok := a.substArgRange(operand, subst); ok {
			switch op {
			case lexer.TOKEN_GTEQ, lexer.TOKEN_GT:
				if r.loKnown {
					*out = append(*out, lawConstraint{op: op, c: r.lo})
				}
			case lexer.TOKEN_LTEQ, lexer.TOKEN_LT:
				if r.hiKnown {
					*out = append(*out, lawConstraint{op: op, c: r.hi})
				}
			}
		}
	}
}

// --- tier-2: bounded linear arithmetic (docs/86) ---------------------------------------------
//
// The tier-1 flow prover (tryProveRefinementByFlow) discharges an obligation only when the subject
// is a BARE immutable integer identifier with a range fact. Tier-2 generalizes the subject to an
// affine form `c0 + sum(ci*xi)` over immutable integer variables, bounds it by interval arithmetic
// over the same range facts, and checks the result entails the law's `self OP const` constraints. It
// is the only tier that can prove a DERIVED index such as `tx*MAPHEIGHT + ty is Bounded[0..<4096]`
// (docs/85 §3 tier 2). The law side is reused verbatim (lawConstraints); only the subject is richer.

// affineForm is c0 + sum over terms of (coeff * variable), all integer. An empty terms map with a
// nonzero const is a literal; a single {x:1} term is a bare variable. Coefficients are exact int64.
type affineForm struct {
	c     int64
	terms map[string]int64
}

func (f affineForm) addTerm(name string, coeff int64) {
	if coeff == 0 {
		return
	}
	f.terms[name] += coeff
	if f.terms[name] == 0 {
		delete(f.terms, name)
	}
}

// affineOf builds the affine form of an integer expression, or returns ok=false when the expression
// leaves the linear-arithmetic fragment (non-linear product, unknown leaf, value-changing cast).
// Only IMMUTABLE integer identifiers are admitted as variables, so a mutable binding can never enter
// a form — the dependence-freeze (docs/85 §5.3) holds for free, same gate tier-1 uses.
func (a *Analyzer) affineOf(expr ast.Expr, scope *Scope) (affineForm, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.affineOf(n.Inner, scope)
	case *ast.IntLit:
		if c, ok := a.constIntValue(n); ok {
			return affineForm{c: c, terms: map[string]int64{}}, true
		}
		return affineForm{}, false
	case *ast.Ident:
		// A const-evaluable identifier (e.g. a module const like MAPHEIGHT) folds to its value.
		if c, ok := a.constIntValue(n); ok {
			return affineForm{c: c, terms: map[string]int64{}}, true
		}
		if name, ok := immutableIntIdentName(a, scope, n); ok {
			return affineForm{c: 0, terms: map[string]int64{name: 1}}, true
		}
		return affineForm{}, false
	case *ast.UnaryExpr:
		if n.Op != lexer.TOKEN_MINUS {
			return affineForm{}, false
		}
		inner, ok := a.affineOf(n.Operand, scope)
		if !ok {
			return affineForm{}, false
		}
		out := affineForm{c: -inner.c, terms: map[string]int64{}}
		for k, v := range inner.terms {
			out.terms[k] = -v
		}
		return out, true
	case *ast.BinaryExpr:
		return a.affineOfBinary(n, scope)
	case *ast.CastExpr:
		// A numeric-to-integer cast is value-preserving only when the target type can represent the
		// subject's whole proven range; a narrowing cast wraps and would make the bound unsound.
		inner, ok := a.affineOf(n.Operand, scope)
		if !ok {
			return affineForm{}, false
		}
		target := a.resolveType(n.Target)
		if _, _, isInt := BitIntInfo(target); !isInt {
			return affineForm{}, false
		}
		r := a.boundAffine(inner, scope)
		if !r.loKnown || !r.hiKnown {
			return affineForm{}, false // unbounded subject: cannot prove the cast is value-preserving
		}
		if !IntegerTypeFitsValue(target, r.lo) || !IntegerTypeFitsValue(target, r.hi) {
			return affineForm{}, false // narrowing/wrapping cast: bound would be unsound
		}
		return inner, true
	default:
		return affineForm{}, false
	}
}

func (a *Analyzer) affineOfBinary(n *ast.BinaryExpr, scope *Scope) (affineForm, bool) {
	switch n.Op {
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS:
		l, ok := a.affineOf(n.Left, scope)
		if !ok {
			return affineForm{}, false
		}
		r, ok := a.affineOf(n.Right, scope)
		if !ok {
			return affineForm{}, false
		}
		sign := int64(1)
		if n.Op == lexer.TOKEN_MINUS {
			sign = -1
		}
		out := affineForm{c: l.c + sign*r.c, terms: map[string]int64{}}
		for k, v := range l.terms {
			out.addTerm(k, v)
		}
		for k, v := range r.terms {
			out.addTerm(k, sign*v)
		}
		return out, true
	case lexer.TOKEN_STAR:
		// Linear only when at least one side is a compile-time constant.
		if c, ok := a.constIntValue(n.Right); ok {
			return a.scaleAffine(n.Left, c, scope)
		}
		if c, ok := a.constIntValue(n.Left); ok {
			return a.scaleAffine(n.Right, c, scope)
		}
		return affineForm{}, false // variable*variable: non-linear, decline (sound)
	default:
		return affineForm{}, false
	}
}

func (a *Analyzer) scaleAffine(expr ast.Expr, k int64, scope *Scope) (affineForm, bool) {
	inner, ok := a.affineOf(expr, scope)
	if !ok {
		return affineForm{}, false
	}
	out := affineForm{c: inner.c * k, terms: map[string]int64{}}
	for name, v := range inner.terms {
		out.addTerm(name, v*k)
	}
	return out, true
}

// boundAffine interval-evaluates an affine form by substituting each variable's known range. A
// nonnegative coefficient keeps the bound orientation; a negative one swaps lo/hi. A variable with
// no range fact (or an open bound on the needed side) makes that side of the result open, so a later
// entailment on that side fails and the prover declines (fail-closed, docs/85 §9.2).
func (a *Analyzer) boundAffine(f affineForm, scope *Scope) numRange {
	out := numRange{loKnown: true, lo: f.c, hiKnown: true, hi: f.c}
	for name, coeff := range f.terms {
		r, ok := a.lookupRangeFact(name)
		if !ok {
			// No branch-derived range, but a live written-constant fact (e.g. an immutable local
			// `k: i32 = 5`) pins the variable to an exact value — a tight point range. Sound: the
			// written-const fact is a proven exact value, invalidated on any mutation.
			if c, known := a.writtenConstInt(name); known {
				r, ok = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}, true
			}
		}
		if !ok {
			return numRange{} // unknown variable: fully open, declines
		}
		var lo, hi int64
		var loK, hiK bool
		if coeff >= 0 {
			lo, loK = r.lo, r.loKnown
			hi, hiK = r.hi, r.hiKnown
		} else {
			lo, loK = r.hi, r.hiKnown
			hi, hiK = r.lo, r.loKnown
		}
		if out.loKnown && loK {
			out.lo += coeff * lo
		} else {
			out.loKnown = false
		}
		if out.hiKnown && hiK {
			out.hi += coeff * hi
		} else {
			out.hiKnown = false
		}
	}
	return out
}

// tryProveRefinementByLinear discharges `value is law[args]` when `value` is an affine form over
// immutable integer variables whose bounded range entails every law constraint. Reuses lawConstraints
// (the law side is unchanged) and rangeEntailsConstraint (the entailment check). Sound: any leaf
// outside the fragment, or any open bound on a needed side, makes it decline to a runtime check.
func (a *Analyzer) tryProveRefinementByLinear(value ast.Expr, decl *ast.FuncDecl, predArgs []ast.Expr, scope *Scope) bool {
	// A bare identifier is already tier-1's job; tier-2 only earns its keep on derived forms.
	if _, isIdent := value.(*ast.Ident); isIdent {
		return false
	}
	if decl == nil || len(predArgs) != len(decl.Params)-1 {
		return false
	}
	form, ok := a.affineOf(value, scope)
	if !ok {
		return false
	}
	// A constant-only form (no variable terms) is const-eval's job, not tier-2's — declining keeps
	// the discharge recorded as proven (const) and tier-2 scoped to genuinely derived subjects.
	if len(form.terms) == 0 {
		return false
	}
	paramConsts := map[string]int64{}
	for i, arg := range predArgs {
		c, ok := a.constIntValue(arg)
		if !ok {
			return false
		}
		paramConsts[decl.Params[i+1].Name] = c
	}
	constraints, ok := a.lawConstraints(decl, paramConsts)
	if !ok || len(constraints) == 0 {
		return false
	}
	r := a.boundAffine(form, scope)
	for _, k := range constraints {
		if !rangeEntailsConstraint(r, k) {
			return false
		}
	}
	return true
}

// tryProveRefinementByFlow attempts a flow-sensitive static proof of `value is law`: when `value`
// is a bare immutable integer identifier with a known range fact, and the law body is a decidable
// conjunction of `self OP const` constraints, it checks the range entails every constraint. Returns
// true only on a sound proof (docs/85 1d-2).
func (a *Analyzer) tryProveRefinementByFlow(value ast.Expr, decl *ast.FuncDecl, predArgs []ast.Expr) bool {
	ident, ok := value.(*ast.Ident)
	if !ok || ident == nil {
		return false
	}
	r, ok := a.lookupRangeFact(ident.Name)
	if !ok {
		return false
	}
	// Bind the law's static params (decl.Params[1:]) to the refinement's bracket-arg constants.
	if decl == nil || len(predArgs) != len(decl.Params)-1 {
		return false
	}
	paramConsts := map[string]int64{}
	for i, arg := range predArgs {
		c, ok := a.constIntValue(arg)
		if !ok {
			return false // a non-constant bracket arg is not statically interpretable
		}
		paramConsts[decl.Params[i+1].Name] = c
	}
	constraints, ok := a.lawConstraints(decl, paramConsts)
	if !ok || len(constraints) == 0 {
		return false
	}
	for _, k := range constraints {
		if !rangeEntailsConstraint(r, k) {
			return false
		}
	}
	return true
}
