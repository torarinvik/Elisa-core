package semantic

import (
	"math/big"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// bitvectorCompare lowers fixed-width integer ordering to SMT bitvector ordering. The integer term
// lane already models machine arithmetic by wrapping results back into the source type's value range;
// comparing the corresponding bit patterns preserves the same source-level signed/unsigned ordering
// for overflow-sensitive guards such as `addr + len >= addr`.
func (tr *smtTranslator) bitvectorCompare(n *ast.BinaryExpr, l, r string, env map[string]string) (string, bool) {
	if smtEnvHasQuantifierBinder(env) {
		return "", false
	}
	// In requires-hypothesis mode an unsigned `sum <= 2^W-1` (a no-overflow precondition) must be read as
	// the ℤ bound, not the wrapped bvule (which `int2bv` makes vacuously true). Decline here so boolTerm
	// falls through to the clean ℤ comparison. Gated to the exact overflow-guard shape the body's clean
	// term is justified by (provablyNoArithWrap), so it cannot over-assume on any other comparison.
	if tr.requiresHypMode && tr.isUnsignedSumTypeMaxGuard(n) {
		return "", false
	}
	if !tr.containsFixedWidthMachineArith(n.Left) && !tr.containsFixedWidthMachineArith(n.Right) {
		return "", false
	}
	signed, bits, ok := tr.compareWidthSign(n.Left, n.Right)
	if !ok || signed || bits <= 0 || bits > 64 {
		return "", false
	}
	// Both operands provably non-negative ⟹ their ℤ values lie in [0, 2^bits): the term lane wraps every
	// machine `+`/`-`/`*` modulo 2^bits and asserts the [0,2^bits) range of each unsigned variable / length
	// symbol, so a non-negative such term cannot exceed the width. Over that range the unsigned bitvector
	// ordering is IDENTICAL to the clean ℤ ordering, so decline the `int2bv` encoding and let boolTerm emit
	// the plain ℤ comparison. This is not just an optimization: the `(_ int2bv …)`/`bvult` mix forces a
	// bitvector+int query that z3 IN INCREMENTAL MODE (push/pop, mbqi) can answer `unknown` on even when it
	// is valid — which silently dropped the loop guard `j + 1 < self.xs.count` and blocked the field-shift
	// termination proofs. SOUNDNESS: restricted to provably-non-negative operands, where ℤ `<` is exactly
	// the unsigned `<`; a comparison that could observe wrap (operand not provably non-negative) keeps the
	// exact bitvector model below.
	if tr.a.provablyNonNeg(n.Left) && tr.a.provablyNonNeg(n.Right) {
		return "", false
	}
	op := ""
	switch n.Op {
	case lexer.TOKEN_GT:
		op = "bvugt"
	case lexer.TOKEN_GTEQ:
		op = "bvuge"
	case lexer.TOKEN_LT:
		op = "bvult"
	case lexer.TOKEN_LTEQ:
		op = "bvule"
	default:
		return "", false
	}
	width := strconv.Itoa(bits)
	return "(" + op + " ((_ int2bv " + width + ") " + l + ") ((_ int2bv " + width + ") " + r + "))", true
}

// isUnsignedSumTypeMaxGuard reports whether `n` is an upper-bound comparison of an unsigned addition by
// a constant within its width — `a + b <= C` or `C >= a + b` with `C <= 2^W-1` (W the sum's width). This
// is the no-overflow precondition shape; in requiresHypMode it is read in ℤ (declining bitvectorCompare).
func (tr *smtTranslator) isUnsignedSumTypeMaxGuard(n *ast.BinaryExpr) bool {
	var sumSide, constSide ast.Expr
	switch n.Op {
	case lexer.TOKEN_LTEQ:
		sumSide, constSide = n.Left, n.Right
	case lexer.TOKEN_GTEQ:
		sumSide, constSide = n.Right, n.Left
	default:
		return false
	}
	sum, ok := stripOptimizationParens(sumSide).(*ast.BinaryExpr)
	if !ok || sum.Op != lexer.TOKEN_PLUS {
		return false
	}
	signed, bits, ok := smtIntWidthSign(tr.a.exprType(sum))
	if !ok || signed || bits <= 0 || bits > 64 {
		return false
	}
	c, ok := tr.a.unsignedConstBound(constSide)
	if !ok {
		return false
	}
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bits)), big.NewInt(1))
	return c.Cmp(max) <= 0
}

