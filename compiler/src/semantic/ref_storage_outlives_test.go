package semantic

import (
	"strings"
	"testing"
)

// The emulator.elisa lie: bytes built in a local darray (region/stack-lived) are
// returned as `static u8&`, claiming a program-long lifetime. Under unsafe-perm
// enforcement this must require an explicit Unsafe opt-out (now a hard error),
// whether reported as a pointer cast or a lifetime-widening cast.
func TestRefStorageOutlivesRequiresOptOutForRegionAsStatic(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "outlives_region_as_static.elisa", `def build(owner: mutable Arena&) -> static u8&:
    out: mutable darray[u8] = []
    in owner:
        out.push(65)
        out.push(0)
    return out[0].ref[static u8&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "can Unsafe.PointerCast") {
		t.Fatalf("expected the region->static cast to require an Unsafe.PointerCast opt-out, got:\n%s", allDiagnostics(result))
	}
}

// The lifetime-widening branch fires for the provenance gap the pointer-cast
// check misses: an explicit stack borrow widened to a longer-lived storage class.
func TestRefStorageOutlivesFlagsExplicitStorageWidening(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "outlives_explicit_widen.elisa", `def widen(p: stack i32&) -> static i32&:
    return p.cast[static i32&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "can Unsafe.PointerCast") {
		t.Fatalf("expected explicit stack->static widening to require an Unsafe.PointerCast opt-out, got:\n%s", allDiagnostics(result))
	}
}

// Explicitly opting out with the precise unsafe grants compiles cleanly: the
// author has acknowledged the lifetime widening, index, and byte-buffer
// reinterpretation hazards.
func TestRefStorageOutlivesOptOutSatisfiesGate(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "outlives_opt_out.elisa", `def build(owner: mutable Arena&) -> static u8&:
    out: mutable darray[u8] = []
    in owner:
        out.push(65)
        out.push(0)
    can Unsafe.PointerCast, Unsafe.UncheckedIndex, Unsafe.BufferReinterpret:
        return out[0].ref[static u8&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, "requires") {
		t.Fatalf("expected opt-out grants to satisfy the gates, got:\n%s", all)
	}
}

// A string literal genuinely has static storage; returning it as `static u8&`
// must NOT be flagged as a lifetime-widening / pointer cast.
func TestRefStorageOutlivesAcceptsStringLiteral(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "outlives_literal.elisa", `def label() -> static u8&:
    return "hello"
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, "lifetime-widening") || strings.Contains(all, "pointer cast") {
		t.Fatalf("expected no lifetime/cast diagnostic for string literal, got:\n%s", all)
	}
}

// Re-borrowing into a `static u8&` PARAMETER (which already points at static
// storage) is honest and must NOT be flagged as lifetime-widening (an orthogonal
// unchecked-index diagnostic on path[i] is expected and ignored here).
func TestRefStorageOutlivesAcceptsStaticParamReborrow(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "outlives_static_param.elisa", `def at(path: static u8&, i: usize) -> static u8&:
    return path[i].ref[static u8&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if strings.Contains(allDiagnostics(result), "lifetime-widening") {
		t.Fatalf("expected no lifetime-widening diagnostic for static-param reborrow, got:\n%s", allDiagnostics(result))
	}
}
