//go:build cgo

package semantic

import "testing"

func TestWhereRefinementTypeErasesToBase(t *testing.T) {
	src := `
def f(n: i64 where true) -> i64:
    return n + 1
`
	result := analyzeTreeTestSource(t, "where_refine_base.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where refinement type should erase to its base and analyze cleanly, got: %v", errs)
	}
}

func TestWhereRefinementConstantPredicateMustBeBool(t *testing.T) {
	src := `
def f(n: i64 where 1) -> i64:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_refine_nonbool.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "where refinement predicate must be bool") {
		t.Fatalf("non-bool where refinement predicate should be rejected, got:\n%s", allDiagnostics(result))
	}
}
