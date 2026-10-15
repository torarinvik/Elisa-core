//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func ensureFailuresFor(errs []string, fn string) int {
	n := 0
	for _, e := range errs {
		if strings.Contains(e, "ensure postcondition of \""+fn+"\"") {
			n++
		}
	}
	return n
}

// A bool-returning callee's ensure (`x < 0 implies not result`) must be usable at a `return f(x)` call
// site, including through a module-qualified call. The call-result symbol used to be declared Int
// while the ensure used it as a Bool, so every such query was ill-sorted and declined.
func TestSMTBoolCalleeEnsureDischargesAtCallSite(t *testing.T) {
	src := `
def nonneg_gate(x: i64, op: i64) -> bool:
    ensure x < 0 implies not result
    return false if x < 0
    return op == 0

def forwards(x: i64) -> bool:
    ensure x < 0 implies not result
    return nonneg_gate(x, 0)

module Gates:
    public:
        def gate(x: i64) -> bool:
            ensure x < 0 implies not result
            return false if x < 0
            return true

module Users:
    public:
        def qualified(x: i64) -> bool:
            ensure x < 0 implies not result
            return Gates::gate(x)
`
	result := analyzeContractStrict(t, "bool_callee_ensure.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("bool callee ensures must discharge the caller's ensure; got: %v", errs)
	}
}

// SOUNDNESS: the callee ensure says nothing about non-negative inputs, so a caller claiming the
// callee returns true for every non-negative x must still fail.
func TestSMTBoolCalleeEnsureDoesNotOverclaim(t *testing.T) {
	src := `
def nonneg_gate(x: i64, op: i64) -> bool:
    ensure x < 0 implies not result
    return false if x < 0
    return op == 0

def overclaims(x: i64) -> bool:
    ensure x >= 0 implies result
    return nonneg_gate(x, 1)
`
	result := analyzeContractStrict(t, "bool_callee_overclaim.elisa", src)
	if ensureFailuresFor(result.Errors(), "overclaims") == 0 {
		t.Fatalf("an ensure the callee contract does not imply must stay unproven; got: %v", result.Errors())
	}
}

// Verdicts must not depend on function order. A failing (or solver-rejected) proof in one function
// used to leave its solver answer buffered and become the verdict of the next function's query, so
// `later` failed only when `earlier` preceded it.
func TestSMTVerdictsIndependentOfFunctionOrder(t *testing.T) {
	later := `
def later(x: i64) -> bool:
    ensure x < 0 implies not result
    return false if x < 0
    return true
`
	earlier := `
def gate(x: i64, op: i64) -> bool:
    ensure x < 0 implies not result
    return false if x < 0
    return op == 0

def earlier(x: i64) -> bool:
    ensure x < 0 implies not result
    return gate(x, 0)
`
	for _, src := range []string{later + earlier, earlier + later} {
		result := analyzeContractStrict(t, "order_independent.elisa", src)
		if n := ensureFailuresFor(result.Errors(), "later"); n != 0 {
			t.Fatalf("`later` must prove regardless of order; got: %v", result.Errors())
		}
	}
}
