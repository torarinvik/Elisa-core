//go:build cgo

package semantic

import (
	"testing"

	"elisacore/src/lexer"
)

// entailment_matrix_test.go: comprehensive matrix of refinement-entailment test cases
// covering all major proving-power tiers (interval, equality, inequality, shifts, scaling,
// type-derived bounds). Each case has a POSITIVE test (prover must succeed statically)
// and a NEAR-MISS NEGATIVE test (soundness: weaker variant must NOT succeed).
//
// This file complements:
//   - refinement_subsumption_test.go (interval ⊆ comparison/interval subsumption)
//   - entailment_strengthening_test.go (inequality, equality facts, unsigned types)
//   - entailment_v2_test.go (additive shift, monotonic scaling)
//
// NEW CASES added here (not covered in the above):
//   1. LTEQ (<=) with intervals and equality facts
//   2. Negative lo/hi boundary cases for interval entailment
//   3. Combined inequality + inequality chains (e.g., [3,7] entails both x>=3 and x<=7)
//   4. Equality to strict inequality boundary push (x=k entails x>k-1 but not x>k)
//   5. Scaled ranges proving inequality comparisons
//   6. Shifted ranges proving inequality comparisons
//   7. Signed-to-unsigned conversions with non-negativity
//   8. Point interval edge cases for shift/scale

// ─── Interval entails LTEQ (<=) ────────────────────────────────────────────────

// Positive: [2, 8] entails x <= 8 (hi=8 <= 8).
func TestMatrixIntervalEntailsLTEQ(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_LTEQ, c: 8}
	r := numRange{loKnown: true, lo: 2, hiKnown: true, hi: 8}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[2,8] must entail x <= 8 (hi=8 <= 8)")
	}
}

// Near-miss: [2, 9] does NOT entail x <= 8 (hi=9 > 8).
func TestMatrixIntervalNotEntailsLTEQ_HiExceeds(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_LTEQ, c: 8}
	r := numRange{loKnown: true, lo: 2, hiKnown: true, hi: 9}
	if rangeEntailsConstraint(r, k) {
		t.Fatal("[2,9] must NOT entail x <= 8 (hi=9 > 8)")
	}
}

// ─── Interval entails LT (<) ──────────────────────────────────────────────────

// Positive: [2, 7] entails x < 8 (hi=7 < 8).
func TestMatrixIntervalEntailsLT(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_LT, c: 8}
	r := numRange{loKnown: true, lo: 2, hiKnown: true, hi: 7}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[2,7] must entail x < 8 (hi=7 < 8)")
	}
}

// Near-miss: [2, 8] does NOT entail x < 8 (hi=8, not < 8).
func TestMatrixIntervalNotEntailsLT_HiEqBound(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_LT, c: 8}
	r := numRange{loKnown: true, lo: 2, hiKnown: true, hi: 8}
	if rangeEntailsConstraint(r, k) {
		t.Fatal("[2,8] must NOT entail x < 8 (hi=8 == bound)")
	}
}

// ─── Interval entails GT (>) ──────────────────────────────────────────────────

// Positive: [5, 10] entails x > 4 (lo=5 > 4).
func TestMatrixIntervalEntailsGT(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_GT, c: 4}
	r := numRange{loKnown: true, lo: 5, hiKnown: true, hi: 10}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[5,10] must entail x > 4 (lo=5 > 4)")
	}
}

// Near-miss: [4, 10] does NOT entail x > 4 (lo=4, not > 4).
func TestMatrixIntervalNotEntailsGT_LoEqBound(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_GT, c: 4}
	r := numRange{loKnown: true, lo: 4, hiKnown: true, hi: 10}
	if rangeEntailsConstraint(r, k) {
		t.Fatal("[4,10] must NOT entail x > 4 (lo=4 == bound)")
	}
}

// ─── Interval entails GTEQ (>=) ───────────────────────────────────────────────

// Positive: [5, 10] entails x >= 5 (lo=5 >= 5).
func TestMatrixIntervalEntailsGTEQ(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_GTEQ, c: 5}
	r := numRange{loKnown: true, lo: 5, hiKnown: true, hi: 10}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[5,10] must entail x >= 5 (lo=5 >= 5)")
	}
}

