//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/lexer"
)

// vcConjuncts flattens a nested top-level conjunction into independent sub-goals and drops the
// trivially-true ones; a non-conjunction returns itself, and `true` returns nothing to prove.
func TestVCConjunctsSplit(t *testing.T) {
	a := vcAtom{SMT: "a"}
	b := vcAtom{SMT: "b"}
	c := vcAtom{SMT: "c"}

	// (a ∧ (b ∧ c)) → [a, b, c]
	nested := vcAnd{L: a, R: vcAnd{L: b, R: c}}
	got := vcConjuncts(nested)
	if len(got) != 3 || emitVCFormula(got[0]) != "a" || emitVCFormula(got[1]) != "b" || emitVCFormula(got[2]) != "c" {
		t.Fatalf("nested conjunction must split to [a b c], got %d: %v", len(got), emitConjuncts(got))
	}

	// A true conjunct is dropped (nothing to prove for it).
	withTrue := vcAnd{L: a, R: vcTrue{}}
	got = vcConjuncts(withTrue)
	if len(got) != 1 || emitVCFormula(got[0]) != "a" {
		t.Fatalf("a ∧ true must split to [a], got %v", emitConjuncts(got))
	}

	// A whole `true` formula has nothing to prove.
	if g := vcConjuncts(vcTrue{}); len(g) != 0 {
		t.Fatalf("true must split to the empty slice, got %v", emitConjuncts(g))
	}

	// A non-conjunction (a disjunction here) is its own single goal — NOT split (only top-level ∧ splits).
	disj := vcOr{L: a, R: b}
	got = vcConjuncts(disj)
	if len(got) != 1 || emitVCFormula(got[0]) != "(or a b)" {
		t.Fatalf("a disjunction must stay one goal, got %v", emitConjuncts(got))
	}

	// A comparison stays a single goal.
	cmp := vcMkCompare(lexer.TOKEN_GT, vcVar{Name: "x", SMT: "v_x"}, vcIntLit{0})
	if g := vcConjuncts(cmp); len(g) != 1 {
		t.Fatalf("a comparison must be one goal, got %v", emitConjuncts(g))
	}
}

func emitConjuncts(fs []vcFormula) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = emitVCFormula(f)
	}
	return out
}

// A conjunctive postcondition whose conjuncts are each independently provable discharges through the
// batched path (each conjunct is a separate query over the shared `requires` hypothesis).
func TestBatchedConjunctiveEnsureProves(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    requires x > 0
    ensure result > 0 and result <= x
    return x
`
	if errs := analyzeContractStrict(t, "batch_ok.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("both conjuncts (`result > 0`, `result <= x`) follow from x>0 and result=x; got: %v", errs)
	}
}

// When one conjunct of a batched goal is false, the batch fails (it is not masked by the provable
// conjunct), and the diagnostic pins the failure rather than silently passing.
func TestBatchedConjunctiveEnsureFailsOnOneConjunct(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    requires x > 0
    ensure result > 0 and result > x
    return x
`
	errs := strings.Join(analyzeContractStrict(t, "batch_bad.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("`result > x` is false at result=x, so the batched goal must fail; got: %v", errs)
	}
}
