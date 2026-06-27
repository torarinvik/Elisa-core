//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// ============================================================
// (a) Facts through `can <effects>:` block
// ============================================================

// A range fact proven just before a `can:` block must be visible inside it.
// `if a > 5:` establishes a >= 6 in scope; the `can:` block opens a child scope
// whose parent chain includes the outer scope, so lookupRangeFact should find it.
func TestRangeFactThroughCanBlock(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def f(a: i64) -> i64:
    if a > 5:
        can Abort.Panic:
            x: i64 is Nat = a
            return x
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "fact_can_block.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("range fact from outer scope must be visible inside can: block (no error under -strict), got: %v", result.Errors())
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("flow-proven refinement inside can: block should emit NO runtime check, got %d", len(result.RefinementChecks))
	}
}

// ============================================================
// (b) Facts through a value-preserving cast rebind
// ============================================================

// y = x.cast[i64] (identity cast) — y must inherit x's range fact.
// `if a > 5:` gives a >= 6; `y = a.cast[i64]` (identity) should carry it to y.
func TestRangeFactThroughIdentityCast(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def f(a: i64) -> i64:
    if a > 5:
        y: i64 = a.cast[i64]
        x: i64 is Nat = y
        return x
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "fact_identity_cast.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("range fact must survive identity cast rebind (no error under -strict), got: %v", result.Errors())
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("flow-proven refinement after identity cast should emit NO runtime check, got %d", len(result.RefinementChecks))
	}
}

// u32 reinterpreted as i64 via .cast: the u32 has a declared range [0, 2^32-1].
// After `if a > 5:`, a has range [6, 2^32-1]. Casting to u64 (same or wider unsigned)
// preserves value — the [6, ...] lower bound carries through.
// NOTE: .cast[T] is reinterpret-only; same-width unsigned-to-unsigned is valid.
func TestRangeFactThroughUnsignedIdentityCast(t *testing.T) {
	src := `
law Nat(self: u64) = self >= 0

def f(a: u64) -> u64:
    if a > 5:
        y: u64 = a.cast[u64]
        x: u64 is Nat = y
        return x
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "fact_u64_identity_cast.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("range fact must survive u64 identity cast rebind (no error under -strict), got: %v", result.Errors())
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("flow-proven refinement after u64 identity cast should emit NO runtime check, got %d", len(result.RefinementChecks))
	}
}

// ============================================================
// Soundness (NEGATIVE) tests
// ============================================================

// SOUNDNESS: a sign-flip cast (u64 -> i64) with an open upper bound MUST NOT carry the lower
// bound. A u64 value in [200, ∞) can exceed 2^63-1, in which case the i64 reinterpretation is
// negative. The range [200, ∞) does NOT transfer to the i64 rebinding.
// (Different-width casts between integers are rejected as value conversions, not reinterprets.)
func TestRangeFactNotCarriedThroughSignFlipCastOpenBound(t *testing.T) {
	src := `
law Big(self: i64) = self >= 200

def f(a: u64) -> i64:
    if a >= 200:
        y: i64 = a.cast[i64]
        x: i64 is Big = y
        return x
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "fact_signflip_cast_sound.elisa", src, AnalyzeOptions{})
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "could not be proven statically") {
		t.Fatalf("SOUNDNESS: sign-flip cast with open upper bound must NOT carry range >= 200; expected proof failure, got: %q", diags)
	}
}

// SOUNDNESS: a mutation inside a `can:` block must invalidate the range fact for that variable.
// After `a <- 0 - 1` inside the block, the outer `a >= 6` range no longer holds for Pos.
// The compiler must either error (violated) or warn (runtime check) — the key is it must NOT
// silently succeed with a static proof.
func TestRangeFactInvalidatedByMutationInsideCanBlock(t *testing.T) {
	src := `
law Pos(self: i64) = self > 0

def f(a: mutable i64) -> i64:
    if a > 5:
        can Abort.Panic:
            a <- 0 - 1
            x: i64 is Pos = a
            return x
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "fact_can_mutation_sound.elisa", src, AnalyzeOptions{})
	diags := allDiagnostics(result)
	// Must produce SOME diagnostic — either "violated" or "could not be proven statically".
	// It must NOT silently succeed with a static proof (that would be unsound).
	if len(result.Errors()) == 0 && !strings.Contains(diags, "could not be proven statically") {
		t.Fatalf("SOUNDNESS: mutation inside can: block must NOT silently prove the refinement; expected diagnostic, got: %q", diags)
	}
}
