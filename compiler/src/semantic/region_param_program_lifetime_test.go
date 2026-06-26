package semantic

import (
	"strings"
	"testing"
)

// docs/75 S2 + S4: a callee that grows a caller-owned container param INSIDE a program-lifetime
// `in perm:` (or `in &<global>:`) scope needs NO threaded caller region — the backing outlives
// every caller — so passing a struct-FIELD container to it must compile (was: "cannot infer region
// parameter __rg_out"). The complement is the soundness gate: growing into a SCOPED arena that
// frees at function return must STILL be rejected (a dangling write into the caller's container).

// Growth inside `in perm:`, called with a struct-field container, compiles with no region threading.
func TestProgramLifetimePermGrowthStructFieldAccepted(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "perm_field.elisa",
		`struct Holder:
    items: mutable darray[i64]
def grow(out: mutable darray[i64]&, v: i64) -> void:
    in perm:
        out.push(v)
def use(h: mutable Holder&) -> void:
    grow(&h.items, 7)
`).Errors(), " | ")
	if errs != "" {
		t.Fatalf("growth into perm called with a struct-field container must compile, got: %s", errs)
	}
}

// Growth inside `in &<global arena>:` is equally program-lifetime — also accepted.
func TestProgramLifetimeGlobalArenaGrowthStructFieldAccepted(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "global_field.elisa",
		`global mutable g_arena: Arena = zeroed
struct Holder:
    items: mutable darray[i64]
def grow(out: mutable darray[i64]&, v: i64) -> void:
    in g_arena:
        out.push(v)
def use(h: mutable Holder&) -> void:
    grow(&h.items, 7)
`).Errors(), " | ")
	if errs != "" {
		t.Fatalf("growth into a global arena called with a struct-field container must compile, got: %s", errs)
	}
}

// SOUNDNESS GATE: growth into a SCOPED arena (freed at function return) must STILL be rejected —
// the program-lifetime skip must not leak to scoped regions, or the caller's container would dangle.
func TestScopedArenaGrowthStructFieldStillRejected(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "scoped_field.elisa",
		`struct Holder:
    items: mutable darray[i64]
def grow(out: mutable darray[i64]&, v: i64) -> void:
    region scratch(1024):
        in scratch:
            out.push(v)
def use(h: mutable Holder&) -> void:
    grow(&h.items, 7)
`).Errors(), " | ")
	if errs == "" {
		t.Fatalf("growing a caller's container into a function-scoped arena is a dangling write and must still be rejected")
	}
}
