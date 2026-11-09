package semantic

import (
	"strings"
	"testing"
)

// Pushing an inner-region container into an outer-region container leaves the
// outer buffer holding a dangling header once the inner region is freed. The
// element type is never region-stamped, so the check must fall back to the
// container's own region.
func TestNestedRegionPushIntoOuterContainerRejected(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "nested_push_escape.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        region outer(8192):
            outer_list: mutable darray[darray[u8]] @outer = []
            region a(4096):
                v: mutable darray[u8] @a = []
                v.push(66)
                outer_list.push(v)
            return i64(outer_list[0][0])
`)
	if all := strings.Join(res.Errors(), "\n"); !strings.Contains(all, "longer-lived region \"outer\"") {
		t.Fatalf("expected inner-region push into outer-region darray to be rejected; got: %s", all)
	}
}

// dict.put of an inner-region value into an outer-region dict is the same
// dangling-element store via the dict mutation path.
func TestNestedRegionDictPutIntoOuterDictRejected(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "nested_dict_put_escape.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        region outer(8192):
            d: mutable dict[i64, darray[u8]] @outer = zeroed
            region a(4096):
                v: mutable darray[u8] @a = []
                v.push(68)
                d.put(1, v)
            return 0
`)
	if all := strings.Join(res.Errors(), "\n"); !strings.Contains(all, "longer-lived region \"outer\"") {
		t.Fatalf("expected inner-region dict.put into outer-region dict to be rejected; got: %s", all)
	}
}

// A view assigned into a region-less binding declared BEFORE the region block
// outlives the region (the binding's scope strictly encloses the region's
// declaration scope), so reading it after the block dangles.
func TestNestedRegionViewIntoOuterLocalRejected(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "view_outer_local_escape.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        w: mutable view[u8] = zeroed
        region a(4096):
            v: mutable darray[u8] @a = []
            v.push(67)
            w <- v[0:1]
        return i64(w[0])
`)
	if all := strings.Join(res.Errors(), "\n"); !strings.Contains(all, "outlives the region") {
		t.Fatalf("expected view store into region-outliving local to be rejected; got: %s", all)
	}
}

// The tuple's fields can carry a borrowed view even though the tuple itself has no
// region qualifier. A postfix `can` wrapper must also preserve the expression provenance.
func TestNestedRegionTupleViewIntoOuterLocalRejected(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "tuple_view_outer_local_escape.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        outer: mutable (known: bool, value: sview) = (false, "")
        region inner(64):
            bytes: mutable darray[u8] @inner = []
            bytes.push(65)
            view: sview @inner = bytes.as_sview()
            outer <- (true, view) can Memory.Allocate
        return i64(outer.value[0])
`)
	if all := strings.Join(res.Errors(), "\n"); !strings.Contains(all, "outlives the region") {
		t.Fatalf("expected tuple view stored into an outer local to be rejected; got: %s", all)
	}
}

// Same hole via a struct field: the struct local is region-less and declared
// outside the region block, so its field store dangles after the block.
func TestNestedRegionStructFieldIntoOuterLocalRejected(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "struct_field_outer_escape.elisa", `struct Holder:
    xs: mutable darray[u8]

def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        h: mutable Holder = zeroed
        region a(4096):
            v: mutable darray[u8] @a = []
            v.push(65)
            h.xs <- v
        return i64(h.xs[0])
`)
	if all := strings.Join(res.Errors(), "\n"); !strings.Contains(all, "outlives the region") {
		t.Fatalf("expected struct-field store into region-outliving local to be rejected; got: %s", all)
	}
}

// Bulk extend copies an inner-region source container's elements into an
// outer-region target; the copied nested-container element headers dangle once
// the source region is freed. The element type carries region storage, so the
// source container's own region is the proxy for the elements' lifetime.
func TestNestedRegionBulkExtendRejected(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "nested_extend_escape.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        region outer(8192):
            outer_list: mutable darray[darray[u8]] @outer = []
            region a(4096):
                v: mutable darray[u8] @a = []
                v.push(66)
                src: mutable darray[darray[u8]] @a = []
                src.push(v)
                outer_list.extend(src)
            return i64(outer_list[0][0])
`)
	if all := strings.Join(res.Errors(), "\n"); !strings.Contains(all, "dangling") {
		t.Fatalf("expected bulk extend of inner-region elements into outer darray to be rejected; got: %s", all)
	}
}

// Bulk push (push of a whole darray/view/array source) is the same hole as extend.
func TestNestedRegionBulkPushRejected(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "nested_bulkpush_escape.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        region outer(8192):
            outer_list: mutable darray[darray[u8]] @outer = []
            region a(4096):
                v: mutable darray[u8] @a = []
                v.push(66)
                src: mutable darray[darray[u8]] @a = []
                src.push(v)
                outer_list.push(src)
            return i64(outer_list[0][0])
`)
	if all := strings.Join(res.Errors(), "\n"); !strings.Contains(all, "dangling") {
		t.Fatalf("expected bulk push of inner-region elements into outer darray to be rejected; got: %s", all)
	}
}

