//go:build cgo

package semantic

import (
	"testing"
)

// A chained widening cast (u32 bounded [0,100], cast .u64() which is itself cast .u64() again
// — same-width identity) must still carry the range through.
func TestCastChainU32ToU64PreservesRange(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def widen_chain(x: u32) -> u64 is Bounded[0, 100]:
    if x >= 0 and x <= 100:
        y: u64 = x.u64()
        return y.u64()
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cast_chain_u32_u64.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("chained widening u32→u64→u64 should prove return, got: %s", allDiagnostics(result))
	}
}

// SOUNDNESS-NEGATIVE: a NARROWING cast (u32 → u8 where x may exceed 255) must NOT carry the
// wider range. The tier-2 prover must decline, leaving a runtime check (or strict error).
func TestCastNarrowingU32ToU8DoesNotCarryRange(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def narrow(x: u32) -> u8 is Bounded[0, 1000]:
    if x >= 0 and x <= 1000:
        return x.u8()
    return 0
`
	// Under strict: x.u8() with proven range [0,1000] cannot fit u8 (max 255); the narrowing
	// cast must be declined — the refinement on the return should fail to prove statically.
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cast_narrow_u32_u8.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) == 0 {
		t.Fatalf("SOUNDNESS: narrowing u32→u8 with range [0,1000] must NOT prove Bounded[0,1000] (range does not fit u8), but no error was reported")
	}
}

// SOUNDNESS-NEGATIVE: a sign-changing cast (i64 with proven negative range → u64) must NOT
// carry the range, since negative values cannot be represented in u64.
func TestCastSignChangeI64ToU64DoesNotCarryNegativeRange(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def sign_change(x: i64) -> u64 is Bounded[-10, 10]:
    if x >= -10 and x <= 10:
        return x.u64()
    return 0
`
	// Under strict: x.u64() on a value in [-10,10] is sign-changing (negatives would wrap).
	// The refinement Bounded[-10,10] on the u64 result must not prove statically.
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cast_sign_change.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) == 0 {
		t.Fatalf("SOUNDNESS: sign-changing cast i64→u64 with range [-10,10] must NOT prove Bounded[-10,10] (negative values do not fit u64), but no error was reported")
	}
}

// --- min/max/clamp range facts via ensures (roi/minmax-clamp-range) ---
func TestMinEnsuresUpperBound(t *testing.T) {
	src := `
law AtMost50(self: i64) = self <= 50

def min(a: i64, b: i64) -> i64:
    ensure result <= a
    ensure result <= b
    return a if a < b else b

def use() -> i64:
    x = min(50, 100)
    y: i64 is AtMost50 = x
    return y
`
	result := analyzeTreeTestSource(t, "min_upper_bound.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("min ensure result<=a should let AtMost50 prove, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("min(50,100)<=50 seeded by ensure: expected 0 runtime checks, got %d", len(result.RefinementChecks))
	}
}

func TestMaxEnsuresLowerBound(t *testing.T) {
	src := `
law AtLeast100(self: i64) = self >= 100

def max(a: i64, b: i64) -> i64:
    ensure result >= a
    ensure result >= b
    return a if a > b else b

def use() -> i64:
    x = max(50, 100)
    y: i64 is AtLeast100 = x
    return y
`
	result := analyzeTreeTestSource(t, "max_lower_bound.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("max ensure result>=b should let AtLeast100 prove, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("max(50,100)>=100 seeded by ensure: expected 0 runtime checks, got %d", len(result.RefinementChecks))
	}
}

func TestClampEnsuresInRange(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def clamp(x: i64, lo: i64, hi: i64) -> i64:
    ensure result >= lo
    ensure result <= hi
    if x < lo:
        return lo
    if x > hi:
        return hi
    return x

def use(x: i64) -> i64:
    c = clamp(x, 0, 100)
    y: i64 is Bounded[0, 100] = c
    return y
`
	result := analyzeTreeTestSource(t, "clamp_in_range.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("clamp ensure result in [lo,hi] should let Bounded[0,100] prove, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("clamp(x,0,100) in [0,100] seeded by ensures: expected 0 runtime checks, got %d", len(result.RefinementChecks))
	}
}

func TestClampDoesNotProveTighterRange(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def clamp(x: i64, lo: i64, hi: i64) -> i64:
    ensure result >= lo
    ensure result <= hi
    if x < lo:
        return lo
    if x > hi:
        return hi
    return x

def use(x: i64) -> i64:
    c = clamp(x, 0, 100)
    y: i64 is Bounded[1, 100] = c
    return y
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "clamp_no_tighter_range.elisa", src)
	if len(result.RefinementChecks) == 0 {
		t.Fatalf("clamp(x,0,100) must NOT prove Bounded[1,100] (tighter than [0,100]): expected a runtime check")
	}
}
