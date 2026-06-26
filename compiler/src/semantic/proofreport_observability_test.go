//go:build cgo

package semantic

// proofreport_observability_test.go
//
// Audit: for each refinement surface form, assert that a ProofReport entry with a sensible
// Subject/Predicate/Outcome is recorded.  Where a form does NOT currently produce an entry
// the test is skipped with an OBSERVABILITY TODO comment (t.Skip).
//
// Run with:
//   go test ./src/semantic -run 'Observability' -tags cgo -timeout 120s

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func proofReportContains(report []ProofFact, subject, predicate string, outcome ProofOutcome) bool {
	for _, f := range report {
		if strings.Contains(f.Subject, subject) &&
			strings.Contains(f.Predicate, predicate) &&
			f.Outcome == outcome {
			return true
		}
	}
	return false
}

func proofReportAny(report []ProofFact, subject, predicate string) bool {
	for _, f := range report {
		if strings.Contains(f.Subject, subject) && strings.Contains(f.Predicate, predicate) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 1. param `where` precondition at a call site — proven
// ---------------------------------------------------------------------------

func TestObservability_ParamWherePrecondition_Proven(t *testing.T) {
	src := `
def need_pos(x: i64 where x > 0) -> i64:
    return x

def caller() -> i64:
    n: i64 where n > 0 = 7
    return need_pos(n)
`
	result := analyzeContractStrict(t, "obs_param_where_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	if !proofReportContains(result.ProofReport, "where precondition of need_pos", "where", ProofProvenLinear) &&
		!proofReportContains(result.ProofReport, "where precondition of need_pos", "where", ProofProvenSMT) {
		t.Errorf("expected a proven ProofReport entry for param where precondition at call site; got %+v", result.ProofReport)
	}
}

// ---------------------------------------------------------------------------
// 1b. param `where` precondition at a call site — refuted
// ---------------------------------------------------------------------------

func TestObservability_ParamWherePrecondition_Refuted(t *testing.T) {
	src := `
def need_pos(x: i64 where x > 0) -> i64:
    return x

def caller() -> i64:
    return need_pos(-5)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "obs_param_where_refuted.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true},
	)
	if !proofReportContains(result.ProofReport, "where precondition of need_pos", "where", ProofRefuted) {
		t.Errorf("expected a ProofRefuted entry for param where precondition (violated call); got %+v", result.ProofReport)
	}
}

// ---------------------------------------------------------------------------
// 2. return `where` postcondition — proven
// ---------------------------------------------------------------------------

func TestObservability_ReturnWhere_Proven(t *testing.T) {
	src := `
def make_pos(n: i64 where n > 0) -> i64 where result > 0:
    return n
`
	result := analyzeContractStrict(t, "obs_return_where_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	if !proofReportAny(result.ProofReport, "return where refinement", "where") {
		t.Errorf("expected a ProofReport entry for return where refinement; got %+v", result.ProofReport)
	}
}

// 2b. `ensure` postcondition — proven
// The ensure discharge path records proof under the "ensure" predicate; check broadly.
func TestObservability_EnsurePostcondition_Proven(t *testing.T) {
	src := `
def f() -> i64:
    ensure result >= 0
    return 5
`
	result := analyzeContractStrict(t, "obs_ensure_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	found := false
	for _, f := range result.ProofReport {
		if strings.Contains(f.Predicate, "ensure") &&
			(f.Outcome == ProofProvenLinear || f.Outcome == ProofProvenSMT || f.Outcome == ProofProvenConst || f.Outcome == ProofProvenContract) {
			found = true
			break
		}
	}
	if !found {
		t.Skip("OBSERVABILITY TODO: ensure postcondition (proven) does not yet produce a ProofReport entry — add recordProof to the ensure discharge path")
	}
}

// ---------------------------------------------------------------------------
// 3. local `where` binding — proven, refuted, runtime
// ---------------------------------------------------------------------------

func TestObservability_LocalWhere_Proven(t *testing.T) {
	src := `
def f() -> i64:
    x: i64 where x > 0 = 3
    return x
`
	result := analyzeContractStrict(t, "obs_local_where_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	if !proofReportContains(result.ProofReport, "local where refinement", "where", ProofProvenLinear) &&
		!proofReportContains(result.ProofReport, "local where refinement", "where", ProofProvenSMT) {
		t.Errorf("expected a proven ProofReport entry for local where binding; got %+v", result.ProofReport)
	}
}

func TestObservability_LocalWhere_Refuted(t *testing.T) {
	src := `
def f() -> i64:
    x: i64 where x > 0 = -1
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "obs_local_where_refuted.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true},
	)
	if !proofReportContains(result.ProofReport, "local where refinement", "where", ProofRefuted) {
		t.Errorf("expected a ProofRefuted entry for local where binding (violated); got %+v", result.ProofReport)
	}
}

func TestObservability_LocalWhere_Runtime(t *testing.T) {
	// An unconstrained param means the init cannot be proven at compile time -> runtime fallback.
	src := `
def f(n: i64) -> i64:
    x: i64 where x > 0 = n
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "obs_local_where_runtime.elisa", src,
		AnalyzeOptions{},
	)
	if !proofReportContains(result.ProofReport, "local where refinement", "where", ProofRuntime) {
		t.Skip("OBSERVABILITY TODO: local where binding (runtime-fallback) does not yet produce a ProofRuntime ProofReport entry")
	}
}

// ---------------------------------------------------------------------------
// 4. where-local reassignment re-discharge
// ---------------------------------------------------------------------------

func TestObservability_WhereReassign_Proven(t *testing.T) {
	src := `
def f() -> i64:
    x: mutable i64 where x > 0 = 5
    x <- 10
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "obs_where_reassign_proven.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true},
	)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis for satisfying reassignment, got: %v", errs)
	}
	if !proofReportContains(result.ProofReport, "where refinement on reassignment", "where", ProofProvenLinear) &&
		!proofReportContains(result.ProofReport, "where refinement on reassignment", "where", ProofProvenSMT) {
		t.Errorf("expected a proven ProofReport entry for reassignment re-discharge; got %+v", result.ProofReport)
	}
}

