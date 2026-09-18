package semantic

import "testing"

// A callee's `requires` is written in ITS namespace: `requires last >= ID_MIN` inside
// `module Ids`. The caller has to spell the same constant `Ids::ID_MIN`, so the goal and
// the caller's guard facts named the constant differently and never unified — a guard that
// literally IS the precondition proved nothing. Within one module the two spellings agree,
// which is why this only appeared across a module boundary.
func TestCrossModuleRequiresDischargesAgainstCallerGuards(t *testing.T) {
	src := `module Ids:
    const ID_MIN: i64 = 0
    const ID_MAX: i64 = 100

    def next_id(last: i64) -> i64:
        requires last >= ID_MIN
        requires last < ID_MAX
        ensure result == last + 1
        last + 1

module Caller:
    def bump(cursor: i64) -> i64:
        if cursor < Ids::ID_MIN:
            0
        elif cursor >= Ids::ID_MAX:
            0
        else:
            Ids::next_id(cursor)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cross_module_requires.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("guards naming the callee's own constants must discharge its requires, got:\n%s", allDiagnostics(r))
	}
}

// The same call inside the declaring module always worked; keep it working.
func TestSameModuleRequiresStillDischarges(t *testing.T) {
	src := `module Ids:
    const ID_MIN: i64 = 0
    const ID_MAX: i64 = 100

    def next_id(last: i64) -> i64:
        requires last >= ID_MIN
        requires last < ID_MAX
        ensure result == last + 1
        last + 1

    def bump(cursor: i64) -> i64:
        if cursor < ID_MIN:
            0
        elif cursor >= ID_MAX:
            0
        else:
            next_id(cursor)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "same_module_requires.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("an in-module requires must still discharge, got:\n%s", allDiagnostics(r))
	}
}

// A caller whose guards do NOT establish the precondition must still be told.
func TestCrossModuleRequiresStillReportsMissingGuard(t *testing.T) {
	src := `module Ids:
    const ID_MIN: i64 = 0
    const ID_MAX: i64 = 100

    def next_id(last: i64) -> i64:
        requires last >= ID_MIN
        requires last < ID_MAX
        ensure result == last + 1
        last + 1

module Caller:
    def bump(cursor: i64) -> i64:
        Ids::next_id(cursor)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cross_module_requires_unguarded.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if !contains(allDiagnostics(r), `precondition of "next_id"`) {
		t.Fatalf("an unguarded cross-module call must still be reported, got:\n%s", allDiagnostics(r))
	}
}
