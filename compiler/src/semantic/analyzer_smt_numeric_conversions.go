package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"math/big"
	"strings"
)

// smtTermMentionsQuantifierBinder reports whether an SMT term names a quantifier binder (`q_…`).
func smtTermMentionsQuantifierBinder(term string) bool {
	if !strings.Contains(term, "q_") {
		return false
	}
	// Binders introduced INSIDE the term (`(forall ((q_k Int)) ...)`) are bound there; only a
	// binder with no enclosing quantifier in the term is free. Binder names carry a unique counter
	// suffix, so a name bound anywhere in the term is bound at all of its occurrences.
	bound := map[string]bool{}
	for _, q := range []string{"(forall (", "(exists ("} {
		rest := term
		for {
			i := strings.Index(rest, q)
			if i < 0 {
				break
			}
			rest = rest[i+len(q):]
			for strings.HasPrefix(rest, "(") {
				end := strings.IndexAny(rest[1:], " )")
				if end < 0 {
					break
				}
				bound[rest[1:1+end]] = true
				close := strings.Index(rest, ")")
				if close < 0 {
					break
				}
				rest = strings.TrimLeft(rest[close+1:], " ")
			}
		}
	}
	for tok := range strings.FieldsFuncSeq(term, func(r rune) bool {
		return r == ' ' || r == '(' || r == ')' || r == '|' || r == ';' || r == '=' || r == ','
	}) {
		if strings.HasPrefix(tok, "q_") && !bound[tok] {
			return true
		}
	}
	return false
}

// mixedSignFitGuard decides whether an integer comparison mixes operand types such that codegen's
// evaluation (the RIGHT operand converted to the LEFT operand's type, then compared in that type:
// `usize_x <= view.len` compares unsigned, a length of -1 reading as 2^64-1; `i32 < u64` truncates)
// can differ from the plain-value comparison. It returns mixed=true and the SMT condition (over the
// right operand's term r) under which the two agree: r is representable in the left type. Constants
// that fit, value-preserving pairs, quantifier binders and operands of unknown type are not mixed.
func (tr *smtTranslator) mixedSignFitGuard(n *ast.BinaryExpr, r string) (string, bool) {
	if tr == nil || tr.a == nil || n == nil || n.LoweredCall != nil {
		return "", false
	}
	a := tr.a
	var leftType, rightType Type
	if tr.calleeBodyDepth != 0 {
		leftType, rightType = a.exprType(n.Left), a.exprType(n.Right)
	} else {
		if a.mentionsUnscopedName(n.Left) || a.mentionsUnscopedName(n.Right) {
			return "", false
		}
		leftType, rightType = a.smtOperandType(n.Left), a.smtOperandType(n.Right)
	}
	lsign, lbits, lok := smtIntWidthSign(leftType)
	rsign, rbits, rok := smtIntWidthSign(rightType)
	if !lok || !rok {
		return "", false
	}
	if (lsign == rsign && lbits >= rbits) || (!rsign && lsign && lbits > rbits) {
		return "", false
	}
	if c, ok := a.constIntValue(n.Right); ok && smtTypeFitsValue(leftType, c) {
		return "", false
	}
	lo := new(big.Int)
	hi := new(big.Int)
	if lsign {
		lo.Neg(new(big.Int).Lsh(big.NewInt(1), uint(lbits-1)))
		hi.Sub(new(big.Int).Lsh(big.NewInt(1), uint(lbits-1)), big.NewInt(1))
	} else {
		hi.Sub(new(big.Int).Lsh(big.NewInt(1), uint(lbits)), big.NewInt(1))
	}
	return "(and (<= " + smtBigInt(lo) + " " + r + ") (<= " + r + " " + hi.String() + "))", true
}

// mixedSignCompare is the comparison as codegen evaluates it. Where the right operand fits the left
// type it is the plain-value comparison. A signed right operand compared in a wider-or-equal unsigned
// left type reads a negative value as r + 2^bits, modeled exactly. Any other out-of-range case
// (truncation) is an unconstrained Bool: Unknown, never a value comparison.
func (tr *smtTranslator) mixedSignCompare(n *ast.BinaryExpr, l, r string) string {
	cmp := smtCompare(n.Op, l, r)
	guard, mixed := tr.mixedSignFitGuard(n, r)
	if !mixed {
		return cmp
	}
	var other string
	if wrap := tr.mixedSignWrapModulus(n); wrap != "" {
		other = smtCompare(n.Op, l, smtArith("+", r, wrap))
	} else if conv, ok := tr.mixedSignConvertedRight(n, r); ok {
		other = smtCompare(n.Op, l, conv)
	} else {
		other = tr.freshBoolAux()
	}
	return smtIteTerm(guard, cmp, other)
}

