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

// SOUNDNESS: the body is analyzed once, against the first iteration's facts. A later iteration sees
// the previous one's writes, so an entry fact about a written variable must not reach the loop head:
// `invariant h == 5` inside a loop that increments h "proved" and was used as a release fact.
func TestLoopHeadDropsEntryFactsOfWrittenVariables(t *testing.T) {
	for name, loop := range map[string]string{
		"while": "    i: mutable i64 = 0\n    while i < n:\n        invariant h == 5\n        h <- h + 1\n        i <- i + 1\n",
		"for":   "    for k in 0..<n:\n        invariant h == 5\n        h <- h + 1\n",
	} {
		src := "def hh(n: i64) -> u64:\n    h: mutable u64 = 5\n" + loop + "    return h\n"
		if errs := strictSMTErrors(t, "loop_head_"+name+".elisa", src); !strings.Contains(strings.Join(errs, "\n"), "could not be proven") {
			t.Fatalf("%s: h changes across iterations; invariant h == 5 must not prove", name)
		}
	}
	// A counter stepped once per iteration under `x < e` keeps its entry lower bound.
	counter := "def f(n: i64, done: bool):\n    requires n >= 0\n    i: mutable i64 = 0\n    while i < n:\n        assert i >= 0\n        if done:\n            break\n        i <- i + 1\n"
	if errs := strictSMTErrors(t, "loop_head_counter.elisa", counter); len(errs) != 0 {
		t.Fatalf("a monotone counter keeps its lower bound, got:\n%s", strings.Join(errs, "\n"))
	}
}

// SOUNDNESS: invariants are preserved JOINTLY. `a == 0` is preserved by `a <- a + b` only under
// `b == 0`, which `b <- b + 1` does not preserve; `a == 0` must not be proven by leaning on it.
func TestLoopInvariantsPreservedJointly(t *testing.T) {
	src := `def f(n: i64) -> i64:
    a: mutable i64 = 0
    b: mutable i64 = 0
    i: mutable i64 = 0
    while i < n:
        invariant b == 0
        invariant a == 0
        a <- a + b
        b <- b + 1
        i <- i + 1
    return a
`
	r := analyzeLoopInvariantWithSMT(t, "joint.elisa", src, false)
	if got := countLoopInvariantProof(r, "preserve", ProofProvenSMT) + countLoopInvariantProof(r, "preserve", ProofProvenLinear); got != 0 {
		t.Fatalf("neither invariant is inductive on its own; got %d preserved: %+v", got, r.ProofReport)
	}
}

// An invariant is checked at body entry, where the guard holds, so it may be established and
// preserved UNDER the guard: `i < n` is not inductive on its own but holds at every body entry. Such
// a guarded invariant fails at exit (i == n there) and must not become an after-loop fact.
func TestGuardedLoopInvariant(t *testing.T) {
	src := `def f(n: i64) -> i64:
    requires n > 0
    ensure result < n
    i: mutable i64 = 0
    while i < n:
        invariant i < n
        i <- i + 1
    return i
`
	errs := strictSMTErrors(t, "guarded_inv.elisa", src)
	joined := strings.Join(errs, "\n")
	if strings.Contains(joined, "loop invariant") || strings.Contains(joined, "invariant could not") {
		t.Fatalf("i < n holds at every body entry, got:\n%s", joined)
	}
	if !strings.Contains(joined, "ensure postcondition") {
		t.Fatalf("i == n at exit; a guarded invariant must not prove ensure result < n, got:\n%s", joined)
	}
}
