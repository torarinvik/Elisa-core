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
	if !strings.Contains(errs, `allocates into function-scoped region "scratch"`) {
		t.Fatalf("growing a caller's container in a function-scoped region must be rejected at the allocation site, got: %s", errs)
	}
}

// An explicit nested store is shorter-lived even when the container itself is a local in an
// enclosing region. This covers both a direct darray receiver and DictEntry returned from a
// method call (whose backing region, not AST lvalue shape, determines the lifetime).
func TestNestedStoreGrowthIntoOuterRegionContainerRejected(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "nested_store_outer_container.elisa", `global mutable shared_entries: dict[cstr, i64] = zeroed

def build() -> i64:
    can Memory.Allocate, Abort.Panic:
        region outer(8192):
            items: mutable darray[i64] @outer = []
            entries: mutable dict[cstr, i64] @outer = zeroed
            region inner(4096):
                in inner:
                    items.push(1)
                    entries.entry("key").insert(2)
                    shared_entries.entry("global").insert(3)
            return items[0]

def build_regionless_local() -> i64:
    can Memory.Allocate, Abort.Panic:
        items: mutable darray[i64] = []
        region inner(4096):
            in inner:
                items.push(4)
        return items[0]
`).Errors(), "\n")
	if strings.Count(errs, `function-scoped region "inner"`) < 4 {
		t.Fatalf("growth in an inner region must not retarget outer, regionless-local, or global container backing (including DictEntry call receivers), got: %s", errs)
	}
}