func smtEnvHasQuantifierBinder(env map[string]string) bool {
	for _, term := range env {
		if strings.HasPrefix(term, "q_") || strings.Contains(term, " q_") {
			return true
		}
	}
	return false
}

func (tr *smtTranslator) compareWidthSign(left, right ast.Expr) (signed bool, bits int, ok bool) {
	ls, lb, lok := smtIntWidthSign(tr.a.exprType(left))
	rs, rb, rok := smtIntWidthSign(tr.a.exprType(right))
	if lok && rok && ls == rs && lb == rb {
		return ls, lb, true
	}
	if lok && isIntegerLiteralExpr(right) {
		return ls, lb, true
	}
	if rok && isIntegerLiteralExpr(left) {
		return rs, rb, true
	}
	return false, 0, false
}

func (tr *smtTranslator) containsFixedWidthMachineArith(expr ast.Expr) bool {
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR:
			if _, bits, ok := smtIntWidthSign(tr.a.exprType(n)); ok && bits > 0 && bits <= 64 {
				return true
			}
		}
		return tr.containsFixedWidthMachineArith(n.Left) || tr.containsFixedWidthMachineArith(n.Right)
	case *ast.UnaryExpr:
		return tr.containsFixedWidthMachineArith(n.Operand)
	case *ast.CastExpr:
		return tr.containsFixedWidthMachineArith(n.Operand)
	case *ast.TernaryExpr:
		return tr.containsFixedWidthMachineArith(n.Cond) ||
			tr.containsFixedWidthMachineArith(n.Value) ||
			tr.containsFixedWidthMachineArith(n.Alt)
	default:
		return false
	}
}

func isIntegerLiteralExpr(expr ast.Expr) bool {
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.IntLit:
		return true
	case *ast.UnaryExpr:
		return n.Op == lexer.TOKEN_MINUS && isIntegerLiteralExpr(n.Operand)
	default:
		return false
	}
}

// collectSelectTriggers gathers the `(select <arr> <idx>)` SMT terms in a quantifier body whose index
// mentions one of the quantifier's bound variables — the canonical E-matching trigger for an
// array-element quantifier (docs/90 brick 90-16). It walks the AST body for IndexExpr nodes, lowers
// each through the same `qenv` (so the array/index symbols match the body), and keeps the distinct
// ones referencing a binder, in stable order. Returns nil when there is no array indexing on a binder
// (a purely arithmetic quantifier), leaving that quantifier patternless for MBQI.
func (tr *smtTranslator) collectSelectTriggers(body ast.Expr, qenv map[string]string, vars []string) []string {
	bound := make(map[string]string, len(vars))
	for _, v := range vars {
		bound[v] = "q_" + v
	}
	return tr.collectSelectTriggersWithBoundSymbols(body, qenv, bound)
}

func (tr *smtTranslator) collectSelectTriggersWithBoundSymbols(body ast.Expr, qenv map[string]string, binders map[string]string) []string {
	bound := make(map[string]bool, len(binders))
	for _, sym := range binders {
		bound[sym] = true
	}
	seen := map[string]bool{}
	var triggers []string
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch n := e.(type) {
		case *ast.ParenExpr:
			walk(n.Inner)
		case *ast.UnaryExpr:
			walk(n.Operand)
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.QuantifierExpr:
			walk(n.Body) // nested quantifier: its own binders may still mention ours
		case *ast.CallExpr:
			// Calls over indexed sviews model the selected view as a function
			// application of the container and quantified index. Use its backing
			// array application as the E-matching trigger; it occurs inside the
			// scalar-call UF application and keeps the binder in a scoped term.
			_, callOK := tr.scalarCallUF(n, qenv, "Bool")
			if !callOK {
				_, callOK = tr.scalarCallUF(n, qenv, "Int")
			}
			if !callOK {
				for _, arg := range proofCallArgs(n) {
					walk(arg)
				}
				return
			}
			for _, arg := range proofCallArgs(n) {
				if base, index, ok := tr.containerViewIndex(arg, qenv); ok {
					data, _, _ := tr.containerViewElement(base, index)
					if termMentionsAnyBinder(data, bound) && !seen[data] {
						seen[data] = true
						triggers = append(triggers, data)
					}
				}
				walk(arg)
			}
		case *ast.IndexExpr:
			if term, ok := tr.termEnv(n, qenv); ok && termMentionsAnyBinder(term, bound) && !seen[term] {
				seen[term] = true
				triggers = append(triggers, term)
			}
			walk(n.Object)
			walk(n.Index)
		}
	}
	walk(body)
	return triggers
}

