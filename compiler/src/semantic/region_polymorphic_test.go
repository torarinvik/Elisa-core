package semantic

import (
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
