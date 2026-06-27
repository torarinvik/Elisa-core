//go:build cgo

package semantic

import "testing"

// COMPLETENESS: a guard on a struct field (`if s.n <= 103:`) records a range fact keyed on the
// field path "s.n", so an obligation on s.n inside the branch proves by flow without a runtime
// check. The struct `s` must be an immutable local.
func TestFieldGuardRangeFactProvesRefinementInBranch(t *testing.T) {
	src := `
law Cap(self: i64, hi: i64) = self <= hi

struct Packet:
    n: mutable i64

def f(s: Packet) -> i64 is Cap[103]:
    if s.n <= 103:
        return s.n
    return 0
`
	r := analyzeContractStrict(t, "field_guard_range_fact_ok.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("guard `if s.n <= 103:` should prove `s.n is Cap[103]` by flow, got:\n%s", allDiagnostics(r))
	}
	// The branch return should be ProvenFlow (not Runtime).
	flowCount := 0
	for _, f := range r.ProofReport {
		if f.Outcome == ProofProvenFlow {
			flowCount++
		}
	}
	if flowCount == 0 {
		t.Fatalf("expected at least one ProofProvenFlow entry for field-guard fact, got report: %+v", r.ProofReport)
	}
}

// COMPLETENESS: same as above but the guard is `obj.count < cap` (strict less-than); inside the
// branch obj.count <= cap-1 is established.
func TestFieldGuardStrictLessThanProvesUpperBound(t *testing.T) {
	src := `
law Bounded(self: i64, hi: i64) = self <= hi

struct Table:
    count: mutable i64

def use_count(t: Table, cap: i64) -> i64 is Bounded[99]:
    if t.count < 100:
        return t.count
    return 99
`
	r := analyzeContractStrict(t, "field_guard_strict_lt.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("guard `if t.count < 100:` should prove `t.count is Bounded[99]` by flow, got:\n%s", allDiagnostics(r))
	}
}

// SOUNDNESS: after `s.n <- 999` the field-path range fact must be dropped; the obligation on
// s.n after the mutation must NOT prove by flow (it emits a runtime check or is refuted by the
// SMT solver which sees the written value). A stale field-path fact would let the flow prover
// claim s.n <= 103 even after the assignment — that is the unsoundness we guard against.
func TestFieldGuardRangeFactDroppedAfterFieldMutation(t *testing.T) {
	src := `
law Cap(self: i64, hi: i64) = self <= hi

struct Packet:
    n: mutable i64

def f(s: mutable Packet) -> i64 is Cap[103]:
    if s.n <= 103:
        s.n <- 999
        return s.n
    return 0
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "field_guard_mutation_unsound.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	// The `return s.n` inside the mutation branch must NOT be proven by flow — the fact was dropped.
	for _, f := range r.ProofReport {
		if f.Outcome == ProofProvenFlow {
			t.Fatalf("after s.n <- 999 the flow prover must NOT prove the obligation (stale fact); got ProofProvenFlow in report: %+v", r.ProofReport)
		}
	}
}

// SOUNDNESS: after a mutation of the struct root `s <- newS`, all field-path facts for "s.*"
// must be invalidated; an obligation after the reassignment must not prove by stale field facts.
func TestFieldGuardRangeFactDroppedAfterStructRootMutation(t *testing.T) {
	src := `
law Cap(self: i64, hi: i64) = self <= hi

struct Packet:
    n: mutable i64

def big() -> Packet:
    return Packet(999)

def f(s: mutable Packet) -> i64 is Cap[103]:
    if s.n <= 103:
        s <- big()
        return s.n
    return 0
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "field_guard_root_mutation_unsound.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	// After `s <- big()` the "s.n" range fact must be gone; the return must not prove by flow.
	for _, f := range r.ProofReport {
		if f.Outcome == ProofProvenFlow {
			t.Fatalf("after s <- big() stale field-path fact must not prove the obligation; got ProofProvenFlow in report: %+v", r.ProofReport)
		}
	}
}