func (tr *smtTranslator) collectQuantifierWitnessSelects(body ast.Expr, qenv map[string]string) {
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch n := e.(type) {
		case *ast.ParenExpr:
			walk(n.Inner)
		case *ast.UnaryExpr:
			walk(n.Operand)
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.TernaryExpr:
			walk(n.Cond)
			walk(n.Value)
			walk(n.Alt)
		case *ast.QuantifierExpr:
			return
		case *ast.IndexExpr:
			if term, ok := tr.termEnv(n, qenv); ok {
				if label := tr.witnessSelectLabel(n, qenv); label != "" {
					tr.ceExprs[label] = term
				}
			}
			walk(n.Object)
			walk(n.Index)
		}
	}
	walk(body)
}

func (tr *smtTranslator) witnessSelectLabel(n *ast.IndexExpr, env map[string]string) string {
	if n == nil {
		return ""
	}
	obj := tr.witnessExprLabel(n.Object, env)
	idx := tr.witnessExprLabel(n.Index, env)
	if obj == "" || idx == "" {
		return ""
	}
	return obj + "[" + idx + "]"
}

func (tr *smtTranslator) witnessExprLabel(expr ast.Expr, env map[string]string) string {
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.Ident:
		if env != nil {
			if bound, ok := env[n.Name]; ok {
				if label := tr.counterexampleLabelForTerm(bound); label != "" {
					return label
				}
			}
		}
		return n.Name
	case *ast.IntLit:
		if c, ok := tr.a.constIntValue(n); ok {
			return strconv.FormatInt(c, 10)
		}
	case *ast.FieldExpr:
		base := tr.witnessExprLabel(n.Object, env)
		if base == "" {
			return ""
		}
		return base + "." + n.Field
	case *ast.BinaryExpr:
		l := tr.witnessExprLabel(n.Left, env)
		r := tr.witnessExprLabel(n.Right, env)
		if l == "" || r == "" {
			return ""
		}
		switch n.Op {
		case lexer.TOKEN_PLUS:
			return l + "+" + r
		case lexer.TOKEN_MINUS:
			return l + "-" + r
		}
	}
	return ""
}

func (tr *smtTranslator) counterexampleLabelForTerm(term string) string {
	for name := range tr.arrayDecls {
		if smtVar(name) == term {
			return name
		}
	}
	for name := range tr.decls {
		if smtVar(name) == term {
			return name
		}
	}
	return ""
}

// termMentionsAnyBinder reports whether an SMT term string contains any of the bound `q_*` symbols as
// a whole token (so `q_i` does not spuriously match `q_index`).
func termMentionsAnyBinder(term string, bound map[string]bool) bool {
	for _, tok := range strings.FieldsFunc(term, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '(' || r == ')' ||
			r == '|' || r == ';' || r == '=' || r == ','
	}) {
		if bound[tok] {
			return true
		}
	}
	return false
}

// freshAux mints a new under-constrained integer symbol for a sub-term we cannot model precisely but
// must keep PRESENT (so the surrounding clause can still discharge). It is constrained only by what
// provably holds — `>= 0` when nonNeg — never by a false relation, so it can never fabricate a proof.
func (tr *smtTranslator) freshAux(nonNeg bool) string {
	tr.auxSeq++
	v := "aux_" + smtInt(int64(tr.auxSeq))
	tr.auxVars = append(tr.auxVars, v)
	tr.auxDecls = append(tr.auxDecls, "(declare-const "+v+" Int)\n")
	if nonNeg {
		tr.auxDecls = append(tr.auxDecls, "(assert (>= "+v+" 0))\n")
	}
	return v
}

func (tr *smtTranslator) freshBoolAux() string {
	tr.auxSeq++
	v := "aux_bool_" + smtInt(int64(tr.auxSeq))
	tr.auxVars = append(tr.auxVars, v)
	tr.auxDecls = append(tr.auxDecls, "(declare-const "+v+" Bool)\n")
	return v
}

