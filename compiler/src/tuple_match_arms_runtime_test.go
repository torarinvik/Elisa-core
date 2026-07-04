package main

import "testing"

// Tuple-yielding match arms: `a, b = match k:` with per-arm comma-separated values.
// Three pieces make this work end-to-end:
//   - parser: an inline match-EXPR arm value may be a top-level comma list (TupleExpr)
//   - semantic: mergeMatchExprArmTypes merges tuple arm types per FIELD, adapting a
//     string-literal element to a string-view field (same literal-only rule as scalars)
//   - backend: emitTupleExpr prefers a compatible EXPECTED tuple type, so each element
//     lowers with the merged field type (literal -> sview conversion at the arm yield)
const tupleMatchArmsBody = `
def split(k: int) -> i64:
    a, b = match k:
        1: 10, 20
        2: 30, 40
        _: 0, 1
    return a * 100 + b

def classify(k: int, v: sview) -> i64:
    is_x, name = match k:
        1: true, "one"
        2: false, v
        _: false, ""
    r: mutable i64 = name.len
    if is_x:
        r <- r + 1000
    return r

@test
def tuple_match_arms() -> void:
    can Abort.Panic:
        if split(1) != 1020:
            panic("split 1")
        if split(2) != 3040:
            panic("split 2")
        if split(9) != 1:
            panic("split other")
        nm: sview = "custom"
        if classify(1, nm) != 1003:
            panic("classify literal arm")
        if classify(2, nm) != 6:
            panic("classify sview arm")
        if classify(9, nm) != 0:
            panic("classify empty arm")
`

func TestTupleMatchArms(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "tuple_match_arms", tupleMatchArmsBody)
	assertAllPassed(t, exit, stdout, stderr, "tuple_match_arms")
}
