//go:build cgo

package semantic

import "testing"

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
