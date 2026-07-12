//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// struct_invariant_reseed_test.go: the bounded-linear precondition prover consults a struct's field
// invariants when bounding a field-read argument (proveRequiresComparison's field-invariant range
// tier). A field read carries no affine form, so before this the prover could only discharge
// `f(c.panel_idx)` against `requires slot >= 0` via a flow fact seeded at method entry — which any
// intervening mutation or mutable-ref call dropped, forcing a runtime check. The invariant
// `invariant self.panel_idx >= 0` is a standing contract (established at construction, re-checked after
// each field store), so the prover may assume it at every read — statelessly, regardless of what came
// before the call. This mirrors the refinement path (structInvariantEntailsFieldRefinement).

const reseedInvariantPrelude = `
struct Box layout(c):
    idx: mutable i32
    tick: mutable i32
    invariant self.idx >= 0

def needs_nonneg(s: i32) -> void:
    requires s >= 0
    return

def touch(b: mutable Box&) -> void:
    b.tick <- b.tick + 1
`

// TestInvariantSurvivesSiblingFieldStore: writing an unrelated field (`b.tick`) must not stop the
// invariant `b.idx >= 0` from discharging `requires s >= 0` at `needs_nonneg(b.idx)`.
func TestInvariantSurvivesSiblingFieldStore(t *testing.T) {
	src := reseedInvariantPrelude + `
def use_box(b: mutable Box&) -> void:
    b.tick <- b.tick + 1
    needs_nonneg(b.idx)
`
	result := analyzeTreeTestSource(t, "inv_sibling_store.elisa", src)
	if hasRuntimeCheck(result) {
		t.Fatalf("sibling store `b.tick <- ...` must not stop `b.idx >= 0` from the invariant discharging:\n%s", allDiagnostics(result))
	}
}

// TestInvariantSurvivesSelfFieldStore: writing the constrained field itself (`b.idx <- b.idx + 1`)
// still leaves the invariant holding (re-checked at runtime after the store), so the precondition
// discharges statelessly.
func TestInvariantSurvivesSelfFieldStore(t *testing.T) {
	src := reseedInvariantPrelude + `
def use_box(b: mutable Box&) -> void:
    b.idx <- b.idx + 1
    needs_nonneg(b.idx)
`
	result := analyzeTreeTestSource(t, "inv_self_store.elisa", src)
	if hasRuntimeCheck(result) {
		t.Fatalf("store to the constrained field is invariant-rechecked; `b.idx >= 0` should discharge:\n%s", allDiagnostics(result))
	}
}

// TestInvariantSurvivesMutableRefCall: this is the real comic_capture_rgb shape — a mutable-ref CALL
// (`touch(b)` mutates the struct) between establishing the invariant and the guarded call. The call
// drops every flow fact about `b`, but the stateless invariant tier still bounds `b.idx`.
func TestInvariantSurvivesMutableRefCall(t *testing.T) {
	src := reseedInvariantPrelude + `
def use_box(b: mutable Box&) -> void:
    touch(b)
    needs_nonneg(b.idx)
`
	result := analyzeTreeTestSource(t, "inv_mutref_call.elisa", src)
	if hasRuntimeCheck(result) {
		t.Fatalf("a mutable-ref call `touch(b)` drops flow facts, but the struct invariant still bounds `b.idx`:\n%s", allDiagnostics(result))
	}
}

// TestInvariantTierRequiresAnInvariant: the tier must not fabricate a bound — a struct WITHOUT the
// relevant invariant leaves the precondition unproven (falls back to a runtime check).
func TestInvariantTierRequiresAnInvariant(t *testing.T) {
	src := `
struct Box layout(c):
    idx: mutable i32
    tick: mutable i32

def needs_nonneg(s: i32) -> void:
    requires s >= 0
    return

def use_box(b: mutable Box&) -> void:
    b.tick <- b.tick + 1
    needs_nonneg(b.idx)
`
	result := analyzeTreeTestSource(t, "inv_absent.elisa", src)
	if noRuntimeCheck(result) {
		t.Fatalf("with no `invariant self.idx >= 0`, the tier must not fabricate `b.idx >= 0`; expected a runtime-check fallback:\n%s", allDiagnostics(result))
	}
}

// TestInvariantTierRefutesOutOfRange: the range tier works in both directions — a field pinned to a
// non-negative invariant provably violates `requires s < 0`, a hard refutation.
func TestInvariantTierRefutesOutOfRange(t *testing.T) {
	src := `
struct Box layout(c):
    idx: mutable i32
    invariant self.idx >= 0

def needs_neg(s: i32) -> void:
    requires s < 0
    return

def use_box(b: mutable Box&) -> void:
    needs_neg(b.idx)
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "inv_refute.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "is violated") {
		t.Fatalf("an invariant `idx >= 0` should refute `requires s < 0`; expected a violation error, got:\n%s", errs)
	}
}
