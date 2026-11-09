package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// shiftRange shifts a known integer range [lo, hi] by a constant c, producing [lo+c, hi+c].
// A bound is preserved only when the addition does not overflow int64; an overflowing bound is
// dropped (open), so the result is always sound — never wider than the true shifted interval.
// Used by tryProveRefinementByFlow to handle additive-shift subjects `x + c`.
func shiftRange(r numRange, c int64) (numRange, bool) {
	out := numRange{}
	anyKnown := false
	if r.loKnown {
		if s, ok := addInt64Checked(r.lo, c); ok {
			out.loKnown, out.lo = true, s
			anyKnown = true
		}
	}
	if r.hiKnown {
		if s, ok := addInt64Checked(r.hi, c); ok {
			out.hiKnown, out.hi = true, s
			anyKnown = true
		}
	}
	return out, anyKnown
}

// scaleRangePositive scales a known integer range [lo, hi] (with lo >= 0) by a positive constant k > 0,
// producing [lo*k, hi*k]. Guard: lo must be >= 0 (monotonic scaling only applies to non-negative ranges),
// and k must be > 0. Either bound that would overflow int64 is dropped (open), keeping the result sound.
// Used by tryProveRefinementByFlow to handle scaling subjects `x * k` when x >= 0.
func scaleRangePositive(r numRange, k int64) (numRange, bool) {
	if k <= 0 {
		return numRange{}, false
	}
	if !r.loKnown || r.lo < 0 {
		return numRange{}, false // monotonic scaling requires a known non-negative lower bound
	}
	out := numRange{}
	anyKnown := false
	if lo, ok := mulInt64Checked(r.lo, k); ok {
		out.loKnown, out.lo = true, lo
		anyKnown = true
	}
	if r.hiKnown {
		if hi, ok := mulInt64Checked(r.hi, k); ok {
			out.hiKnown, out.hi = true, hi
			anyKnown = true
		}
	}
	return out, anyKnown
}

