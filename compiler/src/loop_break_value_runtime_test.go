package main

import "testing"

// docs/119 §3 `break value` and its docs/125 §6b postfix guard, run end to end: a loop
// expression whose header yields a simple accumulator can leave with a value, and
// `break v if cond` is the do-or-skip spelling of `if cond: break v`. The guard form is the one
// a -Wflow-strict engine loop has to use, so every accumulator shape the loop-expression style
// relies on is pinned here -- bool/int/f64 accumulators, `for` and `while`, value and `return`
// positions, a statement loop nested inside, and a guarded completed ternary.
const loopBreakValueBody = `
def contains(xs: darray[i64], sought: i64) -> bool:
    found: bool = for x in xs |hit = false| -> hit:
        break true if x == sought
    return found

def first_square_over(n: i64) -> i64:
    return for i in 0..<n |at = -1| -> at:
        break i if i * i > n

def first_over(xs: darray[i64], limit: i64) -> i64:
    return while i < xs.count |i: usize = 0, seen: i64 = 0| -> seen:
        break xs[i] if xs[i] > limit
        i <- i + 1

def sum_until_negative(xs: darray[i64]) -> f64:
    total: f64 = for x in xs |sum = 0.0| -> sum:
        break sum if x < 0
        sum <- sum + x.f64()
    return total

def has_pair(xs: darray[i64], target: i64) -> bool:
    return for x in xs |ok = false| -> ok:
        for y in xs |ok|:
            ok <- true if x + y == target
        break true if ok

def banded(n: i64) -> i64:
    return for i in 0..<n |band = 0| -> band:
        break (10 if i > 5 else 20) if i * 3 > n

def unguarded(n: i64) -> i64:
    return for i in 0..<n |last = 0| -> last:
        if i == 4:
            break i * 100

@test
def loop_break_value() -> void:
    can Abort.Panic:
        xs: darray[i64] = [3, 8, 1, -2, 9]
        if not contains(xs, 1):
            panic("contains hit")
        if contains(xs, 7):
            panic("contains miss keeps the initial accumulator")
        if first_square_over(10) != 4:
            panic("first_square_over")
        if first_square_over(0) != -1:
            panic("first_square_over on an empty range")
        if first_over(xs, 5) != 8:
            panic("first_over hit")
        if first_over(xs, 50) != 0:
            panic("first_over miss")
        if sum_until_negative(xs) != 12.0:
            panic("f64 accumulator carries its state through break")
        if not has_pair(xs, 17):
            panic("has_pair hit")
        if has_pair(xs, 100):
            panic("has_pair miss")
        if banded(9) != 20:
            panic("guarded ternary value, early")
        if banded(30) != 10:
            panic("guarded ternary value, late")
        if unguarded(10) != 400:
            panic("unguarded break value")
        if unguarded(3) != 0:
            panic("unguarded break value never taken")
`

func TestLoopBreakValue(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "loop_break_value", loopBreakValueBody)
	assertAllPassed(t, exit, stdout, stderr, "loop_break_value")
}
