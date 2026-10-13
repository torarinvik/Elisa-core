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
