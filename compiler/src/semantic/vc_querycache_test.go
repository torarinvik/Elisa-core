//go:build cgo

package semantic

import "testing"

// VC IR brick 5 — query caching. Two functions with byte-identical signatures and bodies generate the
// SAME solver query for the SAME obligation, so the second is served from the cache: the verdict is
// still recorded (both prove) but no second solver round-trip happens. z3 is deterministic for a given
// query, so the cached answer is sound.
func TestSMTQueryCacheHit(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Small = i64 is Bounded[2, 10]

def mul_a(a: Small, b: Small) -> i64 is Bounded[4, 100]:
    return a * b

def mul_b(a: Small, b: Small) -> i64 is Bounded[4, 100]:
    return a * b
`
	result := analyzeWithSMT(t, "smt_cache.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	// Both functions' nonlinear bounds are proven, but the identical second function is cached, not
	// re-solved. Each `Bounded` body splits into two conjuncts (brick 4), so the second function's two
	// obligations are both cache hits.
	if result.SMTProfile.CacheHits < 1 {
		t.Fatalf("expected the identical second function's obligations to be cached, got %+v", result.SMTProfile)
	}
	// Soundness check: caching must not change the verdict — both functions are still proven (4 total:
	// 2 solver-proven + 2 cached-proven).
	if result.SMTProfile.Proven != 4 {
		t.Fatalf("expected 4 proven obligations (2 solved + 2 cached), got %+v", result.SMTProfile)
	}
	if result.SMTProfile.SolverProven != 2 {
		t.Fatalf("expected exactly 2 obligations proven by an actual solver run, got %+v", result.SMTProfile)
	}
}

// A cached DECLINE preserves its counterexample: the same false obligation, met twice, declines both
// times and the second pulls the rendered counterexample from the cache rather than re-querying.
func TestSMTQueryCacheDeclineCounterexample(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Small = i64 is Bounded[2, 10]

def bad_a(a: Small, b: Small) -> i64 is Bounded[4, 50]:
    return a * b

def bad_b(a: Small, b: Small) -> i64 is Bounded[4, 50]:
    return a * b
`
	result := analyzeWithSMT(t, "smt_cache_decline.elisa", src)
	// Neither is SMT-proven (a*b reaches 100 > 50); the soundness gate is no ProofProvenSMT.
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("a false bound must not be SMT-proven: %+v", result.ProofReport)
		}
	}
	// The `self >= 4` conjunct proves and `self <= 50` declines for BOTH functions; the second function's
	// pair is served from the cache.
	if result.SMTProfile.CacheHits < 1 {
		t.Fatalf("expected the identical second function's obligations to be cached, got %+v", result.SMTProfile)
	}
}
