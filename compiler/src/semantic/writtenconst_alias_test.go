//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS (audit follow-up): the written-constant fact channel must also drop a fact when its place
// is mutated through a borrow-local alias — the sibling of cluster C for writtenConst, which the
// original C fix did not route through the alias resolver. `r: mutable i64& = &y; r -= 1` writes y, so
// the `y == 5` written-const must not survive (else `ensure result == 5` is proven while y != 5).
func TestWrittenConstDroppedThroughAliasAugAssign(t *testing.T) {
	src := `
def f() -> i64:
    ensure result == 5
    y: mutable i64 = 5
    r: mutable i64& = &y
    r -= 1
    return y
`
	errs := strings.Join(analyzeContractStrict(t, "wc_alias.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("`r -= 1` (r = &y) writes y, dropping the y==5 fact; `result == 5` must NOT prove, got: %v", errs)
	}
}

// COMPLETENESS: an unmutated written-const fact still discharges.
func TestWrittenConstSurvivesWithoutAliasMutation(t *testing.T) {
	src := `
def f() -> i64:
    ensure result == 5
    y: mutable i64 = 5
    r: mutable i64& = &y
    return y
`
	if errs := analyzeContractStrict(t, "wc_alias_ok.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("an unmutated alias must keep the y==5 fact; `result == 5` should prove, got: %v", errs)
	}
}
