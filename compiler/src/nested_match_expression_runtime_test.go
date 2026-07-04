package main

import "testing"

// docs/119 §4 golden: nested match-EXPRESSION composition. A `match` arm body whose
// tail is itself a `match` (or `if`) yields that inner construct's value, so the
// canonical ML structural-equality shape — `match a: A: match b: A: ...` — parses,
// type-checks, and codegens. The parser converts each arm block's trailing `match`/`if`
// statement into a value expression (valueBlockTail) at the promotion/parse site, so the
// analyzer's existing "arm ends with an expression" path and the ternary/match codegen
// are reused unchanged.
const nestedMatchExprBody = `
enum E:
    A
    B
    C

def pair(a: E, b: E) -> bool:
    return match a:
        E.A:
            match b:
                E.A:
                    true
                _:
                    false
        E.B:
            match b:
                E.B:
                    true
                _:
                    false
        _:
            false

# nested if (ternary) as an arm value composes the same way
def rank(a: E, hi: bool) -> i64:
    return match a:
        E.A:
            if hi:
                100
            else:
                1
        _:
            0

@test
def nested_match_expr_composes() -> void:
    can Abort.Panic:
        if not pair(E.A, E.A):
            panic("AA")
        if pair(E.A, E.B):
            panic("AB")
        if pair(E.B, E.A):
            panic("BA")
        if not pair(E.B, E.B):
            panic("BB")
        if pair(E.C, E.C):
            panic("CC")
        if rank(E.A, true) != 100:
            panic("rank hi")
        if rank(E.A, false) != 1:
            panic("rank lo")
        if rank(E.C, true) != 0:
            panic("rank other")
`

func TestNestedMatchExprComposes(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "nested_match_expr_composes", nestedMatchExprBody)
	assertAllPassed(t, exit, stdout, stderr, "nested_match_expr_composes")
}