// Near-miss: [4, 10] does NOT entail x >= 5 (lo=4 < 5).
func TestMatrixIntervalNotEntailsGTEQ_LoInsufficient(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_GTEQ, c: 5}
	r := numRange{loKnown: true, lo: 4, hiKnown: true, hi: 10}
	if rangeEntailsConstraint(r, k) {
		t.Fatal("[4,10] must NOT entail x >= 5 (lo=4 < 5)")
	}
}

// ─── Negative bound intervals ──────────────────────────────────────────────────

// Positive: [-10, -2] entails x < 0 (hi=-2 < 0).
func TestMatrixNegativeIntervalEntailsNegative(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_LT, c: 0}
	r := numRange{loKnown: true, lo: -10, hiKnown: true, hi: -2}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[-10,-2] must entail x < 0 (hi=-2 < 0)")
	}
}

// Positive: [-10, -2] entails x <= -2 (hi=-2 <= -2).
func TestMatrixNegativeIntervalEntailsLTEQ(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_LTEQ, c: -2}
	r := numRange{loKnown: true, lo: -10, hiKnown: true, hi: -2}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[-10,-2] must entail x <= -2 (hi=-2 <= -2)")
	}
}

// Positive: [-10, -2] entails x >= -10 (lo=-10 >= -10).
func TestMatrixNegativeIntervalEntailsGTEQ(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_GTEQ, c: -10}
	r := numRange{loKnown: true, lo: -10, hiKnown: true, hi: -2}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[-10,-2] must entail x >= -10 (lo=-10 >= -10)")
	}
}

// Near-miss: [-10, -2] does NOT entail x >= -1 (lo=-10 < -1).
func TestMatrixNegativeIntervalNotEntailsGTEQ(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_GTEQ, c: -1}
	r := numRange{loKnown: true, lo: -10, hiKnown: true, hi: -2}
	if rangeEntailsConstraint(r, k) {
		t.Fatal("[-10,-2] must NOT entail x >= -1 (lo=-10 < -1)")
	}
}

// ─── Integration: equality fact entails LTEQ ──────────────────────────────────

// Positive: x=10 entails x <= 10 (rangeFact [10,10]; hi=10 <= 10).
func TestMatrixEqualityFactEntailsLTEQ(t *testing.T) {
	src := `
law AtMost10(self: i64) = self <= 10

def need_at_most_10(v: i64 is AtMost10) -> i64:
    return v

def test() -> i64:
    x: i64 = 10
    return need_at_most_10(x)
`
	result := analyzeTreeTestSource(t, "matrix_eq_lteq.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("x=10 must statically entail x <= 10; got runtime check: %s", allDiagnostics(result))
	}
}

// Near-miss: x=11 does NOT entail x <= 10.
func TestMatrixEqualityFactNotEntailsLTEQ(t *testing.T) {
	src := `
law AtMost10(self: i64) = self <= 10

def need_at_most_10(v: i64 is AtMost10) -> i64:
    return v

def test() -> i64:
    x: i64 = 11
    return need_at_most_10(x)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "matrix_eq_lteq_miss.elisa", src)
	diags := allDiagnostics(result)
	hasRefutation := contains(diags, "violated") || contains(diags, "could not be proven statically")
	if !hasRefutation {
		t.Fatalf("x=11 must NOT entail x <= 10; expected refutation or runtime check, got: %s", diags)
	}
}

// ─── Integration: equality fact entails LT ───────────────────────────────────

// Positive: x=9 entails x < 10 (rangeFact [9,9]; hi=9 < 10).
func TestMatrixEqualityFactEntailsLT(t *testing.T) {
	src := `
law LessThan10(self: i64) = self < 10

def need_lt_10(v: i64 is LessThan10) -> i64:
    return v

def test() -> i64:
    x: i64 = 9
    return need_lt_10(x)
`
	result := analyzeTreeTestSource(t, "matrix_eq_lt.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("x=9 must statically entail x < 10; got runtime check: %s", allDiagnostics(result))
	}
}

// Near-miss: x=10 does NOT entail x < 10 (boundary).
func TestMatrixEqualityFactNotEntailsLT(t *testing.T) {
	src := `
law LessThan10(self: i64) = self < 10

def need_lt_10(v: i64 is LessThan10) -> i64:
    return v

def test() -> i64:
    x: i64 = 10
    return need_lt_10(x)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "matrix_eq_lt_miss.elisa", src)
	diags := allDiagnostics(result)
	hasRefutation := contains(diags, "violated") || contains(diags, "could not be proven statically")
	if !hasRefutation {
		t.Fatalf("x=10 must NOT entail x < 10; expected refutation or runtime check, got: %s", diags)
	}
}

