//go:build cgo

package semantic

import (
	"testing"

	"elisacore/src/lexer"
)

// The term IR folds constants, applies the at-any-width algebraic identities, and PRESERVES the
// machine-wrap `(mod …)` so a lowered arithmetic term is byte-identical to the direct translation.
func TestVCTermFoldEmit(t *testing.T) {
	x := vcOpaque{SMT: "v_x"}
	cases := []struct {
		name string
		got  vcTerm
		want string
	}{
		{"fold-add", vcMkArith("+", vcIntLit{2}, vcIntLit{3}, 0, false), "5"},
		{"fold-mul", vcMkArith("*", vcIntLit{4}, vcIntLit{5}, 0, false), "20"},
		{"add-zero-right", vcMkArith("+", x, vcIntLit{0}, 0, false), "v_x"},
		{"add-zero-left", vcMkArith("+", vcIntLit{0}, x, 0, false), "v_x"},
		{"sub-zero", vcMkArith("-", x, vcIntLit{0}, 0, false), "v_x"},
		{"mul-one", vcMkArith("*", x, vcIntLit{1}, 0, false), "v_x"},
		{"mul-zero", vcMkArith("*", x, vcIntLit{0}, 0, false), "0"},
		{"neg-lit", vcMkNeg(vcIntLit{7}), "(- 7)"},
		{"double-neg", vcMkNeg(vcMkNeg(x)), "v_x"},
		{"structural-add", vcMkArith("+", x, vcIntLit{1}, 0, false), "(+ v_x 1)"},
		{"wrap-preserved", vcMkArith("+", x, vcIntLit{1}, 64, false), "(mod (+ v_x 1) 18446744073709551616)"},
	}
	for _, tc := range cases {
		if got := emitVCTerm(tc.got); got != tc.want {
			t.Errorf("%s: emit = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Comparison folding: literal comparisons reduce to a boolean constant, and a comparison of identical
// operands folds by reflexivity (`< x x` false, `<= x x` true).
func TestVCCompareFold(t *testing.T) {
	x := vcOpaque{SMT: "v_x"}
	if !isVCTrue(vcMkCompare(lexer.TOKEN_GT, vcIntLit{5}, vcIntLit{3})) {
		t.Fatal("5 > 3 must fold to true")
	}
	if !isVCFalse(vcMkCompare(lexer.TOKEN_LT, vcIntLit{5}, vcIntLit{3})) {
		t.Fatal("5 < 3 must fold to false")
	}
	if !isVCFalse(vcMkCompare(lexer.TOKEN_LT, x, x)) {
		t.Fatal("x < x must fold to false")
	}
	if !isVCTrue(vcMkCompare(lexer.TOKEN_LTEQ, x, x)) {
		t.Fatal("x <= x must fold to true")
	}
	// A genuine comparison stays structural and emits via smtCompare.
	if got := emitVCFormula(vcMkCompare(lexer.TOKEN_GT, x, vcIntLit{0})); got != "(> v_x 0)" {
		t.Fatalf("structural compare emit = %q", got)
	}
}

// The VC IR's smart constructors fold the boolean constants and emit SMT-LIB identical to the direct
// translation for the non-constant structure — the property that makes wiring the IR into the discharge
// path behavior-preserving.
func TestVCFormulaSimplifyAndEmit(t *testing.T) {
	p := vcMkAtom("(> v_x 0)")
	q := vcMkAtom("(< v_x 10)")
	cases := []struct {
		name string
		got  vcFormula
		want string
	}{
		{"and-true-left", vcMkAnd(vcTrue{}, p), "(> v_x 0)"},
		{"and-true-right", vcMkAnd(p, vcTrue{}), "(> v_x 0)"},
		{"and-false", vcMkAnd(vcFalse{}, p), "false"},
		{"or-true", vcMkOr(vcTrue{}, p), "true"},
		{"or-false-left", vcMkOr(vcFalse{}, p), "(> v_x 0)"},
		{"not-true", vcMkNot(vcTrue{}), "false"},
		{"not-false", vcMkNot(vcFalse{}), "true"},
		{"double-negation", vcMkNot(vcMkNot(p)), "(> v_x 0)"},
		{"and-structural", vcMkAnd(p, q), "(and (> v_x 0) (< v_x 10))"},
		{"or-structural", vcMkOr(p, q), "(or (> v_x 0) (< v_x 10))"},
		{"atom-true-literal", vcMkAtom("true"), "true"},
		{"atom-false-literal", vcMkAtom("false"), "false"},
		{"not-structural", vcMkNot(p), "(not (> v_x 0))"},
	}
	for _, tc := range cases {
		if got := emitVCFormula(tc.got); got != tc.want {
			t.Errorf("%s: emit = %q, want %q", tc.name, got, tc.want)
		}
	}
	// A goal that folds to the `true` constant is what lets smtCheckGoal skip the solver entirely.
	if !isVCTrue(vcMkOr(p, vcTrue{})) {
		t.Fatalf("`p or true` must fold to the true constant")
	}
	if !isVCFalse(vcMkAnd(p, vcFalse{})) {
		t.Fatalf("`p and false` must fold to the false constant")
	}
}

// End-to-end: an obligation whose propositional structure folds to `true` is discharged via the VC IR
// without involving the solver, yet still proves. `ensure result >= 0 or true` reaches the SMT tier
// (boolean ensures have no const fast path) and lowers to the `true` constant.
func TestVCGoalTautologyShortCircuits(t *testing.T) {
	src := `
def always_ok(x: i64) -> i64:
    ensure result >= 0 or true
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "vc_taut.elisa", src,
		AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a goal that folds to `true` must discharge cleanly, got: %v", errs)
	}
}

// The structurally-complete quantifier node emits SMT-LIB byte-identical to the opaque boolTerm path:
// `(forall ((q_k Int)) body)`, an `exists`, and the `(! body :pattern (...))` trigger wrapping. Smart-
// constructor folding collapses a vacuous body and an empty binder list.
func TestVCQuantEmitAndFold(t *testing.T) {
	k := vcBinder{Sym: "q_k", Sort: "Int"}
	// body: (and (>= q_k 0) (< q_k 10)) built from comparisons over an opaque bound var.
	bk := vcOpaque{SMT: "q_k"}
	body := vcMkAnd(vcMkCompare(lexer.TOKEN_GTEQ, bk, vcIntLit{0}), vcMkCompare(lexer.TOKEN_LT, bk, vcIntLit{10}))

	// forall, no trigger — identical to boolTerm's `(forall (decls) body)`.
	if got := emitVCFormula(vcMkQuant(false, []vcBinder{k}, body, nil)); got != "(forall ((q_k Int)) (and (>= q_k 0) (< q_k 10)))" {
		t.Fatalf("forall emit = %q", got)
	}
	// exists.
	if got := emitVCFormula(vcMkQuant(true, []vcBinder{k}, body, nil)); got != "(exists ((q_k Int)) (and (>= q_k 0) (< q_k 10)))" {
		t.Fatalf("exists emit = %q", got)
	}
	// With an E-matching trigger — the `(! body :pattern (...))` wrapping boolTerm emits for array quants.
	trig := vcMkApply("select", []vcTerm{vcOpaque{SMT: "v_a"}, bk})
	want := "(forall ((q_k Int)) (! (and (>= q_k 0) (< q_k 10)) :pattern ((select v_a q_k))))"
	if got := emitVCFormula(vcMkQuant(false, []vcBinder{k}, body, []vcTerm{trig})); got != want {
		t.Fatalf("forall+trigger emit = %q, want %q", got, want)
	}
	// Folding: a vacuous body, and an empty binder list, degenerate.
	if !isVCTrue(vcMkQuant(false, []vcBinder{k}, vcTrue{}, nil)) {
		t.Fatal("forall x. true must fold to true")
	}
	if !isVCFalse(vcMkQuant(true, []vcBinder{k}, vcFalse{}, nil)) {
		t.Fatal("exists x. false must fold to false")
	}
	if got := emitVCFormula(vcMkQuant(false, nil, body, nil)); got != "(and (>= q_k 0) (< q_k 10))" {
		t.Fatalf("empty-binder quant must degenerate to body, got %q", got)
	}
}

// The uninterpreted-application node emits `(sym args)` as both a predicate (formula) and a function
// (term), and a nullary application emits the bare symbol — byte-identical to the former opaque leaves.
// Substitution rewrites the arguments; substitution under a binder is capture-avoiding.
func TestVCApplyEmitAndSubst(t *testing.T) {
	i := vcVar{Name: "i", SMT: "v_i"}
	// As a term: (select v_a v_i).
	app := vcMkApply("select", []vcTerm{vcOpaque{SMT: "v_a"}, i})
	if got := emitVCTerm(app); got != "(select v_a v_i)" {
		t.Fatalf("apply-as-term emit = %q", got)
	}
	// As a predicate formula: (p v_i).
	pred := vcMkApply("p", []vcTerm{i})
	if got := emitVCFormula(pred); got != "(p v_i)" {
		t.Fatalf("apply-as-formula emit = %q", got)
	}
	// Nullary application is the bare symbol.
	if got := emitVCFormula(vcMkApply("flag", nil)); got != "flag" {
		t.Fatalf("nullary apply emit = %q", got)
	}
	// Substitution rewrites the argument terms.
	subst := substVCTerm(app, "i", vcMkArith("+", i, vcIntLit{1}, 0, false))
	if got := emitVCTerm(subst); got != "(select v_a (+ v_i 1))" {
		t.Fatalf("apply arg subst emit = %q", got)
	}
	// Capture avoidance: substituting a term that mentions the bound symbol leaves the quantifier intact.
	q := vcMkQuant(false, []vcBinder{{Sym: "v_i", Sort: "Int"}}, vcMkCompare(lexer.TOKEN_GTEQ, i, vcIntLit{0}), nil)
	if got := emitVCFormula(substVCFormula(q, "j", i)); got != "(forall ((v_i Int)) (>= v_i 0))" {
		t.Fatalf("capture-avoiding subst changed the quantifier: %q", got)
	}
	// A free substitution that does NOT touch the binder descends into the body, re-running the smart
	// constructors: `i >= 0` with i->5 folds to true, which collapses the whole quantifier.
	q2 := vcMkQuant(false, []vcBinder{{Sym: "q_k", Sort: "Int"}}, vcMkCompare(lexer.TOKEN_GTEQ, i, vcIntLit{0}), nil)
	if got := emitVCFormula(substVCFormula(q2, "i", vcIntLit{5})); got != "true" {
		t.Fatalf("body subst under non-conflicting binder = %q", got)
	}
}

// End-to-end through the term IR: `result + 0 >= result` folds to `result >= result` (the x+0 identity)
// then to `true` by reflexivity — discharged with no solver call.
func TestVCTermIdentityShortCircuits(t *testing.T) {
	src := `
def stable(x: i64) -> i64:
    ensure result + 0 >= result
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "vc_identity.elisa", src,
		AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`result + 0 >= result` must fold to true and discharge, got: %v", errs)
	}
}
