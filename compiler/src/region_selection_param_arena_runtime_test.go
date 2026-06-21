package main

import "testing"

// Region SELECTION for packed-enum store columns through a param-threaded arena.
//
// Regression for the bug where an inner explicit "in a:" arena scope was IGNORED for store
// placement when the enclosing function threads the packed store as an implicit parameter: the
// store (and its columns) was created on a compiler-synthesized __auto_ region in the CALLER
// instead of on a. Root cause: funcOwnsRegion only recognized "in auto:" (RegionStmt) as region
// ownership, missing the "in <arena>:" form (an InStoreStmt over an Arena), so a builder like
// build_into(a: mutable Arena&) was judged region-LESS and the caller hoisted+threaded the store.
//
// Contrast: the LOCAL-arena form (arena declared and scoped in the SAME function) already worked and
// is covered by TestSoAArenaBackedColumnsLiveOnArena. This test pins the PARAM form: after
// build_into(&a) returns, arena_used_bytes(&a) must be non-trivial — i.e. the columns landed on a.
func TestRegionSelectionParamArenaColumnsLiveOnArena(t *testing.T) {
	src := soaPackedEnumDecl + `
def make(depth: i64) -> Expr:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return new[auto] Expr.Int(span: 0, value: 1)
        return new[auto] Expr.Add(span: 0, left: make(depth - 1), right: make(depth - 1))

def eval(node: Expr) -> i64:
    match node:
        Expr.Int(value: v):
            return v
        Expr.Add(left: l, right: r):
            return eval(l) + eval(r)

def build_into(a: mutable Arena&) -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        sum: mutable i64 = 0
        in a:
            root: Expr = make(12)
            sum <- eval(root)
        return sum

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: mutable Arena = zeroed
        before: usize = arena_used_bytes(&a)
        if before != 0:
            panic("fresh arena was not empty")
        s: i64 = build_into(&a)
        after: usize = arena_used_bytes(&a)
        if s != 4096:
            panic("param-threaded arena build produced wrong sum")
        # With the region-selection bug the store is hoisted onto a synthesized __auto_ region in the
        # caller, leaving the param arena empty (after == 0). A depth-12 tree packs well over 8 KiB.
        if after < 8192:
            panic("store columns did NOT land on the param arena (region-selection regression)")
        arena_free(&a)
`
	buildAndRunSoATest(t, "region_selection_param_arena", src, "bt")
}
