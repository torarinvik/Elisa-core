//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func analyzeContractStrict(t *testing.T, name, src string) *Result {
	t.Helper()
	return analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src,
		AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
}

// SOUNDNESS: a branch-condition fact must NOT leak past the `if`. With an early return inside the
// then-branch, the fall-through carries the NEGATED condition (`x <= 10`), not the truthy one — so a
// postcondition that fails there (`result > 5` with x unconstrained below 10) must be reported. The
// previous bug recorded `x > 10` in the function scope, where it contradicted the negation and proved
// any postcondition by ex falso.
func TestEnsureNotProvenAtBranchFallThrough(t *testing.T) {
	src := `
def h(x: i64) -> i64:
    ensure result > 5
    if x > 10:
        return x
    return x
`
	result := analyzeContractStrict(t, "fallthrough_unsound.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("the fall-through return cannot satisfy `result > 5`; it must error, got: %v", result.Errors())
	}
}

// The legitimate counterpart still works: inside the then-branch `x > 10` proves `return x`, and the
// fall-through `return 6` satisfies `result > 5` directly — clean, no contradiction.
func TestEnsureProvenInBranchAndFallThrough(t *testing.T) {
	src := `
def h(x: i64) -> i64:
    ensure result > 5
    if x > 10:
        return x
    return 6
`
	result := analyzeContractStrict(t, "fallthrough_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("the in-branch and fall-through returns are both provable; got: %v", errs)
	}
}

// The fall-through guard fact is genuinely available to the SMT prover: after `if a == 0: return`, the
// continuation legitimately assumes `a != 0`, so a division-style postcondition that needs it proves.
func TestFallThroughGuardFactUsableBySMT(t *testing.T) {
	src := `
def safe(a: u64, b: u64) -> u64:
    ensure result <= a
    if b == 0:
        return 0
    return a / b
`
	result := analyzeContractStrict(t, "guard_div.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`a / b` with the fall-through `b != 0` guard proves `result <= a`; got: %v", errs)
	}
}

// SOUNDNESS: a reassignment whose RHS reads its own target (`y <- y - 1`) must not record the
// self-referential equality `y == y - 1`, which is contradictory and would prove any postcondition.
// `result > 0` does not hold (x=1 -> y=0), so it must error; `result >= 0` DOES hold and is proven
// (by WP threading `(x-1) >= 0` from `x > 0`).
func TestSelfReferentialAssignmentNotContradiction(t *testing.T) {
	bad := `
def f(x: i64) -> i64:
    requires x > 0
    ensure result > 0
    y: mutable i64 = x
    y <- y - 1
    return y
`
	if !strings.Contains(strings.Join(analyzeContractStrict(t, "selfref_bad.elisa", bad).Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("`y <- y - 1` must not prove `result > 0` via a self-referential contradiction")
	}
	good := `
def f(x: i64) -> i64:
    requires x > 0
    ensure result >= 0
    y: mutable i64 = x
    y <- y - 1
    return y
`
	if errs := analyzeContractStrict(t, "selfref_good.elisa", good).Errors(); len(errs) != 0 {
		t.Fatalf("`result >= 0` follows from x>0 via WP; got: %v", errs)
	}
}
