//go:build cgo

package semantic

import (
	"testing"

	"elisacore/src/lexer"
)

// entailment_v2_test.go: tests for the three new sound entailment cases added to
// analyzer_refinement_flow.go (docs/85 v2 additions):
//   Case 3 – additive shift:     x in [lo,hi] ⇒ x+c in [lo+c, hi+c]
//   Case 4 – monotonic scaling:  x in [lo,hi], lo>=0, k>0 ⇒ x*k in [lo*k, hi*k]
//   Case 5 – transitivity:       SKIPPED (no structured relational fact store exists;
//                                  smtAssertFacts only stores raw AST expressions, so
//                                  soundly enumerating `a<=b` + `b<=c` → `a<=c` chains
//                                  would require scanning O(n^2) untyped expr pairs with
//                                  no identity guarantee — deferred to a future relFacts
//                                  map[string]string or SMT tier).

// ─── Unit tests for shiftRange ────────────────────────────────────────────────

// Positive: [2, 8] shifted by +3 gives [5, 11].
func TestShiftRange_Positive(t *testing.T) {
	r := numRange{loKnown: true, lo: 2, hiKnown: true, hi: 8}
	got, ok := shiftRange(r, 3)
	if !ok {
		t.Fatal("shiftRange([2,8], 3): expected ok=true")
	}
	if !got.loKnown || got.lo != 5 {
		t.Fatalf("shiftRange([2,8], 3): lo want 5, got %v (known=%v)", got.lo, got.loKnown)
	}
	if !got.hiKnown || got.hi != 11 {
		t.Fatalf("shiftRange([2,8], 3): hi want 11, got %v (known=%v)", got.hi, got.hiKnown)
	}
}

// Positive: [2, 8] shifted by -5 gives [-3, 3].
func TestShiftRange_NegativeShift(t *testing.T) {
	r := numRange{loKnown: true, lo: 2, hiKnown: true, hi: 8}
	got, ok := shiftRange(r, -5)
	if !ok {
		t.Fatal("shiftRange([2,8], -5): expected ok=true")
	}
	if !got.loKnown || got.lo != -3 {
		t.Fatalf("shiftRange([2,8], -5): lo want -3, got %v (known=%v)", got.lo, got.loKnown)
	}
	if !got.hiKnown || got.hi != 3 {
		t.Fatalf("shiftRange([2,8], -5): hi want 3, got %v (known=%v)", got.hi, got.hiKnown)
	}
}

// Near-miss: overflow on both bounds — range [max-1, max] shifted by +2 overflows both lo and hi.
// shiftRange must return ok=false (neither bound derivable without overflow).
func TestShiftRange_HiOverflow(t *testing.T) {
	const maxI64 = int64(^uint64(0) >> 1)
	r := numRange{loKnown: true, lo: maxI64 - 1, hiKnown: true, hi: maxI64}
	got, ok := shiftRange(r, 2)
	// lo = (maxI64-1)+2 overflows; hi = maxI64+2 overflows. Both bounds are lost.
	// Either ok=false, or if somehow one bound is computed, it must not be hiKnown (hi overflows).
	if ok && got.hiKnown {
		t.Fatalf("shiftRange([max-1,max], 2): hi overflows — hiKnown must be false, got hi=%v", got.hi)
	}
	_ = got
}

// Near-miss: fully-overflow case. [max-0, max] shifted by +1: lo overflows AND hi overflows.
// shiftRange must return ok=false (no bound derivable).
func TestShiftRange_BothOverflow(t *testing.T) {
	const maxI64 = int64(^uint64(0) >> 1)
	r := numRange{loKnown: true, lo: maxI64, hiKnown: true, hi: maxI64}
	_, ok := shiftRange(r, 1)
	if ok {
		t.Fatal("shiftRange([max,max], 1): both bounds overflow; expected ok=false")
	}
}

// ─── Unit tests for scaleRangePositive ───────────────────────────────────────