// scaleRangeNegative scales a known integer range [lo, hi] by a negative constant k < 0,
// producing [hi*k, lo*k]. Multiplication by a negative number reverses order: the new lower
// bound is the old hi scaled by k, and the new upper bound is the old lo scaled by k.
// Either bound that would overflow int64 is dropped (open), keeping the result sound (never
// asserts a tighter bound than the true interval). k must be < 0.
// Used by tryDeriveShiftOrScaleRange to handle scaling subjects `x * k` when k < 0.
func scaleRangeNegative(r numRange, k int64) (numRange, bool) {
	if k >= 0 {
		return numRange{}, false
	}
	out := numRange{}
	anyKnown := false
	// new lo = hi * k  (hi is the largest; multiplied by negative k gives the smallest product)
	if r.hiKnown {
		if lo, ok := mulInt64Checked(r.hi, k); ok {
			out.loKnown, out.lo = true, lo
			anyKnown = true
		}
	}
	// new hi = lo * k  (lo is the smallest; multiplied by negative k gives the largest product)
	if r.loKnown {
		if hi, ok := mulInt64Checked(r.lo, k); ok {
			out.hiKnown, out.hi = true, hi
			anyKnown = true
		}
	}
	return out, anyKnown
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
//
// Strengthened fallbacks (sound, narrowly scoped):
//  1. Written-constant fact: if no rangeFact exists but the variable is pinned to an exact compile-time
//     constant by a live written-const fact (an immutable local assigned a literal), we construct the
//     degenerate interval [c,c] and check against it. This is the equality-fact → comparison case:
//     a variable known to be exactly 5 satisfies any comparison or interval containing 5.
//  2. Declared-type non-negativity: if no rangeFact or written-const is available, but the variable's
//     declared type is unsigned (e.g. u8, u16, u32, u64, usize), we seed a [0, typeMax] range from
//     the declared type — which already provides non-negativity. This is sound because unsigned types
//     cannot hold negative values; the type system enforces the lower bound at construction time.
//  3. Additive shift: if `value` is `x + c` (or `x - c`) for an immutable integer `x` with a known
//     range [lo, hi] and constant c, the shifted range [lo+c, hi+c] is derived (shiftRange) and
//     checked against the law constraints. Overflow-safe: either bound that would overflow int64 is
//     dropped (open), and the check then fails (sound). Integers only.
//  4. Monotonic scaling: if `value` is `x * k` for an immutable integer `x` with known range
//     [lo, hi] where lo >= 0, and constant k > 0, the scaled range [lo*k, hi*k] is derived
//     (scaleRangePositive) and checked. Overflow-safe: overflowing bounds become open. Integers only.
//     4a. Negating scale: if `value` is `x * k` with k < 0, the range flips to [hi*k, lo*k]
//     (scaleRangeNegative). Overflow-safe: overflowing bounds become open. Integers only.
//     4b. Unary negation: if `value` is `-x`, treated as x * -1 via scaleRangeNegative, yielding
//     range [-hi, -lo]. Overflow-safe: overflowing bounds become open. Integers only.
func (a *Analyzer) tryProveRefinementByFlow(value ast.Expr, decl *ast.FuncDecl, predArgs []ast.Expr) bool {
	ident, ok := value.(*ast.Ident)
	if !ok || ident == nil {
		// Field-path case: `s.n` where a guard established a range fact keyed on "s.n".
		if fe, isFE := value.(*ast.FieldExpr); isFE && fe != nil && a.currentScope != nil {
			if pathKey, pkOk := fieldPathKey(a, a.currentScope, fe); pkOk {
				if r, rOk := a.lookupRangeFact(pathKey); rOk {
					// Also intersect with the declared type bounds (unsigned non-negativity, etc.),
					// mirroring the bare-ident path below.
					if ft := a.exprType(value); ft != nil {
						if tr, trOk := declaredTypeRange(ft); trOk {
							r = r.intersect(tr)
						}
					}
					return a.proveConstraintsFromRange(r, decl, predArgs)
				}
			}
		}
		// Cases 3 & 4: additive shift or monotonic scaling of an immutable integer variable.
		if r, derived := a.tryDeriveShiftOrScaleRange(value); derived {
			return a.proveConstraintsFromRange(r, decl, predArgs)
		}
		// Case 5a: ternary/if-expression — join the branch ranges and check the union.
		if tern, isTern := value.(*ast.TernaryExpr); isTern {
			if r, derived := a.tryDeriveTernaryRange(tern); derived {
				return a.proveConstraintsFromRange(r, decl, predArgs)
			}
		}
		// Case 5b: a match expression in value position — the result range is the JOIN of arm ranges.
		// Only attempted here for *ast.MatchExpr to preserve the existing const-eval priority for
		// literal and other constant expressions (which are handled by the const-eval tier after this
		// function returns false). Fail-closed: if any arm is outside the decidable fragment,
		// exprResultRange returns ok=false and we decline.
		if _, isMatch := value.(*ast.MatchExpr); isMatch {
			if r, ranged := a.exprResultRange(value, nil); ranged {
				return a.proveConstraintsFromRange(r, decl, predArgs)
			}
		}
		return false
	}
	r, ok := a.lookupRangeFact(ident.Name)
	if !ok {
		// Fallback 1: written-constant fact → degenerate [c,c] range.
		if c, known := a.writtenConstInt(ident.Name); known {
			r = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}
			ok = true
		}
	}
	// Declared-type bounds (e.g. unsigned non-negativity) always apply and only tighten the range,
	// so merge them in whether or not a range fact already exists. A guard/assert like `x <= 103`
	// records hi=103 but leaves the lower bound open; for an unsigned `x` the declared type supplies
	// lo=0, which the law's `self >= 0` conjunct needs. Without this merge, `if x <= 103: return x`
	// fails to discharge `is InRange[0, 103]` because non-negativity was only consulted as a fallback
	// for a missing range fact (Fallback 2), never combined with a present one.
	if sym, found := a.currentScope.Lookup(ident.Name); found && sym != nil {
		if tr, found := declaredTypeRange(sym.Type); found {
			if ok {
				r = r.intersect(tr)
			} else {
				r = tr
				ok = true
			}
		}
	}
	if !ok {
		// Fallback 3 — in-body monotonic counter bound: for a MUTABLE integer identifier, harvest
		// constant-comparison facts from live smtAssertFacts. When the analyzer enters
		// `while i < n:` (or any branch), it seeds the raw condition as an smtAssertFact in the
		// body scope, and drops that fact whenever `i` is mutated
		// (invalidateSMTAssertFactsForTarget). Reading those facts here is therefore as sound as
		// reading rangeFacts for an immutable identifier: the invalidation guarantee is the same.
		// This lets the tier-1 flow prover handle simple laws like `i is Nat` inside
		// `while i >= 0:`, or `i is Bounded[0..=9]` inside `while i < 10:`, without SMT.
		if sym, found := a.currentScope.Lookup(ident.Name); found && sym != nil &&
			sym.Mutable && IsNumericType(sym.Type) && !IsFloatType(sym.Type) {
			if mr, mok := a.rangeFromSMTAssertFacts(ident.Name); mok {
				if tr, found := declaredTypeRange(sym.Type); found {
					mr = mr.intersect(tr)
				}
				return a.proveConstraintsFromRange(mr, decl, predArgs)
			}
		}
		return false
	}
	return a.proveConstraintsFromRange(r, decl, predArgs)
}

