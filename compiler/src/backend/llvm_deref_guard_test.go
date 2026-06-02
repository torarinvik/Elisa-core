//go:build cgo

package backend

import (
	"strings"
	"testing"
)

const derefGuardFieldReadSrc = `struct P:
    x: mutable i32

def read(p: P&) -> i32:
    return p.x
`

// Under -fbounds-check (ELISACORE_FORCE_BOUNDS_CHECK) a runtime-derived field read is
// guarded: a null/near-null compare on the load address plus llvm.trap on violation,
// so a null-base dereference traps at the deref site instead of faulting downstream.
func TestDerefGuardTrapsWhenForced(t *testing.T) {
	t.Setenv("ELISACORE_FORCE_BOUNDS_CHECK", "1")
	result := parseAndAnalyzeBackendTest(t, "deref_guard_forced.elisa", derefGuardFieldReadSrc)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "pg.valid") {
		t.Fatalf("expected null-deref guard compare on forced field read, got:\n%s", output)
	}
	if !strings.Contains(output, "@llvm.trap") {
		t.Fatalf("expected null-deref guard to emit llvm.trap on violation, got:\n%s", output)
	}
}

const derefGuardFieldStoreSrc = `struct P:
    x: mutable i32

def write(p: mutable P&, v: i32):
    p.x <- v
`

// A field STORE through a reference is guarded at the base too (the store analogue of
// the read guard), so `nullObj.field <- v` traps at the base, not downstream.
func TestDerefGuardCoversFieldStores(t *testing.T) {
	t.Setenv("ELISACORE_FORCE_BOUNDS_CHECK", "1")
	result := parseAndAnalyzeBackendTest(t, "deref_guard_store.elisa", derefGuardFieldStoreSrc)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "pg.valid") {
		t.Fatalf("expected null-deref guard on forced field store, got:\n%s", output)
	}
	if !strings.Contains(output, "@llvm.trap") {
		t.Fatalf("expected null-deref guard to emit llvm.trap on field store, got:\n%s", output)
	}
}

// Without the flag the deref guard is absent even at -O0: it instruments every
// runtime-derived load, so it stays opt-in (unlike the always-on-at-O0 index watchdog).
func TestDerefGuardAbsentByDefaultAtO0(t *testing.T) {
	t.Setenv("ELISACORE_FORCE_BOUNDS_CHECK", "")
	result := parseAndAnalyzeBackendTest(t, "deref_guard_default.elisa", derefGuardFieldReadSrc)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "pg.valid") || strings.Contains(output, "pg.fail") {
		t.Fatalf("expected no deref guard without -fbounds-check at -O0, got:\n%s", output)
	}
}
