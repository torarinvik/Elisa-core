package main

import "testing"

// docs/119 §5 goldens: `rebind` explicit mutation threading. A bare target is an
// existing mutable, moved into the block and re-moved back out on exit; a `name: T`
// target is a fresh binding declared from the corresponding tuple slot. Desugared
// in the parser onto a temp-tuple bind (§2 bare-block) + per-target reassign/decl
// (rides pendingStmts), so scoping/codegen/affine-move are the already-tested paths.
const rebindBody = `
def clamp_step(pos_x: mutable i64, v: i64, max_step: i64) -> i64:
    rebind pos_x, applied: i64 =
        if v > max_step:
            pos_x + max_step, max_step
        else:
            pos_x + v, v
    return pos_x * 100 + applied

def multi_target(a: mutable i64, b: mutable i64) -> i64:
    rebind a, b =
        b, a
    return a * 10 + b

def single_scalar(n: mutable i64) -> i64:
    rebind n =
        n * n + 1
    return n

def fresh_only(seed: i64) -> i64:
    m: mutable i64 = seed
    rebind m, doubled: i64 =
        m + 1, seed * 2
    return m * 1000 + doubled

@test
def rebind_threads() -> void:
    can Abort.Panic:
        # clamp: v under the cap → pos advances by v, applied = v
        if clamp_step(10, 3, 5) != (10 + 3) * 100 + 3:
            panic("clamp under")
        # clamp: v over the cap → pos advances by max_step, applied = max_step
        if clamp_step(10, 9, 5) != (10 + 5) * 100 + 5:
            panic("clamp over")
        # swap via a two-target rebind
        if multi_target(7, 2) != 2 * 10 + 7:
            panic("swap")
        # single scalar self-update
        if single_scalar(4) != 4 * 4 + 1:
            panic("single")
        # existing target + fresh binding in one rebind
        if fresh_only(5) != 6 * 1000 + 10:
            panic("fresh")
`

func TestRebindThreads(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "rebind_threads", rebindBody)
	assertAllPassed(t, exit, stdout, stderr, "rebind_threads")
}
