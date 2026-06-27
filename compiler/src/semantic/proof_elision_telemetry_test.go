//go:build cgo

package semantic

import "testing"

// TestElisionSummaryReturnRefinementProven checks that a return statement whose value is a
// compile-time constant satisfying the law produces an Elided count of 1 and a Runtime count of 0
// in the ReturnRefinements bucket of the ElisionSummary.
func TestElisionSummaryReturnRefinementProven(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def give_five() -> i64 is Positive:
    return 5
`
	result := analyzeTreeTestSource(t, "elision_return_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	s := result.ElisionSummary
	if s.ReturnRefinements.Elided != 1 {
		t.Errorf("expected 1 elided return-refinement, got %d (summary: %+v)", s.ReturnRefinements.Elided, s)
	}
	if s.ReturnRefinements.Runtime != 0 {
		t.Errorf("expected 0 runtime return-refinements, got %d", s.ReturnRefinements.Runtime)
	}
}

// TestElisionSummaryReturnRefinementRuntime checks that a return statement whose value cannot be
// statically proven produces a Runtime count of 1 (and Elided of 0) in ReturnRefinements.
func TestElisionSummaryReturnRefinementRuntime(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def passthrough(n: i64) -> i64 is Positive:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "elision_return_runtime.elisa", src, AnalyzeOptions{})
	s := result.ElisionSummary
	if s.ReturnRefinements.Runtime != 1 {
		t.Errorf("expected 1 runtime return-refinement, got %d (summary: %+v)", s.ReturnRefinements.Runtime, s)
	}
	if s.ReturnRefinements.Elided != 0 {
		t.Errorf("expected 0 elided return-refinements, got %d", s.ReturnRefinements.Elided)
	}
}

// TestElisionSummaryCallArgRefinementProven checks that passing a provable constant argument to a
// refinement-typed parameter records Elided=1, Runtime=0 in CallArgRefinements.
func TestElisionSummaryCallArgRefinementProven(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def need_pos(x: i64 is Positive) -> i64:
    return x

def caller() -> i64:
    return need_pos(3)
`
	result := analyzeTreeTestSource(t, "elision_arg_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	s := result.ElisionSummary
	if s.CallArgRefinements.Elided != 1 {
		t.Errorf("expected 1 elided call-arg-refinement, got %d (summary: %+v)", s.CallArgRefinements.Elided, s)
	}
	if s.CallArgRefinements.Runtime != 0 {
		t.Errorf("expected 0 runtime call-arg-refinements, got %d", s.CallArgRefinements.Runtime)
	}
}

// TestElisionSummaryCallArgRefinementRuntime checks that passing an unproven variable argument to a
// refinement-typed parameter records Runtime=1 in CallArgRefinements.
func TestElisionSummaryCallArgRefinementRuntime(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def need_pos(x: i64 is Positive) -> i64:
    return x

def caller(n: i64) -> i64:
    return need_pos(n)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "elision_arg_runtime.elisa", src, AnalyzeOptions{})
	s := result.ElisionSummary
	if s.CallArgRefinements.Runtime != 1 {
		t.Errorf("expected 1 runtime call-arg-refinement, got %d (summary: %+v)", s.CallArgRefinements.Runtime, s)
	}
	if s.CallArgRefinements.Elided != 0 {
		t.Errorf("expected 0 elided call-arg-refinements, got %d", s.CallArgRefinements.Elided)
	}
}

// TestElisionSummaryCountsMoveOnProof checks that counts in the summary differ between a version
// where all obligations are proven and one where they fall back to runtime — the core invariant
// the telemetry must uphold. Uses return-refinement as the vehicle.
func TestElisionSummaryCountsMoveOnProof(t *testing.T) {
	proven := `
law Positive(self: i64) = self > 0
def f() -> i64 is Positive: return 7
`
	runtime := `
law Positive(self: i64) = self > 0
def f(n: i64) -> i64 is Positive: return n
`
	rProven := analyzeTreeTestSource(t, "elision_move_proven.elisa", proven)
	rRuntime := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "elision_move_runtime.elisa", runtime, AnalyzeOptions{})

	if rProven.ElisionSummary.ReturnRefinements.Elided == 0 {
		t.Error("proven path: expected at least 1 elided return-refinement")
	}
	if rRuntime.ElisionSummary.ReturnRefinements.Runtime == 0 {
		t.Error("runtime path: expected at least 1 runtime return-refinement")
	}
	// Counts must differ between the two compilations.
	if rProven.ElisionSummary.ReturnRefinements.Elided == rRuntime.ElisionSummary.ReturnRefinements.Elided &&
		rProven.ElisionSummary.ReturnRefinements.Runtime == rRuntime.ElisionSummary.ReturnRefinements.Runtime {
		t.Errorf("counts did not move between proven and runtime paths: proven=%+v runtime=%+v",
			rProven.ElisionSummary.ReturnRefinements, rRuntime.ElisionSummary.ReturnRefinements)
	}
}
