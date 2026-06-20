//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS (audit, cluster C): a predicate/refinement fact must be dropped when its underlying place
// is mutated through a BORROW LOCAL alias. `r: mutable i64& = &y; bump(r)` writes y, and
// `ys: mutable darray& = &xs; ys.clear()` empties xs — the narrowed fact on y/xs must not survive. The
// fix resolves a mutation target through its alias roots, so invalidation reaches the borrowed place.
func TestPredFactDroppedThroughBorrowLocalAlias(t *testing.T) {
	cases := []struct{ name, src string }{
		{"scalar_ref_call", `
law NonNeg(self: i64) = self >= 0
def need(x: i64 is NonNeg) -> i64:
    return x
def bump(x: mutable i64&) -> void:
    x <- 0 - 7
def main() -> i64:
    y: mutable i64 = 5
    if y is NonNeg:
        r: mutable i64& = &y
        bump(r)
        return need(y)
    return 0
`},
		{"darray_method", `
law NonEmpty(self: darray[i64]) = self.count > 0
def first(xs: darray[i64] is NonEmpty) -> i64:
    return xs[0]
def main() -> i64:
    xs: mutable darray[i64] = [1, 2, 3]
    if xs is NonEmpty:
        ys: mutable darray[i64]& = &xs
        ys.clear()
        return first(xs)
    return 0
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := strings.Join(analyzeContractStrict(t, "alias_"+tc.name+".elisa", tc.src).Errors(), "\n")
			if !strings.Contains(errs, "could not be proven statically") {
				t.Fatalf("mutation laundered through the borrow alias must drop the fact; the false refinement must NOT prove, got: %v", errs)
			}
		})
	}
}

// COMPLETENESS: aliasing WITHOUT a mutation keeps the fact — `r = &y` then `need(y)` still proves.
func TestPredFactSurvivesAliasWithoutMutation(t *testing.T) {
	src := `
law NonNeg(self: i64) = self >= 0
def need(x: i64 is NonNeg) -> i64:
    return x
def main() -> i64:
    y: mutable i64 = 5
    if y is NonNeg:
        r: mutable i64& = &y
        return need(y)
    return 0
`
	if errs := analyzeContractStrict(t, "alias_nomut.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("an unmutated alias must not drop the fact; need(y) should prove, got: %v", errs)
	}
}
