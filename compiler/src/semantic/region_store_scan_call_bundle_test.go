package semantic

import (
	"strings"
	"testing"
)

// A call result passed where source and target meet (rule 225's bundle) carries from each
// argument only what its callee may return. pick returns a view of the source and nothing from
// table; relabel returns from table only a view (read-only); both bundles hold no writable target
// part, and what keep/keep_pair store is views of a byte-free immutable local: lines 15 and 16
// are accepted, before or after the callees are analyzed. wrap returns table's own darray, so
// via_wrap (line 17) stays rejected.
func TestStoreScanCallBundleUsesReturnSummary(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_scan_call_bundle.elisa", `struct Wrap:
    names: darray[sview]
    n: i64

struct Pair:
    a: sview
    b: darray[sview]

struct Table:
    names: mutable darray[sview] = []

def run(table: mutable Table&, other: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        w: Wrap = Wrap{names: [src, "lit"], n: 2}
        via_pick(&w, table)
        via_relabel(&w, table)
        via_wrap(&w, table)
        other.names.push("x")

def pick(w: Wrap&, table: Table&) -> sview:
    can Abort.Panic:
        return "" if table.names.count == 0
        return w.names[0]

def short_name(table: Table&) -> sview:
    can Abort.Panic:
        return table.names[0]

def relabel(w: Wrap&, table: Table&) -> Pair:
    can Memory.Allocate, Abort.Panic:
        name: sview = short_name(table)
        return Pair{a: name, b: w.names}

def wrap(w: Wrap&, table: Table&) -> Pair:
    can Memory.Allocate, Abort.Panic:
        return Pair{a: w.names[0], b: table.names}

def keep(v: sview, table: mutable Table&) -> void:
    can Memory.Allocate:
        table.names.push(v)

def keep_pair(p: Pair, table: mutable Table&) -> void:
    can Memory.Allocate:
        table.names.push(p.a)

def via_pick(w: Wrap&, table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        keep(pick(w, table), table)

def via_relabel(w: Wrap&, table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        keep_pair(relabel(w, table), table)

def via_wrap(w: Wrap&, table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        keep_pair(wrap(w, table), table)
`)
	errs := result.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0], ":17:") {
		t.Fatalf("expected exactly line 17 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
}
