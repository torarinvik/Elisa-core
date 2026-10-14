package semantic

import (
	"strings"
	"testing"
)

// The store scan judges a callee before or after its body is analyzed (here every callee follows
// its caller, so declarations are all it has). An integer local computed from the source
// (`index: int = last_index(bound, ..)`), a counted-loop index over it, and a builtin push's
// result (`t.f <- t.f.push(v)`, judged by what it appends) carry no reference to the source's
// storage: lines 14-16 are accepted. Pushing the source itself (17) and a name declared both as
// an integer and as the source's copy (18) stay header stores.
func TestStoreScanScalarLocalsAndRangeBinders(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_scan_scalar_binders.elisa", `struct Move:
    id: u32
    pos: i64

struct Table:
    moves: mutable darray[Move] = []
    names: mutable darray[sview] = []
    lists: mutable darray[darray[sview]] = []

def run(table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        bound: mutable darray[sview] = ["x"]
        ids: mutable darray[u32] = [7]
        record(bound, ids, table)
        keep_one(bound, table)
        keep_looped(bound, table)
        keep_whole(bound, table)
        keep_shadowed(bound, table, true)

def last_index(bound: darray[sview]&, name: sview) -> int:
    can Abort.Panic:
        for i in 0..<bound.count |bound|:
            return i.int() if bound[i] == name
        return -1

def record(bound: darray[sview]&, ids: darray[u32]&, table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        index: int = last_index(bound, "x")
        if index >= 0:
            table.moves <- table.moves.push(Move{id: ids[index.usize()], pos: 1})

def keep_one(bound: darray[sview]&, table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        index: int = last_index(bound, "x")
        if index >= 0:
            table.names <- table.names.push(bound[index.usize()])

def keep_looped(src: darray[sview]&, table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        for i in 0..<src.count |table, src|:
            table.names <- table.names.push(src[i])

def keep_whole(bound: darray[sview], table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        index: int = last_index(bound, "x")
        table.lists <- table.lists.push(bound) if index >= 0

def keep_shadowed(bound: darray[sview], table: mutable Table&, flag: bool) -> void:
    can Memory.Allocate, Abort.Panic:
        if flag:
            pick: int = 0
            table.names <- table.names.push(bound[pick.usize()])
        else:
            pick: darray[sview] = bound
            table.lists <- table.lists.push(pick)
`)
	errs := result.Errors()
	if len(errs) != 2 || !strings.Contains(errs[0], ":17:") || !strings.Contains(errs[1], ":18:") {
		t.Fatalf("expected exactly lines 17 and 18 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
}