// maskBoundAux models `X & C` for a non-negative constant C when X's exact bitvector term is
// unavailable (e.g. X = `word >> runtime_shift`, outside the const-shift fragment). The result is
// ALWAYS in [0, C] regardless of X — its set bits are a subset of C's — so a fresh integer bounded
// to [0, C] is a sound OVER-approximation of the exact value. Soundness: the prover concludes only
// on `unsat` of the negated goal; widening the value's feasible region to all of [0, C] can only
// make that unsat harder to reach (decline), never fabricate one. This lets a masked extraction
// like `(word >> sh) & 0xF` prove `< 16` even though the runtime shift is unmodelable.
func (tr *smtTranslator) maskBoundAux(c int64) string {
	tr.auxSeq++
	v := "aux_" + smtInt(int64(tr.auxSeq))
	tr.auxVars = append(tr.auxVars, v)
	tr.auxDecls = append(tr.auxDecls, "(declare-const "+v+" Int)\n")
	tr.auxDecls = append(tr.auxDecls, "(assert (>= "+v+" 0))\n")
	tr.auxDecls = append(tr.auxDecls, "(assert (<= "+v+" "+smtInt(c)+"))\n")
	return v
}

// shiftRightBoundAux models `X >> k` for an unsigned W-bit value X and a constant k when X's exact
// bitvector term is unavailable. A logical right shift by k can only produce values in [0, 2^(W-k)-1]:
// it discards the low k bits and shifts in zeros at the top, so the result fits in W-k bits. A fresh
// integer bounded to [0, 2^(W-k)-1] is a sound OVER-approximation. Soundness: the prover concludes
// only on `unsat` of the negated goal; widening to all of [0, 2^(W-k)-1] can only make unsat harder
// (decline), never fabricate one. This lets goals like `unknownExpr >> 12 < 2^52` prove on the pure
// Int side without bitvector theory, avoiding the BV timeout on relational goals.
func (tr *smtTranslator) shiftRightBoundAux(bits, k int) string {
	tr.auxSeq++
	v := "aux_" + smtInt(int64(tr.auxSeq))
	tr.auxVars = append(tr.auxVars, v)
	hi := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bits-k)), big.NewInt(1)) // 2^(bits-k) - 1
	tr.auxDecls = append(tr.auxDecls, "(declare-const "+v+" Int)\n")
	tr.auxDecls = append(tr.auxDecls, "(assert (>= "+v+" 0))\n")
	tr.auxDecls = append(tr.auxDecls, "(assert (<= "+v+" "+hi.String()+"))\n")
	return v
}

// smtBitvectorRead reads a W-bit bitvector result back as its machine integer value. For an unsigned
// result that is `bv2nat(b)` ∈ [0, 2^W). For a signed result it is the exact two's-complement value
// `bv2nat(b) - 2^W * topbit(b)` ∈ [-2^(W-1), 2^(W-1)) — so the produced Int EQUALS the machine value
// and composes soundly with further Int reasoning (e.g. a sign-extension `(v << k) >> k` proves its
// signed range). The top bit is read via a 1-bit extract.
// smtSameWidthReinterpret is the same-width sign reinterpretation of the Int term `inner` read as a
// `bits`-wide signed or unsigned value, in linear integer arithmetic. It equals
// smtBitvectorRead((_ int2bv bits) inner) for EVERY Int (int2bv reduces modulo 2^bits, exactly what
// `mod` by the constant 2^bits computes; the signed read subtracts 2^bits when the top bit is set),
// but it keeps the query out of bit-vector theory: `x.len.usize()` over an i64 length cost z3 a 64-bit
// int2bv/bv2nat round trip per occurrence, which made termination proofs over view lengths exceed the
// per-query budget.
func smtSameWidthReinterpret(inner string, signed bool, bits int) string {
	m := smtModTerm(inner, smtPow2(bits))
	if !signed {
		return m
	}
	return smtArith("-", m, smtIteTerm(smtCompare(lexer.TOKEN_GTEQ, m, smtPow2(bits-1)), smtPow2(bits), "0"))
}

func smtBitvectorRead(bvExpr string, signed bool, bits int) string {
	if !signed {
		return "(bv2nat " + bvExpr + ")"
	}
	hi := strconv.Itoa(bits - 1)
	signBit := "(bv2nat ((_ extract " + hi + " " + hi + ") " + bvExpr + "))"
	return "(- (bv2nat " + bvExpr + ") (* " + smtPow2(bits) + " " + signBit + "))"
}

func (tr *smtTranslator) callResultTerm(call *ast.CallExpr) (string, bool) {
	return tr.callResultTermEnv(call, nil)
}
