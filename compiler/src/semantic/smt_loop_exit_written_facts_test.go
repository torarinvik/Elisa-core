package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS: a `while` body (and a filtered `for` body) is analyzed in a condition-refined clone
// whose range facts are restored on exit, which resurrected the ENTRY range of every variable the
// body writes. `ensure result == 5` then "proved" for a function returning 8 and the runtime check
// was elided.
func TestLoopExitDropsFactsOfWrittenVariables(t *testing.T) {
	for name, loop := range map[string]string{
		"while":   "    i: mutable usize = 0\n    while i < n:\n        h <- h + 1\n        i <- i + 1\n",
		"assign":  "    i: mutable usize = 0\n    while i < n:\n        h <- 7\n        i <- i + 1\n",
		"forever": "    while true:\n        h <- h + 1\n        break if h > 9\n",
		"nested":  "    i: mutable usize = 0\n    while i < n:\n        i <- i + 1\n        while i < 3:\n            h <- h + 1\n            i <- i + 1\n",
	} {
		src := "def hh(n: usize) -> u64:\n    ensure result == 5\n    h: mutable u64 = 5\n" + loop + "    return h\n"
		if errs := strictSMTErrors(t, "loop_exit_"+name+".elisa", src); !strings.Contains(strings.Join(errs, "\n"), "could not be proven") {
			t.Fatalf("%s: the loop may change h; ensure result == 5 must not prove", name)
		}
	}
	filtered := "def hh(xs: darray[u64]) -> u64:\n    ensure result == 5\n    h: mutable u64 = 5\n    for x in xs where x > 0:\n        h <- h + 1\n    return h\n"
	if errs := strictSMTErrors(t, "loop_exit_filtered.elisa", filtered); !strings.Contains(strings.Join(errs, "\n"), "could not be proven") {
		t.Fatalf("filtered for: the loop may change h; ensure result == 5 must not prove")
	}
	// A variable the loop never writes keeps its entry fact.
	kept := "def hh(n: usize) -> u64:\n    ensure result == 5\n    h: mutable u64 = 5\n    i: mutable usize = 0\n    while i < n:\n        i <- i + 1\n    return h\n"
	if errs := strictSMTErrors(t, "loop_exit_kept.elisa", kept); len(errs) != 0 {
		t.Fatalf("an unwritten variable keeps its entry range, got:\n%s", strings.Join(errs, "\n"))
	}
}

// A ghost function is pure and total, so calls with EQUAL arguments (not merely equally spelled)
// share a value: the loop invariant `suf(n, i, h) == suf(n, 0, 5)` is established at i == 0, h == 5.
func TestGhostCallCongruenceEstablishesFoldInvariant(t *testing.T) {
	src := `ghost def suf(n: usize, index: usize, h: u64) -> u64:
    requires index <= n
    decreases n - index
    ensure index == n implies result == h
    ensure index < n implies result == suf(n, index + 1, h + 1)
    return h if index == n
    return suf(n, index + 1, h + 1)

def hh(n: usize) -> bool:
    h: mutable u64 = 5
    i: mutable usize = 0
    while i < n:
        decreases n - i
        invariant i <= n
        invariant suf(n, i, h) == suf(n, 0, 5)
        h <- h + 1
        i <- i + 1
    return true
`
	for _, err := range strictSMTErrors(t, "ghost_congruence.elisa", src) {
		if strings.Contains(err, "invariant could not be proven") {
			t.Fatalf("the fold invariant should prove by congruence, got:\n%s", err)
		}
	}
}
