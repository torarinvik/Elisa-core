package main

import "testing"

// docs/125 §4 — `when` decision tables compile and RUN natively: the flagship
// literal_fits_in_type table (string × bool columns, or-groups, `_` default),
// integer ranges, enum tags, and the expression form.
func TestWhenDecisionTableRuntime(t *testing.T) {
	body := `
def literal_fits_in_type(value: i64, negated: bool, type_name: sview) -> bool:
    return when type_name, negated:
        "u8",  false -> value <= 255
        "u16", false -> value <= 65535
        "u32", false -> value <= 4294967295
        "u8" | "u16" | "u32", true -> false
        "i8",  false -> value <= 127
        "i8",  true  -> value <= 128
        "i16", false -> value <= 32767
        "i16", true  -> value <= 32768
        "i32", false -> value <= 2147483647
        "i32", true  -> value <= 2147483648
        _ -> true

def classify(n: i64) -> i64:
    return when n:
        0 -> 100
        1..<10 -> 200
        10..=99 -> 300
        _ -> 400

const enum Color of u8:
    Red
    Green
    Blue

def color_score(c: Color) -> i64:
    return when c:
        Color.Red -> 1
        Color.Green | Color.Blue -> 2

@test
def when_table_dispatches() -> void:
    can Abort.Panic:
        if not literal_fits_in_type(200, false, "u8"):
            panic("u8 200")
        if literal_fits_in_type(300, false, "u8"):
            panic("u8 300")
        if literal_fits_in_type(5, true, "u16"):
            panic("u16 neg")
        if not literal_fits_in_type(128, true, "i8"):
            panic("i8 -128")
        if literal_fits_in_type(129, true, "i8"):
            panic("i8 -129")
        if not literal_fits_in_type(7, false, "weird"):
            panic("default row")

@test
def when_ranges_and_default() -> void:
    can Abort.Panic:
        if classify(0) != 100:
            panic("zero")
        if classify(5) != 200:
            panic("low")
        if classify(10) != 300:
            panic("mid lo")
        if classify(99) != 300:
            panic("mid hi")
        if classify(100) != 400:
            panic("high")
        if classify(-1) != 400:
            panic("neg")

@test
def when_enum_tags() -> void:
    can Abort.Panic:
        if color_score(Color.Red) != 1:
            panic("red")
        if color_score(Color.Blue) != 2:
            panic("blue")

@test
def when_expression_form() -> void:
    can Abort.Panic:
        k: i64 = 3
        label: i64 = when k:
            1 -> 11
            2 | 3 -> 22
            _ -> 33
        if label != 22:
            panic("expr form")
`
	exit, stdout, stderr := runStressProgram(t, "when_decision_table", body)
	assertAllPassed(t, exit, stdout, stderr,
		"when_table_dispatches", "when_ranges_and_default", "when_enum_tags", "when_expression_form")
}

// The tuple-column string comparison the flagship table depends on: string patterns
// nested in tuple positions must lower through the runtime string-equality helper
// (they previously emitted an invalid aggregate icmp).
func TestTupleMatchStringColumnRuntime(t *testing.T) {
	body := `
def pick(name: sview, neg: bool) -> i64:
    return match name, neg:
        "a", false: 1
        "b", true: 2
        _: 3

@test
def tuple_string_columns() -> void:
    can Abort.Panic:
        if pick("a", false) != 1:
            panic("a false")
        if pick("b", true) != 2:
            panic("b true")
        if pick("a", true) != 3:
            panic("a true")
        if pick("zz", false) != 3:
            panic("zz")
`
	exit, stdout, stderr := runStressProgram(t, "tuple_match_string_column", body)
	assertAllPassed(t, exit, stdout, stderr, "tuple_string_columns")
}

// An ALL-string-literal decision table returned as `sview`: every arm yields a bare
// literal, so the bottom-up join is `static u8&` with no arm supplying a view to adapt
// toward. The expected (return) type supplies it — each literal lowers to a view. This
// is the common string-classifier shape among the migratable elif ladders. Also checks
// the mixed literal+view table (one arm is a view) still lowers correctly.
func TestWhenAllStringLiteralTableRuntime(t *testing.T) {
	body := `
const enum TK of u8:
    Int
    Float
    Bool

def family_when(k: TK) -> sview:
    return when k:
        TK.Int -> "int"
        TK.Float -> "float"
        _ -> ""

def family_match(k: TK) -> sview:
    return match k:
        TK.Int: "int"
        TK.Float: "float"
        _: ""

def mixed(k: TK, v: sview) -> sview:
    return when k:
        TK.Int -> "int"
        _ -> v

@test
def all_literal_string_table() -> void:
    can Abort.Panic:
        if family_when(TK.Int) != "int":
            panic("when int")
        if family_when(TK.Float) != "float":
            panic("when float")
        if family_when(TK.Bool) != "":
            panic("when default")
        if family_match(TK.Int) != "int":
            panic("match int")
        if mixed(TK.Int, "dyn") != "int":
            panic("mixed literal arm")
        if mixed(TK.Bool, "dyn") != "dyn":
            panic("mixed view arm")
`
	exit, stdout, stderr := runStressProgram(t, "when_all_string_literal_table", body)
	assertAllPassed(t, exit, stdout, stderr, "all_literal_string_table")
}
