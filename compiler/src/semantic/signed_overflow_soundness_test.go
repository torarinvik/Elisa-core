//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS: signed `+`/`-`/`*` WRAP two's-complement on overflow (there is no codegen trap backing the
// old "assume no overflow" stance). A postcondition that holds in unbounded ℤ but is FALSE under wrap
// must NOT be proven. The verifier now models the wrap across all tiers (SMT wrapMachineArith, the VC-IR
// arithWrapInfo used by WP, and the overflow-checked affine prover).
func TestSignedArithOverflowNotProven(t *testing.T) {
	cases := []struct{ name, src string }{
		{"add", `
def f(a: i64, b: i64) -> i64:
    requires a > 0
    requires b > 0
    ensure result > 0
    return a + b
`},
		{"mul", `
def f(a: i64, b: i64) -> i64:
    requires a > 0
    requires b > 0
    ensure result > 0
    return a * b
`},
		{"sub", `
def f(a: i64, b: i64) -> i64:
    requires a > 0
    requires b < 0
    ensure result > 0
    return a - b
`},
		{"add_wp", `
def f(x: i64) -> i64:
    requires x > 0
    ensure result >= x
    y: mutable i64 = x
    y += 1
    return y
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := strings.Join(analyzeContractStrict(t, "so_"+tc.name+".elisa", tc.src).Errors(), "\n")
			if !strings.Contains(errs, "could not be proven statically") {
				t.Fatalf("unbounded signed %s can overflow, so the postcondition is false and must NOT prove, got: %v", tc.name, errs)
			}
		})
	}
}

// SOUNDNESS (audit roi/signed-arith-overflow-audit): canonical fabrication case — x + 1 where the
// prover's ℤ model would say "result > x" (true in ℤ) but INT_MAX + 1 wraps to INT_MIN < INT_MAX.
// The wrap MUST prevent the proof from discharging. Pins the wrapMachineArith / smtSignedWrap path.
func TestSignedAddOneCannotFabricatePostcondition(t *testing.T) {
	cases := []struct{ name, src string }{
		{"x_plus_1_i64", `
def f(x: i64) -> i64:
    ensure result > x
    return x + 1
`},
		{"x_plus_1_i32", `
def f(x: i32) -> i32:
    ensure result > x
    return x + 1
`},
		{"x_mul_2_positive", `
def f(x: i64) -> i64:
    requires x > 0
    ensure result > x
    return x * 2
`},
		{"x_minus_neg1", `
def f(x: i64) -> i64:
    requires x > 0
    ensure result > x
    return x - (-1)
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := strings.Join(analyzeContractStrict(t, "sao_"+tc.name+".elisa", tc.src).Errors(), "\n")
			if !strings.Contains(errs, "could not be proven statically") {
				t.Fatalf("signed overflow can make result > x false for %s — must NOT prove, got: %v", tc.name, errs)
			}
		})
	}
}

// COMPLETENESS: when the operands are bounded so the result provably cannot overflow, the postcondition
// still proves — the wrap model only declines what can actually wrap.
func TestSignedArithBoundedStillProves(t *testing.T) {
	cases := []struct{ name, src string }{
		{"add", `
law B(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Sm = i64 is B[0, 1000]
def f(a: Sm, b: Sm) -> i64 is B[0, 2000]:
    return a + b
`},
		{"sub_guarded", `
def f(a: i64, b: i64) -> i64:
    requires b >= 0
    requires a >= b
    ensure result >= 0
    return a - b
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if errs := analyzeContractStrict(t, "sob_"+tc.name+".elisa", tc.src).Errors(); len(errs) != 0 {
				t.Fatalf("a provably non-overflowing signed %s should still prove, got: %v", tc.name, errs)
			}
		})
	}
}
