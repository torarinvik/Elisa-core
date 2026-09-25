package semantic

import (
	"strings"
	"testing"
)

// Reading a `= zeroed` handle (id) before assignment is a garbage-handle use:
// zero is not a live handle.
func TestZeroedHandleReadBeforeAssignmentIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "zeroed_handle.elisa", `extern Thing
type ThingId = id[Thing]

def bad() -> ThingId:
    h: mutable ThingId = zeroed
    return h
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "uninitialized") {
		t.Fatalf("expected uninitialized-read diagnostic for zeroed handle, got:\n%s", joined)
	}
}

// Reading a `= zeroed` cstr before assignment is a null-pointer hazard.
func TestZeroedCstrReadBeforeAssignmentIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "zeroed_cstr.elisa", `def bad() -> cstr:
    s: mutable cstr = zeroed
    return s
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "uninitialized") {
		t.Fatalf("expected uninitialized-read diagnostic for zeroed cstr, got:\n%s", joined)
	}
}

// Assigning the handle before reading it is fine.
func TestZeroedHandleAssignedThenReadIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "zeroed_handle_ok.elisa", `extern Thing
type ThingId = id[Thing]

def ok(seed: ThingId) -> ThingId:
    h: mutable ThingId = zeroed
    h <- seed
    return h
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics after assigning the handle, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Narrow scope: a `= zeroed` struct is a usable zero-default in this language,
// so reading it must NOT be flagged (zero is valid for non-reference data).
func TestZeroedStructDefaultReadIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "zeroed_struct_ok.elisa", `struct Point:
    x: mutable i32
    y: mutable i32

def ok() -> Point:
    p: mutable Point = zeroed
    return p
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics for zeroed struct default, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Narrow scope: a `= zeroed` scalar is a usable zero-default; not flagged.
func TestZeroedScalarDefaultReadIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "zeroed_scalar_ok.elisa", `def ok() -> i32:
    x: mutable i32 = zeroed
    return x
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics for zeroed scalar default, got:\n%s", strings.Join(errs, "\n"))
	}
}

// A zeroed Arena is a valid empty arena and must not be flagged when used.
func TestZeroedArenaUseIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "zeroed_arena.elisa", `def build() -> usize:
    arena: Arena = zeroed
    in arena:
        xs: mutable darray[i64] = []
        xs.push(1)
        return xs.count
    return 0
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics for zeroed Arena use, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Taking the address does not prove that an unknown callee writes a valid value.
func TestZeroedHandleAddressTakenThenReadIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "zeroed_handle_addr.elisa", `extern Thing
type ThingId = id[Thing]

extern fill(h: mutable ThingId&) -> void

def ok() -> ThingId:
    h: mutable ThingId = zeroed
    fill(&h)
    return h
`)
	if joined := strings.Join(result.Errors(), "\n"); !strings.Contains(joined, "uninitialized") {
		t.Fatalf("expected the escaped handle to remain uninitialized without a write summary, got:\n%s", joined)
	}
}

func TestOmittedReferenceReadBeforeAssignmentIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "omitted_reference_uninitialized.elisa", `def bad() -> i64:
    pointer: mutable i64&
    return pointer[0]
`)
	if joined := strings.Join(result.Errors(), "\n"); !strings.Contains(joined, "uninitialized") {
		t.Fatalf("expected an uninitialized-read diagnostic, got:\n%s", joined)
	}
}

func TestOmittedReferenceCannotBeWrittenThroughBeforeRebind(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "omitted_reference_write_through.elisa", `def bad() -> i64:
    pointer: mutable i64&
    pointer <- 42
    return 0
`)
	if joined := strings.Join(result.Errors(), "\n"); !strings.Contains(joined, "uninitialized reference") {
		t.Fatalf("expected write-through to require an initialized reference, got:\n%s", joined)
	}
}

func TestZeroedReferenceReboundBeforeReadIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "zeroed_reference_rebound.elisa", `def ok() -> i64:
    backing: mutable i64 = 42
    pointer: mutable i64& = zeroed
    pointer <- &backing
    return pointer[0]
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a valid whole-reference rebind to establish the local, got:\n%s", strings.Join(errs, "\n"))
	}
}
