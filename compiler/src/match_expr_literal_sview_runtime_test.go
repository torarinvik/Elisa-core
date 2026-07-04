package main

import "testing"

// Runtime golden for mergeMatchExprArmTypes: a match-expression mixing string-LITERAL
// arms with an sview arm lowers each literal through the same literal-to-view path as a
// typed declaration (the backend emits every arm with the merged type as its expected
// type), so the yielded views carry correct data+len.
const matchExprLiteralSviewBody = `
def classify(k: int, v: sview) -> sview:
    s: sview = match k:
        1: "one"
        2: v
        _: ""
    return s

@test
def literal_arm_yields_valid_sview() -> void:
    can Abort.Panic:
        name: sview = "custom"
        a: sview = classify(1, name)
        if a.len != 3:
            panic("literal arm len")
        b: sview = classify(2, name)
        if b.len != 6:
            panic("sview arm len")
        c: sview = classify(9, name)
        if c.len != 0:
            panic("empty literal arm len")
`

func TestMatchExprLiteralSviewRuntime(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "literal_arm_yields_valid_sview", matchExprLiteralSviewBody)
	assertAllPassed(t, exit, stdout, stderr, "literal_arm_yields_valid_sview")
}
