//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func TestScanValueUsedIdentsStopsAtLoweredCallCycle(t *testing.T) {
	callee := &ast.Ident{Name: "callee"}
	callback := &ast.Ident{Name: "callback"}
	binary := &ast.BinaryExpr{}
	binary.LoweredCall = &ast.CallExpr{
		Func: callee,
		Args: []ast.Expr{callback, binary},
	}

	used := map[string]bool{}
	scanValueUsedIdents(reflect.ValueOf(binary), used)
	if !used["callback"] {
		t.Fatal("value argument was not recorded")
	}
	if used["callee"] {
		t.Fatal("direct call callee was misclassified as a function value")
	}
}

func TestScanFuncNameUsageDeduplicatesDeepSharedDAGAcrossRoots(t *testing.T) {
	var expr ast.Expr = &ast.Ident{Name: "leaf_value"}
	for i := 0; i < 128; i++ {
		child := expr
		expr = &ast.CallExpr{
			Func:         &ast.Ident{Name: fmt.Sprintf("guard_%d", i)},
			Args:         []ast.Expr{child},
			ResolvedArgs: []ast.Expr{child},
		}
	}
	first := &ast.FuncDecl{Name: "first", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
	second := &ast.FuncDecl{Name: "second", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
	decls := []scopedDecl{{Decl: first}, {Decl: second}}

	memo := newScanValueUsedIdentMemo()
	_, used := scanFuncNameUsageWithMemo(decls, memo)
	if !used["leaf_value"] {
		t.Fatal("deep shared value was not recorded")
	}

	single := newScanValueUsedIdentMemo()
	scanFuncNameUsageWithMemo(decls[:1], single)
	// The second declaration adds only its FuncDecl and ReturnStmt pointers; its shared
	// expression DAG must be skipped by the memo shared across declaration roots.
	if got, want := len(memo.seen), len(single.seen)+2; got != want {
		t.Fatalf("shared DAG visited again across roots: seen=%d want=%d", got, want)
	}
}

func TestScanFuncNameUsageDoesNotMemoizeSkippedCalleeBeforeValueUse(t *testing.T) {
	shared := &ast.Ident{Name: "callback"}
	callDecl := &ast.FuncDecl{Name: "call_site", Body: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{Func: shared}},
	}}
	valueDecl := &ast.FuncDecl{Name: "value_site", Body: []ast.Stmt{
		&ast.ExprStmt{Expr: shared},
	}}
	_, used := scanFuncNameUsage([]scopedDecl{{Decl: callDecl}, {Decl: valueDecl}})
	if !used["callback"] {
		t.Fatal("ident skipped as direct callee was lost when later used as a function value")
	}
}

func TestScanValueUsedIdentsKeepsNonIdentCalleeAsValue(t *testing.T) {
	handler := &ast.Ident{Name: "handler"}
	call := &ast.CallExpr{Func: &ast.FieldExpr{Object: handler, Field: "invoke"}}
	used := map[string]bool{}
	scanValueUsedIdents(reflect.ValueOf(call), used)
	if !used["handler"] {
		t.Fatal("non-Ident callee function value was not recorded")
	}
}

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
func TestFuncDisjointNameScanHandlesDeepPureGuardDagWithUnrelatedKernel(t *testing.T) {
	guard := "n >= 0"
	for i := 0; i < 128; i++ {
		guard = "ident((" + guard + " and n >= 0))"
	}
	source := disjointKernelSrc + shortCircuitRequiresHelper + `
def ident(value: bool) -> bool:
	return value

def run_kernel() -> void:
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	axpy(&a, &b)

def caller(n: i64) -> bool:
	return ` + guard + ` and needs_nonnegative(n)
`
	result := analyzeTreeTestSource(t, "fdisjoint_shared_guard_dag.elisa", source)
	if len(result.Errors()) != 0 {
		t.Fatalf("deep shared guard DAG with an unrelated disjoint kernel was rejected: %s", allDiagnostics(result))
	}
	if info := disjointInfoForFunc(result, "axpy"); info == nil || !info.PairDistinct(0, 1) {
		t.Fatalf("unrelated kernel must retain its positive disjoint proof, got %+v", info)
	}
}

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

// Drift guard for the header-copy frontier: even if another site is cleanly distinct,
// a single `b = a` caller shares the buffer and must kill the whole-program callee fact.
func TestFuncDisjointHeaderCopySiteKillsPair(t *testing.T) {
	src := disjointKernelSrc + `
def run_ok() -> void:
	a: mutable darray[f64] = []
	b: mutable darray[f64] = []
	axpy(&a, &b)

def run_header_copy() -> void:
	c: mutable darray[f64] = []
	d: mutable darray[f64] = c
	axpy(&c, &d)
`
	result := analyzeTreeTestSource(t, "fdisjoint_headercopy.elisa", src)
	if info := disjointInfoForFunc(result, "axpy"); info != nil && info.PairDistinct(0, 1) {
		t.Fatalf("a single header-copy call site must drop the pair, got %v", info.DistinctPairs)
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
	handler: fn(mutable darray[f64]&, mutable darray[f64]&) -> void = axpy
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
