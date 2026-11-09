package semantic

import (
	"elisacore/src/lexer"
	"strings"
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

// vcBinder is one quantifier-bound variable: its Elisa-level SMT symbol (e.g. `q_x`) and its sort
// (e.g. `Int`). The binder shadows any same-symbol free occurrence inside the quantifier body.
type vcBinder struct {
	Sym  string
	Sort string
}

// vcQuant is a first-class quantifier — `forall`/`exists` binder(s) + body + optional E-matching
// triggers (brick 5: structural completeness for quantifiers). Previously every quantifier was a
// vcAtom holding a pre-built SMT string, so the IR could not see inside it; this node makes the binder,
// the body formula, and the trigger terms inspectable for future trigger management and
// WP-through-quantifier transport. Emission is byte-identical to the opaque path (see emitVCFormula).
type vcQuant struct {
	Exists   bool
	Binders  []vcBinder
	Body     vcFormula
	Triggers []vcTerm // optional `:pattern` terms; empty means no pattern (left to MBQI)
}

// vcApply is an uninterpreted function/predicate application — a symbol applied to argument terms,
// e.g. `(select arr i)` or `(p x y)`. Brick 1 kept these as opaque leaves (vcAtom for predicates,
// vcOpaque for functions); making them structural lets WP substitute into the arguments and lets future
// passes rewrite the spine. A nullary application (no args) emits just the bare symbol, exactly as the
// opaque leaf did.
type vcApply struct {
	Sym  string
	Args []vcTerm
}

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
func (vcQuant) isVCFormula()   {}

// vcApply is dual-sorted: it is a FORMULA when used as a predicate application and a TERM when used as
// an uninterpreted-function application. Both interfaces are satisfied so the one node serves both roles
// (the lowering picks the role by context), keeping emission identical to the two former opaque paths.
func (vcApply) isVCFormula() {}
func (vcApply) isVCTerm()    {}

// Term-level IR (brick 2): the arithmetic substrate the comparisons compare and that future bricks
// (weakest-precondition substitution, normalization) transform. Integer literals and the wrapping
// machine operators `+`/`-`/`*` are first-class; everything else (identifiers, field projections,
// div/mod, bitwise/shift, casts, array selects) is an OPAQUE term holding its already-translated string,
// so emission stays byte-compatible with the direct translation.
type vcTerm interface{ isVCTerm() }

type (
	vcIntLit struct{ Val int64 }
	vcOpaque struct{ SMT string }
	// vcSelect is an immutable sview byte read. Its array symbol is fixed by the
	// immutableSviewArrayParameter gate; keeping only the index structural lets
	// weakest-precondition substitution rewrite local index aliases without
	// exposing mutable array state to the WP lane.
	vcSelect struct {
		Array string
		Index vcTerm
	}
	// vcOldEntry is a contract `old(expr)` value — the ENTRY-time translation of expr, held as a fixed
	// SMT term. Unlike vcOpaque it is treated as fully-structural (an entry value is immutable, so it is
	// substitution-safe) yet WP never rewrites it: a bare `p` in the same clause is the EXIT value and is
	// threaded, while `old(p)` stays anchored to the entry value `v_p`.
	vcOldEntry struct{ SMT string }
	// vcVar is a substitutable integer variable (its Elisa name plus its SMT symbol). Weakest-
	// precondition transport (brick 3) replaces it with an assigned term; it emits its SMT symbol.
	vcVar struct{ Name, SMT string }
	vcNeg struct{ Arg vcTerm }
	// vcArith is a machine `+`/`-`/`*`. WrapBits>0 reproduces wrapMachineArith's wraparound at that width;
	// 0 means the result is provably in range (emit the clean operation). SignedWrap selects the
	// two's-complement recentered wrap `((raw+2^(W-1)) mod 2^W) - 2^(W-1)` over the unsigned `mod 2^W`.
	vcArith struct {
		Op         string
		L, R       vcTerm
		WrapBits   int
		SignedWrap bool
	}
)

func (vcIntLit) isVCTerm()   {}
func (vcOpaque) isVCTerm()   {}
func (vcSelect) isVCTerm()   {}
func (vcOldEntry) isVCTerm() {}
func (vcVar) isVCTerm()      {}
func (vcNeg) isVCTerm()      {}
func (vcArith) isVCTerm()    {}

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

// vcMkQuant constructs a quantifier, constant-folding the trivially-valid cases: a body that is already
// a boolean constant makes the quantifier that constant (`forall x. true` ≡ `true`, `forall x. false`
// ≡ `false` over a non-empty sort — and SMT sorts here are non-empty; symmetrically for `exists`), and
// an empty binder list degenerates to the body. These folds are sound regardless of triggers (a vacuous
// body needs no instantiation) and match what z3 would conclude, so emission stays behavior-preserving.
func vcMkQuant(exists bool, binders []vcBinder, body vcFormula, triggers []vcTerm) vcFormula {
	if len(binders) == 0 {
		return body
	}
	switch body.(type) {
	case vcTrue, vcFalse:
		return body
	}
	return vcQuant{Exists: exists, Binders: binders, Body: body, Triggers: triggers}
}

// vcMkApply constructs an uninterpreted application. There is nothing to fold (the symbol is
// uninterpreted), so it always builds the node; it exists for constructor-style symmetry and as the one
// place future argument normalization would hook in.
func vcMkApply(sym string, args []vcTerm) vcApply {
	return vcApply{Sym: sym, Args: args}
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
	case vcQuant:
		return emitVCQuant(ff)
	case vcApply:
		return emitVCApply(ff.Sym, ff.Args)
	case vcAtom:
		return ff.SMT
	default:
		return "true"
	}
}

