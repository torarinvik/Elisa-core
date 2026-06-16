//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// A two-parameter whole-array kernel (`axpy`) called only ever with two distinct
// private-fresh darray locals: the whole-program FuncDisjointParams proves the param
// pair distinct (both self-noalias), so under -fnoalias the backend stamps each element
// access with its own disjoint alias.scope and the sibling param's scope as !noalias
// (docs/84 Increment 3b). @inline(never) keeps the kernel body inspectable.
const disjointAxpyKernelSrc = `@inline(never)
def axpy(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:
    for i in 0..<y.count:
        y[i] <- y[i] + x[i]

def driver() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    for i in 0..<8:
        a.push(i.f64())
        b.push(i.f64())
    axpy(&a, &b)
`

// irHasDisjointParamScopes reports whether the module declares the per-parameter
// disjoint alias-scope domain and both per-param scopes (elt.p0 / elt.p1).
func irHasDisjointParamScopes(ir string) bool {
	return strings.Contains(ir, "elisa.disjoint.axpy.aa") &&
		strings.Contains(ir, `"elt.p0"`) &&
		strings.Contains(ir, `"elt.p1"`)
}

func TestDisjointParamScopesStampedUnderFlag(t *testing.T) {
	t.Setenv("ELISACORE_NOALIAS_MUTABLE_REFS", "1")
	result := parseAndAnalyzeBackendTest(t, "disjoint_axpy_stamp.elisa", disjointAxpyKernelSrc)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt error: %v", err)
	}
	if !irHasDisjointParamScopes(ir) {
		t.Fatalf("expected per-param disjoint alias scopes under -fnoalias, got:\n%s", ir)
	}
	// Soundness: every element access carrying an alias.scope from the disjoint domain
	// must ALSO carry a noalias list (the both-lists-same-domain invariant of §3.3). The
	// C helper refuses to emit a half-tag, so any alias.scope on a `double` element line
	// must be paired with a noalias on that same line.
	for _, line := range strings.Split(ir, "\n") {
		if !strings.Contains(line, "double") {
			continue
		}
		if strings.Contains(line, "!alias.scope") && !strings.Contains(line, "!noalias") {
			t.Fatalf("element access carries alias.scope without a paired noalias (unsound half-tag):\n%s", line)
		}
	}
}

// Default (flag off): no disjoint scopes — a noalias miscompile is silent, so the whole
// feature stays gated. The existing shared hdr/elt tagging is unaffected.
func TestDisjointParamScopesAbsentByDefault(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "disjoint_axpy_off.elisa", disjointAxpyKernelSrc)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt error: %v", err)
	}
	if strings.Contains(ir, "elisa.disjoint.") {
		t.Fatalf("expected NO disjoint param scopes without -fnoalias, got:\n%s", ir)
	}
}

// A self-aliasing call site (axpy(&c, &c)) makes the whole-program intersection drop the
// pair: even with -fnoalias, NO disjoint stamp may be emitted (it would be unsound). The
// driver below is the only caller and aliases the two params.
const disjointAxpyAliasedSrc = `@inline(never)
def axpy(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:
    for i in 0..<y.count:
        y[i] <- y[i] + x[i]

def driver() -> void:
    c: mutable darray[f64] = []
    for i in 0..<8:
        c.push(i.f64())
    axpy(&c, &c)
`

func TestDisjointParamScopesAliasedCallSiteNotStamped(t *testing.T) {
	t.Setenv("ELISACORE_NOALIAS_MUTABLE_REFS", "1")
	result := parseAndAnalyzeBackendTest(t, "disjoint_axpy_aliased.elisa", disjointAxpyAliasedSrc)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt error: %v", err)
	}
	if strings.Contains(ir, "elisa.disjoint.") {
		t.Fatalf("an aliased call site must suppress the disjoint stamp, got:\n%s", ir)
	}
}

// Header-copy drift guard: `b = a` creates two mutable darray values sharing one
// backing buffer. The semantic aggregate must therefore withhold FuncDisjointParams,
// and the backend must not stamp per-param scopes even under -fnoalias.
const disjointAxpyHeaderCopySrc = `@inline(never)
def axpy(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:
    for i in 0..<y.count:
        y[i] <- y[i] + x[i]

def driver() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = a
    axpy(&a, &b)
`

func TestDisjointParamScopesHeaderCopyNotStamped(t *testing.T) {
	t.Setenv("ELISACORE_NOALIAS_MUTABLE_REFS", "1")
	result := parseAndAnalyzeBackendTest(t, "disjoint_axpy_headercopy.elisa", disjointAxpyHeaderCopySrc)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt error: %v", err)
	}
	if strings.Contains(ir, "elisa.disjoint.") {
		t.Fatalf("a header-copy call site must suppress the disjoint stamp, got:\n%s", ir)
	}
}

const disjointAxpyMainSrc = `@inline(never)
def axpy(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:
    for i in 0..<y.count:
        y[i] <- y[i] + x[i]

def main() -> int can[Memory.Allocate]:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    for i in 0..<1024:
        a.push(i.f64())
        b.push(i.f64())
    axpy(&a, &b)
    return 0
`

func TestDisjointParamScopesElideO3RuntimeMemcheck(t *testing.T) {
	t.Run("flag off keeps runtime memcheck", func(t *testing.T) {
		t.Setenv("ELISACORE_NOALIAS_MUTABLE_REFS", "")
		result := parseAndAnalyzeBackendTest(t, "disjoint_axpy_o3_off.elisa", disjointAxpyMainSrc)
		ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel3)
		if err != nil {
			t.Fatalf("GenerateLLVMIRWithOpt error: %v", err)
		}
		if !strings.Contains(ir, "vector.body") || !strings.Contains(ir, "llvm.loop.isvectorized") {
			t.Fatalf("expected the baseline loop to vectorize so the memcheck comparison is meaningful, got:\n%s", ir)
		}
		if !strings.Contains(ir, "vector.memcheck") {
			t.Fatalf("expected baseline O3 IR to keep LLVM's runtime alias memcheck, got:\n%s", ir)
		}
	})

	t.Run("flag on elides runtime memcheck", func(t *testing.T) {
		t.Setenv("ELISACORE_NOALIAS_MUTABLE_REFS", "1")
		result := parseAndAnalyzeBackendTest(t, "disjoint_axpy_o3_on.elisa", disjointAxpyMainSrc)
		ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel3)
		if err != nil {
			t.Fatalf("GenerateLLVMIRWithOpt error: %v", err)
		}
		if !strings.Contains(ir, "vector.body") || !strings.Contains(ir, "llvm.loop.isvectorized") {
			t.Fatalf("expected the stamped loop to vectorize, got:\n%s", ir)
		}
		if strings.Contains(ir, "vector.memcheck") {
			t.Fatalf("per-param disjoint scopes should let LLVM elide the runtime alias memcheck, got:\n%s", ir)
		}
	})
}
