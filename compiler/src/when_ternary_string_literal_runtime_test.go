package main

import "testing"

// A `when`/match arm whose value is a value-if of string literals (`1 -> "a" if e else "b"`)
// has no view-typed leaf, so its bottom-up type is `static u8&` just like a bare literal arm.
// In a position with an expected `sview` (return, typed binding) every literal leaf must be
// adapted to the view — the bare-literal arm already was; the value-if arm was rejected with
// "return type expects sview, got static u8&". Pins both the acceptance and the lowered
// values (lengths and contents of each leaf).
const whenTernaryStringLiteralBody = `
def pick(c: u8, e: bool) -> sview can[Abort.Panic]:
    return when c:
        1 -> "aa" if e else "bbb"
        _ -> "c" if e else "dddd" if c == 7 else "eeeee"

@test
def return_position_leaves() -> void:
    can Abort.Panic:
        if pick(1, true) != "aa" or pick(1, false) != "bbb":
            panic("arm 1 wrong")
        if pick(2, true) != "c" or pick(7, false) != "dddd" or pick(8, false) != "eeeee":
            panic("default arm wrong")
        if pick(8, false).len != 5:
            panic("view length wrong")

@test
def typed_binding_leaves() -> void:
    can Abort.Panic:
        n: usize = 5
        flag: bool = n > 2
        k: sview = when n:
            5 -> "q" if flag else "r"
            _ -> "s"
        if k != "q":
            panic("typed binding wrong")
`

func TestWhenTernaryStringLiteralArms(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "when_ternary_string_literal", whenTernaryStringLiteralBody)
	assertAllPassed(t, exit, stdout, stderr, "return_position_leaves", "typed_binding_leaves")
}
