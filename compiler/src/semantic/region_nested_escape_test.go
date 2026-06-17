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

// A LIST LITERAL `[v]` whose element is an inner-region container, assigned into an
// outer-region container, copies the element's header into the longer-lived buffer —
// it dangles once the inner region is freed (runtime-confirmed segfault). The store-site
// check only sees the literal's own (outer) region; element types are never stamped, so
// the element regions must be checked at construction.
func TestNestedRegionListLiteralElementRejected(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "listlit_elem_escape.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        region outer(8192):
            outer_list: mutable darray[darray[u8]] @outer = []
            region a(4096):
                v: mutable darray[u8] @a = []
                v.push(66)
                outer_list <- [v]
            return i64(outer_list[0][0])
`)
	if all := strings.Join(res.Errors(), "\n"); !strings.Contains(all, "longer-lived region \"outer\"") {
		t.Fatalf("expected inner-region list-literal element store into outer container to be rejected; got: %s", all)
	}
}

// Same hole via a COMPREHENSION `[v for i in …]` (a distinct producer that desugars to a
// synthesized build loop, not a list literal) — also a runtime-confirmed segfault.
func TestNestedRegionComprehensionElementRejected(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "comp_elem_escape.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        region outer(8192):
            outer_list: mutable darray[darray[u8]] @outer = []
            region a(4096):
                v: mutable darray[u8] @a = []
                v.push(66)
                outer_list <- [v for i in 0..<2]
            return i64(outer_list[0][0])
`)
	if all := strings.Join(res.Errors(), "\n"); !strings.Contains(all, "longer-lived region \"outer\"") {
		t.Fatalf("expected inner-region comprehension element store into outer container to be rejected; got: %s", all)
	}
}

// Positive control: a list literal whose elements share the target's region is sound and
// must stay accepted (no false positive on the common case).
func TestNestedRegionSameRegionListLiteralAccepted(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "listlit_same_region_ok.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        region a(8192):
            outer_list: mutable darray[darray[u8]] @a = []
            v: mutable darray[u8] @a = []
            v.push(66)
            outer_list <- [v]
            return i64(outer_list[0][0])
`)
	if all := strings.Join(res.Errors(), "\n"); strings.Contains(all, "dangling") {
		t.Fatalf("same-region list-literal element store must be accepted; got: %s", all)
	}
}