// rangeFromSMTAssertFacts scans the live smtAssertFacts in the current scope chain for
// constant-comparison facts about a named variable and returns their conjunction as a numRange.
// Used by tryProveRefinementByFlow Fallback 3 to derive a provable range for MUTABLE integer
// identifiers that are excluded from the immutable-only rangeFacts map. Soundness holds because
// smtAssertFacts is invalidated at every mutation of the variable — the same guarantee that
// makes rangeFacts sound for immutables (docs/85 §5.3 monotonic counter bound).
// Only constant-RHS (or constant-LHS) comparisons contribute; variable-bound facts are skipped.
func (a *Analyzer) rangeFromSMTAssertFacts(name string) (numRange, bool) {
	r := numRange{}
	found := false
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, fact := range sc.smtAssertFacts {
			bin, ok := stripOptimizationParens(fact.Expr).(*ast.BinaryExpr)
			if !ok || bin == nil {
				continue
			}
			op := bin.Op
			var c int64
			var cok bool
			leftIsName := isIdentNamed(bin.Left, name)
			if leftIsName {
				// name OP const
				c, cok = a.constIntValue(bin.Right)
			} else if isIdentNamed(bin.Right, name) {
				// const OP name — flip operator to normalize to name flipOp const
				c, cok = a.constIntValue(bin.Left)
				op = flipComparison(op)
			} else {
				continue
			}
			if !cok {
				continue
			}
			var fact numRange
			switch op {
			case lexer.TOKEN_GT:
				fact = numRange{loKnown: true, lo: c + 1}
			case lexer.TOKEN_GTEQ:
				fact = numRange{loKnown: true, lo: c}
			case lexer.TOKEN_LT:
				fact = numRange{hiKnown: true, hi: c - 1}
			case lexer.TOKEN_LTEQ:
				fact = numRange{hiKnown: true, hi: c}
			case lexer.TOKEN_EQEQ:
				fact = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}
			default:
				continue
			}
			r = r.intersect(fact)
			found = true
		}
	}
	return r, found
}

