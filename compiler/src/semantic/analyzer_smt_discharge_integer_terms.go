package semantic

import (
	"math/big"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// bitwiseTerm models a fixed-width bitwise/shift operator (`&`, `|`, `^`, `<<`, `>>`) by bridging the
// unbounded-Int operand terms into SMT bitvectors at the result's machine width, applying the matching
// bitvector operator, and reading the result back as an unsigned integer. `(_ int2bv W)` reduces each
// operand modulo 2^W — exactly the machine bit pattern (two's complement for any sign) — and `bv2nat`
// reads the result in [0, 2^W), exactly the unsigned machine value. The produced Int term therefore
// EQUALS the machine result, so it composes soundly with further Int reasoning (a mask `x & 0xFFF` is
// provably `< 0x1000`, a shift `x >> 12` provably `< 2^(W-12)`, etc.).
//
// Soundness gates — anything outside them declines (returns ok=false), which only forgoes a proof,
// never fabricates one:
//   - Result width 1..64. An unsigned result reads back via `bv2nat`; a signed result reads back via
//     the exact two's-complement value (smtBitvectorRead), and a signed `>>` uses `bvashr` (arithmetic)
//     to match the LLVM AShr codegen — so sign-extension idioms `(v << k) >> k` prove their signed range.
//   - Operands must share the result width (Elisa bitwise is same-type), so int2bv at W is the exact
//     bit pattern with no narrowing/sign surprise.
//   - Shift amounts must be a compile-time constant in [0, W): the machine leaves shifts ≥ width
//     undefined (LLVM poison), so assuming the bitvector "shift past width ⇒ 0" rule would be unsound.
//
// Bridging the Int and BV theories can be costly for the solver on relational goals; the per-query
// timeout turns any hard case into Unknown → runtime fallback, so soundness holds regardless of cost.
func (tr *smtTranslator) bitwiseTerm(n *ast.BinaryExpr, env map[string]string) (string, bool) {
	signed, bits, ok := smtIntWidthSign(tr.a.exprType(n))
	if !ok || bits <= 0 || bits > 64 {
		return "", false
	}
	width := strconv.Itoa(bits)
	// An operand is bit-faithful at the result width when it is a same-width integer (Elisa bitwise is
	// same-type) or a compile-time constant (`int2bv` reduces it modulo 2^W exactly).
	sameWidthOrConst := func(e ast.Expr) bool {
		if _, ebits, ok := smtIntWidthSign(tr.a.exprType(e)); ok && ebits == bits {
			return true
		}
		_, isConst := tr.a.constIntValue(e)
		return isConst
	}
	if !sameWidthOrConst(n.Left) {
		return "", false
	}
	var bvop, rbv string
	switch n.Op {
	case lexer.TOKEN_AMPERSAND:
		bvop = "bvand"
	case lexer.TOKEN_PIPE:
		bvop = "bvor"
	case lexer.TOKEN_CARET:
		bvop = "bvxor"
	case lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
		c, isConst := tr.a.constIntValue(n.Right)
		if !isConst || c < 0 || c >= int64(bits) {
			return "", false
		}
		rbv = "((_ int2bv " + width + ") " + strconv.FormatInt(c, 10) + ")"
		if n.Op == lexer.TOKEN_LSHIFT {
			bvop = "bvshl"
		} else if signed {
			bvop = "bvashr" // signed result ⇒ arithmetic right shift (matches LLVM AShr codegen)
		} else {
			bvop = "bvlshr" // unsigned result ⇒ logical right shift
		}
	default:
		return "", false
	}
	l, ok := tr.termEnv(n.Left, env)
	if !ok {
		// Operand-independent mask bound (see maskBoundAux): `X & C` ∈ [0, C] for a non-negative
		// constant C, regardless of X — so a masked extraction proves even when X (e.g.
		// `word >> runtime_shift`) is outside the modelable bitvector fragment.
		if n.Op == lexer.TOKEN_AMPERSAND {
			if c, isC := tr.a.constIntValue(n.Right); isC && c >= 0 {
				return tr.maskBoundAux(c), true
			}
		}
		// Operand-independent shift bound (see shiftRightBoundAux): `X >> k` (unsigned, constant k)
		// ∈ [0, 2^(bits-k)-1] regardless of X — so a goal like `unknownExpr >> 4 < 2^(bits-4)` proves
		// on the pure Int side even when X is outside the bitvector fragment.
		if n.Op == lexer.TOKEN_RSHIFT && !signed {
			if c, isC := tr.a.constIntValue(n.Right); isC && c >= 0 && c < int64(bits) {
				return tr.shiftRightBoundAux(bits, int(c)), true
			}
		}
		return "", false
	}
	lbv := "((_ int2bv " + width + ") " + l + ")"
	if rbv == "" {
		// Binary bitwise: the right operand must also be a same-width integer (or a constant).
		if !sameWidthOrConst(n.Right) {
			return "", false
		}
		r, ok := tr.termEnv(n.Right, env)
		if !ok {
			// Symmetric mask bound: `C & X` ∈ [0, C] for a non-negative constant C, regardless of X.
			if n.Op == lexer.TOKEN_AMPERSAND {
				if c, isC := tr.a.constIntValue(n.Left); isC && c >= 0 {
					return tr.maskBoundAux(c), true
				}
			}
			return "", false
		}
		rbv = "((_ int2bv " + width + ") " + r + ")"
		if bvop == "bvand" && !signed {
			result := smtBitvectorRead("("+bvop+" "+lbv+" "+rbv+")", signed, bits)
			tr.auxDecls = append(tr.auxDecls, smtAndBoundLemma(result, l, r, bits))
			return result, true
		}
	}
	return smtBitvectorRead("("+bvop+" "+lbv+" "+rbv+")", signed, bits), true
}

// smtAndBoundLemma states the AND bound for an unsigned `a & b` read back as `result`:
// AND only clears bits, so the result is at most each operand. int2bv reduces its argument
// modulo 2^W, so the bound speaks about the operands' INTEGER terms only when both already lie
// in [0, 2^W); the guard is part of the lemma, so it is vacuous for any operand term outside
// that range (a negative or oversized term, whose bit pattern is its wrapped value).
// Without it z3 cannot carry `bv2nat(bvand …)` back to integer bounds and spends its whole
// budget on goals like `hash & (capacity - 1) < capacity`.
func smtAndBoundLemma(result, a, b string, bits int) string {
	full := smtPow2(bits)
	// One guarded bound per operand: `result <= t` needs only t itself in range (the other operand
	// may be anything — AND with any bit pattern clears bits of t's pattern, which IS t). So a
	// `hash & mask` with an unbounded hash still gets `result <= mask`.
	bound := func(t string) string {
		return "(=> (and (<= 0 " + t + ") (< " + t + " " + full + ")) (<= " + result + " " + t + "))"
	}
	return "(assert (and " + bound(a) + " " + bound(b) + "))" + smtAbstractionOnlyMarker + "\n"
}

// signedDivCannotOverflow reports whether signed `left / right` (or `%`) provably avoids the one
// overflowing case INT_MIN / -1. Safe when the divisor cannot be -1 (a constant other than -1, or
// provably non-negative) OR the dividend cannot be INT_MIN (provably non-negative, or a known lower
// bound above the type minimum). An unsigned operand has no -1 and never overflows. When neither can be
// shown, the caller abstracts the result so the exact (overflowing) model is never trusted.
func (tr *smtTranslator) signedDivCannotOverflow(left, right ast.Expr) bool {
	signed, bits, ok := smtIntWidthSign(tr.a.exprType(left))
	if ok && !signed {
		return true // unsigned: no -1 divisor, cannot overflow
	}
	if c, isConst := tr.a.constIntValue(right); isConst && c != -1 {
		return true // divisor is a constant other than -1
	}
	if tr.a.provablyNonNeg(right) {
		return true // divisor >= 0 (and nonzero is already required) ⇒ != -1
	}
	if tr.a.provablyNonNeg(left) {
		return true // dividend >= 0 ⇒ != INT_MIN
	}
	if ok && signed && bits > 0 && bits <= 64 {
		minVal := -(int64(1) << (bits - 1))
		if f, aok := tr.a.affineOf(left, tr.a.currentScope); aok {
			if rng := tr.a.boundAffine(f, tr.a.currentScope); rng.loKnown && rng.lo > minVal {
				return true // dividend's lower bound is above the type minimum ⇒ != INT_MIN
			}
		}
	}
	return false
}

// provablyNoArithWrap reports whether `n` (an int `+`/`-`/`*`) cannot wrap — its true result is within
// the representable range of its width. It recognizes the no-underflow subtraction shapes (unsigned)
// and, for any width/signedness, an affine result whose interval lies inside [min, max]. Type-level
// reasoning alone is NOT sufficient (a wrapped result is still type-valid), so this is a VALUE check.
func (a *Analyzer) provablyNoArithWrap(n *ast.BinaryExpr, signed bool, bits int) bool {
	// No-underflow subtraction (e.g. `X - X%m`) is exact at any width and is the shape alignment math
	// relies on; it does not depend on the int64 interval prover, so it is safe for 64-bit too.
	if n.Op == lexer.TOKEN_MINUS && !signed && a.provablyNoUnsignedUnderflow(n.Left, n.Right) {
		return true
	}
	// A `requires`-supplied UPPER bound on this exact addition (`requires value + alignment <= 2^W-1`)
	// proves the unsigned sum cannot wrap — the affine interval prover cannot represent the 2^64-1 bound
	// (it computes in int64), so it is recognized symbolically here. This lets an overflow-guarded
	// postcondition (e.g. an AlignUp `ensure result >= value`) discharge from its entry precondition:
	// without the clean term the inner `+` would be emitted as `(mod … 2^W)`, which z3 cannot relate to
	// the surrounding div/mod/* goals. SOUNDNESS: this never makes the conclusion unconditional — it
	// requires an actual `requires` clause bounding the sum below the type maximum, so dropping the guard
	// (the C1 soundness gate) restores the exact-wrap model and the genuine overflow counterexample.
	if n.Op == lexer.TOKEN_PLUS && !signed && a.knownRequiresSumNoWrap(n, bits) {
		return true
	}
	// The affine interval prover now computes in OVERFLOW-CHECKED int64 (boundAffine via
	// mulInt64Checked/addInt64Checked, audit cluster E): a computation that would overflow int64 yields
	// an UNKNOWN (open) bound rather than a wrong one, so the in-range gate below is sound up to 64-bit
	// SIGNED — any int64-representable interval lies within [i64min, i64max]. UNSIGNED 64-bit stays on the
	// exact wrap path: its upper bound 2^64-1 is not int64-representable.
	if bits > 64 || (!signed && bits >= 64) {
		return false
	}
	f, ok := a.affineOf(n, a.currentScope)
	if !ok {
		return false
	}
	r := a.boundAffine(f, a.currentScope)
	if !r.loKnown || !r.hiKnown {
		return false
	}
	var lo, hi int64
	if signed {
		lo, hi = -(int64(1) << (bits - 1)), (int64(1)<<(bits-1))-1
	} else {
		lo, hi = 0, (int64(1)<<bits)-1
	}
	return r.lo >= lo && r.hi <= hi
}

// provablyNoUnsignedUnderflow reports whether `left - right` cannot underflow, i.e. `right <= left`
// holds for all admissible values. It recognizes the canonical alignment shape `X - (X % m)` (a
// remainder is always ≤ its nonnegative dividend) and, more generally, an affine difference whose
// interval lower bound is ≥ 0. Type-level non-negativity is NOT sufficient (an underflowed unsigned
// result is still nonnegative), so this is deliberately a VALUE-level check.
func (a *Analyzer) provablyNoUnsignedUnderflow(left, right ast.Expr) bool {
	l := stripOptimizationParens(left)
	if rem, ok := stripOptimizationParens(right).(*ast.BinaryExpr); ok && rem.Op == lexer.TOKEN_PERCENT {
		// `X - (X % m)`: the remainder of a non-negative dividend is ≤ the dividend.
		if a.provablyNonNeg(rem.Left) && exprsSyntacticallyEqual(l, stripOptimizationParens(rem.Left)) {
			return true
		}
	}
	diff := &ast.BinaryExpr{Position: left.Pos(), Left: left, Op: lexer.TOKEN_MINUS, Right: right}
	if f, ok := a.affineOf(diff, a.currentScope); ok {
		if r := a.boundAffine(f, a.currentScope); r.loKnown && r.lo >= 0 {
			return true
		}
	}
	// A `requires`-supplied relational bound (`requires b <= a`, `requires a >= b`, …) establishes
	// `left >= right` symbolically, which the constant-interval prover above cannot see (it relates two
	// variables, not a variable to a constant). Recognizing it here emits the CLEAN `left - right` term
	// — which the solver discharges trivially — instead of the wrapped `(mod … 2^W)` form that z3 stalls
	// on. This is what lets the natural precondition pattern prove `ensure result <= a; return a - b`.
	if a.knownRequiresGE(left, right) {
		return true
	}
	return false
}

// knownRequiresGE reports whether the enclosing function's `requires` clauses imply `left >= right`.
// Only IMMUTABLE integer identifiers qualify: a `requires` constrains a parameter's ENTRY value, so it
// stays valid for a later subtraction only if neither operand has been reassigned since entry. A clause
// is read as a conjunction; any conjunct of the form `left >= right`, `left > right`, `right <= left`,
// or `right < left` suffices (each implies `left - right` cannot underflow for unsigned operands).
func (a *Analyzer) knownRequiresGE(left, right ast.Expr) bool {
	if a == nil || a.currentFuncDecl == nil || a.currentScope == nil {
		return false
	}
	ln, lok := immutableIntIdentName(a, a.currentScope, stripOptimizationParens(left))
	rn, rok := immutableIntIdentName(a, a.currentScope, stripOptimizationParens(right))
	if !lok || !rok {
		return false
	}
	for _, req := range a.currentFuncDecl.Requires {
		if requiresConjunctImpliesGE(req, ln, rn) {
			return true
		}
	}
	return false
}

// knownRequiresSumNoWrap reports whether the enclosing function's `requires` clauses prove the unsigned
// addition `n` (`a + b`) cannot wrap at `bits` width — i.e. some conjunct `a + b <= C` (or `C >= a + b`)
// with a constant `C <= 2^bits - 1` and `n`'s SAME operands. Only IMMUTABLE-rooted addends qualify: a
// `requires` constrains entry values, so it stays valid only if neither operand was reassigned since
// entry. The bound is read structurally (the requires LHS sum must be syntactically the same two
// addends as `n`, order-insensitive), so this can only conclude no-wrap for a sum the precondition
// actually guards — making the conclusion conditional, never unconditional.
func (a *Analyzer) knownRequiresSumNoWrap(n *ast.BinaryExpr, bits int) bool {
	if a == nil || a.currentFuncDecl == nil || a.currentScope == nil || bits <= 0 || bits > 64 {
		return false
	}
	// Both addends must be immutable since entry for the requires fact to remain valid here.
	if _, ok := immutableIntIdentName(a, a.currentScope, stripOptimizationParens(n.Left)); !ok {
		return false
	}
	if _, ok := immutableIntIdentName(a, a.currentScope, stripOptimizationParens(n.Right)); !ok {
		return false
	}
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bits)), big.NewInt(1))
	for _, req := range a.currentFuncDecl.Requires {
		if a.requiresConjunctBoundsSum(req, n, max) {
			return true
		}
	}
	return false
}

