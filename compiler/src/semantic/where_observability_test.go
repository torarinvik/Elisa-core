package semantic

// where_observability_test.go
//
// Verifies that every where discharge outcome (proven-linear, proven-SMT, refuted,
// runtime-fallback) records exactly one ProofReport entry at each binder position
// (local / return / call-site param).  This is observability only — proof semantics
// are unchanged.
//
// Run with:
//   go test ./src/semantic -run 'WhereObservability' -timeout 120s

import (
	"testing"
)

// countProofEntries counts how many ProofReport entries match subject+predicate+outcome.
func countProofEntries(report []ProofFact, subject, predicate string, outcome ProofOutcome) int {
	n := 0
	for _, f := range report {
		if containsStr(f.Subject, subject) &&
			containsStr(f.Predicate, predicate) &&
			f.Outcome == outcome {
			n++
		}
	}
	return n
}

func containsStr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

// ---------------------------------------------------------------------------
// Local where binding: runtime-fallback
// ---------------------------------------------------------------------------

// TestWhereObservability_Local_Runtime checks that a local `where` binding whose
// initializer cannot be proven at compile time records exactly one ProofRuntime entry.
func TestWhereObservability_Local_Runtime(t *testing.T) {
	src := `
def f(n: i64) -> i64:
    x: i64 where x > 0 = n
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "where_obs_local_runtime.elisa", src,
		AnalyzeOptions{},
	)
	count := countProofEntries(result.ProofReport, "local where refinement", "where", ProofRuntime)
	if count == 0 {
		t.Errorf("expected exactly one ProofRuntime entry for local where (runtime-fallback), got 0; report: %+v", result.ProofReport)
	}
	if count > 1 {
		t.Errorf("expected exactly one ProofRuntime entry for local where (runtime-fallback), got %d (double-recorded); report: %+v", count, result.ProofReport)
	}
}

// ---------------------------------------------------------------------------
// Local where binding: proven — exactly one entry, no duplicates
// ---------------------------------------------------------------------------

func TestWhereObservability_Local_Proven_NoDuplicate(t *testing.T) {
	src := `
def f() -> i64:
    x: i64 where x > 0 = 5
    return x
`
	result := analyzeContractStrict(t, "where_obs_local_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	linear := countProofEntries(result.ProofReport, "local where refinement", "where", ProofProvenLinear)
	smt := countProofEntries(result.ProofReport, "local where refinement", "where", ProofProvenSMT)
	total := linear + smt
	if total == 0 {
		t.Errorf("expected a proven ProofReport entry for local where (proven), got none; report: %+v", result.ProofReport)
	}
	if total > 1 {
		t.Errorf("expected exactly one proven entry for local where, got %d (double-recorded); report: %+v", total, result.ProofReport)
	}
}

// ---------------------------------------------------------------------------
// Local where binding: refuted — exactly one entry, no duplicates
// ---------------------------------------------------------------------------

func TestWhereObservability_Local_Refuted_NoDuplicate(t *testing.T) {
	src := `
def f() -> i64:
    x: i64 where x > 0 = -1
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "where_obs_local_refuted.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true},
	)
	count := countProofEntries(result.ProofReport, "local where refinement", "where", ProofRefuted)
	if count == 0 {
		t.Errorf("expected a ProofRefuted entry for local where (violated), got none; report: %+v", result.ProofReport)
	}
	if count > 1 {
		t.Errorf("expected exactly one ProofRefuted entry for local where, got %d (double-recorded); report: %+v", count, result.ProofReport)
	}
}

// ---------------------------------------------------------------------------
// Return where: runtime-fallback records a ProofRuntime entry
// ---------------------------------------------------------------------------

func TestWhereObservability_Return_Runtime(t *testing.T) {
	src := `
def f(n: i64) -> i64 where result > 0:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "where_obs_return_runtime.elisa", src,
		AnalyzeOptions{},
	)
	count := countProofEntries(result.ProofReport, "return where refinement", "where", ProofRuntime)
	if count == 0 {
		t.Errorf("expected at least one ProofRuntime entry for return where (runtime-fallback), got 0; report: %+v", result.ProofReport)
	}
}

// ---------------------------------------------------------------------------
// Return where: proven — exactly one entry
// ---------------------------------------------------------------------------

func TestWhereObservability_Return_Proven_NoDuplicate(t *testing.T) {
	src := `
def f(n: i64 where n > 0) -> i64 where result > 0:
    return n
`
	result := analyzeContractStrict(t, "where_obs_return_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	linear := countProofEntries(result.ProofReport, "return where refinement", "where", ProofProvenLinear)
	smt := countProofEntries(result.ProofReport, "return where refinement", "where", ProofProvenSMT)
	total := linear + smt
	if total == 0 {
		t.Errorf("expected a proven ProofReport entry for return where, got none; report: %+v", result.ProofReport)
	}
	if total > 1 {
		t.Errorf("expected exactly one proven entry for return where, got %d (double-recorded); report: %+v", total, result.ProofReport)
	}
}

// ---------------------------------------------------------------------------
// Param where at call site: runtime-fallback records a ProofRuntime entry
// ---------------------------------------------------------------------------

func TestWhereObservability_ParamCall_Runtime(t *testing.T) {
	src := `
def need_pos(x: i64 where x > 0) -> i64:
    return x

def caller(n: i64) -> i64:
    return need_pos(n)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "where_obs_param_runtime.elisa", src,
		AnalyzeOptions{},
	)
	count := countProofEntries(result.ProofReport, "where precondition of need_pos", "where", ProofRuntime)
	if count == 0 {
		t.Errorf("expected at least one ProofRuntime entry for param where at call site (runtime-fallback), got 0; report: %+v", result.ProofReport)
	}
}

// ---------------------------------------------------------------------------
// Param where at call site: proven — exactly one entry, no duplicates
// ---------------------------------------------------------------------------

func TestWhereObservability_ParamCall_Proven_NoDuplicate(t *testing.T) {
	src := `
def need_pos(x: i64 where x > 0) -> i64:
    return x

def caller() -> i64:
    return need_pos(7)
`
	result := analyzeContractStrict(t, "where_obs_param_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	linear := countProofEntries(result.ProofReport, "where precondition of need_pos", "where", ProofProvenLinear)
	smt := countProofEntries(result.ProofReport, "where precondition of need_pos", "where", ProofProvenSMT)
	total := linear + smt
	if total == 0 {
		t.Errorf("expected a proven ProofReport entry for param where at call site, got none; report: %+v", result.ProofReport)
	}
	if total > 1 {
		t.Errorf("expected exactly one proven entry for param where at call site, got %d (double-recorded); report: %+v", total, result.ProofReport)
	}
}
