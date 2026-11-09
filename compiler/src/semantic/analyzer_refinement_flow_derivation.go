package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// tryDeriveShiftOrScaleRange attempts to derive a numRange for a compound expression that is either:
//   - an additive shift `x + c` or `x - c` (case 3): range [lo+c, hi+c] derived via shiftRange.
//   - a monotonic scaling `x * k` with k > 0 and x >= 0 (case 4): range [lo*k, hi*k] via scaleRangePositive.
//   - a negating scale `x * k` with k < 0 (case 4a): range [hi*k, lo*k] via scaleRangeNegative.
//   - a unary negation `-x` (case 4b): range [-hi, -lo] (equivalent to x * -1 via scaleRangeNegative).
//
// `x` must be an immutable integer identifier with a known range fact (or written-const/declared-type
// fallback). Returns ok=false for anything outside these forms, or when overflow would occur on both
// bounds, keeping the result sound (abstain rather than approximate unsoundly).
func (a *Analyzer) tryDeriveShiftOrScaleRange(value ast.Expr) (numRange, bool) {
	stripped := stripOptimizationParens(value)

	// Case 4b: unary negation `-x` — treat as x * -1.
	if unary, ok := stripped.(*ast.UnaryExpr); ok && unary != nil && unary.Op == lexer.TOKEN_MINUS {
		name, xOk := a.immutableIntIdentNameFromScope(unary.Operand)
		if !xOk {
			return numRange{}, false
		}
		r, rOk := a.lookupRangeFact(name)
		if !rOk {
			if c, known := a.writtenConstInt(name); known {
				r, rOk = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}, true
			}
		}
		if !rOk {
			return numRange{}, false
		}
		return scaleRangeNegative(r, -1)
	}

	bin, ok := stripped.(*ast.BinaryExpr)
	if !ok || bin == nil {
		return numRange{}, false
	}
	switch bin.Op {
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS:
		// x + c  or  x - c: the constant may be on either side.
		name, xOk := a.immutableIntIdentNameFromScope(bin.Left)
		var shift int64
		if xOk {
			c, cok := a.constIntValue(bin.Right)
			if !cok {
				return numRange{}, false
			}
			if bin.Op == lexer.TOKEN_MINUS {
				c = -c
			}
			shift = c
		} else {
			name, xOk = a.immutableIntIdentNameFromScope(bin.Right)
			if !xOk || bin.Op == lexer.TOKEN_MINUS {
				// `c - x` is not a shift of x (it's a negation + shift); decline.
				return numRange{}, false
			}
			c, cok := a.constIntValue(bin.Left)
			if !cok {
				return numRange{}, false
			}
			shift = c
		}
		r, rOk := a.lookupRangeFact(name)
		if !rOk {
			if c, known := a.writtenConstInt(name); known {
				r, rOk = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}, true
			}
		}
		if !rOk {
			return numRange{}, false
		}
		return shiftRange(r, shift)
	case lexer.TOKEN_STAR:
		// x * k  or  k * x: one side must be a non-zero constant; the other an immutable int identifier.
		// For k > 0 and x >= 0: monotonic scaling via scaleRangePositive.
		// For k < 0: range-flipping via scaleRangeNegative (overflow-safe, always sound).
		name, xOk := a.immutableIntIdentNameFromScope(bin.Left)
		var k int64
		if xOk {
			c, cok := a.constIntValue(bin.Right)
			if !cok {
				return numRange{}, false
			}
			k = c
		} else {
			name, xOk = a.immutableIntIdentNameFromScope(bin.Right)
			if !xOk {
				return numRange{}, false
			}
			c, cok := a.constIntValue(bin.Left)
			if !cok {
				return numRange{}, false
			}
			k = c
		}
		if k == 0 {
			return numRange{}, false // zero scale collapses to a constant; not a range-form
		}
		r, rOk := a.lookupRangeFact(name)
		if !rOk {
			if c, known := a.writtenConstInt(name); known {
				r, rOk = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}, true
			}
		}
		if !rOk {
			return numRange{}, false
		}
		if k > 0 {
			return scaleRangePositive(r, k)
		}
		// k < 0: flip the range via scaleRangeNegative.
		return scaleRangeNegative(r, k)
	case lexer.TOKEN_PERCENT:
		// x % m  (unsigned dividend, positive constant modulus): range is [0, m-1].
		//
		// Soundness conditions (all must hold; any failure → decline, not unsound):
		//  1. Modulus `m` must be a compile-time positive constant (variable modulus: decline).
		//  2. The dividend must be unsigned (non-negative by declared type); signed remainder
		//     can be negative in C-style truncating semantics, so [0, m-1] would be unsound.
		//  3. m must be > 0 (zero modulus is UB; decline to avoid div-by-zero assumptions).
		//
		// Result: hi = m-1 (not m-2). `x % m` can equal m-1 when x ≡ m-1 (mod m), so
		// this proves `<= m-1` but NOT `< m-1` — the required soundness-negative distinction.
		m, mok := a.constIntValue(bin.Right)
		if !mok || m <= 0 {
			return numRange{}, false // non-const or non-positive modulus: decline (sound)
		}
		// Dividend must have a non-negative declared type (unsigned). We check the left
		// operand's type via the immutable-ident lookup (covers the common `x % m` form).
		// Complex sub-expressions that aren't bare identifiers are declined conservatively.
		name, xOk := a.immutableIntIdentNameFromScope(bin.Left)
		if !xOk {
			return numRange{}, false // non-identifier dividend: decline conservatively
		}
		sym, found := a.currentScope.Lookup(name)
		if !found || sym == nil || !smtTypeNonNegative(sym.Type) {
			return numRange{}, false // signed or unknown type: decline (sound)
		}
		return numRange{loKnown: true, lo: 0, hiKnown: true, hi: m - 1}, true
	default:
		return numRange{}, false
	}
}

