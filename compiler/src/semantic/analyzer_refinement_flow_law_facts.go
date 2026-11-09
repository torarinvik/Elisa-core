package semantic

import (
	"fmt"
	"maps"
	"slices"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

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

func (a *Analyzer) tryProveEnsureByReturnCallRange(clause ast.Expr, call *ast.CallExpr) bool {
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok || decl == nil || len(decl.EnsureValues) == 0 {
		return false
	}
	args := proofCallArgs(call)
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	resultRange, ok := a.rangeFromEnsureResult(decl.EnsureValues, subst)
	if !ok {
		return false
	}
	var constraints []lawConstraint
	a.collectResultConstraints(clause, nil, &constraints)
	if len(constraints) == 0 {
		return false
	}
	for _, constraint := range constraints {
		if !rangeEntailsConstraint(resultRange, constraint) {
			return false
		}
	}
	return true
}

func (a *Analyzer) proofCallExpr(expr ast.Expr) (*ast.CallExpr, bool) {
	expr = stripOptimizationParens(expr)
	switch n := expr.(type) {
	case *ast.CallExpr:
		return n, n != nil
	case *ast.StructLitExpr:
		if a != nil && a.loweredInitCalls != nil {
			if call := a.loweredInitCalls[n]; call != nil {
				return call, true
			}
		}
	}
	return nil, false
}

func proofCallArgs(call *ast.CallExpr) []ast.Expr {
	if call == nil {
		return nil
	}
	if call.ResolvedArgsValid && call.ResolvedCommonArgs == nil {
		return call.ResolvedArgs
	}
	return call.Args
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
		if field, ok := stripOptimizationParens(n.Operand).(*ast.FieldExpr); ok {
			if key, keyOK := affineSViewLengthCastKey(a, scope, n, field); keyOK {
				return affineForm{terms: map[string]int64{key: 1}}, true
			}
		}
		target := a.resolveType(n.Target)
		if _, _, isInt := smtIntWidthSign(target); !isInt {
			return affineForm{}, false
		}
		r := a.boundAffine(inner, scope)
		if !r.loKnown || !r.hiKnown {
			return affineForm{}, false // unbounded subject: cannot prove the cast is value-preserving
		}
		if !affineIntegerRangeFitsType(target, r.lo, r.hi) {
			return affineForm{}, false // narrowing/wrapping cast: bound would be unsound
		}
		return inner, true
	case *ast.FieldExpr:
		// An sview's length is immutable even when the bytes it views are not. Keep
		// that projection as a symbolic affine variable so recursive measures such
		// as `text.len - index` can cancel the unchanged length at a recursive call.
		if key, ok := immutableSViewLengthProjection(a, scope, n); ok {
			return affineForm{terms: map[string]int64{key: 1}}, true
		}
		// `v.size` where `v` is a local known to hold a struct literal with a constant `size` field.
		base, ok := n.Object.(*ast.Ident)
		if !ok || base == nil {
			return affineForm{}, false
		}
		// `xs.count` where `xs` is an immutable darray local initialised from a list literal.
		if n.Field == "count" {
			if cnt, ok2 := a.lookupWrittenListCount(base.Name); ok2 {
				return affineForm{c: cnt, terms: map[string]int64{}}, true
			}
		}
		fieldVal, ok2 := a.lookupWrittenStructField(base.Name, n.Field)
		if !ok2 {
			// Fall back to the written-field channel: a direct `s.f <- <const>` assignment on a straight-line
			// path records a compile-time constant in writtenField keyed by smtProjectionName(s.f). Consult
			// that fact here so `s.f <- 5; check(s.f)` proves `requires x >= 5` from the written constant.
			// Sound: writtenField is invalidated on every write to s.f, to s, or on a mutable-ref escape of s
			// (see recordWrittenFieldForTarget / invalidateWrittenFieldsForRoot).
			key := smtProjectionName(n)
			if isPureProjectionKey(key) {
				if wfVal, ok3 := a.lookupWrittenField(key); ok3 && wfVal != nil {
					fieldVal, ok2 = wfVal, true
				}
			}
		}
		if !ok2 {
			return affineForm{}, false
		}
		return a.affineOf(fieldVal, scope)
	default:
		return affineForm{}, false
	}
}

func affineIntegerRangeFitsType(target Type, low, high int64) bool {
	signed, bits, ok := smtIntWidthSign(target)
	if !ok || bits <= 0 || bits > 64 || low > high {
		return false
	}
	if signed {
		if bits == 64 {
			return true
		}
		limit := int64(1) << (bits - 1)
		return low >= -limit && high < limit
	}
	if low < 0 {
		return false
	}
	if bits == 64 {
		return true // affine bounds are int64, so every nonnegative endpoint fits u64
	}
	return uint64(high) < (uint64(1) << bits)
}

