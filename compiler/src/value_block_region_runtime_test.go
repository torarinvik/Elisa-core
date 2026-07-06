package main

import "testing"

// docs/119 value blocks × region inference: a container built INSIDE a value-position
// ExprBlock (a loop expression `for … |acc| -> acc:`, a multi-line if expression branch)
// must still get the function-level auto region as its home. The parser's
// functionBodyNeedsAutoRegion walks statement control flow only, so these allocations
// were invisible behind the ExprBlock and rejected with "darray push requires an active
// in <arena>: scope" / "cannot infer region parameter __rg_*" — while the IDENTICAL
// statement-position loop (whose header desugars to IfStmt/WhileStmt) compiled fine.
// Pinned by exprBlocksNeedAutoRegion (the ExprBlock descent).
const valueBlockRegionBody = `
struct Table:
    total: mutable i64

def grow_into(out: mutable darray[i64]&, v: i64) -> void:
    out.push(v * 10)

def bump(t: lmut Table, by: i64) -> void:
    t.total <- t.total + by

# Loop EXPRESSION body: a fresh local builder (push), a comprehension local, and a
# callee growing the local through a mutable& param (the __rg_* inference path).
def process(items: darray[i64], table: lmut Table) -> void:
    for it in items |table| -> table:
        seen: mutable darray[i64] = []
        seen.push(it)
        bound: mutable darray[i64] = [x for x in items]
        grow_into(bound, it)
        table <- table.bump(seen.count.i64() + bound.count.i64())
        table

@test
def value_loop_local_builders() -> void:
    t: mutable Table = Table{total: 0}
    items: darray[i64] = [1, 2, 3]
    t <- process(items, t)
    # each iteration: seen=1, bound=3+1=4 -> 5; three iterations -> 15
    if t.total != 15:
        panic("value-loop local builders threaded wrong total")

# Multi-line if EXPRESSION branch building a local container.
def pick(items: darray[i64]) -> i64:
    best: i64 =
        if items.count > 0:
            tmp: mutable darray[i64] = []
            tmp.push(items[0])
            tmp[0]
        else:
            0
    return best

@test
def if_expr_local_builder() -> void:
    items: darray[i64] = [7]
    if pick(items) != 7:
        panic("if-expression local builder wrong")
`

func TestValueBlockRegionInference(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "value_block_region", valueBlockRegionBody)
	assertAllPassed(t, exit, stdout, stderr, "value_loop_local_builders", "if_expr_local_builder")
}
