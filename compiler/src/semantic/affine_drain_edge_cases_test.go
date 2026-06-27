package semantic

import (
	"strings"
	"testing"
)

// Edge-case soundness tests for the affine container drain model.
//
// Design: a darray[T] where T contains must-consume values is itself
// must-drain. Dropping it at scope exit without a `for ... in move` drain
// would silently leak every contained handle. These tests assert:
//   - POSITIVE (unsafe): dropping a non-drained darray[Guard] is rejected.
//   - NEGATIVE (safe): a properly drained darray[Guard] is accepted.

const drainEdgePrelude = `linear struct Guard:
    active: bool
def make_guard() -> Guard:
    return Guard(true)
def release_guard(g: Guard) -> void:
    _ = move g
`

// POSITIVE: a darray[Guard] that goes out of scope without being drained must error.
// Before the fix, this compiled silently — leaking all contained Guards.
func TestAffineContainerDroppedWithoutDrainErrors(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "affine_container_drop_nodrain.elisa",
		drainEdgePrelude+`def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        gs: mutable darray[Guard] = []
        gs.push(make_guard())
`).Errors(), " | ")
	if !strings.Contains(errs, "darray of must-consume elements") {
		t.Fatalf("expected undrained darray[Guard] at scope exit to be rejected, got: %s", errs)
	}
}

// NEGATIVE: a darray[Guard] that is properly drained via `for ... in move` must compile.
func TestAffineContainerProperlyDrainedAccepted(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "affine_container_drain_ok2.elisa",
		drainEdgePrelude+`def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        gs: mutable darray[Guard] = []
        gs.push(make_guard())
        for g in move gs:
            release_guard(move g)
`).Errors(), " | ")
	if errs != "" {
		t.Fatalf("a properly drained darray[Guard] must compile, got: %s", errs)
	}
}

// POSITIVE: moving a darray[Guard] into a function and then using the original is rejected.
// The double-use check is independent of the drain model; confirm it is sound.
func TestAffineContainerMovedThenUsedErrors(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "affine_container_move_use.elisa",
		drainEdgePrelude+`def consume_all(gs: darray[Guard]) -> void:
    for g in move gs:
        release_guard(move g)
def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        gs: mutable darray[Guard] = []
        gs.push(make_guard())
        consume_all(move gs)
        gs.push(make_guard())
`).Errors(), " | ")
	if !strings.Contains(errs, "cannot be used") {
		t.Fatalf("expected use of moved darray to be rejected, got: %s", errs)
	}
}

// POSITIVE: a nested darray[darray[Guard]] dropped without draining the outer container
// must error. The outer darray holds inner darrays which themselves hold Guards.
func TestAffineNestedContainerDropErrors(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "affine_nested_container_drop.elisa",
		drainEdgePrelude+`def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        inner: mutable darray[Guard] = []
        inner.push(make_guard())
        outer: mutable darray[darray[Guard]] = []
        outer.push(move inner)
`).Errors(), " | ")
	if !strings.Contains(errs, "darray of must-consume elements") {
		t.Fatalf("expected undrained outer darray[darray[Guard]] to be rejected, got: %s", errs)
	}
}

// NEGATIVE: rebinding after a proper drain is safe — the drained container is consumed,
// a fresh binding starts a new obligation that is also properly drained.
func TestAffineContainerRebindAfterDrainAccepted(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "affine_container_rebind_drain.elisa",
		drainEdgePrelude+`def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        gs: mutable darray[Guard] = []
        gs.push(make_guard())
        for g in move gs:
            release_guard(move g)
        gs2: mutable darray[Guard] = []
        gs2.push(make_guard())
        for g in move gs2:
            release_guard(move g)
`).Errors(), " | ")
	if errs != "" {
		t.Fatalf("rebind+drain after first drain must compile, got: %s", errs)
	}
}
