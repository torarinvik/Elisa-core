package smtquery

import (
	"math/big"
	"strings"
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

// IntLiteral parses a ground SMT Int literal.
func IntLiteral(term string) (*big.Int, bool) {
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

// IntPair parses both operands as literals.
func IntPair(l, r string) (*big.Int, *big.Int, bool) {
	lv, ok := IntLiteral(l)
	if !ok {
		return nil, nil, false
	}
	rv, ok := IntLiteral(r)
	if !ok {
		return nil, nil, false
	}
	return lv, rv, true
}

// Arith renders `(op l r)` for op in `+`, `-`, `*`, folding a closed term to its exact Int value.
func Arith(op, l, r string) string {
	if lv, rv, ok := IntPair(l, r); ok {
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

// ModTerm renders SMT-LIB `(mod l r)`. SMT-LIB `mod` is Euclidean (result in [0, |r|)), which is
// exactly big.Int.Mod. A zero divisor is left unfolded: SMT-LIB leaves `(mod x 0)` uninterpreted.
func ModTerm(l, r string) string {
	if lv, rv, ok := IntPair(l, r); ok && rv.Sign() != 0 {
		return smtBigInt(lv.Mod(lv, rv))
	}
	return "(mod " + l + " " + r + ")"
}

// DivTerm renders SMT-LIB `(div l r)`: Euclidean division (big.Int.Div). Zero divisors stay unfolded.
func DivTerm(l, r string) string {
	if lv, rv, ok := IntPair(l, r); ok && rv.Sign() != 0 {
		return smtBigInt(lv.Div(lv, rv))
	}
	return "(div " + l + " " + r + ")"
}

// IteTerm renders `(ite c a b)`, selecting the branch when the condition is a Bool literal.
func IteTerm(c, a, b string) string {
	switch c {
	case "true":
		return a
	case "false":
		return b
	}
	return "(ite " + c + " " + a + " " + b + ")"
}

func smtBigInt(v *big.Int) string {
	if v.Sign() < 0 {
		return "(- " + new(big.Int).Neg(v).String() + ")"
	}
	return v.String()
}
