package semantic

import (
	"strings"
	"testing"
)

const storeScanMergeShapes = `struct Snap:
    slots: mutable darray[usize] = []
    names: mutable darray[sview] = []

struct Deep:
    lists: mutable darray[darray[sview]] = []

struct Table:
    snap: mutable Snap = Snap{}
    deep: mutable Deep = Deep{}

def run(table: mutable Table&, other: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        block: Snap = Snap{slots: [1], names: [src]}
        keep_merged(table, &block)
        inner: darray[sview] = [src]
        block_deep: Deep = Deep{lists: [inner]}
        keep_merged_deep(table, &block_deep)
        block_deep2: Deep = Deep{lists: [inner]}
        keep_merged_deep2(table, &block_deep2)
        other.snap.slots.push(1)

def keep_merged(table: mutable Table&, block: Snap&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.snap <- merge(table.snap, block)

def keep_merged_deep(table: mutable Table&, block: Deep&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.deep <- merge_deep(table.deep, block)

def probe(slots: darray[usize]&, names: darray[sview]&, name: sview) -> usize:
    can Abort.Panic:
        for i in 0..<names.count |names, name|:
            return i if names[i] == name
        return slots.count

def owner_row(snap: mutable Snap&, side: mutable darray[usize]&, name: sview) -> void:
    can Memory.Allocate:
        snap.names.push(name)
        snap.slots.push(snap.names.count)
        side.push(snap.names.count)

def merge(left: Snap&, right: Snap&) -> Snap:
    can Memory.Allocate, Abort.Panic:
        merged: mutable Snap = Snap{}
        side: mutable darray[usize] = []
        for row in 0..<right.names.count |row, right, merged, side|:
            name: sview = right.names[row]
            slot: usize = probe(merged.slots, merged.names, name)
            owner_row(merged, side, name)
            merged.slots[0] <- slot if merged.slots.count > 0
        for row in 0..<left.names.count |row, left, merged, side|:
            owner_row(merged, side, left.names[row])
        return merged

def merge_deep(left: Deep&, right: Deep&) -> Deep:
    can Memory.Allocate, Abort.Panic:
        merged: mutable Deep = Deep{}
        for row in 0..<right.lists.count |row, right, merged|:
            merged.lists.push(right.lists[row])
        return merged

def push_list(deep: mutable Deep&, list: darray[sview]) -> void:
    can Memory.Allocate:
        deep.lists.push(list)

def merge_deep2(left: Deep&, right: Deep&) -> Deep:
    can Memory.Allocate, Abort.Panic:
        merged: mutable Deep = Deep{}
        for row in 0..<right.lists.count |row, right, merged|:
            push_list(merged, right.lists[row])
        return merged

def keep_merged_deep2(table: mutable Table&, block: Deep&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.deep <- merge_deep2(table.deep, block)
`

// A merge that copies a byte-free block's views into a fresh local (through helpers that only
// push views and scalars into it) stores at most views of the block: line 15 is accepted, the
// block's storage is not kept. A merge that keeps the block's inner LIST (a header, sharing its
// storage) is rejected, whether pushed directly (18) or through a callee writing into the fresh
// local (20) -- the store scan used to miss the callee's write and accept 20, a use-after-free
// once the caller's region is freed.
func TestStoreScanCalleeWritesIntoBodyLocal(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_scan_callee_writes.elisa", storeScanMergeShapes)
	errs := result.Errors()
	want := []string{":18:", ":20:"}
	if len(errs) != len(want) {
		t.Fatalf("expected lines 18, 20 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(errs[i], w) {
			t.Fatalf("expected lines 18, 20 rejected, got:\n%s", strings.Join(errs, "\n"))
		}
	}
}
