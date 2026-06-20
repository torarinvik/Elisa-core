//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS (audit, cluster E): the affine interval prover computes `coeff*lo`/`coeff*hi` in int64. A
// wide u32 product (`a * 3000000000` with a in [0, 4e9]) overflows int64 to a small/negative value, so
// the interval looked in-range and provablyNoArithWrap skipped the wrap model — proving a false
// `result >= a`. With overflow-checked arithmetic the bound goes open and the obligation declines.
func TestAffineOverflowDeclinesWrappingProduct(t *testing.T) {
	src := `
law UB32(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
type U32Hi = u32 is UB32[0, 4000000000]
def mulconst(a: U32Hi) -> u32:
    ensure result >= a
    return a * 3000000000
`
	errs := strings.Join(analyzeContractStrict(t, "affine_ovf.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("u32 a*3e9 wraps, so `result >= a` is false and must NOT prove; got: %v", errs)
	}
}

// COMPLETENESS: a small product that cannot overflow int64 (and stays in u32 range) still proves — the
// fix only opens the bound on actual overflow.
func TestAffineSmallProductStillProves(t *testing.T) {
	src := `
law UB(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
type Small = u32 is UB[0, 100]
def f(a: Small) -> u32:
    ensure result <= 300
    return a * 3
`
	if errs := analyzeContractStrict(t, "affine_small.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("a*3 for a<=100 is <=300 with no overflow; should still prove, got: %v", errs)
	}
}
