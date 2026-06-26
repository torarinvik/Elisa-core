package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// A container allocated in a scope-owned region must not escape via return
// (use-after-free); a container in a caller-provided region param may.
func TestRegionContainerReturnEscapeChecking(t *testing.T) {
	bad := analyzeTreeTestSourceWithSemanticErrors(t, "region_escape_bad.elisa", `def leak() -> darray[u8]:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            v: mutable darray[u8] @a = []
            v.push(65)
            return v
`)
	if all := strings.Join(bad.Errors(), "\n"); !strings.Contains(all, "escapes via return") {
		t.Fatalf("expected scope-owned region container return to be rejected; got: %s", all)
	}

	ok := analyzeTreeTestSourceWithSemanticErrors(t, "region_escape_ok.elisa", `def keep[@r](v: mutable darray[u8] @r) -> darray[u8] @r:
    return v
`)
	if all := strings.Join(ok.Errors(), "\n"); strings.Contains(all, "escapes via return") {
		t.Fatalf("returning a caller-provided region-param container must be allowed; got: %s", all)
	}
}

// A locally-constructed struct whose container fields are region-backed must be passable to a
// callee that grows a field through a struct-ref region param (`def f(b: mutable Bag& @r)`). The
// region is inferred from the local's ambient auto-region and threaded into the callee — the
// locally-constructed analogue of struct-param region threading. Before the fix this failed with
// "cannot infer region parameter" because the local struct's region never reached the call site.
func TestRegionStructLocalThreadsRegionParam(t *testing.T) {
	// Region-polymorphic caller (returns region data): the local's adopted auto-region threads in.
	poly := analyzeTreeTestSourceWithSemanticErrors(t, "region_struct_local_poly.elisa", `struct Bag:
    items: mutable darray[i64]
    extra: mutable darray[i64]

def bag_push(b: mutable Bag&, v: i64) -> void:
    b.items.push(v)

def build() -> darray[i64]:
    bag: mutable Bag = Bag([], [])
    bag_push(bag, 1)
    bag_push(bag, 2)
    return bag.items
`)
	if all := strings.Join(poly.Errors(), "\n"); strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("local struct should thread its region into the callee's region param; got: %s", all)
	}

	// Non-region-polymorphic caller (returns a scalar): the local's function-scoped auto-region
	// still threads in; nothing escapes, so it is accepted.
	nonpoly := analyzeTreeTestSourceWithSemanticErrors(t, "region_struct_local_nonpoly.elisa", `struct Bag:
    items: mutable darray[i64]

def bag_push(b: mutable Bag&, v: i64) -> void:
    b.items.push(v)

def build() -> i64:
    bag: mutable Bag = Bag([])
    bag_push(bag, 1)
    return bag.items.count.i64()
`)
	if all := strings.Join(nonpoly.Errors(), "\n"); strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("non-region-poly local struct should still thread its region; got: %s", all)
	}

	paren := analyzeTreeTestSourceWithSemanticErrors(t, "region_struct_local_paren.elisa", `struct Bag:
    items: mutable darray[i64]

def bag_push(b: mutable Bag&, v: i64) -> void:
    b.items.push(v)

def build() -> i64:
    bag: mutable Bag = (Bag([]))
    bag_push(bag, 1)
    return bag.items.count.i64()
`)
	if all := strings.Join(paren.Errors(), "\n"); strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("parenthesized local struct should still thread its region; got: %s", all)
	}
}

// A struct local initialized from a region-POLYMORPHIC builder CALL (not an inline literal) must
// also carry its ambient region for downstream threading: `bag = make_bag()` returns a fresh
// container-bearing struct allocated in the caller's ambient region, exactly like a struct literal.
func TestRegionStructLocalFromBuilderCallThreadsRegionParam(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "region_struct_local_builder.elisa", `struct Bag:
    items: mutable darray[i64]

def make_bag() -> Bag:
    return Bag([])

def bag_push(b: mutable Bag&, v: i64) -> void:
    b.items.push(v)

def build() -> darray[i64]:
    bag: mutable Bag = make_bag()
    bag_push(bag, 1)
    return bag.items
`)
	if all := strings.Join(res.Errors(), "\n"); strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("struct local from a region-poly builder call should thread its region; got: %s", all)
	}
}