// emitVCQuant renders a quantifier to the SAME SMT-LIB the opaque path emitted: `(forall ((x T) …)
// body)` with the body wrapped as `(! body :pattern (t1 t2 …))` when triggers are present. Binders are
// space-joined in order; this is byte-identical to boolTerm's quantifier emission.
func emitVCQuant(q vcQuant) string {
	kw := "forall"
	if q.Exists {
		kw = "exists"
	}
	decls := make([]string, len(q.Binders))
	for i, b := range q.Binders {
		decls[i] = "(" + b.Sym + " " + b.Sort + ")"
	}
	body := emitVCFormula(q.Body)
	if len(q.Triggers) > 0 {
		pats := make([]string, len(q.Triggers))
		for i, t := range q.Triggers {
			pats[i] = emitVCTerm(t)
		}
		body = "(! " + body + " :pattern (" + strings.Join(pats, " ") + "))"
	}
	return "(" + kw + " (" + strings.Join(decls, " ") + ") " + body + ")"
}

// emitVCApply renders an uninterpreted application: `(sym a b …)`, or the bare `sym` when nullary —
// identical to the opaque function/predicate leaf it replaces.
func emitVCApply(sym string, args []vcTerm) string {
	if len(args) == 0 {
		return sym
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = emitVCTerm(a)
	}
	return "(" + sym + " " + strings.Join(parts, " ") + ")"
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
func vcMkArith(op string, l, r vcTerm, wrapBits int, signedWrap bool) vcTerm {
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
	return vcArith{Op: op, L: l, R: r, WrapBits: wrapBits, SignedWrap: signedWrap}
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
// `< x x` is false, `<= x x` is true, etc.
//
// SOUNDNESS: reflexivity requires the operands to be STRUCTURALLY identical (vcTermsIdentical), not
// merely emit-equal. A bare `p` (vcVar, the EXIT value, threaded by WP) and `old(p)` (vcOldEntry, the
// ENTRY value, never threaded) emit the SAME symbol `v_p` yet denote DIFFERENT values once WP runs —
// folding `p >= old(p)` to `true` here would wrongly prove it after a decrement. Structural identity
// guarantees both operands transform identically, so the fold stays valid through substitution.
func vcMkCompare(op lexer.TokenKind, l, r vcTerm) vcFormula {
	if li, ok := l.(vcIntLit); ok {
		if ri, ok := r.(vcIntLit); ok {
			return vcBoolConst(compareInts(op, li.Val, ri.Val))
		}
	}
	if vcTermsIdentical(l, r) {
		switch op {
		case lexer.TOKEN_GTEQ, lexer.TOKEN_LTEQ:
			return vcTrue{}
		case lexer.TOKEN_GT, lexer.TOKEN_LT:
			return vcFalse{}
		}
	}
	return vcCompare{Op: op, L: l, R: r}
}

// vcTermsIdentical reports whether two terms are the SAME structure — so WP substitution rewrites them
// identically (the precondition for a sound reflexivity fold). A vcVar and a vcOldEntry that print the
// same symbol are NOT identical: one is threaded, the other anchored to the entry value.
func vcTermsIdentical(a, b vcTerm) bool {
	switch av := a.(type) {
	case vcIntLit:
		bv, ok := b.(vcIntLit)
		return ok && av.Val == bv.Val
	case vcVar:
		bv, ok := b.(vcVar)
		return ok && av.Name == bv.Name
	case vcOldEntry:
		bv, ok := b.(vcOldEntry)
		return ok && av.SMT == bv.SMT
	case vcOpaque:
		bv, ok := b.(vcOpaque)
		return ok && av.SMT == bv.SMT
	case vcSelect:
		bv, ok := b.(vcSelect)
		return ok && av.Array == bv.Array && vcTermsIdentical(av.Index, bv.Index)
	case vcNeg:
		bv, ok := b.(vcNeg)
		return ok && vcTermsIdentical(av.Arg, bv.Arg)
	case vcArith:
		bv, ok := b.(vcArith)
		return ok && av.Op == bv.Op && av.WrapBits == bv.WrapBits && av.SignedWrap == bv.SignedWrap &&
			vcTermsIdentical(av.L, bv.L) && vcTermsIdentical(av.R, bv.R)
	}
	return false
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
	case vcSelect:
		return "(select " + tt.Array + " " + emitVCTerm(tt.Index) + ")"
	case vcOldEntry:
		return tt.SMT
	case vcVar:
		return tt.SMT
	case vcApply:
		return emitVCApply(tt.Sym, tt.Args)
	case vcNeg:
		return "(- " + emitVCTerm(tt.Arg) + ")"
	case vcArith:
		raw := "(" + tt.Op + " " + emitVCTerm(tt.L) + " " + emitVCTerm(tt.R) + ")"
		if tt.WrapBits > 0 {
			if tt.SignedWrap {
				return smtSignedWrap(raw, tt.WrapBits)
			}
			return "(mod " + raw + " " + smtPow2(tt.WrapBits) + ")"
		}
		return raw
	default:
		return "0"
	}
}
