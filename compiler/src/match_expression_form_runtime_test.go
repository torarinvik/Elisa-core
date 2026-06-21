package main

import "testing"

// `match` used in expression position (RHS of an assignment, each arm yielding a value) runs
// end-to-end through codegen for both integer and enum scrutinees. The arm value flows out of
// the match into the assigned local, and the whole expression evaluates to the joined arm type.
const matchExpressionFormBody = `
enum Color:
    Red
    Green
    Blue

def classify(k: i64) -> i64:
    n: i64 = match k:
        0: 100
        1: 150
        _: 200
    return n

def rank(c: Color) -> i64:
    x: i64 = match c:
        Color.Red: 0
        Color.Green: 1
        Color.Blue: 2
    return x

@test
def match_expression_form() -> void:
    can Abort.Panic:
        if classify(0) != 100:
            panic("int arm 0")
        if classify(1) != 150:
            panic("int arm 1")
        if classify(9) != 200:
            panic("int wildcard")
        if rank(Color.Red) != 0:
            panic("enum Red")
        if rank(Color.Green) != 1:
            panic("enum Green")
        if rank(Color.Blue) != 2:
            panic("enum Blue")
`

func TestMatchExpressionForm(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "match_expression_form", matchExpressionFormBody)
	assertAllPassed(t, exit, stdout, stderr, "match_expression_form")
}
