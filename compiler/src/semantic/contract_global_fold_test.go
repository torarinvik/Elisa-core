//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// contract_global_fold_test.go: two verification-UX fixes.
//
//  1. An IMMUTABLE global (`global NAME: T = <literal>`, no `mutable`) folds as a proof constant in
//     precondition discharge, exactly like `const`. A non-mutable global is never written (the type
//     checker rejects stores to it), so its literal initializer is a sound compile-time constant.
//  2. The unprovable-precondition suggestion names the WORKING remedy: the `assert(...)` CALL form
//     (a bare `assert COND` is a static assertion and does not seed the prover); and a contract
//     clause rejected for reading an immutable global suggests converting it to `const`.

// TestImmutableGlobalFoldsInPrecondition: `requires lo <= hi` at a call `clamp(x, 8, CAP)` where
// `global CAP = 256` must discharge statically (no runtime-check fallback).
func TestImmutableGlobalFoldsInPrecondition(t *testing.T) {
	src := `
global CAP: i32 = 256

def clamp_i32(v: i32, lo: i32, hi: i32) -> i32:
    requires lo <= hi
    return lo if v < lo else (hi if v > hi else v)

def use_global(x: i32) -> i32:
    return clamp_i32(x, 8, CAP)
`
	result := analyzeTreeTestSource(t, "imm_global_fold.elisa", src)
	if hasRuntimeCheck(result) {
		t.Fatalf("immutable global CAP should fold so `8 <= CAP` discharges; got a runtime-check fallback:\n%s", allDiagnostics(result))
	}
}

// TestMutableGlobalDoesNotFoldInPrecondition: a `global mutable` is genuinely non-deterministic in
// spec position, so it must NOT fold — the clause falls back to a runtime check (sound).
func TestMutableGlobalDoesNotFoldInPrecondition(t *testing.T) {
	src := `
global mutable CAP: i32 = 256

def clamp_i32(v: i32, lo: i32, hi: i32) -> i32:
    requires lo <= hi
    return lo if v < lo else (hi if v > hi else v)

def use_global(x: i32) -> i32:
    return clamp_i32(x, 8, CAP)
`
	result := analyzeTreeTestSource(t, "mut_global_nofold.elisa", src)
	if !hasRuntimeCheck(result) {
		t.Fatalf("mutable global CAP must NOT fold as a proof constant; expected a could-not-be-proven fallback, got none:\n%s", allDiagnostics(result))
	}
}

// TestImmutableGlobalRefutesPrecondition: folding works in BOTH directions — immutable globals that
// make a precondition provably false (`10 <= 3`) trigger the hard refutation error.
func TestImmutableGlobalRefutesPrecondition(t *testing.T) {
	src := `
global LO: i32 = 10
global HI: i32 = 3

def clamp_i32(v: i32, lo: i32, hi: i32) -> i32:
    requires lo <= hi
    return lo if v < lo else (hi if v > hi else v)

def use_global(x: i32) -> i32:
    return clamp_i32(x, LO, HI)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "imm_global_refute.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "is violated") {
		t.Fatalf("immutable globals LO=10, HI=3 should refute `lo <= hi`; expected a violation error, got:\n%v", result.Errors())
	}
}

// TestUnprovablePreconditionSuggestionUsesAssertCall: the suggestion must name `assert(...)` (the
// call form that seeds the prover), never a bare `assert COND` (a static assertion that does not).
func TestUnprovablePreconditionSuggestionUsesAssertCall(t *testing.T) {
	src := `
def needs_nonneg(v: i32) -> i32:
    requires v >= 0
    return v

def caller(x: i32) -> i32:
    return needs_nonneg(x)
`
	result := analyzeTreeTestSource(t, "assert_call_suggestion.elisa", src)
	diags := allDiagnostics(result)
	// The goal renders under the caller's argument substitution (`v` -> the arg `x`).
	if !strings.Contains(diags, "add `assert(x >= 0)`") {
		t.Fatalf("suggestion should propose the `assert(...)` call form; got:\n%s", diags)
	}
	// Guard against a regression to the bare form `add `assert x >= 0``.
	if strings.Contains(diags, "add `assert x") {
		t.Fatalf("suggestion still uses the non-working bare `assert COND` form:\n%s", diags)
	}
}

// TestFollowingAssertCallSuggestionDischarges: the suggested `assert(v >= 0)` actually discharges the
// precondition — proving the advice is truthful (the assert-guarded call no longer warns).
func TestFollowingAssertCallSuggestionDischarges(t *testing.T) {
	src := `
def needs_nonneg(v: i32) -> i32:
    requires v >= 0
    return v

def caller_bare(x: i32) -> i32:
    return needs_nonneg(x)

def caller_asserted(x: i32) -> i32:
    assert(x >= 0)
    return needs_nonneg(x)
`
	result := analyzeTreeTestSource(t, "assert_call_discharges.elisa", src)
	// Exactly one call site (caller_bare) should remain a runtime check; the asserted one discharges.
	n := strings.Count(allDiagnostics(result), "could not be proven statically")
	if n != 1 {
		t.Fatalf("expected exactly 1 unprovable call site (caller_bare); the `assert(x >= 0)` should discharge caller_asserted, got %d:\n%s", n, allDiagnostics(result))
	}
}

// TestContractReadingImmutableGlobalSuggestsConst: a `requires` that READS an immutable global is a
// purity error; the diagnostic must suggest converting that global to `const`.
func TestContractReadingImmutableGlobalSuggestsConst(t *testing.T) {
	src := `
global CAP: i32 = 24000000

def copy_rows(w: i32, h: i32) -> i32:
    requires w * h <= CAP
    return w
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "contract_reads_global.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "must be pure") {
		t.Fatalf("reading a global in a `requires` should be a purity error, got:\n%v", result.Errors())
	}
	if !strings.Contains(errs, "`const`") || !strings.Contains(errs, `"CAP"`) {
		t.Fatalf("purity error should suggest declaring CAP as `const`, got:\n%v", result.Errors())
	}
}

// TestContractReadingMutableGlobalOmitsConstHint: a mutable global read in a contract must NOT get the
// `const` hint — converting a value you mutate to `const` is not a valid fix.
func TestContractReadingMutableGlobalOmitsConstHint(t *testing.T) {
	src := `
global mutable CAP: i32 = 5

def f(w: i32) -> i32:
    requires w <= CAP
    return w
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "contract_reads_mut_global.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "must be pure") {
		t.Fatalf("reading a mutable global in a `requires` should be a purity error, got:\n%v", result.Errors())
	}
	if strings.Contains(errs, "as `const`") {
		t.Fatalf("mutable global must NOT be suggested for `const` conversion, got:\n%v", result.Errors())
	}
}
