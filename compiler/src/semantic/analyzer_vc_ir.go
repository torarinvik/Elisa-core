package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Verification-condition intermediate language (Boogie/Why3-style), brick 1: the propositional layer.
//
// A verification condition is LOWERED from the Elisa AST into a typed formula IR before it is emitted to
// SMT-LIB, rather than each discharge site building the SMT string directly. Brick 1 captures the
// propositional SKELETON — conjunction, disjunction, negation, and the boolean constants — as
// first-class nodes; every atom (a comparison, a quantifier, a predicate, an equality) is translated by
// the existing engine and held as an OPAQUE leaf (the already-emitted SMT-LIB string).
//
// Why a real IR rather than strings: a goal can be SIMPLIFIED by constant folding before the solver
// runs (a goal that reduces to `true` is discharged with NO solver call); the structure is available
// for future caching/normalization; and later bricks (a term-level IR, weakest-precondition transport
// across assignments, multi-goal batching, alternative backends) have an actual tree to transform.
// Opaque leaves keep emission byte-compatible with the direct translation, so wiring the IR into the
// discharge path is behavior-preserving — the existing proof suite is the regression guard.
type vcFormula interface{ isVCFormula() }

type (
	vcTrue  struct{}
	vcFalse struct{}
	vcNot   struct{ Arg vcFormula }
	vcAnd   struct{ L, R vcFormula }
	vcOr    struct{ L, R vcFormula }
	// vcAtom is an opaque, already-translated SMT-LIB boolean term (a comparison, quantifier, predicate,
	// …) that brick 1 does not decompose further.
	vcAtom struct{ SMT string }
)

// vcCompare is an ordering comparison between two IR terms (brick 2). Equality/inequality and pointer
// comparisons stay opaque atoms (their translation has null/bool-valued nuance brick 2 does not model).
type vcCompare struct {
	Op   lexer.TokenKind
	L, R vcTerm
}

func (vcTrue) isVCFormula()    {}
func (vcFalse) isVCFormula()   {}
func (vcNot) isVCFormula()     {}
func (vcAnd) isVCFormula()     {}
func (vcOr) isVCFormula()      {}
func (vcAtom) isVCFormula()    {}
func (vcCompare) isVCFormula() {}

// Term-level IR (brick 2): the arithmetic substrate the comparisons compare and that future bricks
// (weakest-precondition substitution, normalization) transform. Integer literals and the wrapping
// machine operators `+`/`-`/`*` are first-class; everything else (identifiers, field projections,
// div/mod, bitwise/shift, casts, array selects) is an OPAQUE term holding its already-translated string,
// so emission stays byte-compatible with the direct translation.
type vcTerm interface{ isVCTerm() }

type (
	vcIntLit struct{ Val int64 }
	vcOpaque struct{ SMT string }
	// vcVar is a substitutable integer variable (its Elisa name plus its SMT symbol). Weakest-
	// precondition transport (brick 3) replaces it with an assigned term; it emits its SMT symbol.
	vcVar struct{ Name, SMT string }
	vcNeg struct{ Arg vcTerm }
	// vcArith is a machine `+`/`-`/`*`. WrapBits>0 reproduces wrapMachineArith's unsigned wraparound
	// `(mod (op l r) 2^WrapBits)`; 0 means the result is provably in range (emit the clean operation).
	vcArith struct {
		Op       string
		L, R     vcTerm
		WrapBits int
	}
)

func (vcIntLit) isVCTerm() {}
func (vcOpaque) isVCTerm() {}
func (vcVar) isVCTerm()    {}
func (vcNeg) isVCTerm()    {}
func (vcArith) isVCTerm()  {}

// vcMkAtom wraps a translated SMT boolean term, folding the literals so `true`/`false` atoms become the
// IR constants (which then drive the structural simplifications below).
func vcMkAtom(smt string) vcFormula {
	switch smt {
	case "true":
		return vcTrue{}
	case "false":
		return vcFalse{}
	}
	return vcAtom{SMT: smt}
}

func vcMkNot(f vcFormula) vcFormula {
	switch ff := f.(type) {
	case vcTrue:
		return vcFalse{}
	case vcFalse:
		return vcTrue{}
	case vcNot:
		return ff.Arg // ¬¬p ≡ p
	}
	return vcNot{Arg: f}
}

func vcMkAnd(l, r vcFormula) vcFormula {
	if isVCFalse(l) || isVCFalse(r) {
		return vcFalse{}
	}
	if isVCTrue(l) {
		return r
	}
	if isVCTrue(r) {
		return l
	}
	return vcAnd{L: l, R: r}
}

