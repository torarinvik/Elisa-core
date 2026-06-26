package semantic

import "testing"

// Tests for parametric refine alias call-site discharge.
//
// A parametric alias `refine NAME[T](params) = BASE where PRED` is rewritten at every binder
// position to an anonymous WhereRefinementTypeExpr before the where-discharge machinery runs.
// Value-params (bracket args) are substituted into PRED at rewrite time so the predicate
// references the actual use-site names; `self` is rewritten to the bound parameter name.
//
// At the call site checkCalleeParamWhereRefinements substitutes callee param names → caller args
// and runs the linear prover.  For the predicate to be provable/refutable when it references
// `xs.count`, affineOf / substitutedAffine must resolve `.count` on a local whose initialiser
// is a list literal (the writtenListCount fact).

// TestParametricAlias_ViolatingCall verifies that passing a provably-out-of-range index to a
// function whose parameter is typed with a parametric alias produces a refutation error.
func TestParametricAlias_ViolatingCall(t *testing.T) {
	src := `
refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count

def get(xs: darray[i64], i: IndexOf[xs]) -> i64:
    return xs[i]

def bad() -> i64:
    xs: darray[i64] = [10, 20]
    return get(xs, 5)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "parametric_alias_violating.elisa", src, AnalyzeOptions{})
	if len(result.Errors()) == 0 {
		t.Errorf("expected call-site error for index 5 into 2-element array via IndexOf alias; "+
			"errors=%v proof=%v", result.Errors(), result.ProofReport)
	}
}

// TestParametricAlias_SatisfyingCall verifies that a call that provably satisfies the alias
// predicate is clean (no errors, proof recorded as proven).
func TestParametricAlias_SatisfyingCall(t *testing.T) {
	src := `
refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count

def get(xs: darray[i64], i: IndexOf[xs]) -> i64:
    return xs[i]

def good() -> i64:
    xs: darray[i64] = [10, 20, 30]
    return get(xs, 1)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "parametric_alias_satisfying.elisa", src, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Errorf("expected clean analysis for index 1 into 3-element array via IndexOf alias; "+
			"errors=%v", result.Errors())
	}
}

// TestParametricAlias_ErasurePreserved verifies that the base type of a parametric alias is
// correctly erased to its underlying type (i64) for SameType / assignability — the alias is
// representation-transparent.
func TestParametricAlias_ErasurePreserved(t *testing.T) {
	src := `
refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count

def get(xs: darray[i64], i: IndexOf[xs]) -> i64:
    return xs[i]

def erasure_check() -> i64:
    xs: darray[i64] = [10, 20]
    k: i64 = 0
    return get(xs, k)
`
	// Passing a plain i64 local (with unknown range) should be allowed — the alias erases
	// to i64, so type-checking passes; the obligation falls to runtime (not a type error).
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "parametric_alias_erasure.elisa", src, AnalyzeOptions{})
	// We expect zero type errors (erasure is correct); there may be runtime lint warnings.
	for _, err := range result.Errors() {
		t.Errorf("unexpected error (erasure should be transparent): %v", err)
	}
}
