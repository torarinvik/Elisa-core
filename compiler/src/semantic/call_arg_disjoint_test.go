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

// A whole-value read of `a` elsewhere (passed by value to a sink) means a's buffer may be copied
// into another local out of view, so a must not count as private-fresh.
func TestCallArgDisjointWholeValueEscapeNotDistinct(t *testing.T) {
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
	if info := firstDisjointInfo(result); info != nil && info.PairDistinct(0, 1) {
		t.Fatalf("whole-value-escaped `a` must NOT be proven distinct, got %v", info.DistinctPairs)
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