func vcMkOr(l, r vcFormula) vcFormula {
	if isVCTrue(l) || isVCTrue(r) {
		return vcTrue{}
	}
	if isVCFalse(l) {
		return r
	}
	if isVCFalse(r) {
		return l
	}
	return vcOr{L: l, R: r}
}

func isVCTrue(f vcFormula) bool  { _, ok := f.(vcTrue); return ok }
func isVCFalse(f vcFormula) bool { _, ok := f.(vcFalse); return ok }

// vcConjuncts flattens a formula's top-level conjunction into independent sub-goals, dropping the
// trivially-true ones — `A ∧ (B ∧ C)` → [A, B, C]. A non-conjunction returns itself; a `vcTrue`
// returns the empty slice (nothing to prove). This is the splitter behind multi-goal batching (brick
// 4): proving every returned sub-goal proves the original conjunction, so the split is SOUND, and
// because each sub-goal is a strictly smaller query the solver decides them where it might time out
// (return `unknown`) on the whole conjunction — splitting only ever proves MORE. It also lets a failed
// conjunction report exactly WHICH conjunct fails, with that conjunct's own counterexample.
func vcConjuncts(f vcFormula) []vcFormula {
	switch ff := f.(type) {
	case vcTrue:
		return nil
	case vcAnd:
		return append(vcConjuncts(ff.L), vcConjuncts(ff.R)...)
	}
	return []vcFormula{f}
}

// emitVCFormula renders a formula as SMT-LIB. For the propositional nodes it emits exactly what the
// direct translation would; atoms emit their stored string verbatim — so a goal with no foldable
// constant is byte-identical to the pre-IR path.
func emitVCFormula(f vcFormula) string {
	switch ff := f.(type) {
	case vcTrue:
		return "true"
	case vcFalse:
		return "false"
	case vcNot:
		return "(not " + emitVCFormula(ff.Arg) + ")"
	case vcAnd:
		return "(and " + emitVCFormula(ff.L) + " " + emitVCFormula(ff.R) + ")"
	case vcOr:
		return "(or " + emitVCFormula(ff.L) + " " + emitVCFormula(ff.R) + ")"
	case vcCompare:
		return smtCompare(ff.Op, emitVCTerm(ff.L), emitVCTerm(ff.R))
	case vcAtom:
		return ff.SMT
	default:
		return "true"
	}
}

// vcMkNeg folds a negated literal and cancels double negation.
func vcMkNeg(t vcTerm) vcTerm {
	switch tt := t.(type) {
	case vcIntLit:
		return vcIntLit{Val: -tt.Val}
	case vcNeg:
		return tt.Arg
	}
	return vcNeg{Arg: t}
}

// vcMkArith constructs a machine arithmetic term, constant-folding when both operands are literals and
// the result provably does not wrap (WrapBits==0), and applying the algebraic identities that hold at
// any width (`x+0`, `x-0`, `x*1`, `x*0`). Folding never crosses a wrap: a wrapping op keeps its
// structure so the `(mod …)` is emitted.
func vcMkArith(op string, l, r vcTerm, wrapBits int) vcTerm {
	li, lIsLit := l.(vcIntLit)
	ri, rIsLit := r.(vcIntLit)
	if wrapBits == 0 && lIsLit && rIsLit {
		if v, ok := foldArith(op, li.Val, ri.Val); ok {
			return vcIntLit{Val: v}
		}
	}
	// Identities are sound at any width: adding/subtracting 0 and multiplying by 1 cannot wrap, and
	// multiplying by 0 is 0. They simplify substituted/derived terms without a solver.
	switch op {
	case "+":
		if lIsLit && li.Val == 0 {
			return r
		}
		if rIsLit && ri.Val == 0 {
			return l
		}
	case "-":
		if rIsLit && ri.Val == 0 {
			return l
		}
	case "*":
		if (lIsLit && li.Val == 0) || (rIsLit && ri.Val == 0) {
			return vcIntLit{Val: 0}
		}
		if lIsLit && li.Val == 1 {
			return r
		}
		if rIsLit && ri.Val == 1 {
			return l
		}
	}
	return vcArith{Op: op, L: l, R: r, WrapBits: wrapBits}
}

