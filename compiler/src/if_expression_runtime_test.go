package main

import "testing"

// docs/119 §4 goldens: multi-line `if`/`match` expressions. A trailing `if`/`elif`/
// `else` (or `match`) in a block-expression is the block's value; branches are block
// expressions whose tails unify to one type. Desugared in the parser: `if` nests into
// TernaryExpr (E6 unification + codegen reuse the ternary path), `match` maps onto
// MatchExpr. An `if` value must have a final `else` (E7).
const ifExprBody = `
def classify(n: i64) -> i64:
    r: i64 =
        if n < 0:
            -1
        elif n == 0:
            0
        else:
            1
    return r

def with_body(n: i64) -> i64:
    r: i64 =
        if n > 10:
            big: i64 = n * 2
            big + 1
        else:
            n
    return r

def nested(n: i64) -> i64:
    r: i64 =
        if n > 0:
            if n > 100:
                2
            else:
                1
        else:
            0
    return r

def via_match(tag: i64) -> i64:
    r: i64 = match tag:
        0: 100
        1: 200
        _: 999
    return r

@test
def if_expressions() -> void:
    can Abort.Panic:
        if classify(-5) != -1:
            panic("classify neg")
        if classify(0) != 0:
            panic("classify zero")
        if classify(7) != 1:
            panic("classify pos")
        if with_body(20) != 41:
            panic("with_body big")
        if with_body(3) != 3:
            panic("with_body small")
        if nested(200) != 2:
            panic("nested 2")
        if nested(50) != 1:
            panic("nested 1")
        if nested(-1) != 0:
            panic("nested 0")
        if via_match(1) != 200:
            panic("via_match")
        if via_match(5) != 999:
            panic("via_match default")
`

func TestIfExpressions(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "if_expressions", ifExprBody)
	assertAllPassed(t, exit, stdout, stderr, "if_expressions")
}

// docs/119 §4.1: diverging branches and arms (`return`, `raise`, `panic`) unify with any
// value type and never reach the join. The ternary emitter used to add a phi incoming
// from a terminated arm, so `n * 2 if n > 0 else raise E.X` failed LLVM verification.
const ifExprDivergingBody = `
error Bad:
    Neg
    Zero

def if_else_return(n: i64) -> i64:
    v: i64 =
        if n > 0:
            n * 2
        else:
            return -1
    return v + 1

def elif_return(n: i64) -> i64:
    v: i64 =
        k: i64 = n + 1
        if k > 3:
            k * 2
        elif k == 0:
            return 99
        else:
            k
    return v

def nested_all_diverge(n: i64) -> i64:
    v: i64 =
        if n > 10:
            n
        else:
            if n < 0:
                return 1
            else:
                return 2
    return v

def raise_tail(n: i64) -> i64 error[Bad]:
    v: i64 =
        if n > 0:
            n * 2
        else:
            raise Bad.Neg
    return v + 1

def raise_match_arm(n: i64) -> i64 error[Bad]:
    v: i64 =
        match n:
            0:
                raise Bad.Zero
            _:
                n + 1
    return v

def raise_inline_else(n: i64) -> i64 error[Bad]:
    v: i64 = n * 2 if n > 0 else raise Bad.Neg
    return v + 1

@test
def diverging_branches() -> void:
    can Abort.Panic:
        if if_else_return(5) != 11 or if_else_return(-3) != -1:
            panic("if_else_return")
        if elif_return(5) != 12 or elif_return(-1) != 99 or elif_return(1) != 2:
            panic("elif_return")
        if nested_all_diverge(39) != 39 or nested_all_diverge(-5) != 1 or nested_all_diverge(5) != 2:
            panic("nested_all_diverge")
        a: i64 = try raise_tail(20) else 0
        b: i64 = try raise_tail(-1) else 5
        if a != 41 or b != 5:
            panic("raise_tail")
        c: i64 = try raise_match_arm(40) else 0
        d: i64 = try raise_match_arm(0) else 6
        if c != 41 or d != 6:
            panic("raise_match_arm")
        e: i64 = try raise_inline_else(20) else 0
        f: i64 = try raise_inline_else(-1) else 8
        if e != 41 or f != 8:
            panic("raise_inline_else")
`

func TestIfExpressionDivergingBranches(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "if_expressions_diverging", ifExprDivergingBody)
	assertAllPassed(t, exit, stdout, stderr, "diverging_branches")
}
