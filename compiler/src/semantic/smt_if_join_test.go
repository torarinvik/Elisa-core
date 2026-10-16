//go:build cgo

package semantic

import "testing"

// The facts each fall-through branch ends with survive the if as a disjunction.
func TestSMTIfJoinKeepsBranchEndFacts(t *testing.T) {
	cases := map[string]string{
		"guarded_overwrite": `
def guarded_overwrite(end: i64) -> i64:
    ensure end == 0 implies result == 0
    ensure end < 0 implies result == 5
    hi: mutable i64 = 5
    if end >= 0:
        hi <- end
    return hi
`,
		"two_arms": `
def two_arms(x: i64) -> i64:
    requires x > -100 and x < 100
    ensure result >= 0
    y: mutable i64 = 0
    if x >= 0:
        y <- x
    else:
        y <- 0 - x
    return y
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", src)
			if n := ensureFailuresFor(result.Errors(), name); n != 0 {
				t.Fatalf("%s: expected the joined branch facts to discharge, got: %v", name, result.Errors())
			}
		})
	}
	falseCases := map[string]string{
		"join_false": `
def join_false(end: i64) -> i64:
    ensure end == 0 implies result == 5
    hi: mutable i64 = 5
    if end >= 0:
        hi <- end
    return hi
`,
		// The branch declares its own y; its fact is about that binding, not the outer y.
		"join_shadowed": `
def join_shadowed(x: i64) -> i64:
    ensure result == 1
    y: mutable i64 = 0
    if x > 0:
        y: i64 = 1
        assert(y == 1)
    else:
        y <- 1
    return y
`,
		// A write after the join retires the joined fact.
		"join_then_write": `
def join_then_write(x: i64) -> i64:
    requires x > -100 and x < 100
    ensure result >= 0
    y: mutable i64 = 0
    if x >= 0:
        y <- x
    else:
        y <- 0 - x
    y <- y - 200
    return y
`,
	}
	for name, src := range falseCases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", src)
			if ensureFailuresFor(result.Errors(), name) == 0 {
				t.Fatalf("%s: the ensure is false for some input and must stay unproven; got: %v", name, result.Errors())
			}
		})
	}
}
