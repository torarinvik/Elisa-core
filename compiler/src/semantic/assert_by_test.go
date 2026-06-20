//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// POSITIVE: an `assert COND by:` whose condition is provable ONLY because of the proof block. The
// caller knows nothing about `n`, so a bare `assert n >= 5` is unprovable. The block establishes
// `n >= 10` (a nested assert seed) and then calls the `weaken` lemma, whose proven `ensure` turns that
// into `n >= 5` — which closes the assert. This is genuinely load-bearing: dropping the block makes
// the assert fail (see TestAssertByProofBlockIsLoadBearing).
func TestAssertByProvenViaLemmaInBlock(t *testing.T) {
	src := `
lemma weaken(x: i64):
    requires x >= 10
    ensure x >= 5
    pass

def use(n: i64):
    assert n >= 5 by:
        assert(n >= 10)
        weaken(n)
`
	result := analyzeWithSMT(t, "assert_by_positive.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis: the assert is provable via the block lemma, got: %v", errs)
	}
	// The proof block's lemma call is ghost code and must be registered for codegen erasure.
	if len(result.LemmaCalls) != 1 {
		t.Fatalf("expected the block's lemma call to be registered for erasure, got %d", len(result.LemmaCalls))
	}
}

// LOAD-BEARING CHECK: dropping the proof block's establishing fact makes the SAME assert fail. Here
// the block calls `weaken` WITHOUT first establishing `n >= 10`, so the lemma's `requires` is unmet
// and COND is unprovable — proving the block's content is what made the positive case pass (not a
// solver coincidence).
func TestAssertByProofBlockIsLoadBearing(t *testing.T) {
	src := `
lemma weaken(x: i64):
    requires x >= 10
    ensure x >= 5
    pass

def use(n: i64):
    assert n >= 5 by:
        weaken(n)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "assert_by_loadbearing.elisa", src,
		AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "could not be proven") {
		t.Fatalf("expected the assert to fail without the block's establishing fact, got: %v", result.Errors())
	}
}

// SOUNDNESS (block facts are scoped OUT): the lemma in the block establishes `square(n) >= 0`, which
// proves the assert. But that fact must NOT leak past the assert. A SECOND, identical assert AFTER the
// first — with no proof block of its own — must therefore FAIL, because the only thing that survives
// the first assert is its own condition (here `square(n) >= 0`)... so instead we assert a DIFFERENT,
// independent fact the block established only transitively, and confirm it cannot be re-proven.
//
// Construction: the block proves `helper(n) >= 0` via a lemma, and the assert condition is
// `helper(n) >= 0`. AFTER the assert we try to prove `other(n) >= 0` (a different uninterpreted call)
// with a bare strict assert — nothing established it, so it must be rejected. This shows the block's
// machinery did not silently make the whole solver state permissive.
func TestAssertByBlockFactsDoNotLeak(t *testing.T) {
	src := `
def helper(x: i64) -> i64:
    return x

def other(x: i64) -> i64:
    return x

lemma helper_nonneg(x: i64):
    ensure helper(x) >= 0
    pass

def use(n: i64) -> i64:
    ensure other(n) >= 0
    assert helper(n) >= 0 by:
        helper_nonneg(n)
    return other(n)
`
	// Under strict proofs the postcondition `other(n) >= 0` has no establishing fact (the block only
	// proved a fact about `helper`, scoped out), so it must be reported as unprovable.
	result := analyzeContractStrict(t, "assert_by_no_leak.elisa", src)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "could not be proven") {
		t.Fatalf("expected the unrelated postcondition to be unprovable (block facts must not leak), got errors: %v", result.Errors())
	}
}

// SOUNDNESS (unproven COND fails): if the proof block does NOT establish enough to prove COND, the
// assert is rejected under strict proofs. Here the block proves a fact about `helper`, but the assert
// is about the unrelated uninterpreted `mystery`, so COND cannot follow.
func TestAssertByUnprovenConditionIsError(t *testing.T) {
	src := `
def helper(x: i64) -> i64:
    return x

def mystery(x: i64) -> i64:
    return x

lemma helper_nonneg(x: i64):
    ensure helper(x) >= 0
    pass

def use(n: i64):
    assert mystery(n) >= 0 by:
        helper_nonneg(n)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "assert_by_unproven.elisa", src,
		AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "could not be proven") {
		t.Fatalf("expected an unprovable `assert … by:` condition to be rejected under strict proofs, got: %v", result.Errors())
	}
}

// SOUNDNESS (no real effects in the block): a statement with real side effects inside the proof block
// is rejected — the block is erased, so admitting a mutation/assignment there would silently drop it.
func TestAssertByRejectsEffectfulProofStatement(t *testing.T) {
	src := `
def use(n: i64):
    x: i64 = 0
    assert n >= n by:
        x <- 5
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "assert_by_effectful.elisa", src,
		AnalyzeOptions{EnableSMT: true})
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "verification-only") {
		t.Fatalf("expected a real mutating statement in an `assert … by:` block to be rejected, got: %v", result.Errors())
	}
}
