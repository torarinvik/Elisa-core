//go:build cgo

package semantic

import (
	"testing"

	"elisacore/src/lexer"
)

// entailment_strengthening_test.go: tests for the new sound entailment cases added to
// rangeEntailsConstraint and tryProveRefinementByFlow.
//
// Each new case has a POSITIVE test (the prover should discharge statically) and a NEAR-MISS
// NEGATIVE test (soundness: a slightly weaker situation must NOT be discharged statically).

// ─── Unit tests for rangeEntailsConstraint: BANGEQ (x != c) ─────────────────

// TestEntailmentNotEqualBelowRange: range [0,4] entails x != 5 (hi=4 < 5, range fully below c).
// Soundness: every v in [0,4] satisfies v < 5, hence v != 5.
func TestEntailmentNotEqualBelowRange(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_BANGEQ, c: 5}
	r := numRange{loKnown: true, lo: 0, hiKnown: true, hi: 4}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[0,4] must entail x != 5 (hi=4 < 5)")
	}
}

// TestEntailmentNotEqualAboveRange: range [6,10] entails x != 5 (lo=6 > 5, range fully above c).
// Soundness: every v in [6,10] satisfies v > 5, hence v != 5.
func TestEntailmentNotEqualAboveRange(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_BANGEQ, c: 5}
	r := numRange{loKnown: true, lo: 6, hiKnown: true, hi: 10}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[6,10] must entail x != 5 (lo=6 > 5)")
	}
}

// TestEntailmentNotEqualOneSidedLoOnly: range [1,∞) entails x != 0 (lo=1 > 0).
// Soundness: all values are >= 1 > 0, none can equal 0.
func TestEntailmentNotEqualOneSidedLoOnly(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_BANGEQ, c: 0}
	r := numRange{loKnown: true, lo: 1, hiKnown: false}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[1,∞) must entail x != 0 (lo=1 > 0)")
	}
}

// TestEntailmentNotEqualOneSidedHiOnly: range (-∞,9] entails x != 10 (hi=9 < 10).
// Soundness: all values are <= 9 < 10, none can equal 10.
func TestEntailmentNotEqualOneSidedHiOnly(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_BANGEQ, c: 10}
	r := numRange{loKnown: false, hiKnown: true, hi: 9}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("(-∞,9] must entail x != 10 (hi=9 < 10)")
	}
}

// Near-miss: [3,7] does NOT entail x != 5 (5 is inside the range, could be taken).
func TestEntailmentNotEqualNearMiss_InRange(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_BANGEQ, c: 5}
	r := numRange{loKnown: true, lo: 3, hiKnown: true, hi: 7}
	if rangeEntailsConstraint(r, k) {
		t.Fatal("[3,7] must NOT entail x != 5 (5 is within the range)")
	}
}

// Near-miss: [5,7] does NOT entail x != 5 (lo == c, value could be exactly 5).
func TestEntailmentNotEqualNearMiss_LoEqualsC(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_BANGEQ, c: 5}
	r := numRange{loKnown: true, lo: 5, hiKnown: true, hi: 7}
	if rangeEntailsConstraint(r, k) {
		t.Fatal("[5,7] must NOT entail x != 5 (lo=5 == c)")
	}
}

// Near-miss: [3,5] does NOT entail x != 5 (hi == c, value could be exactly 5).
func TestEntailmentNotEqualNearMiss_HiEqualsC(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_BANGEQ, c: 5}
	r := numRange{loKnown: true, lo: 3, hiKnown: true, hi: 5}
	if rangeEntailsConstraint(r, k) {
		t.Fatal("[3,5] must NOT entail x != 5 (hi=5 == c)")
	}
}

// Near-miss: fully open range does NOT entail x != 5.
func TestEntailmentNotEqualNearMiss_OpenRange(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_BANGEQ, c: 5}
	r := numRange{loKnown: false, hiKnown: false}
	if rangeEntailsConstraint(r, k) {
		t.Fatal("(-∞,∞) must NOT entail x != 5")
	}
}

// ─── Integration tests: equality fact (written const) → comparison ────────────

// A constant-initialized local passed to a function requiring a law: x=3 satisfies x >= 3.
// Soundness: x is pinned to exactly 3; rangeFact [3,3] entails x >= 3 since lo=3 >= 3.
func TestEntailmentEqualityFactEntailsGTEQ(t *testing.T) {
	src := `
law AtLeastThree(self: i64) = self >= 3

def need_at_least_three(v: i64 is AtLeastThree) -> i64:
    return v

def test() -> i64:
    x: i64 = 3
    return need_at_least_three(x)
`
	result := analyzeTreeTestSource(t, "entail_eq_gteq.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("x=3 must statically entail x >= 3; got runtime check: %s", allDiagnostics(result))
	}
}