// Positive: [2, 5] scaled by 3 gives [6, 15].
func TestScaleRangePositive_Basic(t *testing.T) {
	r := numRange{loKnown: true, lo: 2, hiKnown: true, hi: 5}
	got, ok := scaleRangePositive(r, 3)
	if !ok {
		t.Fatal("scaleRangePositive([2,5], 3): expected ok=true")
	}
	if !got.loKnown || got.lo != 6 {
		t.Fatalf("scaleRangePositive([2,5], 3): lo want 6, got %v (known=%v)", got.lo, got.loKnown)
	}
	if !got.hiKnown || got.hi != 15 {
		t.Fatalf("scaleRangePositive([2,5], 3): hi want 15, got %v (known=%v)", got.hi, got.hiKnown)
	}
}

// Positive: [0, 10] scaled by 1 gives [0, 10] (identity scaling).
func TestScaleRangePositive_ScaleByOne(t *testing.T) {
	r := numRange{loKnown: true, lo: 0, hiKnown: true, hi: 10}
	got, ok := scaleRangePositive(r, 1)
	if !ok {
		t.Fatal("scaleRangePositive([0,10], 1): expected ok=true")
	}
	if !got.loKnown || got.lo != 0 {
		t.Fatalf("scaleRangePositive([0,10], 1): lo want 0, got %v", got.lo)
	}
	if !got.hiKnown || got.hi != 10 {
		t.Fatalf("scaleRangePositive([0,10], 1): hi want 10, got %v", got.hi)
	}
}

// Near-miss: negative lo — [−1, 5] with k=3 must be REFUSED (lo < 0, not monotonic).
func TestScaleRangePositive_NegativeLoBound_Refused(t *testing.T) {
	r := numRange{loKnown: true, lo: -1, hiKnown: true, hi: 5}
	_, ok := scaleRangePositive(r, 3)
	if ok {
		t.Fatal("scaleRangePositive([-1,5], 3): lo < 0 — must refuse (ok=false)")
	}
}

// Near-miss: k=0 — must be refused (zero is not a positive scaling).
func TestScaleRangePositive_ZeroScale_Refused(t *testing.T) {
	r := numRange{loKnown: true, lo: 0, hiKnown: true, hi: 10}
	_, ok := scaleRangePositive(r, 0)
	if ok {
		t.Fatal("scaleRangePositive([0,10], 0): k=0 — must refuse (ok=false)")
	}
}

// Near-miss: k=-1 — negative scale, must be refused.
func TestScaleRangePositive_NegativeScale_Refused(t *testing.T) {
	r := numRange{loKnown: true, lo: 0, hiKnown: true, hi: 10}
	_, ok := scaleRangePositive(r, -1)
	if ok {
		t.Fatal("scaleRangePositive([0,10], -1): k<0 — must refuse (ok=false)")
	}
}

// Near-miss: overflow on hi. [0, max/2+1] scaled by 2 would overflow hi.
func TestScaleRangePositive_HiOverflow(t *testing.T) {
	const maxI64 = int64(^uint64(0) >> 1)
	r := numRange{loKnown: true, lo: 0, hiKnown: true, hi: maxI64/2 + 1}
	got, ok := scaleRangePositive(r, 2)
	// lo=0*2=0 is fine; hi overflows → hiKnown must be false.
	if !ok {
		t.Fatal("scaleRangePositive([0, max/2+1], 2): lo=0 is still derivable; expected ok=true")
	}
	if got.hiKnown {
		t.Fatalf("scaleRangePositive([0, max/2+1], 2): hi overflows — hiKnown must be false")
	}
	if !got.loKnown || got.lo != 0 {
		t.Fatalf("scaleRangePositive([0, max/2+1], 2): lo want 0, got %v", got.lo)
	}
}

// ─── Integration tests: additive shift ────────────────────────────────────────

// Positive: x in [0,10] (from an `if x >= 0 and x <= 10` guard), prove `x + 5 is Bounded[5, 15]`.
// Soundness: [0+5, 10+5] = [5,15] ⊆ [5,15].
func TestEntailmentV2_AdditiveShift_Positive(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_bounded_5_15(v: i64 is Bounded[5, 15]) -> i64:
    return v

def test(x: i64 is Bounded[0, 10]) -> i64:
    return need_bounded_5_15(x + 5)
`
	result := analyzeTreeTestSource(t, "entail_v2_addshift.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("x in [0,10] must statically prove x+5 is Bounded[5,15]; got runtime check: %s", allDiagnostics(result))
	}
}

// Near-miss: x in [0,10], prove `x + 5 is Bounded[5, 14]` — hi=15 exceeds 14, must NOT prove.
func TestEntailmentV2_AdditiveShift_NearMiss_TightHi(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_bounded_5_14(v: i64 is Bounded[5, 14]) -> i64:
    return v

def test(x: i64 is Bounded[0, 10]) -> i64:
    return need_bounded_5_14(x + 5)
`
	result := analyzeTreeTestSource(t, "entail_v2_addshift_miss.elisa", src)
	if noRuntimeCheck(result) {
		t.Fatalf("x+5 in [5,15] must NOT prove Bounded[5,14] (hi=15 > 14); expected runtime check")
	}
}

