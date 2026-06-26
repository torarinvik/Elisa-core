//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// where-fact control-flow scenarios: comprehensive coverage
//
// These tests exercise advanced control-flow handling of local `where` facts:
//
//  (1) Straight-line preservation: where-facts on params survive unrelated code.
//  (2) Mutation invalidation: local mutations (mutable var reassignment)
//      invalidate derived where-facts.
//  (3) Reassignment re-discharge: where-typed locals are re-proven on assignment.
//  (4) Branch-join isolation: facts don't leak past if/else joins.
//  (5) Loop-mutation persistence: unsound facts from loop-mutated dependencies
//      are not carried forward.
//
// Additional scenarios:
//  (6) Nested if/else: facts survive when no nested branch touches dependency.
//  (7) Multiple params: independent where-facts stay independent.
//  (8) Dependency tracking: facts that depend on mutated vars are invalidated.
//  (9) Reassignment with computed values: re-discharge on complex RHS exprs.
// (10) Loop mutation of dependency: facts are invalidated after loop mutation.
//
// Implementation note: SMT assert-facts are stored on Scope objects and are
// never included in affineFlowSnapshot, so branch-local facts vanish when
// the child scope exits. Mutations walk the scope chain and call
// invalidateSMTAssertFactsForTarget, which removes any fact whose deps
// overlap the mutated name.
// ---------------------------------------------------------------------------

func whScenarioContains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// ---------------------------------------------------------------------------
// (1) Straight-line preservation: basic param where-fact
// ---------------------------------------------------------------------------