// A constant-initialized local passed to a function requiring a law: x=3 satisfies x > 2.
// Soundness: [3,3] entails x > 2 since lo=3 > 2.
func TestEntailmentEqualityFactEntailsGT(t *testing.T) {
	src := `
law GreaterThanTwo(self: i64) = self > 2

def need_gt_two(v: i64 is GreaterThanTwo) -> i64:
    return v

def test() -> i64:
    x: i64 = 3
    return need_gt_two(x)
`
	result := analyzeTreeTestSource(t, "entail_eq_gt.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("x=3 must statically entail x > 2; got runtime check: %s", allDiagnostics(result))
	}
}

// Near-miss: x=3 does NOT statically entail x > 3 (strict; value is exactly at the boundary).
// The prover must either refute (emit a "violated" error) or defer (emit a runtime-check warning).
// It must NOT silently accept (no errors, no warnings). We use WithSemanticErrors to allow errors.
func TestEntailmentEqualityFactNearMiss_StrictBoundary(t *testing.T) {
	src := `
law GreaterThanThree(self: i64) = self > 3

def need_gt_three(v: i64 is GreaterThanThree) -> i64:
    return v

def test() -> i64:
    x: i64 = 3
    return need_gt_three(x)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "entail_eq_gt_miss.elisa", src)
	// Sound: must either refute (error containing "violated") or defer to runtime check.
	diags := allDiagnostics(result)
	hasRefutation := contains(diags, "violated") || contains(diags, "could not be proven statically")
	if !hasRefutation {
		t.Fatalf("x=3 must NOT statically entail x > 3; expected a refutation or runtime-check warning, got: %s", allDiagnostics(result))
	}
}

// Near-miss: x=2 does NOT statically entail x >= 3 (value is below the bound).
// Sound: the prover must refute or defer — not silently approve.
func TestEntailmentEqualityFactNearMiss_WrongValue(t *testing.T) {
	src := `
law AtLeastThree(self: i64) = self >= 3

def need_at_least_three(v: i64 is AtLeastThree) -> i64:
    return v

def test() -> i64:
    x: i64 = 2
    return need_at_least_three(x)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "entail_eq_gteq_miss.elisa", src)
	// Sound: must either refute (error containing "violated") or defer to runtime check.
	diags := allDiagnostics(result)
	hasRefutation := contains(diags, "violated") || contains(diags, "could not be proven statically")
	if !hasRefutation {
		t.Fatalf("x=2 must NOT statically entail x >= 3; expected a refutation or runtime-check warning, got: %s", allDiagnostics(result))
	}
}

// ─── Integration test: non-negativity from unsigned declared type ─────────────

// A u32 parameter satisfies `x >= 0` without any guard — unsigned types are non-negative by
// construction. Soundness: declaredTypeRange(u32) = [0, 4294967295], and lo=0 >= 0.
// The tryProveRefinementByFlow fallback seeds the declared-type range when no tighter rangeFact
// exists, so this case discharges at the call site without a runtime check.
func TestEntailmentUnsignedTypeNonNegativity(t *testing.T) {
	src := `
law NonNegative(self: u32) = self >= 0

def need_nonneg(x: u32 is NonNegative) -> u32:
    return x

def caller(v: u32) -> u32:
    return need_nonneg(v)
`
	result := analyzeTreeTestSource(t, "entail_unsigned_nonneg.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("u32 must statically entail x >= 0 (unsigned type); got runtime check: %s", allDiagnostics(result))
	}
}

// Near-miss: a bare u32 parameter does NOT statically entail `x > 0` (zero is a valid u32).
// The declared-type range is [0, 4294967295] with lo=0; lo > 0 is false, so GT is not entailed.
func TestEntailmentUnsignedTypeNearMiss_StrictPositive(t *testing.T) {
	src := `
law StrictlyPositive(self: u32) = self > 0

def need_pos(x: u32 is StrictlyPositive) -> u32:
    return x

def caller(v: u32) -> u32:
    return need_pos(v)
`
	result := analyzeTreeTestSource(t, "entail_unsigned_pos_miss.elisa", src)
	if noRuntimeCheck(result) {
		t.Fatalf("u32 must NOT statically entail x > 0 (could be 0); expected runtime-check warning")
	}
}