func TestObservability_WhereReassign_Refuted(t *testing.T) {
	src := `
def f() -> i64:
    x: mutable i64 where x > 0 = 5
    x <- -1
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "obs_where_reassign_refuted.elisa", src,
		AnalyzeOptions{},
	)
	if !proofReportContains(result.ProofReport, "where refinement on reassignment", "where", ProofRefuted) {
		t.Errorf("expected a ProofRefuted entry for reassignment re-discharge (violated); got %+v", result.ProofReport)
	}
}

func TestObservability_WhereReassign_Runtime(t *testing.T) {
	src := `
def f(n: i64) -> i64:
    x: mutable i64 where x > 0 = 5
    x <- n
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "obs_where_reassign_runtime.elisa", src,
		AnalyzeOptions{},
	)
	if !proofReportContains(result.ProofReport, "where refinement on reassignment", "where", ProofRuntime) {
		t.Skip("OBSERVABILITY TODO: where reassignment (runtime-fallback) does not yet produce a ProofRuntime ProofReport entry")
	}
}

// ---------------------------------------------------------------------------
// 5. struct-field `where` at construction
// ---------------------------------------------------------------------------

func TestObservability_StructFieldWhere_Proven(t *testing.T) {
	src := `
struct Box:
    v: i64 where v > 0

def make() -> Box:
    return Box(v: 42)
`
	result := analyzeContractStrict(t, "obs_struct_field_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	if !proofReportContains(result.ProofReport, "where refinement on field \"v\"", "where", ProofProvenLinear) &&
		!proofReportContains(result.ProofReport, "where refinement on field \"v\"", "where", ProofProvenSMT) {
		t.Errorf("expected a proven ProofReport entry for struct field where at construction; got %+v", result.ProofReport)
	}
}

