package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

// arithWrapInfo mirrors wrapMachineArith's wrap decision for the VC IR: a `+`/`-`/`*` result of width
// 1..64 that is NOT provably in range wraps at that width — unsigned modulo 2^W, signed two's-complement
// — so the IR models the real machine value rather than the unbounded-ℤ value (which would prove a false
// `ensure result > 0` over an overflowing signed sum). A provably-in-range result emits clean (0).
func (tr *smtTranslator) arithWrapInfo(n *ast.BinaryExpr) (int, bool) {
	signed, bits, ok := smtIntWidthSign(tr.a.exprType(n))
	if !ok || bits <= 0 || bits > 64 {
		return 0, false
	}
	if tr.a.provablyNoArithWrap(n, signed, bits) {
		return 0, false
	}
	return bits, signed
}

// lowerVCTerm lowers an integer Elisa expression into the term IR: literals and the wrapping machine
// operators become structural nodes; everything else delegates to termEnv as an opaque term (exact
// string, same symbol declarations). Declining (ok=false) only when termEnv itself declines.
func (tr *smtTranslator) lowerVCTerm(expr ast.Expr, env map[string]string) (vcTerm, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.lowerVCTerm(n.Inner, env)
	case *ast.CallExpr:
		// `old(inner)` is the value of `inner` at function ENTRY. Lower it as the ENTRY translation of
		// `inner` held as an OPAQUE term, so weakest-precondition transport does NOT rewrite it (a bare
		// `p` in the same clause is the EXIT value and IS threaded). For a param `p` both emit the same
		// symbol `v_p`, which WP anchors to the entry value — making `ensure p >= old(p)` provable when the
		// body keeps/raises p and declined when it lowers p (docs: real old() snapshot modeling).
		if tr.oldAsEntry && ast.IsOldCall(n) && len(n.Args) == 1 {
			if s, ok := tr.termEnv(n.Args[0], env); ok {
				return vcOldEntry{SMT: s}, true
			}
			return nil, false
		}
	case *ast.IntLit:
		term, ok := tr.termEnv(n, env)
		if !ok {
			return nil, false
		}
		// The int64-folding lane may hold only a signed payload of a u64
		// literal. Keep the exact unsigned term when that payload differs.
		if c, ok := tr.a.constIntValue(n); ok && term == smtInt(c) {
			return vcIntLit{Val: c}, true
		}
		return vcOpaque{SMT: term}, true
	case *ast.Ident:
		// A plain free integer variable (mutable locals, params) becomes a substitutable vcVar — the WP
		// transport target. termEnv returns exactly smtVar(name) for that form; env-bound or const-folded
		// idents return a different string and stay opaque.
		s, ok := tr.termEnv(n, env)
		if !ok {
			return nil, false
		}
		if s == smtVar(n.Name) {
			return vcVar{Name: n.Name, SMT: s}, true
		}
		return vcOpaque{SMT: s}, true
	case *ast.FieldExpr:
		// An immutable sview parameter's length is stable across a pure return tree.
		// Keep it as a named structural variable so WP can inline a local length
		// alias into a byte-index select without treating the length term as opaque.
		if n.Field == "len" {
			if id, ok := stripOptimizationParens(n.Object).(*ast.Ident); ok && tr.immutableSviewArrayParameter(id, env) {
				if s, ok := tr.termEnv(n, env); ok {
					return vcVar{Name: "\x00sview-len:" + id.Name, SMT: s}, true
				}
			}
		}
		// A scalar struct-field place `p.pos` becomes a substitutable place variable keyed by its
		// syntactic projection, so weakest-precondition transport can thread a `p.pos <- p.pos + 1`
		// mutation and relate the exit value to `old(p.pos)`. termEnv emits exactly smtVar(projection)
		// only for a plain numeric field READ; a const-valued field, `.count`/`.len`, or a non-numeric
		// field take other paths and stay opaque. The place-aliasing gate (single reference root) is
		// enforced where the MUTATION is captured (wpPlaceKey), so a bare read here is always safe to make
		// substitutable — substitution only ever runs over a captured, gated body.
		s, ok := tr.termEnv(n, env)
		if !ok {
			return nil, false
		}
		if key := smtProjectionName(n); s == smtVar(key) {
			return vcVar{Name: key, SMT: s}, true
		}
		return vcOpaque{SMT: s}, true
	case *ast.IndexExpr:
		// Only an immutable sview parameter in an already-pure return tree is safe
		// to preserve structurally. Mutable views, mutable/local array roots, and
		// unsupported index forms retain the ordinary opaque translation.
		id, isIdent := stripOptimizationParens(n.Object).(*ast.Ident)
		if isIdent && tr.immutableSviewArrayParameter(id, env) {
			array, arrayOK := tr.arrayTermEnv(n.Object, env)
			index, indexOK := tr.lowerVCTerm(n.Index, env)
			if arrayOK && indexOK {
				return vcSelect{Array: array, Index: index}, true
			}
		}
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_MINUS {
			if inner, ok := tr.lowerVCTerm(n.Operand, env); ok {
				return vcMkNeg(inner), true
			}
		}
	case *ast.BinaryExpr:
		var op string
		switch n.Op {
		case lexer.TOKEN_PLUS:
			op = "+"
		case lexer.TOKEN_MINUS:
			op = "-"
		case lexer.TOKEN_STAR:
			op = "*"
		}
		if op != "" {
			if l, ok := tr.lowerVCTerm(n.Left, env); ok {
				if r, ok := tr.lowerVCTerm(n.Right, env); ok {
					wrapBits, signedWrap := tr.arithWrapInfo(n)
					return vcMkArith(op, l, r, wrapBits, signedWrap), true
				}
			}
		}
	}
	s, ok := tr.termEnv(expr, env)
	if !ok {
		return nil, false
	}
	return vcOpaque{SMT: s}, true
}

