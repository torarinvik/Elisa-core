package semantic

import (
	"strings"
	"testing"
)

// `owned Arena` (approach A′) marks a binding as owning a region lifetime: it is
// affine/must-consume. An owned param/local that is never consumed is an error.
func TestOwnedArenaLocalMustBeConsumed(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "owned_local.elisa", `extern new_arena(cap: usize) -> Arena

def f() -> void:
    r: owned Arena = new_arena(64)
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "owned region") || !strings.Contains(all, "must be consumed") {
		t.Fatalf("expected owned Arena local to require consumption; got: %s", all)
	}
}

// An owned region is a first-class owner: destroy discharges it and new[r] works.
func TestOwnedArenaDestroyedIsClean(t *testing.T) {
	analyzeTreeTestSource(t, "owned_destroyed.elisa", `extern new_arena(cap: usize) -> Arena

struct Node in owner:
    value: i32

def f() -> void:
    r: owned Arena = new_arena(64)
    n: r Node[r]& = new[r] Node(7)
    _ = n.value
    destroy r
`)
}

// An `owned Arena` parameter transfers ownership into the callee: the callee
// must consume it (this is what unblocks moving a region into a worker).
func TestOwnedArenaParamMustBeConsumed(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "owned_param.elisa", `def worker(r: owned Arena) -> void:
    _ = r.end_index
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "owned region") || !strings.Contains(all, "must be consumed") {
		t.Fatalf("expected owned Arena param to require consumption; got: %s", all)
	}
}

// An owned Arena param that the callee destroys (and allocates into) is clean.
func TestOwnedArenaParamDestroyedIsClean(t *testing.T) {
	analyzeTreeTestSource(t, "owned_param_ok.elisa", `struct Node in owner:
    value: i32

def worker(r: owned Arena) -> void:
    n: r Node[r]& = new[r] Node(7)
    _ = n.value
    destroy r
`)
}

// Guardrail: a plain (non-owned) Arena value carries NO new obligation — its
// existing semantics are untouched. Ownership comes only via the `owned` marker.
func TestPlainArenaValueHasNoObligation(t *testing.T) {
	analyzeTreeTestSource(t, "plain_arena.elisa", `extern new_arena(cap: usize) -> Arena

def f() -> void:
    r: Arena = new_arena(64)
`)
}
