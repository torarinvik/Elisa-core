package semantic

import "testing"

// A key finished on one path but left provisional (its search met the open frame of top) is no
// cycle of a sibling path: b_side's call to a_side must see a_side's header store of its list.
// When the search kept a VISITED set, the second visit read a_side as "stores nothing", and
// top's verdict depended on which call the scan met first (here: elements, a missed header
// store of names).
const storeScanFramesSource = `struct Table:
    lists: mutable darray[darray[sview]] = []

def top(table: mutable Table&, names: darray[sview], n: i64) -> void:
    can Memory.Allocate, Abort.Panic:
        tmp: mutable darray[sview] = []
        if names.count > 0:
            tmp.push(names[0])
        a_side(table, tmp, n)
        b_side(table, names, n)

def a_side(table: mutable Table&, xs: darray[sview], n: i64) -> void:
    can Memory.Allocate, Abort.Panic:
        if n > 0:
            top(table, xs, n - 1)
        table.lists.push(xs)

def b_side(table: mutable Table&, ys: darray[sview], n: i64) -> void:
    can Memory.Allocate, Abort.Panic:
        a_side(table, ys, n)
`

func TestStoreScanSiblingRevisitIsNotACycle(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_scan_frames.elisa", storeScanFramesSource, AnalyzeOptions{})
	a := result.analyzer
	a.currentScope = a.globalScope
	a.storeScanCache = nil
	if got := a.calleeStoreModeInto(storeScanFindFunc(t, result, "top"), 1, 0); got != storeHeader {
		t.Fatalf("top stores names into table through b_side -> a_side: mode %d, want header", got)
	}
	// Order independence: the same verdict whichever key is asked first.
	for _, first := range []string{"a_side", "b_side"} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_scan_frames.elisa", storeScanFramesSource, AnalyzeOptions{})
		a := result.analyzer
		a.currentScope = a.globalScope
		a.storeScanCache = nil
		a.calleeStoreModeInto(storeScanFindFunc(t, result, first), 1, 0)
		if got := a.calleeStoreModeInto(storeScanFindFunc(t, result, "top"), 1, 0); got != storeHeader {
			t.Fatalf("after %s first: top mode %d, want header", first, got)
		}
	}
}
