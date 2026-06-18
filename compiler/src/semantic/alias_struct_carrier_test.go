package semantic

import (
	"strings"
	"testing"
)

func structCarrierCaught(t *testing.T, name, body string) bool {
	pre := `
struct Ref:
    p: mutable i32&
def wrap(x: mutable i32&) -> Ref:
    return Ref(p: x)
def mutate_pair(a: mutable i32&, b: mutable i32&) -> void:
    pass
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, pre+body, AnalyzeOptions{EnforceUnsafePermissions: true})
	return strings.Contains(allDiagnostics(result), "mutable alias requires")
}

// A mutable struct local carrying a laundered reference (`r = wrap(v)`, r mutable) must still be
// caught when its ref field is aliased with the borrowed param — mutable locals get no value
// binding to resolve through, so a dedicated alias-carrier closes the gap.
func TestMutableStructCarrierLaunderCaught(t *testing.T) {
	if !structCarrierCaught(t, "mut_carrier.elisa", `
def run(v: mutable i32&) -> void:
    r: mutable Ref = wrap(v)
    mutate_pair(r.p, v)
`) {
		t.Fatal("expected mutable struct-carrier launder to require unsafe grant")
	}
}

// Re-binding the whole local from a return-aliasing call refreshes the carrier.
func TestStructCarrierReassignmentCaught(t *testing.T) {
	if !structCarrierCaught(t, "carrier_reassign.elisa", `
def run(v: mutable i32&, w: mutable i32&) -> void:
    r: mutable Ref = wrap(w)
    r = wrap(v)
    mutate_pair(r.p, v)
`) {
		t.Fatal("expected reassigned struct-carrier launder to require unsafe grant")
	}
}

// Re-binding to a DISJOINT parameter replaces the carrier: the stale alias must not linger.
func TestStructCarrierRebindDisjointStaysSafe(t *testing.T) {
	if structCarrierCaught(t, "carrier_rebind_disjoint.elisa", `
def run(v: mutable i32&, w: mutable i32&) -> void:
    r: mutable Ref = wrap(v)
    r = wrap(w)
    mutate_pair(r.p, v)
`) {
		t.Fatal("expected rebind to a disjoint param to drop the stale alias and stay safe")
	}
}

// Re-binding the carried reference field itself also replaces the carrier. The whole-local rebind
// path above already handled `r = wrap(w)`; this locks the field-assignment variant.
func TestStructCarrierFieldRebindDisjointStaysSafe(t *testing.T) {
	if structCarrierCaught(t, "carrier_field_rebind_disjoint.elisa", `
def run(v: mutable i32&, w: mutable i32&) -> void:
    r: mutable Ref = wrap(v)
    r.p <- w
    mutate_pair(r.p, v)
`) {
		t.Fatal("expected field rebind to a disjoint param to drop the stale alias and stay safe")
	}
}

func TestStructCarrierFieldRebindAliasCaught(t *testing.T) {
	if !structCarrierCaught(t, "carrier_field_rebind_alias.elisa", `
def run(v: mutable i32&, w: mutable i32&) -> void:
    r: mutable Ref = wrap(w)
    r.p <- v
    mutate_pair(r.p, v)
`) {
		t.Fatal("expected field rebind to an aliased param to require unsafe grant")
	}
}

func TestStructCarrierFieldRebindMaybeAliasAcrossBranchCaught(t *testing.T) {
	if !structCarrierCaught(t, "carrier_field_rebind_branch_maybe_alias.elisa", `
def run(cond: bool, v: mutable i32&, w: mutable i32&) -> void:
    r: mutable Ref = wrap(v)
    if cond:
        r.p <- w
    mutate_pair(r.p, v)
`) {
		t.Fatal("expected maybe-stale branch carrier to require unsafe grant")
	}
}

func TestStructCarrierFieldRebindDisjointAcrossBranchesStaysSafe(t *testing.T) {
	if structCarrierCaught(t, "carrier_field_rebind_branch_disjoint.elisa", `
def run(cond: bool, v: mutable i32&, w: mutable i32&) -> void:
    r: mutable Ref = wrap(v)
    if cond:
        r.p <- w
    else:
        r.p <- w
    mutate_pair(r.p, v)
`) {
		t.Fatal("expected all-branches field rebind to a disjoint param to stay safe")
	}
}