// A param where-fact must remain active through subsequent unrelated
// straight-line code that does not touch the constrained parameter.
func TestWhereFlowScenariosParamFactPreservation(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(x: i64 where x > 0) -> i64:
    a: i64 = 1
    b: i64 = 2
    c: i64 = a + b
    return need_positive(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scenario_param_fact_preservation.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if whScenarioContains(diag, "could not be proven") {
		t.Errorf("param where-fact should remain active through straight-line code, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// (2) Mutation invalidation: mutable local loses where-fact on reassign
// ---------------------------------------------------------------------------

// A mutable local initialized from a where-fact param, then reassigned to
// a value violating the constraint, should fail proof.
func TestWhereFlowScenariosMutableLocalReassignInvalidation(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(x: i64 where x > 0) -> i64:
    y: mutable i64 = x
    y <- -5
    return need_positive(y)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scenario_mutable_invalidation.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !whScenarioContains(diag, "could not be proven") && !whScenarioContains(diag, "violated") {
		t.Errorf("mutable local reassignment should invalidate fact, expected proof failure, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// (3) Reassignment re-discharge: where-typed local must re-satisfy constraint
// ---------------------------------------------------------------------------

// A where-typed local declaration must be re-proven on reassignment.
// Satisfying reassignment should be clean.
func TestWhereFlowScenariosReassignmentReDischarge(t *testing.T) {
	src := `
def caller() -> i64:
    x: mutable i64 where x > 0 = 5
    x <- 10
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scenario_reassign_redischarge.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if whScenarioContains(diag, "violated") || whScenarioContains(diag, "could not be proven") {
		t.Errorf("satisfying reassignment x <- 10 should be clean, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// (4) Branch-join isolation: facts don't leak past if/else
// ---------------------------------------------------------------------------

// A where-fact from a param should survive an if/else join when neither branch
// mutates the dependency. The fact lives in the outer scope, branch facts are
// local and vanish at the join.
func TestWhereFlowScenariosFactSurvivesIfElseJoin(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(flag: bool, x: i64 where x > 0) -> i64:
    if flag:
        n: i64 = x + 1
    else:
        m: i64 = x + 2
    return need_positive(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scenario_if_else_join.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if whScenarioContains(diag, "could not be proven") {
		t.Errorf("param where-fact should survive if/else join, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// (5) Loop mutation of dependency invalidates fact
// ---------------------------------------------------------------------------

// A where-fact on a param x should be INVALIDATED if the loop mutates x.
// The fact is tied to x's binding; if x changes, the fact must be re-proven.
func TestWhereFlowScenariosLoopMutatesParamDependency(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(n: i64) -> i64:
    x: mutable i64 where x > 0 = 5
    for _ in 0..<n:
        x <- -1
    return need_positive(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scenario_loop_mutates_dependency.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !whScenarioContains(diag, "could not be proven") && !whScenarioContains(diag, "violated") {
		t.Errorf("loop mutation of x should invalidate where-fact, expected proof failure, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// (6) Nested if/else: facts survive when nested branches don't touch dependency
// ---------------------------------------------------------------------------

// Nested if/else blocks should not invalidate outer-scope where-facts if
// the nested branches don't mutate the dependency.
func TestWhereFlowScenariosNestedIfElseNoMutation(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(a: bool, b: bool, x: i64 where x > 0) -> i64:
    if a:
        if b:
            n: i64 = x + 1
        else:
            m: i64 = x + 2
    return need_positive(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scenario_nested_if_else.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if whScenarioContains(diag, "could not be proven") {
		t.Errorf("nested if/else should not invalidate fact when no mutation occurs, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// (7) Multiple params: independent where-facts remain independent
// ---------------------------------------------------------------------------

// Multiple params with where-facts should each be independently provable.
// Mutation of one dependency should not affect facts on other params.
func TestWhereFlowScenariosMultipleParamsIndependent(t *testing.T) {
	src := `
def need_both(x: i64 where x > 0, y: i64 where y < 100) -> i64:
    return x + y

def caller(x: i64 where x > 0, y: i64 where y < 100) -> i64:
    z: mutable i64 = 50
    z <- 75
    return need_both(x, y)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scenario_multiple_params_independent.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if whScenarioContains(diag, "could not be proven") {
		t.Errorf("multiple independent where-params should both remain provable, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// (8) Dependency tracking: facts tied to mutated vars are invalidated
// ---------------------------------------------------------------------------

// If a where-fact on local y depends on x, and x is mutated, the fact should
// be invalidated. The dependency graph tracks which symbols appear in the predicate.
func TestWhereFlowScenariosDependencyTracking(t *testing.T) {
	src := `
def need_ordered(x: i64, y: i64 where x < y) -> i64:
    return y

def caller() -> i64:
    x: mutable i64 = 5
    y: mutable i64 where x < y = 10
    x <- 15
    return need_ordered(x, y)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scenario_dependency_tracking.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	// After x <- 15, the fact "x < y" (with y still 10) is no longer true,
	// so y is no longer provable as satisfying the constraint.
	if !whScenarioContains(diag, "could not be proven") && !whScenarioContains(diag, "violated") {
		t.Errorf("mutation of x should invalidate fact depending on x, expected proof failure, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// (9) Reassignment with complex RHS: re-discharge checks computed values
// ---------------------------------------------------------------------------

// When a where-typed local is reassigned with a complex expression (e.g., result
// of a function call), the re-discharge must handle unknown values via lint.
func TestWhereFlowScenariosReassignmentComplexRHS(t *testing.T) {
	src := `
def some_unknown() -> i64:
    return 0

def caller() -> i64:
    x: mutable i64 where x > 0 = 5
    x <- some_unknown()
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scenario_reassign_complex_rhs.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !whScenarioContains(diag, "could not be proven") && !whScenarioContains(diag, "where") {
		t.Errorf("unknown reassignment should produce lint, not silence, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// (10) Loop mutation: facts on mutable vars don't survive loop reassignment
// ---------------------------------------------------------------------------

// A mutable local with a where-fact that is reassigned inside a loop should
// lose the fact for use after the loop.
func TestWhereFlowScenariosLoopReassignsWhereLocal(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(n: i64) -> i64:
    x: mutable i64 where x > 0 = 5
    for _ in 0..<n:
        x <- -1
    return need_positive(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scenario_loop_reassigns_where_local.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !whScenarioContains(diag, "could not be proven") && !whScenarioContains(diag, "violated") {
		t.Errorf("loop reassignment x <- -1 should invalidate where-fact, expected proof failure, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// Additional scenario: If branch mutates, but else does not
// ---------------------------------------------------------------------------

// When one branch of an if/else mutates the dependency and the other does not,
// the join should conservatively assume the dependency may have been mutated,
// invalidating the where-fact.
func TestWhereFlowScenariosAsymmetricBranchMutation(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(flag: bool, x: mutable i64 where x > 0) -> i64:
    if flag:
        x <- -1
    return need_positive(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scenario_asymmetric_branch_mutation.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	// After the if, x may have been mutated to -1, so the fact is lost.
	if !whScenarioContains(diag, "could not be proven") {
		t.Errorf("asymmetric branch mutation should invalidate fact, expected proof failure, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// Additional scenario: Loop with conditional reassignment
// ---------------------------------------------------------------------------

// A loop that conditionally reassigns a where-typed variable should
// conservatively invalidate the fact after the loop, since the mutation
// may have occurred.
func TestWhereFlowScenariosLoopConditionalReassignment(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(n: i64) -> i64:
    x: mutable i64 where x > 0 = 5
    for i in 0..<n:
        if i > 2:
            x <- -1
    return need_positive(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scenario_loop_conditional_reassignment.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	// After the loop, x may have been mutated, so the fact is conservatively lost.
	if !whScenarioContains(diag, "could not be proven") && !whScenarioContains(diag, "violated") {
		t.Errorf("loop with conditional reassignment should invalidate fact, expected proof failure, got:\n%s", diag)
	}
}