// Scalar (pointer-free) elements can never dangle, so a bulk extend of an inner
// region's u8 elements into an outer u8 darray must stay accepted.
func TestNestedRegionScalarExtendAccepted(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "nested_scalar_extend_ok.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        region outer(8192):
            outer_list: mutable darray[u8] @outer = []
            region a(4096):
                src: mutable darray[u8] @a = []
                src.push(66)
                outer_list.extend(src)
            return i64(outer_list[0])
`)
	if all := strings.Join(res.Errors(), "\n"); strings.Contains(all, "dangling") {
		t.Fatalf("scalar-element extend cannot dangle and must be accepted; got: %s", all)
	}
}

// Copying a region-less struct whose interior darray field was filled from an
// inner region into an outer-living local launders the dangling field past the
// region's death; the struct type carries no region, so the interior-taint
// side-table supplies it. Propagation must also catch a copy chain.
func TestStructCopyInteriorFieldEscapeRejected(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "struct_copy_interior_escape.elisa", `struct Holder:
    xs: mutable darray[u8]

def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        out: mutable Holder = zeroed
        region a(4096):
            v: mutable darray[u8] @a = []
            v.push(65)
            inner: mutable Holder = zeroed
            inner.xs <- v
            mid: mutable Holder = inner
            out <- mid
        return i64(out.xs[0])
`)
	if all := strings.Join(res.Errors(), "\n"); !strings.Contains(all, "outlives the region") {
		t.Fatalf("expected struct copy laundering an inner-region interior field to be rejected; got: %s", all)
	}
}

// A branch-only whole-aggregate assignment must preserve the interior region of a
// fresh struct literal. Otherwise the returned struct's nested darray header loses
// its only provenance marker at the branch join and silently points into a freed
// region.
func TestBranchAssignedFreshAggregateInteriorEscapeRejected(t *testing.T) {
	bad := analyzeTreeTestSourceWithSemanticErrors(t, "branch_assigned_fresh_aggregate_escape.elisa", `struct BranchBox:
    values: darray[i64]

def leak_branch_tainted_box(copy_value: bool) -> BranchBox can[Memory.Allocate, Abort.Panic]:
    region scratch(4096):
        values: mutable darray[i64] = []
        values.push(41)
        box: mutable BranchBox = BranchBox{values: []}
        if copy_value:
            box <- BranchBox{values: values}
        return box
`)
	if all := strings.Join(bad.Errors(), "\n"); !strings.Contains(all, `backed by scope-owned region "scratch" escapes via return`) {
		t.Fatalf("expected branch-assigned struct literal to retain its interior region taint; got: %s", all)
	}

	good := analyzeTreeTestSourceWithSemanticErrors(t, "unchanged_fresh_aggregate_return.elisa", `struct BranchBox:
    values: darray[i64]

def return_unchanged_box() -> BranchBox can[Memory.Allocate, Abort.Panic]:
    region scratch(4096):
        values: mutable darray[i64] = []
        values.push(41)
        box: mutable BranchBox = BranchBox{values: []}
        return box
`)
	if all := strings.Join(good.Errors(), "\n"); all != "" {
		t.Fatalf("empty fresh aggregate has no interior region handle and must remain accepted; got: %s", all)
	}
}

// SOUNDNESS (region-poly taint hole): a region-polymorphic call result is region-less by type but
// lives in the AMBIENT region. Storing it into an interior field of a region-less struct built inside
// `region a:`, then copying the struct out past the region's death, launders the dangling interior.
// valueStoreRegion previously returned "" for a call result, so the taint table never recorded the
// region — silently accepted. Recovering the ambient region must now reject it.
func TestStructCopyInteriorRegionPolyResultEscapeRejected(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "struct_copy_interior_rp_escape.elisa", `struct Holder:
    xs: mutable darray[u8]

def make_xs() -> darray[u8]:
    out: mutable darray[u8] = []
    out.push(65)
    return out

def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        out: mutable Holder = zeroed
        region a(4096):
            inner: mutable Holder = zeroed
            inner.xs <- make_xs()
            out <- inner
        return i64(out.xs[0])
`)
	if all := strings.Join(res.Errors(), "\n"); !strings.Contains(all, "outlives the region") {
		t.Fatalf("expected struct copy laundering a region-poly-result interior field to be rejected; got: %s", all)
	}
}

// Positive: the same pattern where the struct never leaves the region (the interior dies with the
// region, no laundering) must still compile — the taint only drives rejection on actual escape.
func TestStructInteriorRegionPolyResultSameRegionAccepted(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "struct_interior_rp_same_region_ok.elisa", `struct Holder:
    xs: mutable darray[u8]

def make_xs() -> darray[u8]:
    out: mutable darray[u8] = []
    out.push(65)
    return out

def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            inner: mutable Holder = zeroed
            inner.xs <- make_xs()
            return i64(inner.xs[0])
`)
	if all := strings.Join(res.Errors(), "\n"); strings.Contains(all, "dangling") || strings.Contains(all, "outlives the region") {
		t.Fatalf("same-region use of a region-poly-result interior field must be accepted; got: %s", all)
	}
}

// A struct whose interior field points at an OUTER region copied out is sound and
// must stay accepted — the taint must record the field's true (outer) region.
func TestStructCopyOuterInteriorFieldAccepted(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "struct_copy_outer_field_ok.elisa", `struct Holder:
    xs: mutable darray[u8]

def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        region outer(8192):
            ov: mutable darray[u8] @outer = []
            ov.push(70)
            out: mutable Holder = zeroed
            region a(4096):
                inner: mutable Holder = zeroed
                inner.xs <- ov
                out <- inner
            return i64(out.xs[0])
`)
	if all := strings.Join(res.Errors(), "\n"); strings.Contains(all, "dangling") {
		t.Fatalf("struct copy of an outer-region interior field must be accepted; got: %s", all)
	}
}

// Positive controls: same-region nesting and region-less bindings declared
// INSIDE the region block die with the block — no escape, must stay accepted.
func TestNestedRegionSameRegionAndInsideBlockAccepted(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "nested_region_ok.elisa", `struct Holder:
    xs: mutable darray[u8]

def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        region a(8192):
            outer_list: mutable darray[darray[u8]] @a = []
            v: mutable darray[u8] @a = []
            v.push(66)
            outer_list.push(v)
            h: mutable Holder = zeroed
            h.xs <- v
            w: view[u8] = v[0:1]
            return i64(w[0]) + i64(h.xs[0])
`)
	if all := strings.Join(res.Errors(), "\n"); strings.Contains(all, "dangling") {
		t.Fatalf("same-region nesting / inside-block bindings must be accepted; got: %s", all)
	}
}

// DISJOINT SIBLING regions: a value produced in region `b` is stored into a
// container that lives in region `a`, where `a` and `b` are sequential sibling
// blocks (neither nested in the other). `a` is already freed by the time `b`
// runs, so `outer_a`'s buffer is dangling storage. The old monotonic-ordinal
// model ordered the siblings by declaration order (a before b) and SILENTLY
// ACCEPTED this. Siblings are now incomparable, so the cross-store must not be
// proved safe. (We assert it is not silently accepted; an error here is fine —
// incomparable conservatively declines.)
func TestDisjointSiblingRegionCrossStoreNotAccepted(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "sibling_cross_store.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        outer_a: mutable darray[darray[u8]] @a = []
        region a(4096):
            w: mutable darray[u8] @a = []
            w.push(1)
            outer_a.push(w)
        region b(4096):
            v: mutable darray[u8] @b = []
            v.push(66)
            outer_a.push(v)
        return 0
`)
	all := strings.Join(res.Errors(), "\n")
	// The previously-unsound acceptance is the failure mode: storing a b-region
	// value into the a-region container (disjoint sibling) must no longer be
	// proved safe by the old declaration-order ordinal ordering. It must be
	// rejected as a dangling store.
	if !strings.Contains(all, "freed first") {
		t.Fatalf("expected disjoint-sibling cross-store to be rejected as a dangling reference; got: %s", all)
	}
}

