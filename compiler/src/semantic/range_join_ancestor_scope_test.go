//go:build cgo

package semantic

import "testing"

// SOUNDNESS (pre-existing): an `if` (here the synthesized wrapper of a captured loop) nested in a
// `can` block joined its widened range into the `can` scope only, while the function scope kept the
// entry range `d in [0, 0]`; lookup intersects every scope, so `ensure result == 0` was "proven"
// although d is 5 after the loop.
func TestRangeJoinReplacesAncestorScopeFact(t *testing.T) {
	src := `
global mutable counter: i64 = 0

def run(n: i64) -> i64:
    requires n > 0 and n < 100
    ensure result == 0
    d: mutable i64 = 0
    i: mutable i64 = 0
    can Global{Read, Write}:
        while i < n |i, d|:
            d <- 5
            i <- i + 1
    return d

def keep(n: i64) -> i64:
    requires n > 0 and n < 100
    ensure result >= 0
    d: mutable i64 = 0
    i: mutable i64 = 0
    can Global{Read, Write}:
        if n > 3:
            d <- 5
    return d
`
	result := analyzeContractStrict(t, "range_join_can.elisa", src)
	if ensureFailuresFor(result.Errors(), "run") == 0 {
		t.Fatalf("run: d == 0 after the loop must stay unproven; got %v", result.Errors())
	}
	if n := ensureFailuresFor(result.Errors(), "keep"); n != 0 {
		t.Fatalf("keep: joined range [0, 5] must still prove d >= 0; got %v", result.Errors())
	}
}

// The solver-fact if-join already replaces outer-scope facts: a symbolic `d == n` entry fact must
// not survive a write inside a `can` block (captured loop, if, nested if).
func TestSMTJoinDropsAncestorScopeFact(t *testing.T) {
	src := `
global mutable counter: i64 = 0

def run(n: i64) -> i64:
    requires n > 0 and n < 100
    ensure result == n
    d: mutable i64 = n
    i: mutable i64 = 0
    can Global{Read, Write}:
        while i < n |i, d|:
            d <- n + 1
            i <- i + 1
    return d

def run2(n: i64, m: i64) -> i64:
    requires n > 0 and n < 100
    ensure result == n
    d: mutable i64 = n
    can Global{Read, Write}:
        if m > 0:
            if m > 1:
                d <- n + 1
    return d
`
	result := analyzeContractStrict(t, "smt_join_can.elisa", src)
	for _, fn := range []string{"run", "run2"} {
		if ensureFailuresFor(result.Errors(), fn) == 0 {
			t.Fatalf("%s: stale d == n must not prove the ensure; got %v", fn, result.Errors())
		}
	}
}
