//go:build cgo

package semantic

import "testing"

// disjointInfoForFunc returns the aggregated FuncDisjointParamInfo for the uniquely-named
// function `name`, or nil if none was recorded.
func disjointInfoForFunc(result *Result, name string) *FuncDisjointParamInfo {
	for decl, info := range result.FuncDisjointParams {
		if decl != nil && decl.Name == name {
			return info
		}
	}
	return nil
}

// Two call sites, each passing two distinct fresh locals: the pair is distinct at every site, so
// the aggregated whole-program fact proves params 0,1 distinct with both self-noalias bits.
func TestFuncDisjointAllCallSitesDistinct(t *testing.T) {
	src := disjointKernelSrc + `
def run1() -> void:
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	axpy(&a, &b)

def run2() -> void:
	c: mutable darray[f64] = []
	d: mutable darray[f64] = []
	axpy(&c, &d)
`
	result := analyzeTreeTestSource(t, "fdisjoint_all.elisa", src)
	info := disjointInfoForFunc(result, "axpy")
	if info == nil {
		t.Fatalf("expected aggregated FuncDisjointParamInfo for axpy, got none")
	}
	if !info.PairDistinct(0, 1) {
		t.Fatalf("expected params 0,1 distinct at every call site, got %v", info.DistinctPairs)
	}
	if !info.SelfNoalias[0] || !info.SelfNoalias[1] {
		t.Fatalf("expected both params self-noalias, got %v", info.SelfNoalias)
	}
}

// One distinct site and one self-aliasing site axpy(&a,&a): the intersection across sites must
// drop the pair — a single aliasing caller makes the callee-body stamp unsound.
func TestFuncDisjointOneAliasingSiteKillsPair(t *testing.T) {
	src := disjointKernelSrc + `
def run_ok() -> void:
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	axpy(&a, &b)

def run_alias() -> void:
	c: mutable darray[f64] = []
	axpy(&c, &c)
`
	result := analyzeTreeTestSource(t, "fdisjoint_alias.elisa", src)
	if info := disjointInfoForFunc(result, "axpy"); info != nil && info.PairDistinct(0, 1) {
		t.Fatalf("a single aliasing call site must drop the pair, got %v", info.DistinctPairs)
	}
}

// A function whose name is referenced as a value (address-taken) could be invoked from an
// unobserved indirect site, so it is excluded even when every observed call is distinct.
func TestFuncDisjointAddressTakenExcluded(t *testing.T) {
	src := disjointKernelSrc + `
def run() -> void:
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	axpy(&a, &b)
	handler: func(mutable darray[f64]&, mutable darray[f64]&) -> void = axpy
`
	result := analyzeTreeTestSource(t, "fdisjoint_addr.elisa", src)
	if info := disjointInfoForFunc(result, "axpy"); info != nil {
		t.Fatalf("address-taken axpy must be excluded from FuncDisjointParams, got %v", info.DistinctPairs)
	}
}

// A single distinct call site is sufficient: with no contradicting site, the intersection keeps
// the pair (the function is still fully observed because it is free and not address-taken).
func TestFuncDisjointSingleSiteSufficient(t *testing.T) {
	src := disjointKernelSrc + `
def run() -> void:
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	axpy(&a, &b)
`
	result := analyzeTreeTestSource(t, "fdisjoint_single.elisa", src)
	info := disjointInfoForFunc(result, "axpy")
	if info == nil || !info.PairDistinct(0, 1) {
		t.Fatalf("single distinct site should prove the pair, got %+v", info)
	}
}
