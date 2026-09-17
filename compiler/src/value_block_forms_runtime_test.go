package main

import "testing"

// docs/119 value-form spellings that the spec writes but the compiler used to reject, run
// end to end:
//   - §2.1 `return` NEWLINE INDENT block (and the loop/if/match forms in that position);
//   - §2.3/§3.2 single-line `else: VALUE` / `else: STMT` branches;
//   - §2 a block tail that needs the destination type (a string literal into sview, a
//     `null` into an optional).
const valueBlockFormsBody = `
def returned_block(n: i64) -> i64:
    return
        k: i64 = n + 7
        k * k

def returned_loop(xs: darray[i64]) -> i64:
    return
        for x in xs |acc: i64 = 0| -> acc:
            acc <- acc + x

def returned_if(n: i64) -> i64:
    return
        if n > 2:
            t: i64 = n + 4
            t
        else:
            0

def medal(score: i64) -> sview:
    label: sview =
        if score > 90: "gold"
        elif score > 50: "silver"
        else: "bronze"
    return label

def parity(xs: darray[i64]) -> i64:
    evens, odds =
        for x in xs |e = 0, o = 0| -> e, o:
            if x % 2 == 0: e <- e + 1
            else: o <- o + 1
    return evens * 10 + odds

def picked(n: i64) -> i64:
    s: sview =
        pick: bool = n > 2
        "long" if pick else "no"
    return s.len.i64()

def maybe(n: i64) -> i64:
    o: i64? =
        ok: bool = n > 0
        n if ok else null
    if o is v:
        return v
    return -1

@test
def value_block_forms() -> void:
    can Abort.Panic:
        xs: darray[i64] = [1, 2, 3, 4, 5]
        if returned_block(3) != 100:
            panic("return block")
        if returned_loop(xs) != 15:
            panic("return loop block")
        if returned_if(3) != 7:
            panic("return if block, then")
        if returned_if(1) != 0:
            panic("return if block, else")
        if medal(95) != "gold":
            panic("single-line if value")
        if medal(60) != "silver":
            panic("single-line elif value")
        if medal(1) != "bronze":
            panic("single-line else value")
        if parity(xs) != 23:
            panic("single-line else statement in a loop expression")
        if picked(3) != 4:
            panic("sview literal block tail, then")
        if picked(1) != 2:
            panic("sview literal block tail, else")
        if maybe(3) != 3:
            panic("optional block tail, present")
        if maybe(0) != -1:
            panic("optional block tail, null")
`

func TestValueBlockForms(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "value_block_forms", valueBlockFormsBody)
	assertAllPassed(t, exit, stdout, stderr, "value_block_forms")
}
