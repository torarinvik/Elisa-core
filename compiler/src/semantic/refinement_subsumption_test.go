//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// refinement_subsumption_test.go: tests for the interval-subsumption cases in
// refinementPredicatesEntail / refinementPredicateIntervalEntails.
//
// The central claim: a STRONGER known refinement (narrower interval) must entail
// a WEAKER goal refinement (looser interval or single comparison bound) without
// requiring a runtime check.  Only sound cases are accepted — weaker-implies-stronger
// must NOT entail.

// ─── helpers ───────────────────────────────────────────────────────────────────

// noRuntimeCheck returns true iff no "could not be proven statically" warning
// appears in the diagnostics — indicating that every refinement obligation was
// discharged by the prover, not deferred to a runtime check.
func noRuntimeCheck(result *Result) bool {
	return !strings.Contains(allDiagnostics(result), "could not be proven statically")
}

func hasRuntimeCheck(result *Result) bool {
	return strings.Contains(allDiagnostics(result), "could not be proven statically")
}

// ─── interval-implies-comparison ───────────────────────────────────────────────

// A value known to be in InRange[1, n] satisfies Positive (self > 0) because lo=1 > 0.
func TestSubsumptionIntervalImpliesGT(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_positive(x: i64 is Positive) -> i64:
    return x

def caller(v: i64 is InRange[1, 100]) -> i64:
    return need_positive(v)
`
	result := analyzeTreeTestSource(t, "subsum_interval_gt.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("InRange[1,100] must statically entail Positive (> 0); got runtime check: %s", allDiagnostics(result))
	}
}

// A value in InRange[0, n] does NOT satisfy Positive (lo=0, need lo>0).  The
// prover must decline — this tests soundness (no false entailment).
func TestSubsumptionIntervalDoesNotEntailGTWhenLoIsZero(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_positive(x: i64 is Positive) -> i64:
    return x

def caller(v: i64 is InRange[0, 100]) -> i64:
    return need_positive(v)
`
	result := analyzeTreeTestSource(t, "subsum_interval_no_gt.elisa", src)
	// No semantic errors expected (runtime check is the fallback, not a hard error
	// without -strict), but a runtime check warning must appear.
	if noRuntimeCheck(result) {
		t.Fatalf("InRange[0,100] must NOT statically entail Positive (lo=0 is not > 0); expected a runtime-check warning")
	}
}

// A value in InRange[lo, n-1] satisfies UpperBound (self < n) because hi = n-1 < n.
func TestSubsumptionIntervalImpliesLT(t *testing.T) {
	src := `
law UpperBound(self: i64, n: i64) = self < n
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_upper(x: i64 is UpperBound[100]) -> i64:
    return x

def caller(v: i64 is InRange[0, 99]) -> i64:
    return need_upper(v)
`
	result := analyzeTreeTestSource(t, "subsum_interval_lt.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("InRange[0,99] must statically entail UpperBound[100] (hi=99 < 100); got runtime check: %s", allDiagnostics(result))
	}
}

// A value in InRange[lo, 100] does NOT satisfy UpperBound[100] (hi=100 is NOT < 100).
func TestSubsumptionIntervalDoesNotEntailLTWhenHiEqBound(t *testing.T) {
	src := `
law UpperBound(self: i64, n: i64) = self < n
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_upper(x: i64 is UpperBound[100]) -> i64:
    return x

def caller(v: i64 is InRange[0, 100]) -> i64:
    return need_upper(v)
`
	result := analyzeTreeTestSource(t, "subsum_interval_no_lt.elisa", src)
	if noRuntimeCheck(result) {
		t.Fatalf("InRange[0,100] must NOT statically entail UpperBound[100] (hi=100 is not < 100); expected runtime-check warning")
	}
}

// A value in InRange[5, 10] satisfies AtLeast[3] (self >= 3) because lo=5 >= 3.
func TestSubsumptionIntervalImpliesGTEQ(t *testing.T) {
	src := `
law AtLeast(self: i64, k: i64) = self >= k
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_atleast(x: i64 is AtLeast[3]) -> i64:
    return x

def caller(v: i64 is InRange[5, 10]) -> i64:
    return need_atleast(v)
`
	result := analyzeTreeTestSource(t, "subsum_interval_gteq.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("InRange[5,10] must statically entail AtLeast[3] (lo=5 >= 3); got runtime check: %s", allDiagnostics(result))
	}
}

