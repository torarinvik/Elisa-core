//go:build cgo

package semantic

import "testing"

// Range-loop invariants are proven inductively (as the equivalent counted while loop) and give
// inv[i := E] after the loop.
func TestSMTForRangeInvariantInduction(t *testing.T) {
	src := `
def cnt(n: i64) -> i64:
    requires n >= 0 and n < 1000
    ensure result == n
    c: mutable i64 = 0
    for i in 0..<n |c|:
        invariant c == i
        c <- c + 1
    return c

def cntbad(n: i64) -> i64:
    requires n >= 0 and n < 1000
    ensure result == n + 1
    c: mutable i64 = 0
    for i in 0..<n |c|:
        invariant c == i
        c <- c + 1
    return c

`
	result := analyzeContractStrict(t, "for_range_inv.elisa", src)
	if n := ensureFailuresFor(result.Errors(), "cnt"); n != 0 {
		t.Fatalf("cnt: expected proof, got %v", result.Errors())
	}
	if ensureFailuresFor(result.Errors(), "cntbad") == 0 {
		t.Fatalf("cntbad: false ensure must stay unproven; got %v", result.Errors())
	}
	// SOUNDNESS: a `continue` skips the rewritten increment, and a body that writes the bound
	// diverges from the once-evaluated range; both decline (the invariant in withcontinue is false).
	declined := `
def withcontinue(n: i64) -> i64:
    requires n >= 0 and n < 1000
    ensure result == n
    c: mutable i64 = 0
    for i in 0..<n |c|:
        invariant c == i
        if i == 3:
            continue
        c <- c + 1
    return c

def endw(n0: i64) -> i64:
    requires n0 >= 0 and n0 < 100
    ensure result == n
    n: mutable i64 = n0
    c: mutable i64 = 0
    for i in 0..<n |c, n|:
        invariant c == i
        c <- c + 1
        n <- n - 1
    return c

`
	result = analyzeContractStrict(t, "for_range_declined.elisa", declined)
	for _, name := range []string{"withcontinue", "endw"} {
		if ensureFailuresFor(result.Errors(), name) == 0 {
			t.Fatalf("%s: ensure must stay unproven; got %v", name, result.Errors())
		}
	}
}
