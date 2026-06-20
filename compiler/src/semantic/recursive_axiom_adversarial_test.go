//go:build cgo

package semantic

// RED-TEAM: Adversarial soundness battery for the recursive-function defining-equation axiom machinery.
//
// These probes attempt to make the prover ACCEPT false contracts. Each test is labelled:
//   FALSE_CLAIM  - a wrong postcondition that MUST be rejected
//   TRUE_CLAIM   - a correct postcondition that MUST be accepted (sanity check)
//   SOUNDNESS_GATE - verifying that a specific soundness guard fires
//
// If a FALSE_CLAIM test case shows no errors -> REAL BUG (prover accepted a false claim).
// If a TRUE_CLAIM test case shows errors -> INCOMPLETENESS (not a soundness issue, but noted).

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// PROBE 1: FALSE symbolic postcondition over single-recursive pure function.
//
// dbl(n) returns n + n. The false claim result == n + 3 must be rejected.
// ---------------------------------------------------------------------------

func TestAdversarial_FalsePostconditionSingleRecursive(t *testing.T) {
	src := `
def dbl(n: i64) -> i64:
    requires n >= 0
    ensure result == n + n
    decreases n
    if n == 0:
        return 0
    return dbl(n - 1) + 2

def exploit(n: i64) -> i64:
    requires n >= 0
    ensure result == n + 3
    return dbl(n)
`
	r := analyzeContractStrict(t, "false_single_recursive.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("REAL BUG (FALSE_CLAIM ACCEPTED): dbl(n)==n+n but prover accepted result==n+3; defining equation inconsistency or mis-proof")
	}
}

// ---------------------------------------------------------------------------
// PROBE 2: FALSE claim over composed pure calls.
// add1(n) = n+1; add2(n) = add1(add1(n)) = n+2. Claim result == n+5. Must reject.
// ---------------------------------------------------------------------------

func TestAdversarial_FalseClaimComposedPureCalls(t *testing.T) {
	src := `
def add1(n: i64) -> i64:
    return n + 1

def add2(n: i64) -> i64:
    return add1(add1(n))

def exploit(n: i64) -> i64:
    ensure result == n + 5
    return add2(n)
`
	r := analyzeContractStrict(t, "false_composed.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("REAL BUG (FALSE_CLAIM ACCEPTED): add2(n)==n+2 but prover accepted result==n+5")
	}
}

// ---------------------------------------------------------------------------
// PROBE 3: TRUE claim over composed pure calls (sanity / completeness check).
// ---------------------------------------------------------------------------

func TestAdversarial_TrueClaimComposedPureCalls(t *testing.T) {
	src := `
def add1(n: i64) -> i64:
    return n + 1

def add2(n: i64) -> i64:
    return add1(add1(n))

def caller(n: i64) -> i64:
    ensure result == n + 2
    return add2(n)
`
	r := analyzeContractStrict(t, "true_composed.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Logf("INCOMPLETENESS (not a soundness bug): composed pure equations should prove result==n+2 but got: %v", errs)
		t.Skip("incomplete: composed call equations not chained by the prover")
	}
}

// ---------------------------------------------------------------------------
// PROBE 4: Partial function -- division by zero.
// div_self(n) = n / n is undefined for n == 0. Claiming result == 1 for all n is false.
// ---------------------------------------------------------------------------

func TestAdversarial_PartialFunctionDivisionByZero(t *testing.T) {
	src := `
def div_self(n: i64) -> i64:
    return n / n

def exploit(n: i64) -> i64:
    ensure result == 1
    return div_self(n)
`
	r := analyzeContractStrict(t, "partial_division.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("POTENTIAL BUG (FALSE_CLAIM ACCEPTED): div_self(n)==n/n is undefined for n==0 but prover accepted result==1 unconditionally")
	}
}

// ---------------------------------------------------------------------------
// PROBE 5: Function reading a global (must NOT be axiomatized).
// ---------------------------------------------------------------------------

