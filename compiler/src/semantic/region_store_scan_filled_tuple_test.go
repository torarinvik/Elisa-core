package semantic

import "testing"

const storeScanFilledTupleSource = `struct Snap:
    slots: mutable darray[usize] = []
    names: mutable darray[sview] = []

struct Pair:
    names: darray[sview] = []

def probe(slots: darray[usize]&, names: darray[sview]&, name: sview) -> (slot: usize, found: bool):
    can Abort.Panic:
        for row in 0..<names.count |names, name|:
            if names[row] == name:
                return (row, true)
        return (0, false)

def leak(names: darray[sview]&, name: sview) -> (list: darray[sview], found: bool):
    can Abort.Panic:
        return (names, false)

def snap_merge(left: Snap, right: Snap) -> Snap:
    can Memory.Allocate, Abort.Panic:
        merged: mutable Snap = Snap{}
        for row in 0..<right.names.count |row, right, merged|:
            name: sview = right.names[row]
            slot, found = probe(merged.slots, merged.names, name)
            if not found:
                merged.names.push(name)
                merged.slots.push(slot)
        return merged

def snap_leak(left: Snap, right: Snap) -> Pair:
    can Memory.Allocate, Abort.Panic:
        merged: mutable Snap = Snap{}
        for row in 0..<right.names.count |row, right, merged|:
            merged.names.push(right.names[row])
        list, found = leak(merged.names, "")
        return Pair{names: list}
`

// A destructuring bind of scalars over a filled local (`slot, found = probe(merged.slots, ..)`)
// aliases none of it: merged stays filled and the merge returns views. A destructured value
// that does share (leak hands back the darray header) is still a binder over its storage.
func TestStoreScanFilledLocalScalarTupleBind(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_scan_filled_tuple.elisa", storeScanFilledTupleSource, AnalyzeOptions{})
	a := result.analyzer
	a.currentScope = a.globalScope
	if got := a.calleeReturnModeOf(storeScanFindFunc(t, result, "snap_merge"), 1, map[storeScanKey]bool{}); got != storeViews {
		t.Errorf("snap_merge returns mode %d, want views", got)
	}
	if got := a.calleeReturnModeOf(storeScanFindFunc(t, result, "snap_leak"), 1, map[storeScanKey]bool{}); got == storeViews || got == storeNone {
		t.Errorf("snap_leak returns mode %d, want above views", got)
	}
}
