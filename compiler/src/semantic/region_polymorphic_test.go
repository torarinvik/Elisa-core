package semantic

import (
	"strings"
	"testing"
)

// regionPolymorphicFuncType looks up a top-level function's FuncType from an analysis result.
func regionPolymorphicFuncType(t *testing.T, result *Result, name string) *FuncType {
	t.Helper()
	if result == nil || result.GlobalScope == nil {
		t.Fatalf("analysis result has no global scope")
	}
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("function %q not found in global scope", name)
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("symbol %q is not a function (got %T)", name, sym.Type)
	}
	return fnType
}

// docs/75 step 1 (detection): a function that returns a value built with `new[auto]` carries a
// synthesized inferred region (`__auto_*`) and is classified region-polymorphic — it will thread
// that region from the caller. The classification is recorded even while the escape error still
// fires (threading is layered on in later steps), so this asserts the flag, not the diagnostics.
func TestRegionPolymorphicDetectedOnNewAutoReturn(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rp_detect.elisa", `struct Box:
    value: i64
def make() -> Box&:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        b: Box& = new[auto] Box(7)
        return b
`, AnalyzeOptions{})
	fn := regionPolymorphicFuncType(t, result, "make")
	if !fn.RegionPolymorphic {
		t.Fatalf("a function returning a new[auto] value must be classified RegionPolymorphic")
	}
}

// The classification is inference-driven: no explicit region block is needed (the region is
// synthesized for the body), exactly the shape the recursive packed-enum builder needs.
func TestRegionPolymorphicDetectedWithoutExplicitBlock(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rp_inf.elisa", `struct Box:
    value: i64
def make() -> Box&:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        b: Box& = new[auto] Box(7)
        return b
`, AnalyzeOptions{})
	fn := regionPolymorphicFuncType(t, result, "make")
	if !fn.RegionPolymorphic {
		t.Fatalf("region-polymorphism detection must be inference-driven (no explicit in-auto required)")
	}
}

// A function that allocates with new[auto] but does NOT return it is not region-polymorphic — the
// value lives and dies inside the function, so no region needs to be threaded from the caller.
func TestRegionPolymorphicNotSetWhenAutoValueDoesNotEscape(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rp_local.elisa", `struct Box:
    value: i64
def make() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        b: Box& = new[auto] Box(7)
        return b.value
`, AnalyzeOptions{})
	fn := regionPolymorphicFuncType(t, result, "make")
	if fn.RegionPolymorphic {
		t.Fatalf("a function whose new[auto] value never escapes must NOT be region-polymorphic")
	}
}

// An explicitly-named local region (`in scratch:`) is never region-polymorphic: returning a value
// from it is a real escape bug, not an inferred region to thread. The flag stays false (and the
// escape error still fires, asserted elsewhere).
func TestRegionPolymorphicNotSetForNamedLocalRegion(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rp_named.elisa", `struct Box:
    value: i64
def make() -> Box&:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(reserve_commit):
            b: Box& = new[scratch] Box(7)
            return b
`, AnalyzeOptions{})
	fn := regionPolymorphicFuncType(t, result, "make")
	if fn.RegionPolymorphic {
		t.Fatalf("an explicitly-named local region must NOT make a function region-polymorphic")
	}
}

// A region-polymorphic constructor's result (resolved even though a same-named struct shadows the
// constructor in the value scope) whose container field is returned INSIDE A DIFFERENT struct
// literal makes the function region-polymorphic: the field's buffer lives in the threaded region
// and must be adopted by the caller, not freed on return. Regression for the stage1 parser's
// `parser = Parser(...); return Ast::File{errors: parser.errors, ...}` — which silently produced a
// use-after-free (the File's container headers copy out, but their backing freed: count reads fine,
// element reads segfault).
func TestRegionPolymorphicSetForConstructorFieldInReturnedStruct(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rp_ctorfield.elisa", `struct Bag:
    xs: mutable darray[i64]
struct Out:
    a: darray[i64]
def Bag() -> Bag:
    return Bag{xs: []}
def build() -> Out:
    can Memory.Allocate, Abort.Panic:
        b: mutable Bag = Bag()
        b.xs.push(7)
        return Out{a: b.xs}
`, AnalyzeOptions{})
	fn := regionPolymorphicFuncType(t, result, "build")
	if !fn.RegionPolymorphic {
		t.Fatalf("a function returning a region-poly constructor's container field inside a struct literal must be region-polymorphic")
	}
}

// SOUNDNESS-NEGATIVE companion: returning a SCALAR field of a region-fed local (copied out by
// value) must NOT make the function region-polymorphic — no region escapes, so no caller region is
// needed. Guards the struct-literal-field rule above from over-classifying scalar field reads.
func TestRegionPolymorphicNotSetForScalarFieldReturn(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rp_scalarfield.elisa", `struct Box:
    value: i64
def make() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        b: Box& = new[auto] Box(7)
        return b.value
`, AnalyzeOptions{})
	fn := regionPolymorphicFuncType(t, result, "make")
	if fn.RegionPolymorphic {
		t.Fatalf("returning a scalar field copy must NOT make a function region-polymorphic")
	}
}

// An explicit region-parameter function is also a valid caller context for a
// region-polymorphic builder. This is the out-parameter shape used by the
// machine lowering helpers: the builder's fresh AST/container result must be
// allocated in the caller's @r region before it is pushed through `out`.
// Previously regionPolymorphicCallerRegionArg only recognized a hidden
// __region_auto caller or an active `in NAME:` scope, so this sound program was
// rejected as having no region in which to instantiate the callee.
func TestRegionPolymorphicCallFromExplicitRegionParam(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "rp_explicit_region_caller.elisa", `def make_row(width: usize) -> darray[i64]:
    row: mutable darray[i64] = []
    i: mutable usize = 0
    while i < width:
        row.push(i.i64())
        i <- i + 1
    return row

def append_row[@r](rows: mutable darray[darray[i64]]& @r, width: usize) -> void:
    row: darray[i64] @r = make_row(width)
    rows.push(row)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("an explicit region-param caller must be able to call a region-polymorphic builder, got:\n%s", strings.Join(errs, "\n"))
	}
}