// Direct lattice unit check: disjoint sibling regions must be incomparable, while
// a true ancestor relationship must still order correctly.
func TestRegionOutlivesSiblingsIncomparable(t *testing.T) {
	a := &Analyzer{}
	a.regionLifetimeOrdinals = map[string]int{}
	a.regionLifetimeAncestors = map[string][]string{}
	// Simulate: region outer { region inner } ; then sibling sib (outer closed).
	a.regionLifetimeOrdinals["outer"] = 1
	a.regionLifetimeAncestors["outer"] = nil
	a.regionLifetimeOrdinals["inner"] = 2
	a.regionLifetimeAncestors["inner"] = []string{"outer"}
	a.regionLifetimeOrdinals["sib"] = 3
	a.regionLifetimeAncestors["sib"] = nil // outer already closed → no ancestor

	if !a.regionOutlives("outer", "inner") {
		t.Fatalf("true nesting: outer must outlive inner")
	}
	if a.regionOutlives("inner", "outer") {
		t.Fatalf("inner must not outlive its ancestor outer")
	}
	if a.regionOutlives("outer", "sib") {
		t.Fatalf("disjoint siblings outer/sib must be incomparable (outer must not be proved to outlive sib)")
	}
	if a.regionOutlives("sib", "outer") {
		t.Fatalf("disjoint siblings sib/outer must be incomparable")
	}
	if a.regionOutlives("inner", "sib") || a.regionOutlives("sib", "inner") {
		t.Fatalf("disjoint regions inner/sib must be incomparable")
	}
}
