//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// `old(p)` snapshot modeling: a void/fall-through postcondition over a mutated ref param relates the
// EXIT value (bare p, WP-threaded) to the ENTRY value (old(p)). `ensure p >= old(p)` proves when the
// body keeps p (`p += 0`) and when a bounded increment cannot overflow; it DECLINES on a decrement and
// on an unbounded increment that can overflow.
func TestOldSnapshotProveAndDecline(t *testing.T) {
	cases := []struct {
		name    string
		ensure  string
		reqs    string
		body    string
		prove   bool
	}{
		{"unchanged", "p >= old(p)", "", "p += 0", true},
		{"decrement", "p >= old(p)", "", "p -= 1", false},
		{"unbounded_incr_overflow", "p >= old(p)", "", "p += 5", false},
		{"bounded_incr", "p >= old(p)", "    requires p > 0\n    requires p < 1000\n", "p += 5", true},
		{"strict_gt_unchanged", "p > old(p)", "", "p += 0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "def g(p: mutable i64&) changes p:\n" + tc.reqs + "    ensure " + tc.ensure + "\n    " + tc.body + "\n"
			errs := strings.Join(analyzeContractStrict(t, "old_"+tc.name+".elisa", src).Errors(), "\n")
			rejected := strings.Contains(errs, "could not be proven statically")
			if tc.prove && rejected {
				t.Fatalf("%s should PROVE, got: %v", tc.name, errs)
			}
			if !tc.prove && !rejected {
				t.Fatalf("%s should DECLINE (false/overflow), but it was accepted", tc.name)
			}
		})
	}
}

// SOUNDNESS: the `old()` entry-symbol semantics is WP-only. A decrement on an unsigned param must
// decline (it underflows), never proving `p >= old(p)` by collapsing old(p) onto bare p.
func TestOldSnapshotUnsignedDecrementDeclines(t *testing.T) {
	src := `
def g(p: mutable u64&) changes p:
    ensure p >= old(p)
    p -= 1
`
	errs := strings.Join(analyzeContractStrict(t, "old_u64_dec.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("u64 `p -= 1` underflows, so `p >= old(p)` must NOT prove, got: %v", errs)
	}
}