// ─── Integration tests: monotonic scaling ─────────────────────────────────────

// Positive: x in [2, 5] (lo >= 0), prove `x * 3 is Bounded[6, 15]`.
// Soundness: [2*3, 5*3] = [6, 15] ⊆ [6, 15].
func TestEntailmentV2_MonotonicScale_Positive(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_bounded_6_15(v: i64 is Bounded[6, 15]) -> i64:
    return v

def test(x: i64 is Bounded[2, 5]) -> i64:
    return need_bounded_6_15(x * 3)
`
	result := analyzeTreeTestSource(t, "entail_v2_scale.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("x in [2,5] must statically prove x*3 is Bounded[6,15]; got runtime check: %s", allDiagnostics(result))
	}
}

// Near-miss: x in [2, 5], prove `x * 3 is Bounded[6, 14]` — hi=15 > 14, must NOT prove.
func TestEntailmentV2_MonotonicScale_NearMiss_TightHi(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_bounded_6_14(v: i64 is Bounded[6, 14]) -> i64:
    return v

def test(x: i64 is Bounded[2, 5]) -> i64:
    return need_bounded_6_14(x * 3)
`
	result := analyzeTreeTestSource(t, "entail_v2_scale_miss.elisa", src)
	if noRuntimeCheck(result) {
		t.Fatalf("x*3 in [6,15] must NOT prove Bounded[6,14] (hi=15 > 14); expected runtime check")
	}
}

// Near-miss (scaleRangePositive unit level): a range with negative lo is refused.
// This is a pure unit test of the helper — the integration path (tier-2 affine prover) can
// handle negative-lo ranges via boundAffine, but scaleRangePositive is a tier-1 extension
// that requires lo>=0 for the monotonic guarantee.
func TestEntailmentV2_MonotonicScale_NearMiss_NegativeLo(t *testing.T) {
	// [−2, 5] * 3: lo < 0, so scaleRangePositive must refuse.
	r := numRange{loKnown: true, lo: -2, hiKnown: true, hi: 5}
	_, ok := scaleRangePositive(r, 3)
	if ok {
		t.Fatal("scaleRangePositive([-2,5], 3): lo < 0 — must refuse (not monotonically increasing from non-negative)")
	}
}

// ─── Unit test: rangeEntailsConstraint with shifted/scaled ranges ─────────────

// Shifted range [5,15] entails self >= 5 (lo=5 >= 5).
func TestEntailmentV2_ShiftedRangeEntailsGTEQ(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_GTEQ, c: 5}
	r := numRange{loKnown: true, lo: 5, hiKnown: true, hi: 15}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[5,15] must entail self >= 5 (lo=5 >= 5)")
	}
}

// Scaled range [6,15] entails self <= 15 (hi=15 <= 15).
func TestEntailmentV2_ScaledRangeEntailsLTEQ(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_LTEQ, c: 15}
	r := numRange{loKnown: true, lo: 6, hiKnown: true, hi: 15}
	if !rangeEntailsConstraint(r, k) {
		t.Fatal("[6,15] must entail self <= 15 (hi=15 <= 15)")
	}
}

// Near-miss: [5,16] does NOT entail self <= 15 (hi=16 > 15).
func TestEntailmentV2_ScaledRangeNearMiss_HiExceeds(t *testing.T) {
	k := lawConstraint{op: lexer.TOKEN_LTEQ, c: 15}
	r := numRange{loKnown: true, lo: 5, hiKnown: true, hi: 16}
	if rangeEntailsConstraint(r, k) {
		t.Fatal("[5,16] must NOT entail self <= 15 (hi=16 > 15)")
	}
}