// requiresConjunctBoundsSum reports whether a `requires` conjunct establishes `sum <= max` where `sum`
// is the same two-addend addition as `n` (`a + b`, order-insensitive). Accepts `sum <= C` and `C >= sum`.
func (a *Analyzer) requiresConjunctBoundsSum(e ast.Expr, n *ast.BinaryExpr, max *big.Int) bool {
	bin, ok := stripOptimizationParens(e).(*ast.BinaryExpr)
	if !ok {
		return false
	}
	if bin.Op == lexer.TOKEN_AND {
		return a.requiresConjunctBoundsSum(bin.Left, n, max) || a.requiresConjunctBoundsSum(bin.Right, n, max)
	}
	var sumSide, constSide ast.Expr
	switch bin.Op {
	case lexer.TOKEN_LTEQ: // sum <= C
		sumSide, constSide = bin.Left, bin.Right
	case lexer.TOKEN_GTEQ: // C >= sum
		sumSide, constSide = bin.Right, bin.Left
	default:
		return false
	}
	cBig, ok := a.unsignedConstBound(constSide)
	if !ok {
		return false
	}
	// cBig <= max (= 2^bits-1) proves the user-supplied sum bound forbids the wrap.
	if cBig.Cmp(max) > 0 {
		return false
	}
	sum, ok := stripOptimizationParens(sumSide).(*ast.BinaryExpr)
	if !ok || sum.Op != lexer.TOKEN_PLUS {
		return false
	}
	return additionsSameOperands(sum, n)
}

