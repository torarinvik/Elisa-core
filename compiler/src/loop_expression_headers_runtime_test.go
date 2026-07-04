package main

import "testing"

// docs/119 §3 goldens: loop expression headers. `|name = init|` decls are
// loop-private mutables; `-> yield` makes the loop a value evaluated against
// the final accumulator state (break yields current state). Statement-position
// headers (`while cond |i = 0|:`) give loop-private state without a value.
// Desugared in the parser onto ExprBlock (yield form) / scoped-if (statement
// form); bitwise `|` in conditions/iterables is disambiguated by the `IDENT =`
// lookahead (see amb probe: `while n < (a | b):` untouched).
const loopHeaderBody = `
def sum_fold(xs: darray[i64]) -> i64:
    sum: i64 =
        for x in xs |acc = 0| -> acc:
            acc <- acc + x
    return sum

def multi_acc(xs: darray[i64]) -> i64:
    evens, odds =
        for x in xs |e = 0, o = 0| -> e, o:
            if x % 2 == 0:
                e <- e + 1
            else:
                o <- o + 1
    return evens * 10 + odds

def early_exit(xs: darray[i64], needle: i64) -> bool:
    found: bool =
        for x in xs |ok = false| -> ok:
            if x == needle:
                ok <- true
                break
    return found

def range_fold(n: i64) -> i64:
    total: i64 =
        for i in 0..<n |acc = 0| -> acc:
            acc <- acc + i
    return total

def while_private(n: i64) -> i64:
    count: mutable i64 = 0
    while i < n |i = 0|:
        count <- count + 1
        i <- i + 1
    return count

def typed_acc(xs: darray[i64]) -> u64:
    # docs/119 §3: an accumulator may carry an explicit type when its initializer
    # can't pin it down.
    total: u64 =
        for x in xs |acc: u64 = 0| -> acc:
            acc <- acc + x.u64()
    return total

@test
def loop_expression_headers() -> void:
    can Abort.Panic:
        xs: darray[i64] = [1, 2, 3, 4, 5]
        if sum_fold(xs) != 15:
            panic("sum_fold")
        if multi_acc(xs) != 23:
            panic("multi_acc")
        if not early_exit(xs, 4):
            panic("early_exit hit")
        if early_exit(xs, 99):
            panic("early_exit miss")
        if range_fold(10) != 45:
            panic("range_fold")
        if while_private(7) != 7:
            panic("while_private")
        if typed_acc(xs) != 15:
            panic("typed_acc")
`

func TestLoopExpressionHeaders(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "loop_expression_headers", loopHeaderBody)
	assertAllPassed(t, exit, stdout, stderr, "loop_expression_headers")
}
