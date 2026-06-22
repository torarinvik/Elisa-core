package semantic

import "testing"

func TestBranchMutatedMutableLocalJoinsIntervalForReturnRefinement(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def f(flag: bool) -> i64 is Bounded[0, 1]:
    r: mutable i64 = 1
    if flag:
        r <- 0
    return r
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "branch_interval_join_ok.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("branch-mutated local should prove joined interval [0,1], got:\n%s", allDiagnostics(r))
	}
}

func TestBranchMutatedMutableLocalDisjunctiveEnsureRejectsTooWideJoin(t *testing.T) {
	src := `
def f(flag: bool) -> i64:
    ensure result == 0 or result == 1
    r: mutable i64 = 2
    if flag:
        r <- 0
    return r
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "branch_interval_join_bad.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "ensure postcondition") {
		t.Fatalf("joined interval [0,2] must not prove `result == 0 or result == 1`, got:\n%s", allDiagnostics(r))
	}
}