// unsignedConstBound returns the non-negative big.Int value of a constant `+`-bound expression. It
// handles a u64-max literal like 0xFFFFFFFFFFFFFFFF (which does not fit int64, so the const folder reads
// it as -1): the raw IntLit string is parsed directly as a big.Int. Other forms fall back to the int64
// const evaluator; a negative result is rejected (a sum upper bound below zero is meaningless here).
func (a *Analyzer) unsignedConstBound(e ast.Expr) (*big.Int, bool) {
	if lit, ok := stripOptimizationParens(e).(*ast.IntLit); ok && lit != nil {
		base := 10
		s := lit.Value
		if lit.IsHex {
			base = 16
			s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
		}
		s = strings.ReplaceAll(s, "_", "")
		if v, ok := new(big.Int).SetString(s, base); ok && v.Sign() >= 0 {
			return v, true
		}
	}
	if c, ok := a.constIntValue(e); ok && c >= 0 {
		return big.NewInt(c), true
	}
	return nil, false
}

// additionsSameOperands reports whether two `+` expressions add the same pair of lvalue operands
// (order-insensitive), using the path-stable SMT projection name for each addend.
func additionsSameOperands(x, y *ast.BinaryExpr) bool {
	xl, xr := smtProjectionName(stripOptimizationParens(x.Left)), smtProjectionName(stripOptimizationParens(x.Right))
	yl, yr := smtProjectionName(stripOptimizationParens(y.Left)), smtProjectionName(stripOptimizationParens(y.Right))
	if xl == "" || xr == "" || yl == "" || yr == "" {
		return false
	}
	return (xl == yl && xr == yr) || (xl == yr && xr == yl)
}

