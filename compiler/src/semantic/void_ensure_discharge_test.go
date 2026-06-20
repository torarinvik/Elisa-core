//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// A boolean `ensure` over a mutated ref param on a VOID / fall-through function (no explicit
// `return <expr>`) must be discharged at the synthetic exit under -strict, mirroring the explicit-return
// path. `ensure p >= old(p)` with a decrementing body is FALSE (and underflows at p=0); previously it
// was never checked statically and silently relied on the debug runtime check.
func TestVoidEnsureFalsePostconditionRejected(t *testing.T) {
	src := `
def g(p: mutable u64&) changes p:
    ensure p >= old(p)
    p -= 1
`
	errs := strings.Join(analyzeContractStrict(t, "void_ensure_false.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("a void function's false `ensure p >= old(p)` (decrement) must be rejected under -strict, got: %v", errs)
	}
}

// A provable void postcondition (no `old`, holds by type) still passes — the discharge rejects only what
// it cannot prove, it does not blanket-reject void ensures.
func TestVoidEnsureProvablePostconditionPasses(t *testing.T) {
	src := `
def g(p: mutable u64&) changes p:
    ensure p >= 0
    p <- 5
`
	if errs := analyzeContractStrict(t, "void_ensure_true.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("`p >= 0` holds for all u64; a provable void postcondition must still discharge, got: %v", errs)
	}
}