// proveConstraintsFromRange checks whether the given known range entails all law constraints.
// Shared by tryProveRefinementByFlow (bare identifier and derived-range cases).
func (a *Analyzer) proveConstraintsFromRange(r numRange, decl *ast.FuncDecl, predArgs []ast.Expr) bool {
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

// tryProveRefinementByRelational discharges `subject is law[args]` when one or more bracket args are
// non-constant immutable-integer identifiers whose relationship to the subject is directly stated by
// a `requires` clause of the enclosing function (docs/85 dependent-arg extension).
//
// Example: `raw is InRange[0, cap]` with law `self >= 0 and self < cap` proves when:
//   - `raw` is an immutable integer param of unsigned type (giving self >= 0 via declared-type range), AND
//   - `requires raw < cap` is a precondition of the enclosing function (giving self < cap relationally).
//
// Sound design: BOTH the subject and every non-const bracket arg must be immutable integer
// identifiers (sym.Mutable == false). Immutable params cannot be reassigned, so the `requires` fact
// holds throughout the entire body — no mutation-invalidation tracking is needed. Any mutable
// subject or mutable bracket arg causes an immediate decline. A constant bracket arg is resolved
// through the normal paramConsts path; only non-const bracket args use the relational channel.
func (a *Analyzer) tryProveRefinementByRelational(value ast.Expr, decl *ast.FuncDecl, predArgs []ast.Expr) bool {
	if a == nil || decl == nil || a.currentFuncDecl == nil || a.currentScope == nil {
		return false
	}
	if len(predArgs) != len(decl.Params)-1 {
		return false
	}
	// Subject must be an immutable integer identifier.
	subjectName, subjectOk := immutableIntIdentName(a, a.currentScope, value)
	if !subjectOk || subjectName == "" {
		return false
	}
	// Partition bracket args into constants and relational (immutable-ident) entries.
	paramConsts := map[string]int64{}
	paramRelNames := map[string]string{} // law-param-name → caller-side variable name
	anyRelational := false
	for i, arg := range predArgs {
		lawParamName := decl.Params[i+1].Name
		if c, cok := a.constIntValue(arg); cok {
			paramConsts[lawParamName] = c
			continue
		}
		argName, argOk := immutableIntIdentName(a, a.currentScope, arg)
		if !argOk || argName == "" {
			return false // outside the relational fragment — decline soundly
		}
		paramRelNames[lawParamName] = argName
		anyRelational = true
	}
	if !anyRelational {
		// All args were constant: the existing proveConstraintsFromRange handles this.
		return false
	}
	// Build the subject's range from live range facts and declared-type bounds (for channel A).
	// We use smtIntWidthSign (rather than BitIntInfo/declaredTypeRange) so that wide unsigned types
	// such as usize/uintptr (64-bit, excluded by BitIntInfo's width>=63 guard) also contribute their
	// non-negativity lower bound.
	subjectRange := numRange{}
	subjectRangeOk := false
	if r, found := a.lookupRangeFact(subjectName); found {
		subjectRange = r
		subjectRangeOk = true
	}
	if sym, found := a.currentScope.Lookup(subjectName); found && sym != nil {
		// First, try declaredTypeRange for exact-width types (u8/u16/u32, i8/…).
		if tr, found := declaredTypeRange(sym.Type); found {
			if subjectRangeOk {
				subjectRange = subjectRange.intersect(tr)
			} else {
				subjectRange = tr
				subjectRangeOk = true
			}
		} else if signed, _, ok := smtIntWidthSign(sym.Type); ok && !signed {
			// Wide unsigned (usize/uintptr/u64): declaredTypeRange declined (width>=63), but we
			// can still supply the non-negativity lower bound lo=0 soundly. The upper bound is
			// left open (unknown) — we only assert what we know for certain.
			unsignedNonneg := numRange{loKnown: true, lo: 0}
			if subjectRangeOk {
				subjectRange = subjectRange.intersect(unsignedNonneg)
			} else {
				subjectRange = unsignedNonneg
				subjectRangeOk = true
			}
		}
	}
	// Walk the law body and prove each atom via either:
	//   A) constant operand (paramConsts or literal) → range entailment on subjectRange
	//   B) relational operand (paramRelNames → callerVarName) → live requires clause
	return a.proveRelationalLawBody(decl, subjectName, paramConsts, paramRelNames, subjectRange, subjectRangeOk)
}

// proveRelationalLawBody extracts the single-return law body and walks it as a conjunction.
func (a *Analyzer) proveRelationalLawBody(decl *ast.FuncDecl, subjectName string, paramConsts map[string]int64, paramRelNames map[string]string, subjectRange numRange, subjectRangeOk bool) bool {
	if decl == nil || len(decl.Params) == 0 || len(decl.Body) != 1 {
		return false
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok || ret == nil || ret.Value == nil {
		return false
	}
	self := decl.Params[0].Name
	return a.proveRelationalLawExpr(ret.Value, self, subjectName, paramConsts, paramRelNames, subjectRange, subjectRangeOk)
}

// proveRelationalLawExpr proves a single law body expression using the mixed const+relational
// strategy. Returns false (decline) if any atom is undecidable or unprovable.
func (a *Analyzer) proveRelationalLawExpr(expr ast.Expr, self string, subjectName string, paramConsts map[string]int64, paramRelNames map[string]string, subjectRange numRange, subjectRangeOk bool) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.proveRelationalLawExpr(n.Inner, self, subjectName, paramConsts, paramRelNames, subjectRange, subjectRangeOk)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			return a.proveRelationalLawExpr(n.Left, self, subjectName, paramConsts, paramRelNames, subjectRange, subjectRangeOk) &&
				a.proveRelationalLawExpr(n.Right, self, subjectName, paramConsts, paramRelNames, subjectRange, subjectRangeOk)
		}
		// Normalize to `self OP operand`.
		var operand ast.Expr
		var op lexer.TokenKind
		switch {
		case isSelfIdent(n.Left, self):
			operand, op = n.Right, n.Op
		case isSelfIdent(n.Right, self):
			operand, op = n.Left, flipComparison(n.Op)
		default:
			return false // non-self atom — undecidable in this tier, decline
		}
		// Channel A: operand resolves to a constant (literal or paramConsts).
		if c, cok := a.operandConst(operand, paramConsts); cok {
			if !subjectRangeOk {
				return false
			}
			return rangeEntailsConstraint(subjectRange, lawConstraint{op: op, c: c})
		}
		// Channel B: operand is a law param bound to a caller-side variable via paramRelNames.
		opIdent, identOk := operand.(*ast.Ident)
		if !identOk || opIdent == nil {
			return false
		}
		callerVarName, relOk := paramRelNames[opIdent.Name]
		if !relOk {
			return false // operand is not a known law param — undecidable, decline
		}
		return a.requiresClauseProves(subjectName, op, callerVarName)
	default:
		return false
	}
}

