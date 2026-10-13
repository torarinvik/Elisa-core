package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS: a callee that stores a by-value darray parameter into a field reached through a
// struct reference parameter (`h.xs <- xs`) keeps the argument's backing alive past the call.
// A caller passing a container that lives in a shorter-lived local region (the function's
// inferred region, or a per-iteration loop region) left `h.xs` dangling: it compiled and read
// freed memory at runtime. The caller must reject the call.
func TestCallStoringLocalDarrayIntoStructRefFieldRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_call_store.elisa", `struct Holder:
    xs: mutable darray[i64] = []

def stash(h: mutable Holder&, xs: darray[i64]) -> void:
    h.xs <- xs

def fill(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        for i in 0..<50 |local|:
            local.push(i.i64())
        stash(h, local)

def fill_loop(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        for k in 0..<3 |h|:
            local: mutable darray[i64] = []
            local.push(k.i64())
            stash(h, local)

def stash_alias(h: mutable Holder&, xs: darray[i64]) -> void:
    tmp: darray[i64] = xs
    h.xs <- tmp

def stash_forward(h: mutable Holder&, xs: darray[i64]) -> void:
    stash(h, xs)

def fill_alias(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        stash_alias(h, local)

def fill_forward(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        stash_forward(h, local)

def fill_ufcs(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        h.stash(local)
`)
	joined := strings.Join(result.Errors(), "\n")
	if got := strings.Count(joined, "call may store a value in region"); got != 5 {
		t.Fatalf("expected both local-darray stores through a struct ref to be rejected, got %d:\n%s", got, joined)
	}
}

// The check is directed by the callee's store summary: a callee that only READS the darray
// argument (`h.count <- xs.count`) stores nothing, and a target struct living in the same
// region as the stored container cannot outlive it.
func TestCallStoringIntoStructRefFieldSafeFormsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_call_store_ok.elisa", `struct Holder:
    xs: mutable darray[i64] = []
    count: mutable usize = 0

def note(h: mutable Holder&, xs: darray[i64]) -> void:
    h.count <- xs.count

def stash(h: mutable Holder&, xs: darray[i64]) -> void:
    h.xs <- xs

def fill(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        note(h, local)

def same_scope() -> usize:
    can Memory.Allocate, Abort.Panic:
        g: mutable Holder = Holder{}
        local: mutable darray[i64] = []
        local.push(3)
        stash(g, local)
        return g.xs.count
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected read-only and same-region forms to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}

// A by-value darray parameter is a header copy that SHARES its element buffer with the caller:
// `put0(h.rows, xs)` with `rows[0] <- xs` writes into h's buffer, so a local-region xs must be
// rejected exactly like a store through a reference (runtime-verified UAF when exempted).
// A by-value parameter with no shared indirect storage (a plain scalar struct) stays exempt.
func TestCallStoringThroughByValueSharedBufferRejected(t *testing.T) {
	bad := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_byvalue_shared.elisa", `struct Rows:
    rows: mutable darray[darray[i64]] = []

def put0(rows: darray[darray[i64]], xs: darray[i64]) -> void:
    can Abort.Panic:
        rows[0] <- xs

def stash2(h: mutable Rows&, xs: darray[i64]) -> void:
    can Abort.Panic:
        put0(h.rows, xs)

def fill(h: mutable Rows&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        stash2(h, local)
`)
	if joined := strings.Join(bad.Errors(), "\n"); !strings.Contains(joined, "call may store a value in region") {
		t.Fatalf("an element write through a by-value darray that shares the target's buffer must be rejected, got:\n%s", joined)
	}
	ok := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_byvalue_scalar.elisa", `struct Pt:
    x: i64
    y: i64

struct Holder:
    xs: mutable darray[i64] = []
    at: mutable Pt = Pt{x: 0, y: 0}

def bump(p: Pt, xs: darray[i64]) -> i64:
    return p.x + xs.count.i64()

def note(h: mutable Holder&, xs: darray[i64]) -> void:
    h.at <- Pt{x: bump(h.at, xs), y: 0}

def fill(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        note(h, local)
`)
	if errs := ok.Errors(); len(errs) != 0 {
		t.Fatalf("a by-value scalar struct parameter shares nothing with the caller; expected acceptance, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Element-mode stores: copying scalar elements out of a local darray into the target retains no
// local storage (accepted); copying an element that is itself a local-region container header
// (`h.rows.push(rows[0])` with `rows: darray[darray[i64]]` built locally) does (rejected).
func TestCallStoringElementsIntoStructRefField(t *testing.T) {
	ok := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_elem_ok.elisa", `struct Holder:
    xs: mutable darray[i64] = []

def copy_in(h: mutable Holder&, xs: darray[i64]) -> void:
    can Memory.Allocate, Abort.Panic:
        for x in xs |h|:
            h.xs.push(x)

def fill(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        local.push(1)
        copy_in(h, local)
`)
	if errs := ok.Errors(); len(errs) != 0 {
		t.Fatalf("copying scalar elements into a struct ref must be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
	bad := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_target_elem_bad.elisa", `struct Rows:
    rows: mutable darray[darray[i64]] = []

def take_first(h: mutable Rows&, rows: darray[darray[i64]]) -> void:
    can Memory.Allocate, Abort.Panic:
        h.rows.push(rows[0])

def fill(h: mutable Rows&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[darray[i64]] = []
        inner: mutable darray[i64] = []
        inner.push(1)
        local.push(inner)
        take_first(h, local)
`)
	if joined := strings.Join(bad.Errors(), "\n"); !strings.Contains(joined, "call may store a value in region") {
		t.Fatalf("copying a local-region container element into a struct ref must be rejected, got:\n%s", joined)
	}
}
