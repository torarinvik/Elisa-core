package main

import "testing"

// docs/119 §4 golden: expression-form `match` with PAYLOAD-DESTRUCTURING arms —
// including multi-statement (block-bodied) arms whose tail expression is the arm
// value — runs end-to-end through codegen for enum and error-set scrutinees.
// This was historically believed broken (stale memory note: "statement form
// only"); these goldens lock in the working behavior so it cannot rot silently.
const matchExpressionPayloadBody = `
enum E:
    Lit(v: i64)
    Add(a: i64, b: i64)

error PE:
    BadDigit(pos: i64)
    Empty

def eval(e: E) -> i64:
    k: i64 = match e:
        E.Lit(v): v
        E.Add(a, b): a + b
    return k

def eval_block_arm(e: E) -> i64:
    k: i64 = match e:
        E.Lit(v):
            doubled: i64 = v * 2
            doubled
        E.Add(a, b): a + b
    return k

def errpos(err: PE) -> i64:
    p: i64 = match err:
        PE.BadDigit(pos): pos
        PE.Empty: -1
    return p

@test
def match_expression_payload() -> void:
    can Abort.Panic:
        if eval(E.Lit(7)) != 7:
            panic("enum payload Lit")
        if eval(E.Add(30, 12)) != 42:
            panic("enum payload Add")
        if eval_block_arm(E.Lit(21)) != 42:
            panic("block-bodied arm tail value")
        if eval_block_arm(E.Add(1, 2)) != 3:
            panic("block-bodied sibling arm")
        e1: PE = PE.BadDigit(9)
        if errpos(e1) != 9:
            panic("error-set payload")
        e2: PE = PE.Empty
        if errpos(e2) != -1:
            panic("error-set bare variant")
`

func TestMatchExpressionPayload(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "match_expression_payload", matchExpressionPayloadBody)
	assertAllPassed(t, exit, stdout, stderr, "match_expression_payload")
}