// immutableIntIdentNameFromScope is a scope-aware wrapper for tryDeriveShiftOrScaleRange: looks up
// `expr` as an immutable integer identifier in the current scope chain.
func (a *Analyzer) immutableIntIdentNameFromScope(expr ast.Expr) (string, bool) {
	if a == nil || a.currentScope == nil {
		return "", false
	}
	return immutableIntIdentName(a, a.currentScope, expr)
}

// tryDeriveTernaryRange attempts to derive a numRange for a ternary expression `value if cond else alt`
// by computing the range of each branch in its condition-refined scope and joining them (union).
// This is the range-merge rule for conditional expressions: a value produced by a ternary is bounded
// by the union of the two branch bounds, which is sound because exactly one branch executes.
// Returns ok=false when no useful range can be derived from either branch.
func (a *Analyzer) tryDeriveTernaryRange(expr *ast.TernaryExpr) (numRange, bool) {
	if a == nil || a.currentScope == nil || expr == nil {
		return numRange{}, false
	}
	savedScope := a.currentScope

	// Compute range of the truthy branch (expr.Value) in the truthy refined scope.
	truthyScope := a.refinedScopeForCondition(savedScope, expr.Cond, true)
	a.currentScope = truthyScope
	leftRange, leftOk := a.rangeForBranchExpr(expr.Value)
	a.currentScope = savedScope

	// Compute range of the falsy branch (expr.Alt) in the falsy refined scope.
	falsyScope := a.refinedScopeForCondition(savedScope, expr.Cond, false)
	a.currentScope = falsyScope
	rightRange, rightOk := a.rangeForBranchExpr(expr.Alt)
	a.currentScope = savedScope

	if !leftOk && !rightOk {
		return numRange{}, false
	}
	if !leftOk {
		return rightRange, true
	}
	if !rightOk {
		return leftRange, true
	}
	return leftRange.join(rightRange), true
}