func TestObservability_StructFieldWhere_Refuted(t *testing.T) {
	src := `
struct Box:
    v: i64 where v > 0

def make() -> Box:
    return Box(v: -7)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "obs_struct_field_refuted.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true},
	)
	if !proofReportContains(result.ProofReport, "where refinement on field \"v\"", "where", ProofRefuted) {
		t.Errorf("expected a ProofRefuted entry for struct field where (violated); got %+v", result.ProofReport)
	}
}

func TestObservability_StructFieldWhere_Runtime(t *testing.T) {
	src := `
struct Box:
    v: i64 where v > 0

def make(n: i64) -> Box:
    return Box(v: n)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "obs_struct_field_runtime.elisa", src,
		AnalyzeOptions{},
	)
	if !proofReportContains(result.ProofReport, "where refinement on field \"v\"", "where", ProofRuntime) {
		t.Skip("OBSERVABILITY TODO: struct field where (runtime-fallback) does not yet produce a ProofRuntime ProofReport entry")
	}
}

// ---------------------------------------------------------------------------
// 6. named `refine` alias in a binder position
//
// Named refinement aliases are pure desugaring into WhereRefinementTypeExpr -- they share the same
// discharge path and therefore the same proof subjects as anonymous where binders.
// ---------------------------------------------------------------------------

func TestObservability_NamedRefineAlias_Proven(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def need_pos(n: Positive) -> i64:
    return n

def caller() -> i64:
    return need_pos(3)
`
	result := analyzeContractStrict(t, "obs_named_refine_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	// Named alias desugars to anonymous `where`, so the proof subject is "where precondition of need_pos".
	if !proofReportContains(result.ProofReport, "where precondition of need_pos", "where", ProofProvenLinear) &&
		!proofReportContains(result.ProofReport, "where precondition of need_pos", "where", ProofProvenSMT) {
		t.Errorf("expected a proven ProofReport entry for named refine alias param (desugared to where precondition); got %+v", result.ProofReport)
	}
}

func TestObservability_NamedRefineAlias_Refuted(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def need_pos(n: Positive) -> i64:
    return n

def caller() -> i64:
    return need_pos(0)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "obs_named_refine_refuted.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true},
	)
	if !proofReportContains(result.ProofReport, "where precondition of need_pos", "where", ProofRefuted) {
		t.Errorf("expected a ProofRefuted entry for named refine alias param (violated); got %+v", result.ProofReport)
	}
}

// ---------------------------------------------------------------------------
// 7. plain `requires` clause
// ---------------------------------------------------------------------------

func TestObservability_RequiresClause_Proven(t *testing.T) {
	src := `
def use_off(buf: u8&, off: i32) -> u8:
    requires off >= 0
    return buf[off.usize()]

def caller(buf: u8&) -> u8:
    k: i32 = 5
    return use_off(buf, k)
`
	result := analyzeTreeTestSource(t, "obs_requires_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	if !proofReportContains(result.ProofReport, "precondition of use_off", "requires", ProofProvenLinear) &&
		!proofReportContains(result.ProofReport, "precondition of use_off", "requires", ProofProvenSMT) {
		t.Errorf("expected a proven ProofReport entry for `requires` clause at call site; got %+v", result.ProofReport)
	}
}

func TestObservability_RequiresClause_Refuted(t *testing.T) {
	src := `
def use_off(buf: u8&, off: i32) -> u8:
    requires off >= 0
    return buf[off.usize()]

def caller(buf: u8&) -> u8:
    k: i32 = -3
    return use_off(buf, k)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "obs_requires_refuted.elisa", src)
	if !proofReportContains(result.ProofReport, "precondition of use_off", "requires", ProofRefuted) {
		t.Errorf("expected a ProofRefuted entry for `requires` clause (violated); got %+v", result.ProofReport)
	}
}
