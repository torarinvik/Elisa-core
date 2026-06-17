//go:build cgo

package semantic

import "testing"

// ADVERSARIAL: a fresh-declared local reassigned to alias another buffer must LOSE its freshness,
// else the fresh-anchor widening would wrongly prove distinct while the buffers alias. These are
// the silent-miscompile tripwires for the one-side-fresh rule.
func TestDisjointWideningReassignmentAliasNotDistinct(t *testing.T) {
	cases := []struct{ name, body string }{
		{
			// b declared fresh, then whole-value reassigned to a: b now shares a's buffer.
			name: "reassign fresh to alias",
			body: `
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	b <- a
	axpy(&a, &b)
`,
		},
		{
			// b reassigned to another fresh local c: b aliases c, and axpy(&b,&c) aliases.
			name: "reassign fresh to other fresh",
			body: `
	b: mutable darray[f64] = []
	c: mutable darray[f64] = []
	b <- c
	axpy(&b, &c)
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := disjointKernelSrc + `
def run() -> void:` + tc.body
			result := analyzeTreeTestSource(t, "disjoint_widen_adv.elisa", src)
			if info := firstDisjointInfo(result); info != nil && info.PairDistinct(0, 1) {
				t.Fatalf("reassignment-aliased pair must NOT be distinct, got %v", info.DistinctPairs)
			}
		})
	}
}