// lowerVCFormula lowers a boolean Elisa expression into the formula IR. The propositional structure
// (`and`/`or`/`not`, parentheses, the boolean literals) becomes IR nodes; every other boolean shape is
// translated by boolTerm and kept as an opaque atom. The short-circuit on a decided left operand mirrors
// boolTerm exactly, so the right operand's symbols are declared in the same cases (behavior-preserving).
// Lowering an atom has the same declaration side effects as the direct translation, so factPreamble
// still sees every symbol the goal introduces.
func (tr *smtTranslator) lowerVCFormula(expr ast.Expr, env map[string]string) (vcFormula, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.lowerVCFormula(n.Inner, env)
	case *ast.BoolLit:
		if n.Value {
			return vcTrue{}, true
		}
		return vcFalse{}, true
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			inner, ok := tr.lowerVCFormula(n.Operand, env)
			if !ok {
				return nil, false
			}
			return vcMkNot(inner), true
		}
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND || n.Op == lexer.TOKEN_OR {
			l, ok := tr.lowerVCFormula(n.Left, env)
			if !ok {
				return nil, false
			}
			if n.Op == lexer.TOKEN_OR && isVCTrue(l) {
				return vcTrue{}, true
			}
			if n.Op == lexer.TOKEN_AND && isVCFalse(l) {
				return vcFalse{}, true
			}
			r, ok := tr.lowerVCFormula(n.Right, env)
			if !ok {
				return nil, false
			}
			if n.Op == lexer.TOKEN_AND {
				return vcMkAnd(l, r), true
			}
			return vcMkOr(l, r), true
		}
	}
	// Ordering comparisons lower into a first-class vcCompare over the term IR (foldable/normalizable).
	if bin, ok := expr.(*ast.BinaryExpr); ok {
		switch bin.Op {
		case lexer.TOKEN_GT, lexer.TOKEN_GTEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ:
			// A mixed-sign/width comparison is not a plain-value comparison at runtime; it falls
			// through to the atom path, whose boolTerm guards it (mixedSignGuard).
			if _, mixed := tr.mixedSignFitGuard(bin, ""); mixed {
				break
			}
			if l, ok := tr.lowerVCTerm(bin.Left, env); ok {
				if r, ok := tr.lowerVCTerm(bin.Right, env); ok {
					return vcMkCompare(bin.Op, l, r), true
				}
			}
		}
	}
	// Atom: delegate the remaining leaves to the existing translator (equality, quantifier, predicate).
	s, ok := tr.boolTerm(expr, env)
	if !ok {
		// GOAL GENERALIZATION (smtCheckGoal only): an atom outside the fragment becomes a fresh,
		// unconstrained Bool. The atoms reached here sit in the goal's propositional skeleton
		// (and/or/not), never under a quantifier, so the generalized goal G[b] proven for every b
		// entails G[atom]. Each occurrence is its own variable: no correlation is assumed. This is
		// never applied to hypotheses, where a free stand-in would not be implied by the fact.
		if tr.generalizeGoalAtoms {
			return vcMkAtom(tr.freshBoolAux()), true
		}
		return nil, false
	}
	return vcMkAtom(s), true
}

// --- Weakest-precondition transport (brick 3) -------------------------------------------------------
//
// WP over a straight-line scalar block computes the precondition of a postcondition Q by backward
// substitution: wp(x := e; rest, Q) = wp(rest, Q[x := e]). The substitution is single-pass per
// assignment (the replacement term's own variables are resolved by EARLIER assignments, processed in a
// later step), so processing the assignments in reverse threads each value back to the function's
// inputs. The smart constructors re-run on every rewrite, so the result is folded/normalized as it is
// built — `((x+1)*2) > 2` collapses toward a goal over the parameters alone.

// substVCTerm replaces every vcVar named `name` with `repl` (single pass — `repl` is not re-substituted).
func substVCTerm(t vcTerm, name string, repl vcTerm) vcTerm {
	switch tt := t.(type) {
	case vcVar:
		if tt.Name == name {
			return repl
		}
		return tt
	case vcSelect:
		return vcSelect{Array: tt.Array, Index: substVCTerm(tt.Index, name, repl)}
	case vcNeg:
		return vcMkNeg(substVCTerm(tt.Arg, name, repl))
	case vcArith:
		return vcMkArith(tt.Op, substVCTerm(tt.L, name, repl), substVCTerm(tt.R, name, repl), tt.WrapBits, tt.SignedWrap)
	case vcApply:
		// An uninterpreted-function application is rewritten in its ARGUMENTS only (the head symbol is
		// not a substitutable variable). The symbol `name` is a vcVar name; it cannot equal the head, so
		// no shadowing concern arises.
		return vcMkApply(tt.Sym, substVCTerms(tt.Args, name, repl))
	default:
		return t
	}
}

