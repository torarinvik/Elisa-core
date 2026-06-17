//go:build cgo

package semantic

import "testing"

// The forwarding fixpoint: a kernel reached only through a forwarder that passes ITS OWN params is
// proven distinct, because the forwarder is itself always called with distinct fresh buffers. The
// distinctness flows: run() proves solve(p,q) distinct (fresh args) -> solve forwarding axpy(p,q)
// proves axpy distinct.
func TestFuncDisjointForwardingChainProven(t *testing.T) {
	src := disjointKernelSrc + `
def solve(p: mutable darray[f64]&, q: mutable darray[f64]&) -> void:
	axpy(p, q)

def run() -> void:
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	solve(&a, &b)
`
	result := analyzeTreeTestSource(t, "fwd_chain.elisa", src)
	axpy := disjointInfoForFunc(result, "axpy")
	if axpy == nil || !axpy.PairDistinct(0, 1) {
		t.Fatalf("forwarded axpy should be proven distinct via solve's disjoint params, got %+v", axpy)
	}
	if solve := disjointInfoForFunc(result, "solve"); solve == nil || !solve.PairDistinct(0, 1) {
		t.Fatalf("solve should be proven distinct from its fresh-distinct call site, got %+v", solve)
	}
}

// A two-hop forwarding chain (run -> outer -> inner -> axpy) still grounds out in the fresh args.
func TestFuncDisjointForwardingTwoHops(t *testing.T) {
	src := disjointKernelSrc + `
def inner(p: mutable darray[f64]&, q: mutable darray[f64]&) -> void:
	axpy(p, q)

def outer(p: mutable darray[f64]&, q: mutable darray[f64]&) -> void:
	inner(p, q)

def run() -> void:
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	outer(&a, &b)
`
	result := analyzeTreeTestSource(t, "fwd_twohops.elisa", src)
	if axpy := disjointInfoForFunc(result, "axpy"); axpy == nil || !axpy.PairDistinct(0, 1) {
		t.Fatalf("axpy should be proven distinct through a two-hop forwarding chain, got %+v", axpy)
	}
}

// ADVERSARIAL: a forwarder reached from an ALIASING call site must not prove its forwardee. One
// caller passes distinct fresh buffers, another passes the same buffer twice; the intersection
// across solve's call sites fails, so neither solve nor axpy is proven.
func TestFuncDisjointForwardingAliasedCallerKills(t *testing.T) {
	src := disjointKernelSrc + `
def solve(p: mutable darray[f64]&, q: mutable darray[f64]&) -> void:
	axpy(p, q)

def run() -> void:
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	c: mutable darray[f64] = []
	solve(&a, &b)
	solve(&c, &c)
`
	result := analyzeTreeTestSource(t, "fwd_aliased.elisa", src)
	if solve := disjointInfoForFunc(result, "solve"); solve != nil && solve.PairDistinct(0, 1) {
		t.Fatalf("solve must NOT be proven (one caller aliases), got %v", solve.DistinctPairs)
	}
	if axpy := disjointInfoForFunc(result, "axpy"); axpy != nil && axpy.PairDistinct(0, 1) {
		t.Fatalf("axpy must NOT be proven through an aliased forwarder, got %v", axpy.DistinctPairs)
	}
}

// The reassignment hazard (a forwarder rebinding `p <- q` so p aliases q before forwarding) is the
// soundness crux of the depend edge. Elisa's borrow checker is the PRIMARY defense: rebinding a
// `darray&` param to another reference is already a hard error ("storing a forwarded reference
// into longer-lived storage"), so the hazard cannot even be written. The analyzer's
// reassigned-param guard (enclosingParamIndexForArg) is sound defense-in-depth behind it. This
// test locks in that the rebind is rejected at compile time.
func TestFuncDisjointForwardingReassignedRefParamRejected(t *testing.T) {
	src := disjointKernelSrc + `
def solve(p: mutable darray[f64]&, q: mutable darray[f64]&) -> void:
	p <- q
	axpy(p, q)

def run() -> void:
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	solve(&a, &b)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "fwd_reassign.elisa", src, AnalyzeOptions{})
	if len(result.Errors()) == 0 {
		t.Fatal("rebinding a darray& param (`p <- q`) should be a hard error (the primary defense against forwarded-param aliasing)")
	}
}

// ADVERSARIAL (least-fixpoint grounding): a pair of mutually-forwarding functions with NO
// fresh-anchored base must never be proven — their only evidence is a `depend` on each other, which
// a least-fixpoint never discharges. A greatest-fixpoint would unsoundly leave the cycle true.
func TestFuncDisjointForwardingUngroundedCycleNotProven(t *testing.T) {
	src := disjointKernelSrc + `
def ping(p: mutable darray[f64]&, q: mutable darray[f64]&) -> void:
	pong(p, q)

def pong(p: mutable darray[f64]&, q: mutable darray[f64]&) -> void:
	ping(p, q)
`
	result := analyzeTreeTestSource(t, "fwd_cycle.elisa", src)
	for _, name := range []string{"ping", "pong"} {
		if info := disjointInfoForFunc(result, name); info != nil && info.PairDistinct(0, 1) {
			t.Fatalf("ungrounded forwarding cycle %q must NOT be proven, got %v", name, info.DistinctPairs)
		}
	}
}
