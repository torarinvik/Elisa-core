package semantic

import (
	"math/big"
	"strings"

	"elisacore/src/lexer"
)

// Ground-constant folding for SMT Int terms.
//
// The term builders compose machine-arithmetic models (wraps, reinterprets, truncating division) over
// operand terms. When every operand is a literal the composed term is a closed Int expression whose
// value the Go side can compute exactly — e.g. `(mod (+ (mod (+ 0 1) 2^64) 1) 2^64)` is `2`. Emitting
// the value instead of the expression shrinks the query text (Go string building and z3 parsing are a
// large share of seed time) without changing meaning: each fold below computes precisely the SMT-LIB
// Int semantics of the operator it replaces, and folding only ever applies to closed terms, so it never
// changes which terms are opaque or which symbols a query mentions.
//
// Literals are recognised only in the two forms the translator emits: a decimal numeral `N` and a
// negated numeral `(- N)` (smtInt/smtBigInt). Anything else is left untouched.

// smtIntLiteral parses a ground SMT Int literal.
func smtIntLiteral(term string) (*big.Int, bool) {
	if term == "" {
		return nil, false
	}
	negative := false
	digits := term
	if term[0] == '(' {
		if !strings.HasPrefix(term, "(- ") || term[len(term)-1] != ')' {
			return nil, false
		}
		digits = term[3 : len(term)-1]
		negative = true
	}
	if digits == "" {
		return nil, false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return nil, false
		}
	}
	v, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, false
	}
	if negative {
		v.Neg(v)
	}
	return v, true
}

// smtIntPair parses both operands as literals.
func smtIntPair(l, r string) (*big.Int, *big.Int, bool) {
	lv, ok := smtIntLiteral(l)
	if !ok {
		return nil, nil, false
	}
	rv, ok := smtIntLiteral(r)
	if !ok {
		return nil, nil, false
	}
	return lv, rv, true
}

// smtArith renders `(op l r)` for op in `+`, `-`, `*`, folding a closed term to its exact Int value.
func smtArith(op, l, r string) string {
	if lv, rv, ok := smtIntPair(l, r); ok {
		switch op {
		case "+":
			return smtBigInt(lv.Add(lv, rv))
		case "-":
			return smtBigInt(lv.Sub(lv, rv))
		case "*":
			return smtBigInt(lv.Mul(lv, rv))
		}
	}
	return "(" + op + " " + l + " " + r + ")"
}

// smtModTerm renders SMT-LIB `(mod l r)`. SMT-LIB `mod` is Euclidean (result in [0, |r|)), which is
// exactly big.Int.Mod. A zero divisor is left unfolded: SMT-LIB leaves `(mod x 0)` uninterpreted.
func smtModTerm(l, r string) string {
	if lv, rv, ok := smtIntPair(l, r); ok && rv.Sign() != 0 {
		return smtBigInt(lv.Mod(lv, rv))
	}
	return "(mod " + l + " " + r + ")"
}

// smtDivTerm renders SMT-LIB `(div l r)`: Euclidean division (big.Int.Div). Zero divisors stay unfolded.
func smtDivTerm(l, r string) string {
	if lv, rv, ok := smtIntPair(l, r); ok && rv.Sign() != 0 {
		return smtBigInt(lv.Div(lv, rv))
	}
	return "(div " + l + " " + r + ")"
}

// smtIteTerm renders `(ite c a b)`, selecting the branch when the condition is a Bool literal.
func smtIteTerm(c, a, b string) string {
	switch c {
	case "true":
		return a
	case "false":
		return b
	}
	return "(ite " + c + " " + a + " " + b + ")"
}

// smtFoldCompare evaluates a comparison of two Int literals; ok=false when either side is not ground.
func smtFoldCompare(op lexer.TokenKind, l, r string) (string, bool) {
	lv, rv, ok := smtIntPair(l, r)
	if !ok {
		return "", false
	}
	c := lv.Cmp(rv)
	var v bool
	switch op {
	case lexer.TOKEN_GT:
		v = c > 0
	case lexer.TOKEN_GTEQ:
		v = c >= 0
	case lexer.TOKEN_LT:
		v = c < 0
	case lexer.TOKEN_LTEQ:
		v = c <= 0
	case lexer.TOKEN_EQEQ:
		v = c == 0
	case lexer.TOKEN_BANGEQ:
		v = c != 0
	default:
		return "", false
	}
	if v {
		return "true", true
	}
	return "false", true
}
