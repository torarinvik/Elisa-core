//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Completeness: range-for seeds [lo, hi] numRange for the loop variable.
// ---------------------------------------------------------------------------

// COMPLETENESS: `for i in 0..<n:` where n is a constant — i is in [0, n-1] so
// passing i to a function requiring i < n proves without a check.
func TestForRangeBoundsConstEndProves(t *testing.T) {
	src := `
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_in_range(x: i64 is InRange[0, 9]) -> i64:
    return x

def caller() -> void:
    for i in 0..<10:
        _ = need_in_range(i)
`
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "for_range_const.elisa", src, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("for i in 0..<10: seeds i in [0,9]; passing to InRange[0,9] must prove, got errors: %v", errs)
	}
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, "could not be proven") {
		t.Fatalf("for i in 0..<10: refinement proof must succeed, got warning: %s", all)
	}
}

// COMPLETENESS: `for i in 0..<arr.count:` — i is provably >= 0, so arr[i] elides bounds check.
// This tests the existing indexBoundFact path still works after the numRange addition.
func TestForRangeBoundsIndexElision(t *testing.T) {
	src := `
def sum(arr: darray[i64]&) -> i64:
    acc: mutable i64 = 0
    for i in 0..<arr.count:
        acc <- acc + arr[i]
    return acc
`
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "for_range_elision.elisa", src, AnalyzeOptions{EnforceUnsafePermissions: true})
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, "unchecked index requires") {
		t.Fatalf("for i in 0..<arr.count: must elide bounds check on arr[i], got warning: %s", all)
	}
}

// COMPLETENESS: `for i in 0..<n:` seeds lo=0, so i >= 0 proofs discharge.
func TestForRangeBoundsNonNegProves(t *testing.T) {
	src := `
law NonNeg(self: i64) = self >= 0

def need_nonneg(x: i64 is NonNeg) -> i64:
    return x

def caller(n: i64) -> void:
    for i in 0..<n:
        _ = need_nonneg(i)
`
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "for_range_nonneg.elisa", src, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("for i in 0..<n: seeds i >= 0; NonNeg proof must succeed, got errors: %v", errs)
	}
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, "could not be proven") {
		t.Fatalf("for i in 0..<n: NonNeg proof must succeed, got warning: %s", all)
	}
}

// COMPLETENESS: const-bounded loop, i carries [2, 6] in `for i in 2..<7:`.
func TestForRangeBoundsNonZeroStartProves(t *testing.T) {
	src := `
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_range(x: i64 is InRange[2, 6]) -> i64:
    return x

def caller() -> void:
    for i in 2..<7:
        _ = need_range(i)
`
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "for_range_nonzero.elisa", src, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("for i in 2..<7: seeds i in [2,6]; InRange[2,6] proof must succeed, got errors: %v", errs)
	}
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, "could not be proven") {
		t.Fatalf("for i in 2..<7: seeds i in [2,6]; InRange[2,6] proof must succeed, got warning: %s", all)
	}
}

// ---------------------------------------------------------------------------
// Soundness-negative: inclusive ..= gives wider interval, proofs at hi+1 fail.
// ---------------------------------------------------------------------------

// SOUNDNESS: `for i in 0..=9:` desugars to `0..<10`, so i in [0,9]. Passing to
// InRange[0,8] (which needs hi<=8) must FAIL because i can be 9.
func TestForRangeBoundsInclusiveDoesNotOverProve(t *testing.T) {
	src := `
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_small(x: i64 is InRange[0, 8]) -> i64:
    return x

def caller() -> void:
    for i in 0..=9:
        _ = need_small(i)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "for_range_inclusive_sound.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !strings.Contains(diag, "could not be proven") && !strings.Contains(diag, "violated") && !strings.Contains(diag, "does not satisfy") {
		t.Fatalf("for i in 0..=9 gives [0,9]; InRange[0,8] must NOT prove (i can be 9), got clean:\n%s", diag)
	}
}

// SOUNDNESS: a nested rebinding of the same name `i` inside the loop body must NOT
// inherit the loop variable's range fact. The inner `i` is a different symbol.
func TestForRangeBoundsNestedRebindDoesNotLeak(t *testing.T) {
	src := `
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_small(x: i64 is InRange[0, 3]) -> i64:
    return x

def caller() -> void:
    for i in 0..<5:
        i: i64 = 99
        _ = need_small(i)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "for_range_nested_rebind.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	// The inner `i = 99` must shadow the loop var; passing it to InRange[0,3] must fail.
	if !strings.Contains(diag, "could not be proven") && !strings.Contains(diag, "violated") && !strings.Contains(diag, "does not satisfy") {
		t.Fatalf("nested rebind of `i` to 99 must not inherit loop range; InRange[0,3] must fail, got clean:\n%s", diag)
	}
}

// SOUNDNESS: `for i in 0..<n:` with variable n — only lo=0 is known, hi is not.
// Passing i to InRange[0, 5] must FAIL because n could be larger.
func TestForRangeBoundsVariableEndNoOverProve(t *testing.T) {
	src := `
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_small(x: i64 is InRange[0, 5]) -> i64:
    return x

def caller(n: i64) -> void:
    for i in 0..<n:
        _ = need_small(i)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "for_range_var_end.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !strings.Contains(diag, "could not be proven") && !strings.Contains(diag, "violated") && !strings.Contains(diag, "does not satisfy") {
		t.Fatalf("for i in 0..<n with variable n gives no hi bound; InRange[0,5] must NOT prove, got clean:\n%s", diag)
	}
}
