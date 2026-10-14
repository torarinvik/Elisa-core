package main

import "testing"

// A merge that rebuilds a byte-free block into a fresh local through helpers that push only
// views and scalars (TestStoreScanCalleeWritesIntoBodyLocal, line 15) is accepted: the result
// stored into the table holds views of the caller's text and its own fresh storage, so it stays
// valid after run's region is freed and the arena is churned.
func TestStoreScanFilledMergeStoresRun(t *testing.T) {
	src := `
struct Snap:
    slots: mutable darray[usize] = []
    names: mutable darray[sview] = []

struct Table:
    snap: mutable Snap = Snap{}

def run(table: mutable Table&, other: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        block: Snap = Snap{slots: [1], names: [src]}
        keep_merged(table, &block)
        other.snap.slots.push(1)

def keep_merged(table: mutable Table&, block: Snap&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.snap <- merge(table.snap, block)

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

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[i64] = []
        for i in 0..<4000 |junk|:
            junk.push(i.i64() * 7)
        return junk[3999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        owner: mutable dstr = []
        for i in 0..<5 |owner|:
            owner.push(72 + i.u8())
        table: mutable Table = Table{}
        other: mutable Table = Table{}
        run(&table, &other, owner.as_sview())
        run(&table, &other, owner.as_sview())
        c: i64 = churn()
        if table.snap.names.count != 2 or table.snap.slots.count != 2 or table.snap.names[1].len != 5 or table.snap.names[1][4] != 76 or c != 27993:
            panic("merged snapshot lost")
`
	buildAndRunSoATest(t, "store_scan_filled_merge", src, "bt")
}
