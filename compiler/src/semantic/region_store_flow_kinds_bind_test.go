package semantic

import "testing"

// Ternary element reads and tuple binds from a call keep copied sview elements at Elements.
func TestStoreFlowEdgeKindsTernaryAndTupleBind(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_flow_kinds_bind.elisa", `struct Snap:
    slots: mutable darray[usize] = []
    names: mutable darray[sview] = []
    fields: mutable darray[sview] = []

def probe(slots: darray[usize]&, names: darray[sview]&, name: sview) -> (slot: usize, found: bool):
    return (0, false)

def k_tern(names: darray[sview]&, fields: darray[sview]&) -> Snap:
    can Memory.Allocate, Abort.Panic:
        snap: mutable Snap = Snap{}
        row: mutable usize = names.count
        while row > 0 |row, snap, fields|:
            row <- row - 1
            field: sview = fields[row] if row < fields.count else ""
            snap.fields.push(field)
        return snap

def k_tuple(names: darray[sview]&) -> Snap:
    can Memory.Allocate, Abort.Panic:
        snap: mutable Snap = Snap{}
        row: mutable usize = names.count
        while row > 0 |row, snap|:
            row <- row - 1
            name: sview = names[row]
            slot, found = probe(snap.slots, snap.names, name)
            if not found:
                snap.names.push(name)
                snap.slots[slot] <- 1
        return snap

def k_plain(names: darray[sview]&) -> Snap:
    can Memory.Allocate, Abort.Panic:
        snap: mutable Snap = Snap{}
        row: mutable usize = names.count
        while row > 0 |row, snap|:
            row <- row - 1
            name: sview = names[row]
            snap.names.push(name)
        return snap
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("errors %v", errs)
	}
	a := result.analyzer
	a.currentScope = a.globalScope
	for _, n := range []string{"k_tern", "k_tuple", "k_plain"} {
		sum := a.storeFlowKindsFor(storeScanFindFunc(t, result, n))
		last := len(sum.retKind) - 1
		if got := sum.retKindOf(last); got != storeFlowEdgeElements {
			t.Errorf("%s: result edge from its last parameter is %v, want elements (copied sview elements)", n, got)
		}
	}
}
