package semantic

import (
	"strings"
	"testing"
)

const affineDrainPrelude = `linear struct Guard:
    active: bool
def make() -> Guard:
    return Guard{active: true}
def release(g: Guard) -> void:
    _ = move g
`

// The sanctioned way to empty a container of must-consume (affine) handles: a move-drain
// `for g in move gs:` moves each element out (binding by value) and the body consumes it.
// The container's aggregate obligation is discharged once drained. This must compile clean.
func TestAffineMoveDrainConsumesEachElement(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "affine_drain_ok.elisa",
		affineDrainPrelude+`def drain() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        gs: mutable darray[Guard] = []
        gs.push(make())
        gs.push(make())
        for g in move gs:
            release(move g)
`).Errors(), " | ")
	if errs != "" {
		t.Fatalf("a move-drain that consumes every element must compile, got: %s", errs)
	}
}

// A move-drain whose body does NOT consume the moved element leaks it — must error.
func TestAffineMoveDrainUnconsumedElementErrors(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "affine_drain_leak.elisa",
		affineDrainPrelude+`def drain() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        gs: mutable darray[Guard] = []
        gs.push(make())
        for g in move gs:
            _ = g.active
`).Errors(), " | ")
	if !strings.Contains(errs, "must be consumed in each iteration") {
		t.Fatalf("expected unconsumed moved element to error, got: %s", errs)
	}
}

// `break`/`return` inside a move-drain would skip the remaining elements, leaking them.
func TestAffineMoveDrainRejectsEarlyExit(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "affine_drain_break.elisa",
		affineDrainPrelude+`def drain() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        gs: mutable darray[Guard] = []
        gs.push(make())
        for g in move gs:
            release(move g)
            break
`).Errors(), " | ")
	if !strings.Contains(errs, "must consume every element") {
		t.Fatalf("expected break in a move-drain to be rejected, got: %s", errs)
	}
}

// Non-consuming removal silently drops must-consume elements: clear / truncate / resize /
// indexed overwrite on an affine-element darray must all be rejected.
func TestAffineContainerNonConsumingDropsRejected(t *testing.T) {
	cases := []struct {
		name string
		op   string
		want string
	}{
		{"clear", "gs.clear()", "darray clear would drop must-consume"},
		{"truncate", "_ = gs.truncate(0.usize())", "darray truncate would drop must-consume"},
		{"resize", "_ = gs.resize(0.usize())", "darray resize would drop must-consume"},
		{"index_overwrite", "gs[0] <- make()", "indexed assignment would drop must-consume"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "affine_drop_"+tc.name+".elisa",
				affineDrainPrelude+`def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        gs: mutable darray[Guard] = []
        gs.push(make())
        `+tc.op+`
        for g in move gs:
            release(move g)
`).Errors(), " | ")
			if !strings.Contains(errs, tc.want) {
				t.Fatalf("expected %s to be rejected with %q, got: %s", tc.op, tc.want, errs)
			}
		})
	}
}

// A move-drain over a NON-container source (a range, a borrowed view) is rejected: there is no
// owned container to consume.
func TestAffineMoveDrainRequiresOwnedContainer(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "affine_drain_view.elisa",
		`def f(v: view[i64]) -> void:
    total: mutable i64 = 0
    for x in move v:
        total <- total + x
`).Errors(), " | ")
	if !strings.Contains(errs, "requires a darray source") {
		t.Fatalf("expected move-drain over a view to be rejected, got: %s", errs)
	}
}

// Draining a BORROWED container (`darray&` param) would consume the caller's elements behind
// its back — must be rejected; the owner must move it into a local first.
func TestAffineMoveDrainRejectsBorrowedContainer(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "affine_drain_borrow.elisa",
		affineDrainPrelude+`def drain(gs: mutable darray[Guard]&) -> void:
    for g in move gs:
        release(move g)
`).Errors(), " | ")
	if !strings.Contains(errs, "cannot `for ... in move` a borrowed container") {
		t.Fatalf("expected move-drain of a borrowed darray to be rejected, got: %s", errs)
	}
}
