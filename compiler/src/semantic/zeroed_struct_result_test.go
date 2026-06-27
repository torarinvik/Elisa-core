package semantic

import "testing"

// Tests for "zeroed-struct-then-mutated local returned as result" postcondition proofs.
//
// Pattern: `out: T = zeroed; out.f <- v; return out` with `ensure result.f == v`.
// The prover resolves `result.f` via the writtenField scope chain (last written value)
// or falls back to 0 for unwritten fields on a zeroed local (zeroedStructLocals tracking).

// --- POSITIVE / COMPLETENESS TESTS ---

// TestZeroedStructResultWrittenFieldProves: a field written after zeroed initialization proves.
func TestZeroedStructResultWrittenFieldProves(t *testing.T) {
	src := `
struct Pair:
    x: mutable i64
    y: mutable i64
def make_pair(v: i64) -> Pair:
    ensure result.x == v
    out: mutable Pair = zeroed
    out.x <- v
    return out
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "zsrf_written.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("ensure over written zeroed-struct field should prove, got:\n%s", allDiagnostics(r))
	}
}

// TestZeroedStructResultUnwrittenFieldIsZero: an unwritten field on a zeroed local proves == 0.
func TestZeroedStructResultUnwrittenFieldIsZero(t *testing.T) {
	src := `
struct Pair:
    x: mutable i64
    y: mutable i64
def make_pair(v: i64) -> Pair:
    ensure result.y == 0
    out: mutable Pair = zeroed
    out.x <- v
    return out
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "zsrf_unwritten_zero.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("ensure over unwritten zeroed field (== 0) should prove, got:\n%s", allDiagnostics(r))
	}
}

// --- SOUNDNESS / NEGATIVE TESTS ---

// TestZeroedStructResultWrongValueMustNotProve: ensure result.x == v+1 must NOT prove when out.x <- v.
func TestZeroedStructResultWrongValueMustNotProve(t *testing.T) {
	src := `
struct Pair:
    x: mutable i64
    y: mutable i64
def make_pair(v: i64) -> Pair:
    ensure result.x == v + 1
    out: mutable Pair = zeroed
    out.x <- v
    return out
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "zsrf_wrong_val.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "could not be proven") {
		t.Fatalf("ensure over wrong value (v+1 != v) must NOT prove, got:\n%s", allDiagnostics(r))
	}
}

// TestZeroedStructResultConditionalWriteDeclines: when a field is conditionally written (inside an if),
// the whole-root invalidation path is not triggered but the field has multiple possible values.
// The prover must decline (not assert a single value) — the written-field fact is dropped at the
// if/else merge, so result.x is unresolvable and cannot be proven equal to v.
func TestZeroedStructResultConditionalWriteDeclines(t *testing.T) {
	src := `
struct Pair:
    x: mutable i64
    y: mutable i64
def make_pair(v: i64, flag: bool) -> Pair:
    ensure result.x == v
    out: mutable Pair = zeroed
    if flag:
        out.x <- v
    return out
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "zsrf_conditional.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "could not be proven") {
		t.Fatalf("ensure over conditionally-written field must NOT prove (not all paths write it), got:\n%s", allDiagnostics(r))
	}
}

// TestZeroedStructResultMutatingCalleeDropsFact: passing out to a mutating callee invalidates all
// field facts. The prover must decline — the callee may have changed out.x.
func TestZeroedStructResultMutatingCalleeDropsFact(t *testing.T) {
	src := `
struct Pair:
    x: mutable i64
    y: mutable i64
def bump(p: mutable Pair&) -> void:
    p.x <- 99
def make_pair(v: i64) -> Pair:
    ensure result.x == v
    out: mutable Pair = zeroed
    out.x <- v
    bump(out)
    return out
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "zsrf_mutating_callee.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "could not be proven") {
		t.Fatalf("passing out to a mutating callee must drop written-field fact, got:\n%s", allDiagnostics(r))
	}
}
