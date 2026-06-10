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