// SOUNDNESS-NEGATIVE: a builder that is NOT region-polymorphic carrying ambient data must not have
// its result threaded with the caller's ambient region. Here `make_bag` takes the container by
// value and returns it inside a struct — the data lives wherever the CALLER built it, not in
// make_bag's (or the outer) ambient region, so the region-poly gate
// (exprIsRegionPolyResultCarryingRegionData) does not fire and the local is not recorded. The
// downstream `bag_push` then conservatively re-rejects with "cannot infer region parameter" rather
// than threading a wrong region (which would be a use-after-free).
func TestRegionStructLocalFromNonRegionPolyBuilderNotThreaded(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "region_struct_local_param_builder.elisa", `struct Bag:
    items: mutable darray[i64]

def wrap(xs: darray[i64]) -> Bag:
    return Bag(xs)

def bag_push(b: mutable Bag&, v: i64) -> void:
    b.items.push(v)

def build(seed: darray[i64]) -> i64:
    bag: mutable Bag = wrap(seed)
    bag_push(bag, 1)
    return bag.items.count.i64()
`)
	all := strings.Join(res.Errors(), "\n")
	// `wrap` is not region-polymorphic over an ambient region (it forwards a by-value param), so the
	// ambient-region threading path must NOT fire. The downstream push therefore re-rejects.
	if !strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("non-region-poly builder result must not be threaded with the ambient region; got: %s", all)
	}
}

// Reassigning a struct local across region scopes must NOT thread the stale declaration-time
// region: a reassignment inside a `region inner:` then a use after `inner` dies would be a
// use-after-free. The record is cleared on reassignment, so the later call conservatively
// re-rejects with "cannot infer region parameter" rather than threading a dead region.
func TestRegionStructLocalReassignClearsStaleRegion(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "region_struct_local_reassign.elisa", `struct Bag:
    items: mutable darray[i64]

def bag_push(b: mutable Bag&, v: i64) -> void:
    b.items.push(v)

def build() -> darray[i64]:
    bag: mutable Bag = Bag([])
    can Memory.Allocate, Abort.Panic:
        region inner(4096):
            bag <- Bag([])
    bag_push(bag, 1)
    return bag.items
`)
	if all := strings.Join(res.Errors(), "\n"); !strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("reassigning a struct local across regions must clear the stale region and re-reject; got: %s", all)
	}
}

// USE-SITE LIVENESS: a struct local constructed inside a `region r(N):` scope whose region has
// already EXITED at the point of a field-growing call must NOT thread the dead region. The
// declaration-time record is pinned to `r`, but `r` is no longer live at the call → threading is
// suppressed and the call conservatively re-rejects with "cannot infer region parameter" rather
// than binding a dead region (use-after-free). This is distinct from reassignment (handled
// separately): here the local is never reassigned, only used after its region's scope exits.
func TestRegionStructLocalDeadRegionNotThreaded(t *testing.T) {
	// (a) destroy the region, then use the local: dead region, must reject.
	destroyed := analyzeTreeTestSourceWithSemanticErrors(t, "region_struct_local_destroy.elisa", `struct Bag:
    items: mutable darray[i64]

def bag_push(b: mutable Bag&, v: i64) -> void:
    b.items.push(v)

def build() -> i64:
    can Memory.Allocate, Abort.Panic:
        region r(4096):
            bag: mutable Bag = Bag([])
            destroy r
            bag_push(bag, 1)
            return bag.items.count.i64()
    return 0
`)
	if all := strings.Join(destroyed.Errors(), "\n"); !strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("using a struct local after its region was destroyed must not thread the dead region; got: %s", all)
	}

	// (b) construct inside the region scope, then call AFTER the block exits: dead region, must reject.
	exited := analyzeTreeTestSourceWithSemanticErrors(t, "region_struct_local_scopeexit.elisa", `struct Bag:
    items: mutable darray[i64]

def bag_push(b: mutable Bag&, v: i64) -> void:
    b.items.push(v)

global mutable g: Bag = zeroed

def build() -> void:
    can Memory.Allocate, Abort.Panic:
        region r(4096):
            bag: mutable Bag = Bag([])
            g <- bag
    bag_push(g, 1)
`)
	if all := strings.Join(exited.Errors(), "\n"); !strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("using a struct local after its region scope exited must not thread the dead region; got: %s", all)
	}

	// (c) in-scope use within the live region still threads (happy path preserved).
	live := analyzeTreeTestSourceWithSemanticErrors(t, "region_struct_local_live.elisa", `struct Bag:
    items: mutable darray[i64]

def bag_push(b: mutable Bag&, v: i64) -> void:
    b.items.push(v)

def build() -> i64:
    can Memory.Allocate, Abort.Panic:
        region r(4096):
            bag: mutable Bag = Bag([])
            bag_push(bag, 1)
            return bag.items.count.i64()
    return 0
`)
	if all := strings.Join(live.Errors(), "\n"); strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("in-scope use within a live region must still thread; got: %s", all)
	}
}