// substVCTerms substitutes into each term of a slice.
func substVCTerms(ts []vcTerm, name string, repl vcTerm) []vcTerm {
	if len(ts) == 0 {
		return ts
	}
	out := make([]vcTerm, len(ts))
	for i, t := range ts {
		out[i] = substVCTerm(t, name, repl)
	}
	return out
}

// substVCFormula pushes a substitution through the propositional structure and the comparison terms.
func substVCFormula(f vcFormula, name string, repl vcTerm) vcFormula {
	switch ff := f.(type) {
	case vcNot:
		return vcMkNot(substVCFormula(ff.Arg, name, repl))
	case vcAnd:
		return vcMkAnd(substVCFormula(ff.L, name, repl), substVCFormula(ff.R, name, repl))
	case vcOr:
		return vcMkOr(substVCFormula(ff.L, name, repl), substVCFormula(ff.R, name, repl))
	case vcCompare:
		return vcMkCompare(ff.Op, substVCTerm(ff.L, name, repl), substVCTerm(ff.R, name, repl))
	case vcApply:
		// Predicate application: rewrite the argument terms (the head predicate symbol is fixed).
		return vcMkApply(ff.Sym, substVCTerms(ff.Args, name, repl))
	case vcQuant:
		// The quantifier binder SHADOWS its body: if the replacement term mentions a bound symbol the
		// substitution would CAPTURE it, so decline to descend and leave the quantifier intact (sound —
		// WP only ever forgoes a rewrite). A free `name` (a vcVar Elisa name) and a binder `Sym` live in
		// distinct namespaces, so capture is normally impossible, but the conservative guard keeps the
		// transport safe if a future caller substitutes a symbol-bearing term. Triggers are NOT rewritten
		// here: they are instantiation hints, not part of the logical content, and the conservative guard
		// already keeps any binder-symbol-bearing replacement out of the whole quantifier.
		for _, b := range ff.Binders {
			if vcTermMentionsSym(repl, b.Sym) {
				return f
			}
		}
		return vcMkQuant(ff.Exists, ff.Binders, substVCFormula(ff.Body, name, repl), ff.Triggers)
	default:
		return f
	}
}

// vcTermMentionsSym reports whether the emitted form of `t` would contain the SMT symbol `sym` — used as
// the capture-avoidance guard before substituting under a quantifier binder.
func vcTermMentionsSym(t vcTerm, sym string) bool {
	switch tt := t.(type) {
	case vcVar:
		return tt.SMT == sym
	case vcSelect:
		return tt.Array == sym || vcTermMentionsSym(tt.Index, sym)
	case vcOpaque:
		return strings.Contains(tt.SMT, sym)
	case vcOldEntry:
		return strings.Contains(tt.SMT, sym)
	case vcApply:
		if tt.Sym == sym {
			return true
		}
		for _, a := range tt.Args {
			if vcTermMentionsSym(a, sym) {
				return true
			}
		}
	case vcNeg:
		return vcTermMentionsSym(tt.Arg, sym)
	case vcArith:
		return vcTermMentionsSym(tt.L, sym) || vcTermMentionsSym(tt.R, sym)
	}
	return false
}

// vcTermFullyStructural reports whether a term contains NO opaque leaf — every part is a literal, a
// substitutable variable, or arithmetic over those. Only such terms can be soundly WP-substituted (an
// opaque string might mention the assigned variable in a way substitution would silently miss).
func vcTermFullyStructural(t vcTerm) bool {
	switch tt := t.(type) {
	case vcIntLit, vcVar, vcOldEntry:
		return true
	case vcSelect:
		return tt.Array != "" && vcTermFullyStructural(tt.Index)
	case vcNeg:
		return vcTermFullyStructural(tt.Arg)
	case vcArith:
		return vcTermFullyStructural(tt.L) && vcTermFullyStructural(tt.R)
	default:
		return false // vcOpaque (or unknown)
	}
}

// vcFormulaFullyStructural reports whether a formula is built only from boolean constants, conjunction,
// disjunction, negation, and comparisons of fully-structural terms — no opaque atom. Required before WP
// substitution is trusted.
func vcFormulaFullyStructural(f vcFormula) bool {
	switch ff := f.(type) {
	case vcTrue, vcFalse:
		return true
	case vcNot:
		return vcFormulaFullyStructural(ff.Arg)
	case vcAnd:
		return vcFormulaFullyStructural(ff.L) && vcFormulaFullyStructural(ff.R)
	case vcOr:
		return vcFormulaFullyStructural(ff.L) && vcFormulaFullyStructural(ff.R)
	case vcCompare:
		return vcTermFullyStructural(ff.L) && vcTermFullyStructural(ff.R)
	default:
		return false // vcAtom (or unknown)
	}
}