// A cast of an unchanged immutable sview length is a stable affine atom even
// when its numeric range is not known. Entry and recursive-call casts are equal
// because the view binding is unchanged; no range or no-wrap assumption is made.
func affineSViewLengthCastKey(a *Analyzer, scope *Scope, cast *ast.CastExpr, field *ast.FieldExpr) (string, bool) {
	if a == nil || cast == nil || field == nil {
		return "", false
	}
	projection, ok := immutableSViewLengthProjection(a, scope, field)
	if !ok {
		return "", false
	}
	target := a.resolveType(cast.Target)
	if _, _, ok := smtIntWidthSign(target); !ok {
		return "", false
	}
	return "\x00sview-len-cast:" + projection + ":" + target.String(), true
}

// immutableSViewLengthProjection identifies the stable length of an immutable sview binding.
// Other containers are deliberately excluded: a darray's count can change through an alias or
// a call, while an sview's length metadata is fixed for the lifetime of the view.
func immutableSViewLengthProjection(a *Analyzer, scope *Scope, expr ast.Expr) (string, bool) {
	field, ok := expr.(*ast.FieldExpr)
	if !ok || field == nil || field.Field != "len" {
		return "", false
	}
	base, ok := field.Object.(*ast.Ident)
	if !ok || base == nil || scope == nil {
		return "", false
	}
	sym, ok := scope.Lookup(base.Name)
	if !ok || sym == nil || sym.Mutable {
		return "", false
	}
	typ := sym.Type
	if ref, ok := typ.(*RefType); ok {
		// A mutable reference can replace the view binding while a recursive
		// call is in progress, so its length is not a stable affine variable.
		if ref == nil || ref.Mutable || ref.State != RefStateNonNull {
			return "", false
		}
		typ = ref.Elem
	}
	if _, ok := typ.(*SViewType); !ok {
		return "", false
	}
	if sym.Node == nil {
		return "", false
	}
	// Scope lookup can resolve two same-named sviews in different lexical scopes. Key
	// the projection by the binding declaration, not just its spelling, so shadowing
	// cannot equate unrelated lengths in affine or SMT proofs.
	pos := sym.Node.Pos()
	return fmt.Sprintf("\x00sview-len:%s:%d:%d:%s:%d", pos.File, pos.Line, pos.Offset, sym.Kind, sym.ParamIndex), true
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
	for _, name := range slices.Sorted(maps.Keys(f.terms)) { // map: sort — checked summation order decides where an overflow opens a bound
		coeff := f.terms[name]
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
			if sym, found := a.currentScope.Lookup(name); found && sym != nil && smtTypeNonNegative(sym.Type) {
				r, ok = numRange{loKnown: true, lo: 0}, true
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
		// SOUNDNESS: do the interval arithmetic with int64-OVERFLOW detection. A wide product such as a
		// u32 `a*3000000000` with a in [0, 4e9] computes `3e9 * 4e9 = 1.2e19`, which overflows int64 and
		// wraps to a small/negative value — making the interval look in-range so provablyNoArithWrap
		// wrongly skips the wrap model and proves a false `result >= a`. On any overflow the bound becomes
		// unknown (open), which only declines a proof, never admits an unsound one (audit cluster E).
		if out.loKnown && loK {
			if p, ok := mulInt64Checked(coeff, lo); ok {
				if s, ok := addInt64Checked(out.lo, p); ok {
					out.lo = s
				} else {
					out.loKnown = false
				}
			} else {
				out.loKnown = false
			}
		} else {
			out.loKnown = false
		}
		if out.hiKnown && hiK {
			if p, ok := mulInt64Checked(coeff, hi); ok {
				if s, ok := addInt64Checked(out.hi, p); ok {
					out.hi = s
				} else {
					out.hiKnown = false
				}
			} else {
				out.hiKnown = false
			}
		} else {
			out.hiKnown = false
		}
	}
	return out
}

// mulInt64Checked multiplies with overflow detection (ok=false on overflow).
// Special-cases: minInt64 * -1 overflows (result would be maxInt64+1) but the division
// check p/b==a wraps back to minInt64 in two's complement and gives a false "ok" result,
// so we guard that explicitly.
func mulInt64Checked(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	const minI64 = -int64(^uint64(0)>>1) - 1
	// minInt64 * -1 would be maxInt64+1 which does not fit in int64.
	if (a == minI64 && b == -1) || (b == minI64 && a == -1) {
		return 0, false
	}
	p := a * b
	if p/b != a {
		return 0, false
	}
	return p, true
}

// addInt64Checked adds with overflow detection (ok=false on overflow).
func addInt64Checked(a, b int64) (int64, bool) {
	s := a + b
	if (s > a) != (b > 0) {
		return 0, false
	}
	return s, true
}
