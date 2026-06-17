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
	if decl == nil || len(decl.Params) == 0 || len(decl.Body) != 1 {
		return nil, false
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok || ret == nil || ret.Value == nil {
		return nil, false
	}
	self := decl.Params[0].Name
	var out []lawConstraint
	if !a.collectLawConstraints(ret.Value, self, paramConsts, &out) {
		return nil, false
	}
	return out, true
}

// collectLawConstraints walks a conjunction of `self OP <const>` comparisons, where the operand is
// a literal constant or a static param bound in paramConsts. Any other shape makes the whole body
// undecidable (returns false) so the prover stays sound by declining.
func (a *Analyzer) collectLawConstraints(expr ast.Expr, self string, paramConsts map[string]int64, out *[]lawConstraint) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.collectLawConstraints(n.Inner, self, paramConsts, out)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			return a.collectLawConstraints(n.Left, self, paramConsts, out) && a.collectLawConstraints(n.Right, self, paramConsts, out)
		}
		// `self OP <const>` (or the constant on the left).
		if isSelfIdent(n.Left, self) {
			if c, ok := a.operandConst(n.Right, paramConsts); ok {
				*out = append(*out, lawConstraint{op: n.Op, c: c})
				return true
			}
		}
		if isSelfIdent(n.Right, self) {
			if c, ok := a.operandConst(n.Left, paramConsts); ok {
				*out = append(*out, lawConstraint{op: flipComparison(n.Op), c: c})
				return true
			}
		}
		return false
	default:
		return false
	}
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
