package semantic

import (
	"strings"
	"testing"
)

// new[auto] heap-allocates a struct into the innermost active inferred region (the native stack
// arena), no explicit region/pool. Inside an `in auto:` it compiles cleanly and the value is used
// within the region.
func TestNewAutoAllocatesInInferredRegionCleanly(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "na_ok.elisa", `struct Box:
    value: i64
def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            b: Box& = new[auto] Box(7)
            return b.value
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("new[auto] inside an in-auto region must compile cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
}

// docs/75: returning a new[auto] value is NOT an escape — it makes the function region-polymorphic.
// The inferred region is threaded from the caller (the hidden `__region_auto` Arena& param), so the
// result outlives the call by construction. Such a function compiles cleanly and is classified
// region-polymorphic; the threading makes the return sound (verified end-to-end at runtime).
func TestNewAutoReturnIsRegionPolymorphic(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "na_esc.elisa", `struct Box:
    value: i64
def f() -> Box&:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            b: Box& = new[auto] Box(7)
            return b
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("returning a new[auto] value must compile (region-polymorphic), got:\n%s", strings.Join(errs, "\n"))
	}
	fn := regionPolymorphicFuncType(t, result, "f")
	if !fn.RegionPolymorphic {
		t.Fatalf("a function returning a new[auto] value must be classified region-polymorphic")
	}
}

// An explicitly-named local region (`in scratch:`) is NOT region-polymorphic: returning a value out
// of it is a real escape bug and must still be rejected — the region is freed at scope exit and
// nothing threads it from the caller.
func TestNamedLocalRegionEscapeStillRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "na_named_esc.elisa", `struct Box:
    value: i64
def f() -> Box&:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(reserve_commit):
            b: Box& = new[scratch] Box(7)
            return b
`, AnalyzeOptions{})
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "cannot return reference") {
		t.Fatalf("a reference escaping an explicitly-named local region must be rejected, got:\n%s", all)
	}
}

// new[auto] is inference-driven like a container: a function that uses it WITHOUT any explicit
// `in auto:` has the region synthesized for it automatically (the lifetime is detected and the
// allocation placed in the matching inferred region). So it compiles cleanly — no "undefined
// identifier auto", no "needs a region".
func TestNewAutoInfersRegionWithoutInAuto(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "na_inf.elisa", `struct Box:
    value: i64
def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        b: Box& = new[auto] Box(7)
        return b.value
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("new[auto] must work without an explicit in-auto (region synthesized by inference), got:\n%s", strings.Join(errs, "\n"))
	}
}

// new[auto] structs are tracked as fixed-footprint members of the inferred region: assigned to the
// shared stack 0 (reclaimed in bulk at region exit) and counted, so the lifetime-class summary is
// exact (a region holding only new[auto] structs has one class — the region exit).
func TestNewAutoTrackedAsFixedFootprintMember(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "na_track.elisa", `struct Box:
    value: i64
def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        b: Box& = new[auto] Box(7)
        c: Box& = new[auto] Box(8)
        return b.value + c.value
`, AnalyzeOptions{})
	asn := onlyRegionStack(t, result)
	if asn.StackOf["b"] != 0 || asn.StackOf["c"] != 0 {
		t.Fatalf("new[auto] structs must be tracked on the shared stack 0, got StackOf=%v", asn.StackOf)
	}
	if got := asn.regionLifetimeClasses(); got != 1 {
		t.Fatalf("a region holding new[auto] structs must count 1 lifetime class (region exit), got %d", got)
	}
}