// ─── Integration: equality to strict inequality boundary ────────────────────────

// Positive: x=5 entails x > 4 (rangeFact [5,5]; lo=5 > 4).
func TestMatrixEqualityFactEntailsStrictGT(t *testing.T) {
	src := `
law GreaterThan4(self: i64) = self > 4

def need_gt_4(v: i64 is GreaterThan4) -> i64:
    return v

def test() -> i64:
    x: i64 = 5
    return need_gt_4(x)
`
	result := analyzeTreeTestSource(t, "matrix_eq_gt_strict.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("x=5 must statically entail x > 4; got runtime check: %s", allDiagnostics(result))
	}
}

// Near-miss: x=4 does NOT entail x > 4 (boundary; 4 is not > 4).
func TestMatrixEqualityFactNotEntailsStrictGT(t *testing.T) {
	src := `
law GreaterThan4(self: i64) = self > 4

def need_gt_4(v: i64 is GreaterThan4) -> i64:
    return v

def test() -> i64:
    x: i64 = 4
    return need_gt_4(x)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "matrix_eq_gt_strict_miss.elisa", src)
	diags := allDiagnostics(result)
	hasRefutation := contains(diags, "violated") || contains(diags, "could not be proven statically")
	if !hasRefutation {
		t.Fatalf("x=4 must NOT entail x > 4; expected refutation or runtime check, got: %s", diags)
	}
}

// ─── Integration: combined inequality chain (both lo and hi) ────────────────────

// Positive: x in [3, 7], satisfy BOTH x >= 3 AND x <= 7 in a chain call.
func TestMatrixIntervalEntailsBothBounds(t *testing.T) {
	src := `
law Bounded(self: i64) = self >= 3 and self <= 7

def need_bounded(v: i64 is Bounded) -> i64:
    return v

def test(x: i64) -> i64:
    if x >= 3 and x <= 7:
        return need_bounded(x)
    return 0
`
	result := analyzeTreeTestSource(t, "matrix_both_bounds.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("range [3,7] must statically entail x >= 3 AND x <= 7; got runtime check: %s", allDiagnostics(result))
	}
}

// Near-miss: x in [3, 8], fail to satisfy x <= 7 (hi=8 > 7).
func TestMatrixIntervalNotEntailsBothBounds_HiViolation(t *testing.T) {
	src := `
law Bounded(self: i64) = self >= 3 and self <= 7

def need_bounded(v: i64 is Bounded) -> i64:
    return v

def test(x: i64) -> i64:
    if x >= 3 and x <= 8:
        return need_bounded(x)
    return 0
`
	result := analyzeTreeTestSource(t, "matrix_both_bounds_miss.elisa", src)
	// Must see a runtime check or error; the hi=8 violates x<=7.
	if noRuntimeCheck(result) {
		t.Fatalf("[3,8] must NOT entail x >= 3 AND x <= 7 (hi=8 > 7); expected runtime check")
	}
}

// ─── Shifted range entails comparison: x+c in [lo+c, hi+c] ⊆ comparison ──────

// Positive: x in [2, 5], prove x+3 > 4 (shifted [5,8]; lo=5 > 4).
func TestMatrixShiftedRangeEntailsGT(t *testing.T) {
	src := `
law GreaterThan4(self: i64) = self > 4

def need_gt_4(v: i64 is GreaterThan4) -> i64:
    return v

def test(x: i64 is GreaterThan[1]) -> i64:
    return need_gt_4(x + 3)

law GreaterThan(self: i64, k: i64) = self > k
`
	result := analyzeTreeTestSource(t, "matrix_shift_gt.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("x > 1 must allow x+3 to prove x+3 > 4; got runtime check: %s", allDiagnostics(result))
	}
}

// ─── Scaled range entails comparison: x*k in [lo*k, hi*k] ⊆ comparison ───────

// Positive: x in [2, 3], prove x*4 >= 8 (scaled [8,12]; lo=8 >= 8).
func TestMatrixScaledRangeEntailsGTEQ(t *testing.T) {
	src := `
law AtLeast8(self: i64) = self >= 8

def need_at_least_8(v: i64 is AtLeast8) -> i64:
    return v

def test(x: i64 is InRange[2, 3]) -> i64:
    return need_at_least_8(x * 4)

law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
`
	result := analyzeTreeTestSource(t, "matrix_scale_gteq.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("x in [2,3] must allow x*4 to prove x*4 >= 8; got runtime check: %s", allDiagnostics(result))
	}
}

// ─── Point interval edge cases for shift/scale ────────────────────────────────

// Positive: x=5 (point [5,5]), prove x+2 is in [7,7] (shifted point).
func TestMatrixPointIntervalShift(t *testing.T) {
	src := `
law PointSeven(self: i64) = self >= 7 and self <= 7

def need_seven(v: i64 is PointSeven) -> i64:
    return v

def test(x: i64 is PointFive) -> i64:
    return need_seven(x + 2)

law PointFive(self: i64) = self >= 5 and self <= 5
`
	result := analyzeTreeTestSource(t, "matrix_point_shift.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("point [5,5] must allow x+2 to prove [7,7]; got runtime check: %s", allDiagnostics(result))
	}
}

// Positive: x=3 (point [3,3]), prove x*4 is in [12,12] (scaled point).
func TestMatrixPointIntervalScale(t *testing.T) {
	src := `
law Point12(self: i64) = self >= 12 and self <= 12

def need_12(v: i64 is Point12) -> i64:
    return v

def test(x: i64 is Point3) -> i64:
    return need_12(x * 4)

law Point3(self: i64) = self >= 3 and self <= 3
`
	result := analyzeTreeTestSource(t, "matrix_point_scale.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("point [3,3] must allow x*4 to prove [12,12]; got runtime check: %s", allDiagnostics(result))
	}
}

// ─── Signed-to-unsigned with non-negativity ────────────────────────────────────

// Positive: i64 in [0, 10] is assignable to u32 (requires non-negative); then u32 >= 0
// without additional checks.
func TestMatrixSignedNonnegativeToUnsigned(t *testing.T) {
	src := `
law Bounded(self: u32) = self >= 0

def need_bounded(v: u32 is Bounded) -> u32:
    return v

def test() -> u32:
    x: i64 = 5
    if x >= 0:
        y: u32 = x.u32()
        return need_bounded(y)
    return 0
`
	result := analyzeTreeTestSource(t, "matrix_signed_to_unsigned.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("u32 must statically entail >= 0; got runtime check: %s", allDiagnostics(result))
	}
}

// ─── Two-sided open intervals ────────────────────────────────────────────────────

// Positive: fully-bounded [3, 10] entails all four comparisons in a chain.
func TestMatrixFullyBoundedInterval(t *testing.T) {
	src := `
law FourWay(self: i64) = self >= 3 and self <= 10 and self > 2 and self < 11

def need_four_way(v: i64 is FourWay) -> i64:
    return v

def test(x: i64 is Bounded[3, 10]) -> i64:
    return need_four_way(x)

law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
`
	result := analyzeTreeTestSource(t, "matrix_fully_bounded.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("[3,10] must statically entail all four bounds; got runtime check: %s", allDiagnostics(result))
	}
}

// Near-miss: [3, 10] does NOT entail x < 10 (hi=10 is NOT < 10).
func TestMatrixFullyBoundedNotStrict(t *testing.T) {
	src := `
law StrictUpper(self: i64) = self < 10

def need_strict(v: i64 is StrictUpper) -> i64:
    return v

def test(x: i64 is Bounded[3, 10]) -> i64:
    return need_strict(x)

law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
`
	result := analyzeTreeTestSource(t, "matrix_fully_bounded_strict_miss.elisa", src)
	if noRuntimeCheck(result) {
		t.Fatalf("[3,10] must NOT entail x < 10 (hi=10 == bound); expected runtime check")
	}
}

// ─── One-sided intervals (lo only, hi unknown) ───────────────────────────────────

// Positive: [5, ∞) entails x >= 5 (lo=5 >= 5).
func TestMatrixOneSidedLoEntailsGTEQ(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_GTEQ, c: 5}
	r := numRange{loKnown: true, lo: 5, hiKnown: false}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[5,∞) must entail x >= 5 (lo=5 >= 5)")
	}
}

// Positive: [5, ∞) entails x > 4 (lo=5 > 4).
func TestMatrixOneSidedLoEntailsGT(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_GT, c: 4}
	r := numRange{loKnown: true, lo: 5, hiKnown: false}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[5,∞) must entail x > 4 (lo=5 > 4)")
	}
}

// Near-miss: [5, ∞) does NOT entail x >= 6 (lo=5 < 6).
func TestMatrixOneSidedLoNotEntailsHigherGTEQ(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_GTEQ, c: 6}
	r := numRange{loKnown: true, lo: 5, hiKnown: false}
	if rangeEntailsConstraint(r, k) {
		t.Fatal("[5,∞) must NOT entail x >= 6 (lo=5 < 6)")
	}
}

// ─── One-sided intervals (hi only, lo unknown) ───────────────────────────────────

// Positive: (-∞, 8] entails x <= 8 (hi=8 <= 8).
func TestMatrixOneSidedHiEntailsLTEQ(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_LTEQ, c: 8}
	r := numRange{loKnown: false, hiKnown: true, hi: 8}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("(-∞,8] must entail x <= 8 (hi=8 <= 8)")
	}
}

// Positive: (-∞, 8] entails x < 9 (hi=8 < 9).
func TestMatrixOneSidedHiEntailsLT(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_LT, c: 9}
	r := numRange{loKnown: false, hiKnown: true, hi: 8}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("(-∞,8] must entail x < 9 (hi=8 < 9)")
	}
}

// Near-miss: (-∞, 8] does NOT entail x < 8 (hi=8 is NOT < 8).
func TestMatrixOneSidedHiNotEntailsStrictLT(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_LT, c: 8}
	r := numRange{loKnown: false, hiKnown: true, hi: 8}
	if rangeEntailsConstraint(r, k) {
		t.Fatal("(-∞,8] must NOT entail x < 8 (hi=8 == bound)")
	}
}

// ─── Zero-bound intervals (special case) ───────────────────────────────────────

// Positive: [0, 10] entails x >= 0.
func TestMatrixZeroBoundIntervalGTEQZero(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_GTEQ, c: 0}
	r := numRange{loKnown: true, lo: 0, hiKnown: true, hi: 10}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[0,10] must entail x >= 0 (lo=0 >= 0)")
	}
}

// Near-miss: [-1, 10] does NOT entail x >= 0 (lo=-1 < 0).
func TestMatrixNegativeLoBoundNotGTEQZero(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_GTEQ, c: 0}
	r := numRange{loKnown: true, lo: -1, hiKnown: true, hi: 10}
	if rangeEntailsConstraint(r, k) {
		t.Fatal("[-1,10] must NOT entail x >= 0 (lo=-1 < 0)")
	}
}

// ─── Integration: narrower nested interval entails wider ────────────────────────

// Positive: [5, 8] ⊆ [3, 10]; narrower entails wider.
func TestMatrixNestedIntervalEntails(t *testing.T) {
	src := `
law Wide(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
law Narrow(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_wide(v: i64 is Wide[3, 10]) -> i64:
    return v

def test(x: i64 is Narrow[5, 8]) -> i64:
    return need_wide(x)
`
	result := analyzeTreeTestSource(t, "matrix_nested_entails.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("[5,8] must entail [3,10]; got runtime check: %s", allDiagnostics(result))
	}
}

// Near-miss: [3, 8] does NOT entail [5, 10] (lo=3 < 5 expected).
func TestMatrixNestedIntervalNotEntails_LoTooSmall(t *testing.T) {
	src := `
law Wide(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
law Narrow(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_wide(v: i64 is Wide[5, 10]) -> i64:
    return v

def test(x: i64 is Narrow[3, 8]) -> i64:
    return need_wide(x)
`
	result := analyzeTreeTestSource(t, "matrix_nested_not_entails.elisa", src)
	if noRuntimeCheck(result) {
		t.Fatalf("[3,8] must NOT entail [5,10] (lo=3 < 5); expected runtime check")
	}
}
