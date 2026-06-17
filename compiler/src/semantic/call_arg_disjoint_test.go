//go:build cgo

package semantic

import "testing"

// kernel with two container-ref params is the unit under test; the caller varies.
const disjointKernelSrc = `
def axpy(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:
	for i in 0..<y.count:
		y[i] <- y[i] + x[i]
`

// firstDisjointInfo returns the single recorded CallArgDisjointInfo, or nil. The kernel itself
// has no nested calls with two container args, so the caller's axpy(...) is the only candidate.
func firstDisjointInfo(result *Result) *CallArgDisjointInfo {
	for _, info := range result.CallArgDisjoint {
		return info
	}
	return nil
}

func TestCallArgDisjointDistinctFreshLocals(t *testing.T) {
	src := disjointKernelSrc + `
def run() -> void:
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	axpy(&a, &b)
`
	result := analyzeTreeTestSource(t, "disjoint_ok.elisa", src)
	info := firstDisjointInfo(result)
	if info == nil {
		t.Fatalf("expected a CallArgDisjointInfo for axpy(&a,&b), got none")
	}
	if !info.PairDistinct(0, 1) {
		t.Fatalf("expected params 0,1 proven distinct, got DistinctPairs=%v", info.DistinctPairs)
	}
	if !info.SelfNoalias[0] || !info.SelfNoalias[1] {
		t.Fatalf("expected both params self-noalias, got %v", info.SelfNoalias)
	}
}

func TestCallArgDisjointSelfAliasNotDistinct(t *testing.T) {
	src := disjointKernelSrc + `
def run() -> void:
	a: mutable darray[f64] = []
	axpy(&a, &a)
`
	result := analyzeTreeTestSource(t, "disjoint_self.elisa", src)
	if info := firstDisjointInfo(result); info != nil && info.PairDistinct(0, 1) {
		t.Fatalf("axpy(&a,&a) must NOT be proven distinct, got %v", info.DistinctPairs)
	}
}

// The soundness-critical case: a MUTABLE header copy `b = a` shares a's buffer, yet leaves two
// distinct fresh-local NAMES. The escape scan must disqualify both, so the pair is NOT distinct.
func TestCallArgDisjointMutableHeaderCopyNotDistinct(t *testing.T) {
	src := disjointKernelSrc + `
def run() -> void:
	a: mutable darray[f64] = []
	b: mutable darray[f64] = a
	axpy(&a, &b)
`
	result := analyzeTreeTestSource(t, "disjoint_headercopy.elisa", src)
	if info := firstDisjointInfo(result); info != nil && info.PairDistinct(0, 1) {
		t.Fatalf("header-copy b=a must NOT be proven distinct (shared buffer), got %v", info.DistinctPairs)
	}
}

// `a` escapes (passed by value to a sink), so `a` is not private-fresh and cannot anchor
// disjointness. But `b` is still a pristine fresh buffer that nothing else can reference, so it
// anchors the pair: a brand-new buffer cannot alias `a` regardless of what `a` escaped into.
// Proven distinct via the single fresh anchor (`b`) — sound; a runtime check would not trip.
func TestCallArgDisjointFreshAnchorSurvivesOtherSideEscape(t *testing.T) {
	src := disjointKernelSrc + `
def sink(v: darray[f64]) -> void:
	return

def run() -> void:
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	sink(a)
	axpy(&a, &b)
`
	result := analyzeTreeTestSource(t, "disjoint_escape.elisa", src)
	info := firstDisjointInfo(result)
	if info == nil || !info.PairDistinct(0, 1) {
		t.Fatalf("fresh anchor `b` should prove the pair distinct even though `a` escaped, got %+v", info)
	}
}