// foldArith evaluates a constant machine op in int64 with overflow detection; on overflow it declines
// (keeps the term structural) rather than fold a wrong value.
func foldArith(op string, a, b int64) (int64, bool) {
	switch op {
	case "+":
		s := a + b
		if (s > a) != (b > 0) {
			return 0, false
		}
		return s, true
	case "-":
		d := a - b
		if (d < a) != (b > 0) {
			return 0, false
		}
		return d, true
	case "*":
		if a == 0 || b == 0 {
			return 0, true
		}
		p := a * b
		if p/b != a {
			return 0, false
		}
		return p, true
	}
	return 0, false
}

// vcMkCompare folds a comparison of two literals and recognizes reflexivity (identical operands) — a
// `< x x` is false, `<= x x` is true, etc. Operand identity is checked by emitted form, which is sound
// for the side-effect-free integer terms compared here.
func vcMkCompare(op lexer.TokenKind, l, r vcTerm) vcFormula {
	if li, ok := l.(vcIntLit); ok {
		if ri, ok := r.(vcIntLit); ok {
			return vcBoolConst(compareInts(op, li.Val, ri.Val))
		}
	}
	if emitVCTerm(l) == emitVCTerm(r) {
		switch op {
		case lexer.TOKEN_GTEQ, lexer.TOKEN_LTEQ:
			return vcTrue{}
		case lexer.TOKEN_GT, lexer.TOKEN_LT:
			return vcFalse{}
		}
	}
	return vcCompare{Op: op, L: l, R: r}
}

func vcBoolConst(b bool) vcFormula {
	if b {
		return vcTrue{}
	}
	return vcFalse{}
}

func compareInts(op lexer.TokenKind, a, b int64) bool {
	switch op {
	case lexer.TOKEN_GT:
		return a > b
	case lexer.TOKEN_GTEQ:
		return a >= b
	case lexer.TOKEN_LT:
		return a < b
	case lexer.TOKEN_LTEQ:
		return a <= b
	}
	return false
}

// emitVCTerm renders a term as SMT-LIB, reproducing wrapMachineArith's `(mod …)` for wrapping ops so a
// lowered arithmetic term is byte-identical to the direct translation.
func emitVCTerm(t vcTerm) string {
	switch tt := t.(type) {
	case vcIntLit:
		return smtInt(tt.Val)
	case vcOpaque:
		return tt.SMT
	case vcVar:
		return tt.SMT
	case vcNeg:
		return "(- " + emitVCTerm(tt.Arg) + ")"
	case vcArith:
		raw := "(" + tt.Op + " " + emitVCTerm(tt.L) + " " + emitVCTerm(tt.R) + ")"
		if tt.WrapBits > 0 {
			return "(mod " + raw + " " + smtPow2(tt.WrapBits) + ")"
		}
		return raw
	default:
		return "0"
	}
}

// arithWrapBits mirrors wrapMachineArith's wrap decision: an unsigned `+`/`-`/`*` result of width 1..64
// that is not provably in range wraps modulo 2^width; everything else emits clean (0).
func (tr *smtTranslator) arithWrapBits(n *ast.BinaryExpr) int {
	signed, bits, ok := smtIntWidthSign(tr.a.exprTypes[n])
	if !ok || signed || bits <= 0 || bits > 64 {
		return 0
	}
	if tr.a.provablyNoArithWrap(n, signed, bits) {
		return 0
	}
	return bits
}

// lowerVCTerm lowers an integer Elisa expression into the term IR: literals and the wrapping machine
// operators become structural nodes; everything else delegates to termEnv as an opaque term (exact
// string, same symbol declarations). Declining (ok=false) only when termEnv itself declines.
func (tr *smtTranslator) lowerVCTerm(expr ast.Expr, env map[string]string) (vcTerm, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.lowerVCTerm(n.Inner, env)
	case *ast.IntLit:
		if c, ok := tr.a.constIntValue(n); ok {
			return vcIntLit{Val: c}, true
		}
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
					return vcMkArith(op, l, r, tr.arithWrapBits(n)), true
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
	case vcNeg:
		return vcMkNeg(substVCTerm(tt.Arg, name, repl))
	case vcArith:
		return vcMkArith(tt.Op, substVCTerm(tt.L, name, repl), substVCTerm(tt.R, name, repl), tt.WrapBits)
	default:
		return t
	}
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
	default:
		return f
	}
}

// vcTermFullyStructural reports whether a term contains NO opaque leaf — every part is a literal, a
// substitutable variable, or arithmetic over those. Only such terms can be soundly WP-substituted (an
// opaque string might mention the assigned variable in a way substitution would silently miss).
func vcTermFullyStructural(t vcTerm) bool {
	switch tt := t.(type) {
	case vcIntLit, vcVar:
		return true
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