// dict containers carry their region and are escape-checked too.
func TestRegionDictReturnEscapeChecking(t *testing.T) {
	bad := analyzeTreeTestSourceWithSemanticErrors(t, "region_dict_escape.elisa", `def leak_dict() -> dict[i64, i64]:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            d: mutable dict[i64, i64] @a = zeroed
            d.insert(1, 2)
            return d
`)
	if all := strings.Join(bad.Errors(), "\n"); !strings.Contains(all, "escapes via return") {
		t.Fatalf("expected scope-owned region dict return to be rejected; got: %s", all)
	}
}

// Storing a scope-owned-region container into storage that outlives the region
// (here a global) is rejected — same use-after-free, via store rather than return.
func TestRegionContainerStoreEscapeChecking(t *testing.T) {
	bad := analyzeTreeTestSourceWithSemanticErrors(t, "region_store_bad.elisa", `global mutable g: darray[u8] = zeroed

def stash() -> void:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            v: mutable darray[u8] @a = []
            v.push(65)
            g <- v
`)
	if all := strings.Join(bad.Errors(), "\n"); !strings.Contains(all, "escapes via store") {
		t.Fatalf("expected scope-owned region container store-into-global to be rejected; got: %s", all)
	}
}

// The design's canonical form — pushing directly into a container inside a
// `region r(...):` scope — must be allowed: the container's region r is live
// (it is the active region scope), so the grow sources r's arena. Previously the
// push check accepted only a plain `in <arena>:` owner or a region param and
// wrongly rejected this, even though codegen handled it.
func TestRegionScopeDirectPushAllowed(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "region_direct_push.elisa", `def build() -> void:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            v: mutable darray[u8] @a = []
            v.push(65)
            v.reserve(16)
`)
	if all := strings.Join(res.Errors(), "\n"); strings.Contains(all, "requires an active in <arena>: scope") {
		t.Fatalf("push/reserve into a live-region container must be allowed; got: %s", all)
	}
}

func TestRegionParamDictDirectPutAllowed(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "region_param_dict_direct_put.elisa", `def fill[@r](d: mutable dict[cstr, i64] @r) -> void:
    d.reserve(8)
    _ = d.put("alpha", 10)
    _ = d.get_or_insert("beta", 20)
`)
	if all := strings.Join(res.Errors(), "\n"); strings.Contains(all, "requires an active in <arena>: scope") {
		t.Fatalf("direct reserve/put/get_or_insert into a live-region dict must be allowed; got: %s", all)
	}
}

func TestLiveRegionDictEntryMutationAllowedInOtherOwnerScope(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "live_region_dict_entry_other_owner.elisa", `layout soa struct Pending:
    id: u32

def fill() -> void:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            d: mutable dict[cstr, i64] @a = zeroed
            pending: mutable Pending = zeroed
            in pending:
                _ = d.entry("alpha").insert(10)
                _ = d.entry("beta").get_or_insert(20)
`)
	if all := strings.Join(res.Errors(), "\n"); strings.Contains(all, "requires an active in <arena>: scope") {
		t.Fatalf("dict entry insert/get_or_insert into a live-region dict must be allowed in another owner scope; got: %s", all)
	}
}

func TestRegionScopeStoreGrowthAllowed(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "region_scope_store_growth.elisa", `layout soa struct Pending:
    name_key: u32
    depth: u32

def build() -> void:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            pending: mutable Pending = zeroed
            pending.reserve(4)
            pending.push(1, 2)
`)
	if all := strings.Join(res.Errors(), "\n"); strings.Contains(all, "requires an active in <arena>: scope") {
		t.Fatalf("store reserve/push in an active region scope must be allowed; got: %s", all)
	}
}

// A safe `darray[u8] @r -> sview/cstr @r` conversion carries the region, so the
// escape checker rejects returning it out of a scope-owned region (the bytes are
// freed at scope exit) — the safety the unsafe `out[0].ref[static u8&]` idiom
// lacked.
func TestRegionDarrayViewConversionEscapeChecking(t *testing.T) {
	bad := analyzeTreeTestSourceWithSemanticErrors(t, "region_view_escape.elisa", `def leak_sview() -> sview:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            d: mutable darray[u8] @a = []
            d.push(65)
            return d.as_sview()

def leak_cstr() -> cstr:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            d: mutable darray[u8] @a = []
            d.push(65)
            return d.as_cstr()
`)
	all := strings.Join(bad.Errors(), "\n")
	if strings.Count(all, "escapes via return") < 2 {
		t.Fatalf("expected both sview and cstr from a scope-owned region to be rejected; got: %s", all)
	}
}

