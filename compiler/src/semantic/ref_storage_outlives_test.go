package semantic

import (
	"strings"
	"testing"
)

// The emulator.elisa lie: bytes built in a local darray (region/stack-lived) are
// returned as `static u8&`, claiming a program-long lifetime. Under unsafe-perm
// enforcement this must require an explicit Unsafe opt-out (the safety property
// we care about), whether reported as a pointer cast or a lifetime-widening cast.
func TestRefStorageOutlivesRequiresOptOutForRegionAsStatic(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "outlives_region_as_static.elisa", `def build(owner: mutable Arena&) -> static u8&:
    out: mutable darray[u8] = []
    in owner:
        out.push(65)
        out.push(0)
    return out[0].ref[static u8&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	joined := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(joined, "can Unsafe.PointerCast") {
		t.Fatalf("expected the region->static cast to require an Unsafe.PointerCast opt-out, got:\n%s", joined)
	}
}

// The lifetime-widening branch fires for the provenance gap the pointer-cast
// check misses: a borrow that is assignable (so not a pointer cast) yet widens a
// stack/region borrow to a longer-lived storage class.
func TestRefStorageOutlivesFlagsExplicitStorageWidening(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "outlives_explicit_widen.elisa", `def widen(p: stack i32&) -> static i32&:
    return p.cast[static i32&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	joined := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(joined, "can Unsafe.PointerCast") {
		t.Fatalf("expected explicit stack->static widening to require an Unsafe.PointerCast opt-out, got:\n%s", joined)
	}
}

// Explicitly opting out with `can Unsafe.PointerCast` suppresses the warning:
// the author has acknowledged the unsafe lifetime claim.
func TestRefStorageOutlivesOptOutSuppressesWarning(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "outlives_opt_out.elisa", `def build(owner: mutable Arena&) -> static u8&:
    out: mutable darray[u8] = []
    in owner:
        out.push(65)
        out.push(0)
    can Unsafe.PointerCast:
        return out[0].ref[static u8&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	joined := strings.Join(result.Warnings(), "\n")
	if strings.Contains(joined, "lifetime-widening reference cast") {
		t.Fatalf("expected opt-out to suppress lifetime-widening warning, got:\n%s", joined)
	}
}

// A string literal genuinely has static storage; returning it as `static u8&`
// must NOT be flagged.
func TestRefStorageOutlivesAcceptsStringLiteral(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "outlives_literal.elisa", `def label() -> static u8&:
    return "hello"
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	joined := strings.Join(result.Warnings(), "\n")
	if strings.Contains(joined, "lifetime-widening reference cast") {
		t.Fatalf("expected no lifetime-widening warning for string literal, got:\n%s", joined)
	}
}

// Re-borrowing into a `static u8&` PARAMETER (which already points at static
// storage) as `static u8&` is honest and must NOT be flagged.
func TestRefStorageOutlivesAcceptsStaticParamReborrow(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "outlives_static_param.elisa", `def at(path: static u8&, i: usize) -> static u8&:
    return path[i].ref[static u8&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	joined := strings.Join(result.Warnings(), "\n")
	if strings.Contains(joined, "lifetime-widening reference cast") {
		t.Fatalf("expected no lifetime-widening warning for static-param reborrow, got:\n%s", joined)
	}
}