func requiresConjunctImpliesGE(e ast.Expr, geName, leName string) bool {
	bin, ok := stripOptimizationParens(e).(*ast.BinaryExpr)
	if !ok {
		return false
	}
	if bin.Op == lexer.TOKEN_AND {
		return requiresConjunctImpliesGE(bin.Left, geName, leName) || requiresConjunctImpliesGE(bin.Right, geName, leName)
	}
	li, lok := stripOptimizationParens(bin.Left).(*ast.Ident)
	ri, rok := stripOptimizationParens(bin.Right).(*ast.Ident)
	if !lok || !rok || li == nil || ri == nil {
		return false
	}
	switch bin.Op {
	case lexer.TOKEN_GTEQ, lexer.TOKEN_GT: // geName >= leName  /  geName > leName
		return li.Name == geName && ri.Name == leName
	case lexer.TOKEN_LTEQ, lexer.TOKEN_LT: // leName <= geName  /  leName < geName
		return li.Name == leName && ri.Name == geName
	}
	return false
}

// exprsSyntacticallyEqual reports whether two expressions are the same lvalue path (ident or nested
// field access). It is intentionally conservative — only the shapes the underflow check needs — and
// uses the SMT projection name (already path-stable for idents/fields) for the comparison.
func exprsSyntacticallyEqual(x, y ast.Expr) bool {
	switch x.(type) {
	case *ast.Ident, *ast.FieldExpr, *ast.ParenExpr:
		switch y.(type) {
		case *ast.Ident, *ast.FieldExpr, *ast.ParenExpr:
			return smtProjectionName(x) == smtProjectionName(y)
		}
	}
	return false
}

