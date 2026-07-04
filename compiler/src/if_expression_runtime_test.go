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