// requiresClauseProves reports whether the enclosing function's `requires` clauses contain a
// precondition that entails `subjectName OP argName`. Both variables must be immutable integer
// identifiers (enforced by the caller); since they cannot be reassigned, the requires fact is valid
// throughout the entire body without mutation-invalidation tracking.
//
// Sound: we only conclude when the requires clause's normalized operator is at least as strong as
// the wanted operator (see relationalOpProves). We scan ALL requires clauses and return true on the
// first match; if none match, we decline (return false).
func (a *Analyzer) requiresClauseProves(subjectName string, op lexer.TokenKind, argName string) bool {
	if a == nil || a.currentFuncDecl == nil || subjectName == "" || argName == "" {
		return false
	}
	for _, req := range a.currentFuncDecl.Requires {
		if a.requiresClauseMatchesRelational(req, subjectName, op, argName) {
			return true
		}
	}
	return false
}

// requiresClauseMatchesRelational reports whether the clause `expr` normalizes to a fact
// `subjectName clauseOp argName` where clauseOp proves wantOp (see relationalOpProves).
// The normalization flips the operator when identifiers appear in reversed order.
func (a *Analyzer) requiresClauseMatchesRelational(expr ast.Expr, subjectName string, wantOp lexer.TokenKind, argName string) bool {
	bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr)
	if !ok || bin == nil {
		return false
	}
	var clauseOp lexer.TokenKind
	switch {
	case isIdentNamed(bin.Left, subjectName) && isIdentNamed(bin.Right, argName):
		clauseOp = bin.Op
	case isIdentNamed(bin.Right, subjectName) && isIdentNamed(bin.Left, argName):
		clauseOp = flipComparison(bin.Op)
	default:
		return false
	}
	return relationalOpProves(clauseOp, wantOp)
}

// relationalOpProves reports whether having a `requires` clause with operator `have` (in normalized
// `subject OP arg` form) is sufficient to prove a law constraint with operator `want`.
//
// Sound entailment rules:
//
//	have=LT  proves want=LT (exact) and want=LTEQ  (x<y ⇒ x≤y, integer semantics)
//	have=LTEQ proves want=LTEQ only
//	have=GT  proves want=GT and want=GTEQ
//	have=GTEQ proves want=GTEQ only
//	have=EQEQ proves want=EQEQ, LTEQ, GTEQ
//	anything else: decline
func relationalOpProves(have, want lexer.TokenKind) bool {
	if have == want {
		return true
	}
	switch have {
	case lexer.TOKEN_LT:
		return want == lexer.TOKEN_LTEQ
	case lexer.TOKEN_GT:
		return want == lexer.TOKEN_GTEQ
	case lexer.TOKEN_EQEQ:
		return want == lexer.TOKEN_LTEQ || want == lexer.TOKEN_GTEQ
	}
	return false
}
