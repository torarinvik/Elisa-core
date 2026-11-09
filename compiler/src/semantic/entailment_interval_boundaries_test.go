//go:build cgo

package semantic

import (
	"elisacore/src/lexer"
	"testing"
)

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