// A plain `view[T]` sliced from a region-backed `darray[T] @r` now carries r
// (Phase 2: region tracking generalized from sview-only to all views), so the
// escape checker rejects returning the borrowed window out of the scope-owned
// region whose bytes are freed at scope exit.
func TestRegionDarraySliceViewEscapeChecking(t *testing.T) {
	bad := analyzeTreeTestSourceWithSemanticErrors(t, "region_slice_view_escape.elisa", `def leak_view() -> view[i64]:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            d: mutable darray[i64] @a = []
            d.push(7)
            return d[0:1]
`)
	if all := strings.Join(bad.Errors(), "\n"); !strings.Contains(all, "escapes via return") {
		t.Fatalf("expected a view sliced from a scope-owned region to be rejected as escaping; got: %s", all)
	}
}

// Nested-region escape: pushing a value from an inner (shorter-lived) region
// into a darray whose element region is an outer (longer-lived) region leaves a
// dangling reference once the inner region is freed. Rejected by the outlives
// lattice. The symmetric case (outer value into an inner-region container) is
// safe and must NOT be rejected.
func TestRegionNestedStoreEscapeChecking(t *testing.T) {
	bad := analyzeTreeTestSourceWithSemanticErrors(t, "region_nested_bad.elisa", `def nest_bad() -> void:
    can Memory.Allocate, Abort.Panic:
        region outer(4096):
            big: mutable darray[darray[u8] @outer] @outer = []
            region inner(4096):
                small: mutable darray[u8] @inner = []
                big.push(small)
`)
	if all := strings.Join(bad.Errors(), "\n"); !strings.Contains(all, "is freed first") {
		t.Fatalf("expected inner-region value pushed into outer container to be rejected; got: %s", all)
	}

	badDecl := analyzeTreeTestSourceWithSemanticErrors(t, "region_nested_decl_bad.elisa", `def nest_decl_bad() -> void:
    can Memory.Allocate, Abort.Panic:
        region outer(4096):
            region inner(4096):
                small: mutable darray[u8] @inner = []
                small.push(1)
                stash: darray[u8] @outer = small
                _ = stash
`)
	if all := strings.Join(badDecl.Errors(), "\n"); !strings.Contains(all, "is freed first") {
		t.Fatalf("expected inner-region value bound into an outer-region var to be rejected; got: %s", all)
	}

	ok := analyzeTreeTestSourceWithSemanticErrors(t, "region_nested_ok.elisa", `def nest_ok() -> void:
    can Memory.Allocate, Abort.Panic:
        region outer(4096):
            small: mutable darray[u8] @outer = []
            region inner(4096):
                big: mutable darray[darray[u8] @outer] @inner = []
                big.push(small)
`)
	if all := strings.Join(ok.Errors(), "\n"); strings.Contains(all, "is freed first") {
		t.Fatalf("outer-region value into inner-region container must be allowed; got: %s", all)
	}
}

// A darray region param `@r` must bind to the argument's region at a call and
// substitute through (mirroring RefType region unification).
func TestDArrayRegionUnificationAndSubstitution(t *testing.T) {
	a := &Analyzer{}
	i64 := &BuiltinType{Name: "i64"}
	pattern := &DArrayType{Elem: i64, Shape: &WildcardShape{}, SurfaceName: "darray", Region: "r"}
	actual := &DArrayType{Elem: i64, Shape: &WildcardShape{}, SurfaceName: "darray", Region: "a"}

	regionBindings := map[string]string{}
	a.collectTypeBindings(
		pattern, actual,
		map[string]Type{}, map[string]Shape{}, regionBindings,
		map[string][]ast.PermissionRef{}, map[string]bool{"r": true},
	)
	if regionBindings["r"] != "a" {
		t.Fatalf("region binding: want r->a, got %q", regionBindings["r"])
	}

	out := a.substituteType(pattern, map[string]Type{}, map[string]Shape{}, regionBindings, map[string][]ast.PermissionRef{})
	da, ok := out.(*DArrayType)
	if !ok || da.Region != "a" {
		t.Fatalf("substitute: want darray @a, got %#v", out)
	}

	// A non-param region name must NOT bind (only declared region params unify).
	notParam := map[string]string{}
	a.collectTypeBindings(
		pattern, actual,
		map[string]Type{}, map[string]Shape{}, notParam,
		map[string][]ast.PermissionRef{}, map[string]bool{},
	)
	if _, bound := notParam["r"]; bound {
		t.Fatalf("region %q is not a declared param; must not bind, got %q", "r", notParam["r"])
	}
}
