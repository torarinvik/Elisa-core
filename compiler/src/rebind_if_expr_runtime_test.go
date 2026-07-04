package main

import "testing"

// docs/119 gap #2 resolution: a value-yielding conditional that also updates outer
// state is written with `rebind` over an `if`-EXPRESSION (each branch yields a tuple
// of the new outer value(s) + the produced value), NOT an if-capture header. This is
// the §5.3/§6.2 clamp, and it is the idiom — captures stay on loop headers, where the
// `|…|` grammar is unambiguous; `rebind` is the explicit form for conditionals/blocks.
const rebindIfExprBody = `
struct Vec2:
    x: i64
    y: i64

def step(pos: mutable Vec2, v: i64, max_step: i64) -> i64:
    rebind pos, applied: i64 =
        if v > max_step:
            Vec2{x: pos.x + max_step, y: pos.y}, max_step
        else:
            Vec2{x: pos.x + v, y: pos.y}, v
    return pos.x * 1000 + applied

@test
def rebind_if_expr() -> void:
    can Abort.Panic:
        p: mutable Vec2 = Vec2{x: 10, y: 0}
        if step(p, 3, 5) != 13 * 1000 + 3:
            panic("under cap")
        q: mutable Vec2 = Vec2{x: 10, y: 0}
        if step(q, 9, 5) != 15 * 1000 + 5:
            panic("over cap")
`

func TestRebindIfExpr(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "rebind_if_expr", rebindIfExprBody)
	assertAllPassed(t, exit, stdout, stderr, "rebind_if_expr")
}
