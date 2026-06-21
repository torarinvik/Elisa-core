//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/85 refinement flow-narrowing: inside `if x is NonNeg:` the law's body (`x >= 0`) is recorded as
// an assumable SMT flow fact, so a raw comparison `assert x >= 0` discharges (no proof hole).
func TestRefinementNarrowingAssertProvesInsideBranch(t *testing.T) {
	src := `
law NonNeg(x: i64) = x >= 0

def use_it(x: i64) -> i64:
    if x is NonNeg:
        assert x >= 0
        return x
    return 0
`
	result := analyzeProofHole(t, "narrow_then.elisa", src)
	joined := strings.Join(result.Warnings(), "\n")
	if strings.Contains(joined, "proof hole") {
		t.Fatalf("the assert inside the `is NonNeg` branch should discharge from the narrowed law fact, got: %v", result.Warnings())
	}
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

// SOUNDNESS: the same assert OUTSIDE the branch must NOT prove — the narrowed law fact lives only in the
// branch where the condition holds, so a proof-hole hint is surfaced.
func TestRefinementNarrowingAssertFailsOutsideBranch(t *testing.T) {
	src := `
law NonNeg(x: i64) = x >= 0

def use_it(x: i64, flag: bool) -> i64:
    if flag and x is NonNeg:
        return 1
    assert x >= 0
    return 0
`
	result := analyzeProofHole(t, "narrow_outside.elisa", src)
	joined := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(joined, "proof hole") {
		t.Fatalf("the assert AFTER the branch must NOT be provable from a fact scoped to the branch, got: %v", result.Warnings())
	}
}

// A compound condition `if x is NonNeg and y > 0:` narrows BOTH conjuncts in the then branch: the law
// fact from `x is NonNeg` and the comparison fact from `y > 0`.
func TestRefinementNarrowingCompoundNarrowsBoth(t *testing.T) {
	src := `
law NonNeg(x: i64) = x >= 0

def use_both(x: i64, y: i64) -> i64:
    if x is NonNeg and y > 0:
        assert x >= 0
        assert y > 0
        return x + y
    return 0
`
	result := analyzeProofHole(t, "narrow_compound.elisa", src)
	joined := strings.Join(result.Warnings(), "\n")
	if strings.Contains(joined, "proof hole") {
		t.Fatalf("both conjuncts should be narrowed in the then branch, got: %v", result.Warnings())
	}
}

// The ELSE branch of a decidable law records the negation: under `if x is NonNeg: ... else:` the else
// knows `not (x >= 0)`, i.e. `x < 0`, so `assert x < 0` discharges.
func TestRefinementNarrowingElseRecordsNegation(t *testing.T) {
	src := `
law NonNeg(x: i64) = x >= 0

def classify(x: i64) -> i64:
    if x is NonNeg:
        return 1
    else:
        assert x < 0
        return 0
`
	result := analyzeProofHole(t, "narrow_else.elisa", src)
	joined := strings.Join(result.Warnings(), "\n")
	if strings.Contains(joined, "proof hole") {
		t.Fatalf("the else branch should know the negated law `x < 0`, got: %v", result.Warnings())
	}
}

// A parametric law narrows with its bracket args substituted: under `if x is Bounded[0, 10]:` the body
// `x >= 0 and x < 10` is the flow fact, so `assert x < 10` discharges inside the branch.
func TestRefinementNarrowingParametricLaw(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self < hi

def clamp(x: i64) -> i64:
    if x is Bounded[0, 10]:
        assert x < 10
        assert x >= 0
        return x
    return 0
`
	result := analyzeProofHole(t, "narrow_parametric.elisa", src)
	joined := strings.Join(result.Warnings(), "\n")
	if strings.Contains(joined, "proof hole") {
		t.Fatalf("the parametric law body should narrow both bounds in the then branch, got: %v", result.Warnings())
	}
}
