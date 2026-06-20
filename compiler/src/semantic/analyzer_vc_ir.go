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

func (vcTrue) isVCFormula()  {}
func (vcFalse) isVCFormula() {}
func (vcNot) isVCFormula()   {}
func (vcAnd) isVCFormula()   {}
func (vcOr) isVCFormula()    {}
func (vcAtom) isVCFormula()  {}

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
	case vcAtom:
		return ff.SMT
	default:
		return "true"
	}
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
	// Atom: delegate the leaf to the existing translator (comparison, quantifier, equality, predicate).
	s, ok := tr.boolTerm(expr, env)
	if !ok {
		return nil, false
	}
	return vcMkAtom(s), true
}
