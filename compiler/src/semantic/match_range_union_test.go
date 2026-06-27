//go:build cgo

package semantic

import "testing"

// Integer match expression in value position: the result's range is the JOIN of all arm-value
// ranges. When every arm yields an integer literal in [lo, hi], a downstream `is Bounded[lo, hi]`
// refinement should discharge statically with no runtime check (completeness-positive test).

// COMPLETENESS-POSITIVE (immutable local): all arms within [0, 2] — Bounded[0, 2] proves statically.
func TestMatchExprRangeUnionImmutableProves(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def f(k: i64) -> i64:
    x: i64 is Bounded[0, 2] = match k:
        0: 0
        1: 1
        _: 2
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "match_range_immut.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("all arms in [0,2]: Bounded[0,2] should prove statically, got:\n%v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("proven refinement should emit NO runtime check, got %d", len(result.RefinementChecks))
	}
}

// COMPLETENESS-POSITIVE (mutable local): same arms, same bound, mutable declaration.
func TestMatchExprRangeUnionMutableProves(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def f(k: i64) -> i64:
    x: mutable i64 = match k:
        0: 0
        1: 1
        _: 2
    y: i64 is Bounded[0, 2] = x
    return y
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "match_range_mut.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("mutable match result in [0,2]: Bounded[0,2] should prove statically, got:\n%v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("proven refinement should emit NO runtime check, got %d", len(result.RefinementChecks))
	}
}

// SOUNDNESS-NEGATIVE: one arm returns a value OUTSIDE [0, 2] — the bound MUST NOT prove.
// The refinement should fall back to runtime (or a strict error) rather than being silently accepted.
func TestMatchExprRangeUnionOutOfRangeArmDoesNotProve(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def f(k: i64) -> i64:
    x: i64 is Bounded[0, 2] = match k:
        0: 0
        1: 1
        _: 99
    return x
`
	// With EnforceStrictProofs the unproven refinement becomes a hard error (the wildcard arm
	// yields 99, which is outside [0, 2], so the joined range [0, 99] does not entail Bounded[0, 2]).
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "match_range_oob.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	diags := allDiagnostics(result)
	if len(result.Errors()) == 0 && len(result.RefinementChecks) == 0 {
		t.Fatalf("arm yields 99 which is outside [0,2]: bound MUST NOT prove silently, got no error and no runtime check\n%s", diags)
	}
}