// rangeForBranchExpr attempts to derive a numRange for an expression appearing as a ternary branch.
// It supports:
//   - bare immutable integer identifiers (via lookupRangeFact + written-const + declared-type)
//   - additive shift / monotonic scaling (via tryDeriveShiftOrScaleRange)
//
// Called with a.currentScope already set to the condition-refined branch scope.
func (a *Analyzer) rangeForBranchExpr(expr ast.Expr) (numRange, bool) {
	if expr == nil {
		return numRange{}, false
	}
	ident, ok := expr.(*ast.Ident)
	if ok && ident != nil {
		r, found := a.lookupRangeFact(ident.Name)
		if !found {
			if c, known := a.writtenConstInt(ident.Name); known {
				r, found = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}, true
			}
		}
		if sym, symFound := a.currentScope.Lookup(ident.Name); symFound && sym != nil {
			if tr, typeFound := declaredTypeRange(sym.Type); typeFound {
				if found {
					r = r.intersect(tr)
				} else {
					r, found = tr, true
				}
			}
		}
		return r, found
	}
	// Additive shift / scaling.
	if r, derived := a.tryDeriveShiftOrScaleRange(expr); derived {
		return r, true
	}
	return numRange{}, false
}

// rangeFactWrite is one range-fact invalidation recorded while a loop body is analyzed.
type rangeFactWrite struct {
	name   string
	path   bool
	prefix bool
}

func (a *Analyzer) logRangeFactWrite(w rangeFactWrite) {
	if n := len(a.rangeFactWriteLogs); n != 0 {
		a.rangeFactWriteLogs[n-1] = append(a.rangeFactWriteLogs[n-1], w)
	}
}

// analyzeLoopBodyKeepingWrites runs a loop body analysis whose block helper restores the scope
// chain's range facts on exit (the condition-refined clone), then re-applies every range-fact
// invalidation the body made. The restore exists to drop the CONDITION's refinements, but it also
// resurrected entry facts of variables the body writes: `h: mutable u64 = 5; while i < n: h <- h + 1`
// left `h in [5, 5]` after the loop and proved `ensure result == 5` for a function returning 8.
// The body may run, so a fact it invalidates is unknown after the loop. Nested loops propagate
// their writes to the enclosing log.
func (a *Analyzer) analyzeLoopBodyKeepingWrites(analyze func() affineFlowSnapshot) affineFlowSnapshot {
	a.rangeFactWriteLogs = append(a.rangeFactWriteLogs, nil)
	snapshot := analyze()
	n := len(a.rangeFactWriteLogs)
	writes := a.rangeFactWriteLogs[n-1]
	a.rangeFactWriteLogs = a.rangeFactWriteLogs[:n-1]
	for _, w := range writes {
		switch {
		case w.prefix:
			a.invalidateRangeFactsWithRootPrefix(w.name)
		case w.path:
			a.invalidateRangeFactsForFieldPath(w.name)
		default:
			a.invalidateRangeFacts(w.name)
		}
	}
	return snapshot
}

// rangeIntInfo is BitIntInfo extended with the pointer-sized integer names (usize/uintptr are
// unsigned 64-bit, isize/int signed 64-bit on every supported target), so range facts carry
// through the `.usize()` conversions that index arithmetic is full of.
func rangeIntInfo(t Type) (signed bool, width int, ok bool) {
	if b, isBuiltin := t.(*BuiltinType); isBuiltin {
		switch b.Name {
		case "usize", "uintptr":
			return false, 64, true
		case "isize", "int":
			return true, 64, true
		}
	}
	return BitIntInfo(t)
}

// rangeIntFits reports whether value is representable in integer type t (per rangeIntInfo).
// Unlike IntegerTypeFitsValue it is fail-closed: an unrecognized type fits nothing.
func rangeIntFits(t Type, value int64) bool {
	signed, width, ok := rangeIntInfo(t)
	if !ok {
		return false
	}
	if signed {
		return width >= 64 || (value >= -(int64(1)<<(width-1)) && value <= (int64(1)<<(width-1))-1)
	}
	return value >= 0 && (width >= 64 || value <= (int64(1)<<width)-1)
}