// mixedSignConvertedRight is the right operand exactly as codegen converts it to the left type
// (truncate, or sign/zero-extend, then reinterpret): m = r mod 2^bits, read as m - 2^bits when the
// left type is signed and m >= 2^(bits-1). A deterministic term, so equal comparisons translate equal.
func (tr *smtTranslator) mixedSignConvertedRight(n *ast.BinaryExpr, r string) (string, bool) {
	a := tr.a
	var leftType Type
	if tr.calleeBodyDepth != 0 {
		leftType = a.exprType(n.Left)
	} else {
		leftType = a.smtOperandType(n.Left)
	}
	lsign, lbits, ok := smtIntWidthSign(leftType)
	if !ok || lbits <= 0 || lbits > 128 {
		return "", false
	}
	mod := new(big.Int).Lsh(big.NewInt(1), uint(lbits)).String()
	m := smtModTerm(r, mod)
	if !lsign {
		return m, true
	}
	half := new(big.Int).Lsh(big.NewInt(1), uint(lbits-1)).String()
	return smtIteTerm(smtCompare(lexer.TOKEN_GTEQ, m, half), smtArith("-", m, mod), m), true
}

// mixedSignWrapModulus returns 2^bits when the left operand is unsigned with at least the right
// (signed) operand's width: the only mixed case where out-of-range means r < 0 and the converted
// value is exactly r + 2^bits.
func (tr *smtTranslator) mixedSignWrapModulus(n *ast.BinaryExpr) string {
	a := tr.a
	var leftType, rightType Type
	if tr.calleeBodyDepth != 0 {
		leftType, rightType = a.exprType(n.Left), a.exprType(n.Right)
	} else {
		leftType, rightType = a.smtOperandType(n.Left), a.smtOperandType(n.Right)
	}
	lsign, lbits, lok := smtIntWidthSign(leftType)
	rsign, rbits, rok := smtIntWidthSign(rightType)
	if !lok || !rok || lsign || !rsign || lbits < rbits {
		return ""
	}
	return new(big.Int).Lsh(big.NewInt(1), uint(lbits)).String()
}

// smtOperandType is an expression's analyzed type, falling back for synthetic/substituted nodes to
// the scope type of a bare identifier, `result`'s return type, the resolved type of a field read, or
// an sview's i64 `.len`.
func (a *Analyzer) smtOperandType(e ast.Expr) Type {
	if t := a.exprType(e); t != nil {
		return t
	}
	switch op := stripOptimizationParens(e).(type) {
	case *ast.FieldExpr:
		if t, ok := a.fieldReadResolvedType(op); ok {
			return t
		}
		if op.Field == "len" {
			if _, isView := stripRefForBounds(a.smtOperandType(op.Object)).(*SViewType); isView {
				return a.scanTypeNamed("i64")
			}
		}
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(op.Name); ok && sym != nil {
				return sym.Type
			}
		}
		if op.Name == "result" && a.currentReturn != nil {
			return a.currentReturn
		}
	}
	return a.exprType(stripOptimizationParens(e))
}

// mentionsUnscopedName reports whether an expression reads a name that is neither in scope, nor
// `result`, nor a constant: a quantifier binder (a mathematical integer, never a runtime value).
func (a *Analyzer) mentionsUnscopedName(e ast.Expr) bool {
	found := false
	var walk func(x ast.Expr, depth int)
	walk = func(x ast.Expr, depth int) {
		if found || x == nil || depth > 32 {
			return
		}
		switch n := x.(type) {
		case *ast.Ident:
			if n.Name == "result" {
				return
			}
			if _, ok := a.constIntValue(n); ok {
				return
			}
			if a.currentScope != nil {
				if sym, ok := a.currentScope.Lookup(n.Name); ok && sym != nil {
					return
				}
			}
			found = true
		case *ast.ParenExpr:
			walk(n.Inner, depth+1)
		case *ast.BinaryExpr:
			walk(n.Left, depth+1)
			walk(n.Right, depth+1)
		case *ast.UnaryExpr:
			walk(n.Operand, depth+1)
		case *ast.CastExpr:
			walk(n.Operand, depth+1)
		case *ast.FieldExpr:
			walk(n.Object, depth+1)
		case *ast.IndexExpr:
			walk(n.Object, depth+1)
			walk(n.Index, depth+1)
		}
	}
	walk(e, 0)
	return found
}

// smtTypeFitsValue reports that an integer constant is representable in an integer type, including
// the builtin usize/isize/char kinds (IntegerTypeFitsValue treats those as "fits anything").
func smtTypeFitsValue(t Type, value int64) bool {
	signed, bits, ok := smtIntWidthSign(t)
	if !ok {
		return false
	}
	if signed {
		if bits >= 64 {
			return true
		}
		return value >= -(int64(1)<<(bits-1)) && value <= (int64(1)<<(bits-1))-1
	}
	if value < 0 {
		return false
	}
	return bits >= 63 || value <= (int64(1)<<bits)-1
}

func smtBigInt(v *big.Int) string {
	if v.Sign() < 0 {
		return "(- " + new(big.Int).Neg(v).String() + ")"
	}
	return v.String()
}

// scanTypeNamed resolves a type NAME for the syntactic conversion scan: builtin integer/bool names
// directly, anything else through the declared named types.
func (a *Analyzer) scanTypeNamed(name string) Type {
	if name == "bool" || isIntegerConversionTargetName(name) {
		return &BuiltinType{Name: name}
	}
	return a.namedTypes[name]
}