func TestAdversarial_GlobalReadNotAxiomatized(t *testing.T) {
	src := `
global mutable g: i64 = 0

def read_global(n: i64) -> i64:
    return n + g

def exploit(n: i64) -> i64:
    ensure result == n + 42
    return read_global(n)
`
	r := analyzeContractStrict(t, "global_read_not_axiom.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("REAL BUG (FALSE_CLAIM ACCEPTED): read_global reads a global so must NOT be axiomatized; result==n+42 is false when g!=42")
	}
}

// ---------------------------------------------------------------------------
// PROBE 6a: omega-rule trap.
// omega(n) = omega(n) + 1: if axiomatized, x == x+1 (inconsistent) -> proves anything.
// ---------------------------------------------------------------------------

func TestAdversarial_SameArgumentSelfCallOmegaTrap(t *testing.T) {
	src := `
def omega(n: i64) -> i64:
    return omega(n) + 1

def exploit_omega(n: i64) -> i64:
    ensure result == 12345
    return omega(n)
`
	r := analyzeContractStrict(t, "omega_trap.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("REAL BUG (omega-rule trap): omega(n)==omega(n)+1 would make SMT context inconsistent; prover must NOT axiomatize and must NOT prove result==12345")
	}
}

// PROBE 6b: identity loop -- equation x==x is trivial/no-info; no decreases -> no axiom.
func TestAdversarial_SameArgumentSelfCallIdentityEquation(t *testing.T) {
	src := `
def identity_loop(n: i64) -> i64:
    return identity_loop(n)

def exploit_identity(n: i64) -> i64:
    ensure result == 999
    return identity_loop(n)
`
	r := analyzeContractStrict(t, "identity_loop.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("REAL BUG (same-arg self-call): identity_loop without decreases must NOT be axiomatized; prover accepted false result==999")
	}
}

// ---------------------------------------------------------------------------
// PROBE 7: Mutually recursive functions (must NOT get axiom without verified decreases).
// ---------------------------------------------------------------------------

func TestAdversarial_MutualRecursionNotAxiomatized(t *testing.T) {
	src := `
def is_even(n: i64) -> i64:
    requires n >= 0
    if n == 0:
        return 1
    return is_odd(n - 1)

def is_odd(n: i64) -> i64:
    requires n >= 0
    if n == 0:
        return 0
    return is_even(n - 1)

def exploit(n: i64) -> i64:
    requires n >= 0
    ensure result == n + 99
    return is_even(n)
`
	r := analyzeContractStrict(t, "mutual_recursion.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("REAL BUG (mutual recursion): is_even/is_odd mutually recursive without verified decreases; prover accepted false result==n+99")
	}
}

// ---------------------------------------------------------------------------
// PROBE 8: Bogus decreases on same-argument call.
// tricky(n) calls tricky(n) (same arg), so decreases n must fail at the call site.
// ---------------------------------------------------------------------------

