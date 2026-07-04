package main

import "testing"

// docs/119 §2 golden: the bare block-expression form — `x =` / `x: T =` followed
// by NEWLINE+INDENT statements with a tail expression — evaluates to the tail
// value, with block locals scoped to the block. Parses onto the same ExprBlock
// node as the pre-existing `do:` form. Covers: typed + untyped binds, nesting,
// a match expression inside a block, and read of outer immutables.
const bareBlockExpressionBody = `
def nested() -> i64:
    x: i64 = 2
    y =
        a: i64 = x + 3
        b =
            c: i64 = a * 2
            c + 1
        b + a
    return y

def with_match(k: i64) -> i64:
    label =
        doubled: i64 = k * 2
        m: i64 = match doubled:
            0: 100
            _: doubled
        m + 1
    return label

def typed_form() -> i64:
    v: i64 =
        base: i64 = 40
        base + 2
    return v

def tuple_form() -> i64:
    c, d =
        a: i64 = 40
        b: i64 = 2
        a, b
    return c + d

def assign_form() -> i64:
    m: mutable i64 = 0
    m <-
        base: i64 = 40
        base + 2
    return m

@test
def bare_block_expression() -> void:
    can Abort.Panic:
        if nested() != 16:
            panic("nested blocks")
        if with_match(3) != 7:
            panic("match inside block")
        if with_match(0) != 101:
            panic("match zero arm inside block")
        if typed_form() != 42:
            panic("typed bare block")
        if tuple_form() != 42:
            panic("tuple-bind bare block")
        if assign_form() != 42:
            panic("assign bare block")
`

func TestBareBlockExpression(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "bare_block_expression", bareBlockExpressionBody)
	assertAllPassed(t, exit, stdout, stderr, "bare_block_expression")
}
