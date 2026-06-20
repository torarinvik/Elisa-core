//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// feat/recursive-axiom-b: a PURE, TOTAL function's defining equation `f(args) == body[params:=args]`
// is made available to the SMT tier so the prover can reason THROUGH a `def` (and, for a terminating
// recursive function, perform induction). Soundness rests on two airtight gates enforced by
// functionDefiningEquationEligible: PURITY (single integer-returning `return <pure-expr>` body, no
// mutation/loops/effects) and TOTALITY (any recursive function must carry a VERIFIED `decreases`).
//
// The tests below pin BOTH the positive capability and the soundness-negative gates. Soundness is
// paramount here: an inconsistent function axiom proves anything, so every negative must hold.

// POSITIVE: the defining equation lets a caller's `ensure` see THROUGH a helper that has NO `ensure`
// of its own. `dbl` is pure/total with body `x + x`; only its defining equation (dbl(n) == n + n) can
// discharge `result == n + n`. (The pre-existing callee-ensure-assumption path contributes nothing —
// `dbl` declares no ensure.)
func TestDefiningEquationSeesThroughHelper(t *testing.T) {
	src := `
def dbl(x: i64) -> i64:
    return x + x

def f(n: i64) -> i64:
    ensure result == n + n
    return dbl(n)
`
	r := analyzeContractStrict(t, "eqn_helper.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("the pure helper's defining equation should discharge `result == n + n`, got: %v", errs)
	}
}

// POSITIVE (induction): a recursive PURE TOTAL function proves an inductive `ensure` from its own body
// equation plus the inductive hypothesis (the callee-ensure at the strictly-smaller argument). `twice`
// terminates (`decreases n`), so its equation is sound; `result == twice(n-1) + 2` together with the
// IH `twice(n-1) == 2*(n-1)` closes `result == 2 * n`.
func TestRecursivePureInductiveEnsure(t *testing.T) {
	src := `
def twice(n: i64) -> i64:
    requires n >= 0
    ensure result == 2 * n
    decreases n
    if n == 0:
        return 0
    return twice(n - 1) + 2
`
	r := analyzeContractStrict(t, "eqn_twice.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("expected the inductive ensure to prove for a terminating pure recursive function, got: %v", errs)
	}
}

// SOUNDNESS: the equation must be HONEST, not vacuously-true. `dbl(n) == n + n`, so a caller claiming
// `result == n + n + 1` is FALSE and must be REFUTED even though `dbl` IS axiomatized. (Guards against
// an equation that accidentally over-constrains/contradicts and proves everything.)
func TestDefiningEquationRefutesFalseClaim(t *testing.T) {
	src := `
def dbl(x: i64) -> i64:
    return x + x

def f(n: i64) -> i64:
    ensure result == n + n + 1
    return dbl(n)
`
	r := analyzeContractStrict(t, "eqn_false.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("a false claim about a pure function must be refuted; the equation must be honest")
	}
}

// SOUNDNESS (purity gate): an IMPURE helper (multi-statement body — here a local binding, so not a
// single pure `return <expr>`) is NOT eligible, so NO defining equation is emitted. The caller's
// `ensure result == n + n` then has no way to prove and MUST fail. This is the control that shows the
// equation — not some other path — is doing the work, and that the purity gate is enforced.
func TestImpureHelperNotAxiomatized(t *testing.T) {
	src := `
def dbl(x: i64) -> i64:
    y: i64 = x + x
    return y

def f(n: i64) -> i64:
    ensure result == n + n
    return dbl(n)
`
	r := analyzeContractStrict(t, "eqn_impure.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("an impure (non single-pure-return) helper must NOT be axiomatized, so the ensure must not prove")
	}
}

// SOUNDNESS (totality gate): a NON-TERMINATING recursive function `bad(n) == bad(n) + 1` has an
// INCONSISTENT defining equation (it implies 0 == 1). It carries no `decreases`, so the totality gate
// rejects it and NO equation is emitted. A caller exploiting the (would-be) inconsistency to prove an
// arbitrary falsehood (`result == 999`) MUST fail. This is the central soundness test: an axiomatized
// non-terminating function would let the prover prove ANYTHING.
func TestNonTerminatingFunctionNotAxiomatized(t *testing.T) {
	src := `
def bad(n: i64) -> i64:
    return bad(n) + 1

def exploit(n: i64) -> i64:
    ensure result == 999
    return bad(n)
`
	r := analyzeContractStrict(t, "eqn_bad.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("a non-terminating function's inconsistent equation must NOT be axiomatized (would prove anything)")
	}
}

// SOUNDNESS (totality gate, take 2): the SAME non-terminating shape WITH a bogus `decreases n` is
// rejected at the termination check itself (the measure cannot be shown to decrease at the self-call),
// so it never becomes eligible — and the exploit still fails.
func TestBogusDecreasesRejectedNotAxiomatized(t *testing.T) {
	src := `
def bad(n: i64) -> i64:
    decreases n
    return bad(n) + 1

def exploit(n: i64) -> i64:
    ensure result == 999
    return bad(n)
`
	r := analyzeContractStrict(t, "eqn_bad_dec.elisa", src)
	errs := r.Errors()
	if len(errs) == 0 {
		t.Fatalf("a bogus decreases must be rejected and the function must not be axiomatized")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "decreases") {
		t.Fatalf("expected a termination (decreases) error, got: %v", errs)
	}
}