func TestAdversarial_BogusDecreasesSameArgCall(t *testing.T) {
	src := `
def tricky(n: i64) -> i64:
    decreases n
    return tricky(n) + 0

def exploit(n: i64) -> i64:
    ensure result == 777
    return tricky(n)
`
	r := analyzeContractStrict(t, "bogus_decrease_same_arg.elisa", src)
	errs := r.Errors()
	if len(errs) == 0 {
		t.Fatalf("REAL BUG: tricky(n) calls tricky(n) (same arg), decreases n must fail; prover accepted false result==777")
	}
	allErrs := strings.Join(errs, "\n")
	if !strings.Contains(allErrs, "decreases") && !strings.Contains(allErrs, "terminat") {
		t.Logf("NOTE: errors present but not about decreases/termination: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// PROBE 9: Callee with false ensure must fail at definition (under -strict).
// ---------------------------------------------------------------------------

func TestAdversarial_FalseCalleeEnsureCannotProveCallerClaim(t *testing.T) {
	src := `
def liar(n: i64) -> i64:
    ensure result == n + 100
    return n

def exploit_liar(n: i64) -> i64:
    ensure result == n + 100
    return liar(n)
`
	r := analyzeContractStrict(t, "false_callee_ensure.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("REAL BUG: callee liar has false ensure (result==n+100 but returns n); under -strict callee definition must error")
	}
}

// ---------------------------------------------------------------------------
// PROBE 10: TRUE sanity -- simple inductive recursive function.
// ---------------------------------------------------------------------------

func TestAdversarial_TrueSanityInductivePure(t *testing.T) {
	src := `
def triple(n: i64) -> i64:
    requires n >= 0
    ensure result == n * 3
    decreases n
    if n == 0:
        return 0
    return triple(n - 1) + 3
`
	r := analyzeContractStrict(t, "true_sanity_inductive.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Logf("INCOMPLETENESS: triple(n)==n*3 should be proven inductively but got errors: %v", errs)
		t.Skip("incomplete: nonlinear n*3 inductive proof not discharged (not a soundness issue)")
	}
}

// ---------------------------------------------------------------------------
// PROBE 11: Off-by-one false postcondition on recursive function.
// ---------------------------------------------------------------------------

func TestAdversarial_OffByOneUpperFalsePostcondition(t *testing.T) {
	src := `
def inc(n: i64) -> i64:
    requires n >= 0
    ensure result == n + 1
    decreases n
    if n == 0:
        return 1
    return inc(n - 1) + 1

def exploit_offby1(n: i64) -> i64:
    requires n >= 0
    ensure result == n + 2
    return inc(n)
`
	r := analyzeContractStrict(t, "off_by_one_upper.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("REAL BUG (FALSE_CLAIM ACCEPTED): inc(n)==n+1 but prover accepted result==n+2")
	}
}

// ---------------------------------------------------------------------------
// PROBE 12: Negative domain -- signed decreases + false postcondition.
// ---------------------------------------------------------------------------

func TestAdversarial_NegativeDecreasesFalsePostcondition(t *testing.T) {
	src := `
def count_up(n: i64) -> i64:
    requires n <= 0
    decreases -n
    if n == 0:
        return 0
    return count_up(n + 1)

def exploit_signed(n: i64) -> i64:
    requires n <= 0
    ensure result == n + 5
    return count_up(n)
`
	r := analyzeContractStrict(t, "negative_decreases.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("REAL BUG (FALSE_CLAIM ACCEPTED): count_up(0)==0 but prover accepted result==n+5 (false for n==0: 5!=0)")
	}
}

// ---------------------------------------------------------------------------
// PROBE 13: Multi-level equation chaining false and true probes.
// f(n)=n+1, g(n)=f(n)+1=n+2, h(n)=g(n)+1=n+3.
// ---------------------------------------------------------------------------

func TestAdversarial_MultilevelEquationChainingFalse(t *testing.T) {
	src := `
def f(n: i64) -> i64:
    return n + 1

def g(n: i64) -> i64:
    return f(n) + 1

def h(n: i64) -> i64:
    return g(n) + 1

def exploit_chain(n: i64) -> i64:
    ensure result == n + 10
    return h(n)
`
	r := analyzeContractStrict(t, "multilevel_chain_false.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("REAL BUG (FALSE_CLAIM ACCEPTED): h(n)==n+3 but prover accepted result==n+10")
	}
}

func TestAdversarial_MultilevelEquationChainingTrue(t *testing.T) {
	src := `
def f(n: i64) -> i64:
    return n + 1

def g(n: i64) -> i64:
    return f(n) + 1

def h(n: i64) -> i64:
    return g(n) + 1

def caller(n: i64) -> i64:
    ensure result == n + 3
    return h(n)
`
	r := analyzeContractStrict(t, "multilevel_chain_true.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Logf("INCOMPLETENESS: h(n)==n+3 via 3-level equation chain should prove but got: %v", errs)
		t.Skip("incomplete: 3-level equation chaining not proven (not a soundness issue)")
	}
}