// A value in InRange[2, 10] does NOT entail AtLeast[3] (lo=2 is not >= 3).
func TestSubsumptionIntervalDoesNotEntailGTEQWhenLoInsufficient(t *testing.T) {
	src := `
law AtLeast(self: i64, k: i64) = self >= k
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_atleast(x: i64 is AtLeast[3]) -> i64:
    return x

def caller(v: i64 is InRange[2, 10]) -> i64:
    return need_atleast(v)
`
	result := analyzeTreeTestSource(t, "subsum_interval_no_gteq.elisa", src)
	if noRuntimeCheck(result) {
		t.Fatalf("InRange[2,10] must NOT statically entail AtLeast[3] (lo=2 < 3); expected runtime-check warning")
	}
}

// ─── interval-implies-interval (narrower ⊆ wider) ─────────────────────────────

// InRange[2, 8] ⊆ InRange[0, 10]: narrower should entail the wider.
func TestSubsumptionNarrowerIntervalEntailsWider(t *testing.T) {
	src := `
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_wide(x: i64 is InRange[0, 10]) -> i64:
    return x

def caller(v: i64 is InRange[2, 8]) -> i64:
    return need_wide(v)
`
	result := analyzeTreeTestSource(t, "subsum_narrow_to_wide.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("InRange[2,8] must statically entail InRange[0,10]; got runtime check: %s", allDiagnostics(result))
	}
}

// InRange[0, 10] does NOT entail InRange[2, 8] (lo=0 < 2, wider does not imply narrower).
func TestSubsumptionWiderIntervalDoesNotEntailNarrower(t *testing.T) {
	src := `
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_narrow(x: i64 is InRange[2, 8]) -> i64:
    return x

def caller(v: i64 is InRange[0, 10]) -> i64:
    return need_narrow(v)
`
	result := analyzeTreeTestSource(t, "subsum_wide_no_narrow.elisa", src)
	if noRuntimeCheck(result) {
		t.Fatalf("InRange[0,10] must NOT statically entail InRange[2,8]; expected runtime-check warning")
	}
}

// ─── exact-bound boundary cases ────────────────────────────────────────────────

// InRange[3, 3] is a point — it trivially entails itself (exact match via interval path).
func TestSubsumptionPointIntervalEntailsItself(t *testing.T) {
	src := `
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_range(x: i64 is InRange[3, 3]) -> i64:
    return x

def caller(v: i64 is InRange[3, 3]) -> i64:
    return need_range(v)
`
	result := analyzeTreeTestSource(t, "subsum_point_self.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("InRange[3,3] must entail itself; got runtime check: %s", allDiagnostics(result))
	}
}

// InRange[3, 3] (point = 3) entails Positive (> 0) since 3 > 0.
func TestSubsumptionPointIntervalEntailsComparison(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_pos(x: i64 is Positive) -> i64:
    return x

def caller(v: i64 is InRange[3, 3]) -> i64:
    return need_pos(v)
`
	result := analyzeTreeTestSource(t, "subsum_point_gt.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("InRange[3,3] (point=3) must statically entail Positive (> 0); got runtime check: %s", allDiagnostics(result))
	}
}

// ─── return-type subsumption ──────────────────────────────────────────────────

// A function returning i64 is InRange[1,10] satisfies a caller requiring i64 is Positive.
func TestSubsumptionReturnTypeEntailsWeakerReturn(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def produce() -> i64 is InRange[1, 10]:
    return 5

def wrap() -> i64 is Positive:
    return produce()
`
	result := analyzeTreeTestSource(t, "subsum_return_forward.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hasRuntimeCheck(result) {
		t.Fatalf("return of produce() (InRange[1,10]) must statically satisfy wrap()'s Positive return contract; got runtime check: %s", allDiagnostics(result))
	}
}

// A function returning i64 is InRange[0,10] does NOT satisfy a return contract of Positive
// (lo=0 not > 0) — must fall back to runtime check.
func TestSubsumptionReturnTypeDoesNotEntailWeakerWhenUnsound(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def produce() -> i64 is InRange[0, 10]:
    return 5

def wrap() -> i64 is Positive:
    return produce()
`
	result := analyzeTreeTestSource(t, "subsum_return_no_forward.elisa", src)
	// InRange[0,10] does NOT entail Positive (lo=0 is not > 0); must see a runtime check or error
	if noRuntimeCheck(result) && len(result.Errors()) == 0 {
		t.Fatalf("InRange[0,10] must NOT entail Positive; expected a runtime-check warning or error")
	}
}