// smtPow2 returns 2^bits as a decimal literal (bits in [1, 64]).
func smtPow2(bits int) string {
	return new(big.Int).Lsh(big.NewInt(1), uint(bits)).String()
}

// markIntVar records a free var's integer type so factPreamble can assert its true representable
// range — `[0, 2^width)` for unsigned, `[-2^(width-1), 2^(width-1))` for signed. These bounds are
// guaranteed by the type and therefore always sound; they make the wraparound model precise.
func (tr *smtTranslator) markIntVar(name string, t Type) {
	signed, bits, ok := smtIntWidthSign(t)
	if !ok || bits <= 0 || bits > 64 {
		// Width unknown (e.g. an abstract numeric) — fall back to the non-negativity flag alone.
		if smtTypeNonNegative(t) {
			tr.nonNegDecls[name] = true
		}
		return
	}
	if signed {
		tr.signedBits[name] = bits
	} else {
		tr.nonNegDecls[name] = true
		tr.unsignedBits[name] = bits
	}
}

// provablyNonNeg reports whether an expression is provably ≥ 0 — by an unsigned type, or by the
// interval prover's lower bound. Used to gate sound division/modulo translation.
func (a *Analyzer) provablyNonNeg(expr ast.Expr) bool {
	if t := a.exprType(expr); t != nil && indexTypeGuaranteedNonNegative(t) {
		return true
	}
	if f, ok := a.affineOf(expr, a.currentScope); ok {
		r := a.boundAffine(f, a.currentScope)
		if r.loKnown && r.lo >= 0 {
			return true
		}
	}
	return false
}

