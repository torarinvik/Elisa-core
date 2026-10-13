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

def wrap_of(t: sview) -> (prefix: sview, suffix: sview) can[Abort.Panic]:
    conversion: (prefix: sview, suffix: sview) = when t:
        "bool" -> ("!!(", ")")
        "i8" -> ("((", " << 24) >> 24)")
        _ -> ("", "")
    return conversion

@test
def tuple_literal_columns() -> void:
    can Abort.Panic:
        b: (prefix: sview, suffix: sview) = wrap_of("bool")
        if b.prefix != "!!(" or b.suffix != ")":
            panic("bool row wrong")
        i: (prefix: sview, suffix: sview) = wrap_of("i8")
        if i.prefix.len != 2 or i.suffix != " << 24) >> 24)":
            panic("i8 row wrong")
        d: (prefix: sview, suffix: sview) = wrap_of("x")
        if d.prefix.len != 0 or d.suffix.len != 0:
            panic("default row wrong")
`

func TestWhenTernaryStringLiteralArms(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "when_ternary_string_literal", whenTernaryStringLiteralBody)
	assertAllPassed(t, exit, stdout, stderr, "return_position_leaves", "typed_binding_leaves", "tuple_literal_columns")
}
