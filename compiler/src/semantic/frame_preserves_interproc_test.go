//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Interprocedural `preserves` (docs/87): the task's canonical example. `keeps_name` requires and
// must `ensure c.name == n`; the intervening `bump(c)` declares `changes c.value`, so `c.name` is
// provably untouched and the postcondition survives the call to discharge cleanly.
func TestInterprocPreservesPostconditionSurvivesDisjointCall(t *testing.T) {
	src := `
struct Counter:
    value: mutable i64
    name: mutable i64

def bump(c: mutable Counter&) changes c.value:
    c.value <- c.value + 1

def keeps_name(c: mutable Counter&, n: i64) -> void:
    requires c.name == n
    ensure c.name == n
    bump(c)
`
	result := analyzeContractStrict(t, "interproc_preserves_clean.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`bump` changes only c.value, so `c.name == n` is preserved across the call and `ensure c.name == n` must prove; got: %v", errs)
	}
}

// SOUNDNESS: when the callee's frame DOES cover the preserved place, the caller's postcondition must
// NOT survive. Here `clobber` declares `changes c.name`, so the fact `c.name == n` is dropped across
// the call and `ensure c.name == n` cannot be proven.
func TestInterprocPreservesPostconditionDroppedOnOverlap(t *testing.T) {
	src := `
struct Counter:
    value: mutable i64
    name: mutable i64

def clobber(c: mutable Counter&) changes c.name:
    c.name <- 0

def keeps_name(c: mutable Counter&, n: i64) -> void:
    requires c.name == n
    ensure c.name == n
    clobber(c)
`
	result := analyzeContractStrict(t, "interproc_preserves_overlap.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("a call that `changes c.name` must drop the `c.name == n` fact so the postcondition fails; got: %v", result.Errors())
	}
}

// SOUNDNESS: an UNFRAMED callee (no `changes`/`preserves`) may change everything, so the caller's
// postcondition about ANY field must be dropped conservatively — survival is earned only by a
// declared frame.
func TestInterprocPreservesPostconditionDroppedWhenCalleeUnframed(t *testing.T) {
	src := `
struct Counter:
    value: mutable i64
    name: mutable i64

def touch(c: mutable Counter&) -> void:
    c.value <- c.value + 1

def keeps_name(c: mutable Counter&, n: i64) -> void:
    requires c.name == n
    ensure c.name == n
    touch(c)
`
	result := analyzeContractStrict(t, "interproc_preserves_unframed.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("an unframed callee may change anything, so `c.name == n` must not survive; got: %v", result.Errors())
	}
}

// A caller that `preserves c.name` may not call a callee that `changes c.name` on that very place
// (docs/87 channel 2): the callee's frame is refined onto the argument and checked against the
// caller's own `preserves` set.
func TestInterprocCallerPreservesViolatedByCalleeChanges(t *testing.T) {
	src := `
struct Counter:
    value: mutable i64
    name: mutable i64

def clobber(c: mutable Counter&) changes c.name:
    c.name <- 0

def outer(c: mutable Counter&) preserves c.name:
    clobber(c)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "interproc_caller_preserves_err.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "preserves") {
		t.Fatalf("a function that `preserves c.name` must not call one that `changes c.name`; got:\n%s", allDiagnostics(result))
	}
}

// The dual: a caller that `preserves c.name` calling a callee that changes only the DISJOINT
// `c.value` is clean — the refined arg place (`c.value`) does not overlap the preserved place.
func TestInterprocCallerPreservesCleanOnDisjointCalleeChange(t *testing.T) {
	src := `
struct Counter:
    value: mutable i64
    name: mutable i64

def bump(c: mutable Counter&) changes c.value:
    c.value <- c.value + 1

def outer(c: mutable Counter&) preserves c.name:
    bump(c)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "interproc_caller_preserves_clean.elisa", src, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`bump` changes only c.value, disjoint from the preserved c.name, so this is clean; got: %v", errs)
	}
}