// provablyPositive reports whether an expression is provably ≥ 1 — a constant ≥ 1, or an interval
// lower bound ≥ 1. (An unsigned type alone only gives ≥ 0, so it does not qualify.)
func (a *Analyzer) provablyPositive(expr ast.Expr) bool {
	if c, ok := a.constIntValue(expr); ok {
		return c >= 1
	}
	if f, ok := a.affineOf(expr, a.currentScope); ok {
		r := a.boundAffine(f, a.currentScope)
		if r.loKnown && r.lo >= 1 {
			return true
		}
	}
	return false
}

// provablyNegative reports whether an expression is provably <= -1.
func (a *Analyzer) provablyNegative(expr ast.Expr) bool {
	if c, ok := a.constIntValue(expr); ok {
		return c <= -1
	}
	if f, ok := a.affineOf(expr, a.currentScope); ok {
		r := a.boundAffine(f, a.currentScope)
		if r.hiKnown && r.hi <= -1 {
			return true
		}
	}
	return false
}

// provablyNonZero reports whether an expression is provably outside zero. This is the soundness gate
// for SMT division/modulo because SMT-LIB's arithmetic is total at zero but Elisa division is not.
func (a *Analyzer) provablyNonZero(expr ast.Expr) bool {
	return a.provablyPositive(expr) || a.provablyNegative(expr)
}
