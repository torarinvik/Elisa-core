package semantic

import (
	"math/bits"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Built-in modular-arithmetic refinement predicates (docs/85, modular tier). These behave exactly
// like user-declared `law`s — they are recognized by name, applied with `is`, and discharged through
// the same refinement-discharge ladder — but their bodies are SYNTHESIZED here (no user `law` decl)
// and lowered to plain integer/bitwise expressions the SMT bitvector lane already proves precisely:
//
//	Aligned[N]      — `(self % N) == 0`         value is a multiple of N
//	Divides[N]      — `(self % N) == 0`         N divides value (general alias of Aligned)
//	Fits[bits]      — `self < (1 << bits)`      value representable in `bits` bits
//	MaskedZero[mask]— `(self & mask) == 0`      the masked bits are clear
//
// The subject param is named `self`; the single static param is named `n`. Discharge substitutes the
// refinement's bracket argument for `n`, exactly like a user law's value parameter, so the same
// flow / fact-set / linear / SMT / const-eval tiers apply unchanged. SOUNDNESS: the bodies are
// ordinary executable predicates, so an unprovable obligation is reported (and refuted with a
// counterexample) just like any other refinement — nothing here can fabricate a proof.
func isBuiltinModularLawName(name string) bool {
	switch name {
	case "Aligned", "Divides", "Fits", "MaskedZero":
		return true
	}
	return false
}

// lookupBuiltinModularLaw returns (and memoizes) the synthetic law decl for a built-in modular
// predicate name, or nil if the name is not one. Memoization keeps a single stable *ast.FuncDecl per
// name so decl-keyed maps (proof caches, quantifier checks) behave like a real law decl.
func (a *Analyzer) lookupBuiltinModularLaw(name string) *ast.FuncDecl {
	if a == nil || !isBuiltinModularLawName(name) {
		return nil
	}
	if a.builtinModularLaws != nil {
		if d, ok := a.builtinModularLaws[name]; ok {
			return d
		}
	}
	decl := buildBuiltinModularLawDecl(name)
	if a.builtinModularLaws != nil {
		a.builtinModularLaws[name] = decl
	}
	return decl
}

// buildBuiltinModularLawDecl synthesizes the FuncDecl for one built-in modular predicate. The body is
// a single `return <bool-expr>` so lawBodyExpr extracts it; params are `(self: u64, n: u64)` (a u64
// subject is the widest integer subject, and refinement application erases to the base type so the
// actual subject width is taken from the use site by the SMT lane). The param name `n` is the static
// argument bound to the bracket value at discharge time.
func buildBuiltinModularLawDecl(name string) *ast.FuncDecl {
	pos := lexer.Pos{}
	self := func() ast.Expr { return &ast.Ident{Position: pos, Name: "self"} }
	n := func() ast.Expr { return &ast.Ident{Position: pos, Name: "n"} }
	intLit := func(v string) ast.Expr { return &ast.IntLit{Position: pos, Value: v} }
	paren := func(e ast.Expr) ast.Expr { return &ast.ParenExpr{Position: pos, Inner: e} }
	bin := func(op lexer.TokenKind, l, r ast.Expr) ast.Expr {
		return &ast.BinaryExpr{Position: pos, Op: op, Left: l, Right: r}
	}

	var body ast.Expr
	switch name {
	case "Aligned", "Divides":
		// (self % n) == 0
		body = bin(lexer.TOKEN_EQEQ, paren(bin(lexer.TOKEN_PERCENT, self(), n())), intLit("0"))
	case "MaskedZero":
		// (self & n) == 0
		body = bin(lexer.TOKEN_EQEQ, paren(bin(lexer.TOKEN_AMPERSAND, self(), n())), intLit("0"))
	case "Fits":
		// self < (1 << n)
		body = bin(lexer.TOKEN_LT, self(), paren(bin(lexer.TOKEN_LSHIFT, intLit("1"), n())))
	default:
		return nil
	}

	u64 := func() ast.TypeExpr { return &ast.NamedType{Position: pos, Name: "u64"} }
	return &ast.FuncDecl{
		Position: pos,
		Name:     name,
		IsLaw:    true,
		Params: []ast.ParamDecl{
			{Position: pos, Name: "self", Type: u64()},
			{Position: pos, Name: "n", Type: u64()},
		},
		ReturnType: &ast.NamedType{Position: pos, Name: "bool"},
		Body:       []ast.Stmt{&ast.ReturnStmt{Position: pos, Value: body}},
	}
}

// tryDischargeModularCheaply discharges a built-in modular obligation WITHOUT invoking the SMT solver,
// for the common syntactic shapes. Returns (proven, handled): handled=true means the cheap tier
// reached a verdict (proven true) and the SMT lane can be skipped; handled=false means defer to SMT.
// SOUNDNESS: every recognized shape is a ground theorem (mask-clear of a power-of-two-minus-one,
// alignment of a syntactically-masked value, or an already-aligned param fact), so it can only PROVE,
// never refute — a non-match defers, it never concludes false.
func (a *Analyzer) tryDischargeModularCheaply(value ast.Expr, predName string, args []ast.Expr) bool {
	if a == nil || value == nil {
		return false
	}
	switch predName {
	case "Aligned", "Divides":
		if len(args) != 1 {
			return false
		}
		nv, ok := a.constUint64Value(args[0])
		if !ok || nv == 0 {
			return false
		}
		return a.exprIsKnownMultipleOf(value, nv)
	case "MaskedZero":
		if len(args) != 1 {
			return false
		}
		mask, ok := a.constUint64Value(args[0])
		if !ok {
			return false
		}
		// `(x & C)` always has the `mask` bits clear when C & mask == 0.
		if and := asBitAnd(value); and != nil {
			if c, ok := a.constUint64Value(and.constSide); ok && (c&mask) == 0 {
				return true
			}
		}
		// A power-of-two-aligned value with mask == N-1 has those low bits clear.
		if isUintPowerOfTwoMinusOne(mask) {
			return a.exprIsKnownMultipleOf(value, mask+1)
		}
		return false
	case "Fits":
		if len(args) != 1 {
			return false
		}
		nbits, ok := a.constUint64Value(args[0])
		if !ok || nbits >= 64 {
			return false
		}
		limit := uint64(1) << nbits // 2^bits; the value must be < limit
		// `x & C` with C < 2^bits has only the low `bits` bits possibly set, so the result is < 2^bits.
		if and := asBitAnd(value); and != nil {
			if c, ok := a.constUint64Value(and.constSide); ok && c < limit {
				return true
			}
		}
		// A constant that already fits.
		if c, ok := a.constUint64Value(value); ok {
			return c < limit
		}
		return false
	}
	return false
}

// exprIsKnownMultipleOf reports a cheap, sound proof that `value` is a multiple of n (n > 0):
//   - a constant value divisible by n
//   - `x & C` (in either order) where, for power-of-two n, `~(n-1) & C == ~(n-1) & C` clears the low
//     bits — specifically `C & (n-1) == 0` means every set bit of the result is >= n's bit, so the
//     value is a multiple of n
//   - a bare variable already refined / known `Aligned[n]` (or a tighter multiple) via the fact set
//   - `k * n` or `n * k` for an integer k (constant or variable)
func (a *Analyzer) exprIsKnownMultipleOf(value ast.Expr, n uint64) bool {
	if n == 0 {
		return false
	}
	value = stripOptimizationParens(value)
	if c, ok := a.constUint64Value(value); ok {
		return c%n == 0
	}
	// `x & C` with the low log2(n) bits of C clear (power-of-two n): every set bit of the result is at
	// or above n's bit, so the result is a multiple of n.
	if isUintPowerOfTwo(n) {
		if and := asBitAnd(value); and != nil {
			if c, ok := a.constUint64Value(and.constSide); ok && (c&(n-1)) == 0 {
				return true
			}
		}
	}
	if bin, ok := value.(*ast.BinaryExpr); ok && bin != nil {
		switch bin.Op {
		case lexer.TOKEN_STAR:
			// `k * n` / `n * k` for a constant multiple-of-n factor.
			if c, ok := a.constUint64Value(bin.Right); ok && c%n == 0 {
				return true
			}
			if c, ok := a.constUint64Value(bin.Left); ok && c%n == 0 {
				return true
			}
			// k * m where m is itself a known multiple of n stays a multiple of n.
			if a.exprIsKnownMultipleOf(bin.Left, n) || a.exprIsKnownMultipleOf(bin.Right, n) {
				return true
			}
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS:
			// (a ± b) is a multiple of n when BOTH operands are. This proves `aligned_param + 4096`,
			// `aligned_param - aligned_param`, etc., with no solver.
			if a.exprIsKnownMultipleOf(bin.Left, n) && a.exprIsKnownMultipleOf(bin.Right, n) {
				return true
			}
		}
	}
	// A bare param/var whose declared refinement is `Aligned[m]` / `Divides[m]` with n | m is a
	// multiple of n (the caller proved the alignment; refinement types erase to base on read).
	if id, ok := value.(*ast.Ident); ok && id != nil && a.identAlignedToMultipleOf(id.Name, n) {
		return true
	}
	return false
}

// identAlignedToMultipleOf reports whether the named immutable param carries a declared (or aliased)
// refinement `Aligned[m]` / `Divides[m]` with n | m, making it a provable multiple of n.
func (a *Analyzer) identAlignedToMultipleOf(name string, n uint64) bool {
	if a == nil || a.currentFuncDecl == nil || n == 0 {
		return false
	}
	for _, param := range a.currentFuncDecl.Params {
		if param.Name != name || a.paramIsMutable(param) {
			continue
		}
		rt, ok := a.paramRefinementTypeExpr(param.Type)
		if !ok || rt == nil {
			return false
		}
		for _, pred := range rt.Preds {
			if pred.Name != "Aligned" && pred.Name != "Divides" {
				continue
			}
			if len(pred.Args) != 1 {
				continue
			}
			if m, ok := a.constUint64Value(pred.Args[0]); ok && m != 0 && m%n == 0 {
				return true
			}
		}
		return false
	}
	return false
}

// constUint64Value reads a compile-time constant as a uint64, accepting alignment/mask constants that
// exceed i64 range (e.g. a 64-bit page mask `0xFFFFFFFFFFFFF000`). It first tries the analyzer's
// constant evaluator (int64), then falls back to parsing a bare integer literal directly as uint64 so
// large hex/decimal masks are usable.
func (a *Analyzer) constUint64Value(expr ast.Expr) (uint64, bool) {
	if c, ok := a.constIntValue(expr); ok {
		return uint64(c), true
	}
	lit, ok := stripOptimizationParens(expr).(*ast.IntLit)
	if !ok || lit == nil {
		return 0, false
	}
	text := strings.TrimRight(lit.Value, "uUlL")
	u, err := strconv.ParseUint(text, 0, 64)
	if err != nil {
		return 0, false
	}
	return u, true
}

type bitAndParts struct {
	constSide ast.Expr
}

// asBitAnd returns the parts of a `a & b` where one side is the variable-ish term and the other is a
// candidate constant mask; returns nil if not an `&`.
func asBitAnd(e ast.Expr) *bitAndParts {
	bin, ok := stripOptimizationParens(e).(*ast.BinaryExpr)
	if !ok || bin == nil || bin.Op != lexer.TOKEN_AMPERSAND {
		return nil
	}
	return &bitAndParts{constSide: bin.Right}
}

func isUintPowerOfTwo(n uint64) bool {
	return n != 0 && (n&(n-1)) == 0
}

// isUintPowerOfTwoMinusOne reports whether n is 2^k - 1 (all low bits set), i.e. n+1 is a power of two.
func isUintPowerOfTwoMinusOne(n uint64) bool {
	return bits.OnesCount64(n+1) == 1
}
