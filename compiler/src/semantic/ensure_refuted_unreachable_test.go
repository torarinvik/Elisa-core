//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func refutedEnsureErrors(errs []string) int {
	n := 0
	for _, e := range errs {
		if strings.Contains(e, "is provably violated") {
			n++
		}
	}
	return n
}

// CORRECTNESS: the "postcondition is provably violated" hard error fires without -strict and must
// fire only on a REACHABLE exit. `return 2` below is dead (x is 5, so the guard always returns), but
// the interval tier saw only `result == 2` and refuted `result == 1`, rejecting a correct program.
func TestRefutedEnsureNotReportedOnUnreachableReturn(t *testing.T) {
	src := `
def dead_return(x: i64) -> i64:
    ensure result == 1
    if x > 0:
        y: i64 = 5
        return 1 if y == 5
        return 2
    return 1
`
	for _, strict := range []bool{false, true} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dead_return.elisa", src,
			AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: strict})
		if n := refutedEnsureErrors(result.Errors()); n != 0 {
			t.Fatalf("strict=%v: `return 2` is unreachable, nothing is violated; got: %v", strict, result.Errors())
		}
	}
}

// The same shape with a REACHABLE `return 2` (y = -5 fails the guard) is still a hard error.
func TestRefutedEnsureStillReportedOnReachableReturn(t *testing.T) {
	src := `
def live_return(x: i64) -> i64:
    ensure result == 1
    if x > 0:
        y: i64 = -5
        return 1 if y > 0
        return 2
    return 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "live_return.elisa", src,
		AnalyzeOptions{EnableSMT: true})
	if n := refutedEnsureErrors(result.Errors()); n != 1 {
		t.Fatalf("`return 2` is reachable for every x > 0 and violates the ensure; want 1 refutation, got: %v", result.Errors())
	}
}
