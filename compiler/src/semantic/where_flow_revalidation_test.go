//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// where-fact control-flow soundness regression tests
//
// These tests lock in the following invariants (confirmed sound by audit):
//
//  (a) Straight-line preservation: a where-fact seeded at a param binding
//      remains active through subsequent unrelated straight-line code.
//
//  (b) Unannotated variable is not provable: passing a variable with no
//      where annotation to a callee requiring a constraint fails to prove.
//
//  (c) Branch isolation: a where-fact from a param survives an if/else join
//      when neither branch touches the dependency.
//
//  (d) Loop non-mutation: a param where-fact survives a loop that never
//      mutates the dependency.
//
// Implementation note: SMT assert-facts are stored on Scope objects and are
// never included in affineFlowSnapshot, so branch-local facts vanish when
// the child scope exits. Mutations walk the scope chain and call
// invalidateSMTAssertFactsForTarget, which removes any fact whose deps
// overlap the mutated name.
// ---------------------------------------------------------------------------

// whFlowContains is a package-local helper scoped to this file.
func whFlowContains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// ---------------------------------------------------------------------------
// (a) Straight-line preservation
// ---------------------------------------------------------------------------

// A param where-fact must remain active through subsequent unrelated
// straight-line code that does not touch the constrained parameter.
func TestWhereFlowStraightLinePreservation(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(x: i64 where x > 0, y: i64) -> i64:
    n: i64 = y + 1
    m: i64 = n * 2
    return need_positive(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_straight_line.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if whFlowContains(diag, "could not be proven") {
		t.Errorf("param where-fact should remain active after unrelated straight-line code, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// (b) Baseline: no annotation means unprovable
// ---------------------------------------------------------------------------

// An unannotated variable cannot satisfy a callee's where precondition.
// This establishes the baseline that proofLints do fire when the fact is absent.
func TestWhereFlowNoAnnotationIsUnprovable(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(x: i64) -> i64:
    return need_positive(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_no_annotation.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !whFlowContains(diag, "could not be proven") && !whFlowContains(diag, "violated") {
		t.Errorf("expected proof failure for unannotated arg, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// (c) Branch isolation
// ---------------------------------------------------------------------------

// A param where-fact must survive an if/else join when neither branch
// mutates the dependency.
func TestWhereFlowFactSurvivesIfBlockWithoutMutation(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(flag: bool, x: i64 where x > 0) -> i64:
    if flag:
        n: i64 = x + 1
    else:
        n: i64 = x + 2
    return need_positive(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_fact_survives_if.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if whFlowContains(diag, "could not be proven") {
		t.Errorf("param where-fact should survive if/else when neither branch mutates the dependency, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// (d) Loop non-mutation
// ---------------------------------------------------------------------------

// A param where-fact must remain available after a loop that never mutates
// the constrained parameter.
func TestWhereFlowLoopNonMutationPreservesFact(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(n: i64, x: i64 where x > 0) -> i64:
    acc: mutable i64 = 0
    for _ in 0..<n:
        acc <- acc + x
    return need_positive(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_loop_no_mutation.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if whFlowContains(diag, "could not be proven") {
		t.Errorf("param where-fact should survive a loop that does not mutate the dependency, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// Mutation invalidation (via mutable local)
// ---------------------------------------------------------------------------

// A mutable local y initialized from a where-annotated param x, then
// reassigned to a value that violates the constraint, should cause a
// proof failure when y is passed to a callee that requires the constraint.
func TestWhereFlowMutableLocalReassignmentIsUnprovable(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(x: i64 where x > 0) -> i64:
    y: mutable i64 = x
    y <- -1
    return need_positive(y)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_mutable_reassign.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !whFlowContains(diag, "could not be proven") && !whFlowContains(diag, "violated") {
		t.Errorf("expected proof failure after y <- -1; y has no where-fact, got:\n%s", diag)
	}
}
