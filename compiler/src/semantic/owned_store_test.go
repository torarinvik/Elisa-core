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

struct Node[@owner]:
    value: i32

def f() -> void:
    r: owned Arena = new_arena(64)
    n: Node[r]& @r = new[r] Node{value: 7}
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
	analyzeTreeTestSource(t, "owned_param_ok.elisa", `struct Node[@owner]:
    value: i32

def worker(r: owned Arena) -> void:
    n: Node[r]& @r = new[r] Node{value: 7}
    _ = n.value
    destroy r
`)
}

// An owned region is move-only: binding it to another `owned` binding by copy
// would create two owners of one region (double-free). Require `move`.
func TestOwnedArenaIsMoveOnly(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "owned_copy.elisa", `extern new_arena(cap: usize) -> Arena

def f() -> void:
    r: owned Arena = new_arena(64)
    s: owned Arena = r
    destroy r
    destroy s
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "move-only") {
		t.Fatalf("expected owned region copy to be rejected as move-only; got: %s", all)
	}
}

// Moving an owned region to a new owner transfers it: the source is consumed,
// only the destination must be (and may be) consumed.
func TestOwnedArenaMoveTransfersToNewOwner(t *testing.T) {
	analyzeTreeTestSource(t, "owned_move_ok.elisa", `extern new_arena(cap: usize) -> Arena

def f() -> void:
    r: owned Arena = new_arena(64)
    s: owned Arena = move r
    destroy s
`)
	// And the source is unavailable after the move.
	used := analyzeTreeTestSourceWithSemanticErrors(t, "owned_move_used.elisa", `extern new_arena(cap: usize) -> Arena

def f() -> void:
    r: owned Arena = new_arena(64)
    s: owned Arena = move r
    destroy r
    destroy s
`)
	if all := strings.Join(used.Errors(), "\n"); !strings.Contains(all, "already been destroyed") && !strings.Contains(all, "cannot") {
		t.Fatalf("expected source region to be unavailable after move; got: %s", all)
	}
}

// A closure capturing a value allocated from an owned region cannot be used
// after the region is destroyed (no laundering of the UAF through the capture).
func TestClosureCapturingOwnedRegionDepRejectedAfterDestroy(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "owned_closure.elisa", `extern new_arena(cap: usize) -> Arena

struct Node[@owner]:
    value: i32

def f() -> i32:
    r: owned Arena = new_arena(64)
    first: Node[r]& @r = new[r] Node{value: 7}
    g: func() -> i32 = lambda () => first.value
    destroy r
    return g()
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "cannot be used") || !strings.Contains(all, "region") {
		t.Fatalf("expected closure-after-destroy on an owned region to be rejected; got: %s", all)
	}
}

// An `owned Arena` parameter on a regular function transfers ownership: the
// caller must `move` an owned region in (consuming it), and may not use it
// afterwards. Without `move` it is rejected (would copy into a second owner).
func TestOwnedParamCallTransfersOwnership(t *testing.T) {
	prelude := `extern new_arena(cap: usize) -> Arena
def sink(x: owned Arena) -> void:
    destroy x
`
	// move transfers; caller is clean.
	analyzeTreeTestSource(t, "owned_param_move.elisa", prelude+`
def f() -> void:
    r: owned Arena = new_arena(64)
    sink(move r)
`)
	// without move -> rejected.
	noMove := analyzeTreeTestSourceWithSemanticErrors(t, "owned_param_no_move.elisa", prelude+`
def f() -> void:
    r: owned Arena = new_arena(64)
    sink(r)
`)
	if all := strings.Join(noMove.Errors(), "\n"); !strings.Contains(all, "move-only") {
		t.Fatalf("expected owned param call without move to be rejected; got: %s", all)
	}
	// using the region after the move-in -> rejected (transferred).
	used := analyzeTreeTestSourceWithSemanticErrors(t, "owned_param_used.elisa", prelude+`
def f() -> void:
    r: owned Arena = new_arena(64)
    sink(move r)
    destroy r
`)
	if all := strings.Join(used.Errors(), "\n"); !strings.Contains(all, "already been destroyed") && !strings.Contains(all, "cannot") {
		t.Fatalf("expected region unavailable after move into owned param; got: %s", all)
	}
}

// No-laundering through generic instantiation: a generic function's `owned`
// parameter keeps its ownership obligation when instantiated (FuncType.
// OwnedParams must propagate through substitution).
func TestOwnedParamSurvivesGenericInstantiation(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "owned_param_generic.elisa", `extern new_arena(cap: usize) -> Arena
def sink[T](tag: T, x: owned Arena) -> void:
    destroy x

def f() -> void:
    r: owned Arena = new_arena(64)
    sink(0, r)
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "move-only") {
		t.Fatalf("expected owned-ness to survive generic instantiation (require move); got: %s", all)
	}
}

// Guardrail: a plain (non-owned) Arena value carries NO new obligation — its
// existing semantics are untouched. Ownership comes only via the `owned` marker.
func TestPlainArenaValueHasNoObligation(t *testing.T) {
	analyzeTreeTestSource(t, "plain_arena.elisa", `extern new_arena(cap: usize) -> Arena

def f() -> void:
    r: Arena = new_arena(64)
`)
}