// ADVERSARIAL: when the OTHER side is a laundered header-copy of the fresh local (`c` shares b's
// buffer via a returning call), the fresh local has ESCAPED (passed to the launderer), so it is no
// longer private-fresh and cannot anchor — the pair is correctly NOT distinct. This is what keeps
// the single-anchor widening sound against return-value aliasing.
func TestCallArgDisjointLaunderedReturnNotDistinct(t *testing.T) {
	src := disjointKernelSrc + `
def alias_of(v: darray[f64]) -> darray[f64]:
	return v

def run() -> void:
	b: mutable darray[f64] = []
	c: mutable darray[f64] = alias_of(b)
	axpy(&b, &c)
`
	result := analyzeTreeTestSource(t, "disjoint_launder.elisa", src)
	if info := firstDisjointInfo(result); info != nil && info.PairDistinct(0, 1) {
		t.Fatalf("laundered header-copy `c` must NOT be proven distinct from `b`, got %v", info.DistinctPairs)
	}
}

// Params are opaque: two distinct `mutable darray&` params can alias at the caller, so a kernel
// that forwards its own params to another two-container call proves nothing.
func TestCallArgDisjointParamsNotDistinct(t *testing.T) {
	src := disjointKernelSrc + `
def forward(p: mutable darray[f64]&, q: mutable darray[f64]&) -> void:
	axpy(p, q)
`
	result := analyzeTreeTestSource(t, "disjoint_params.elisa", src)
	if info := firstDisjointInfo(result); info != nil && info.PairDistinct(0, 1) {
		t.Fatalf("param-backed args must NOT be proven distinct, got %v", info.DistinctPairs)
	}
}

func TestCallArgDisjointDriftFrontier(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		distinct bool
	}{
		{
			name: "empty literals are distinct fresh buffers",
			body: `
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	axpy(&a, &b)
`,
			distinct: true,
		},
		{
			name: "clone is a distinct fresh buffer",
			body: `
	a: mutable darray[f64] = []
	b: mutable darray[f64] = clone[darray[f64]](a)
	axpy(&a, &b)
`,
			distinct: true,
		},
		{
			name: "same local aliases",
			body: `
	a: mutable darray[f64] = []
	axpy(&a, &a)
`,
		},
		{
			name: "header copy aliases",
			body: `
	a: mutable darray[f64] = []
	b: mutable darray[f64] = a
	axpy(&a, &b)
`,
		},
		{
			// `a` escaped (not an anchor), but the fresh, never-escaped `b` anchors the pair: a
			// brand-new buffer cannot alias `a`. Genuinely disjoint — a runtime check would not trip.
			name: "fresh anchor survives other-side escape",
			body: `
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	sink(a)
	axpy(&a, &b)
`,
			distinct: true,
		},
		{
			// The fresh local `b` is whole-value reassigned to alias `a`, so it is no longer a
			// pristine buffer and cannot anchor. Aliased — must NOT be distinct.
			name: "reassignment to alias kills the anchor",
			body: `
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	b <- a
	axpy(&a, &b)
`,
		},
		{
			// `c` is a laundered header-copy of `b` (returned by a call); passing `&b` to the
			// launderer escapes `b`, so neither side anchors. Aliased — must NOT be distinct.
			name: "laundered return value aliases",
			body: `
	b: mutable darray[f64] = []
	c: mutable darray[f64] = alias_of(b)
	axpy(&b, &c)
`,
		},
		{
			name: "index-derived root is rejected",
			body: `
	outer: mutable darray[darray[f64]] = []
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	outer.push(a)
	outer.push(b)
	axpy(&outer[0], &outer[1])
`,
		},
	}
	preamble := disjointKernelSrc + `
def sink(v: darray[f64]) -> void:
	return

def alias_of(v: darray[f64]) -> darray[f64]:
	return v
`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := preamble + `
def run() -> void:` + tc.body
			result := analyzeTreeTestSource(t, "disjoint_drift_frontier.elisa", src)
			info := firstDisjointInfo(result)
			got := info != nil && info.PairDistinct(0, 1)
			if got != tc.distinct {
				t.Fatalf("PairDistinct(0,1)=%v, want %v; info=%+v", got, tc.distinct, info)
			}
		})
	}
}
