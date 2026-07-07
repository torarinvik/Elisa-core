package main

import (
	"strings"
	"testing"
)

// docs/122 §5 pattern features, compiled and RUN natively via `-emit test`:
// arm-header guards (§5.1), range patterns (§5.2), bound list rest (§5.3),
// as-bindings (§5.4), binding or-patterns (§5.5, regression — landed for free via
// arm fan-out), and struct/variant rest `_` (§5.7).

func TestDocs122PatternFeaturesRuntime(t *testing.T) {
	body := `enum Tok:
    Ident(name: cstr)
    Keyword(name: cstr)
    Num(value: i64, width: i64)

struct Ev:
    key: i64
    x: i64
    y: i64

def classify_guarded(t: Tok) -> i64:
    match t:
        Tok.Num(value: v, _) if v > 100:
            return 3
        Tok.Num(value: v, _):
            return 2
        Tok.Ident(name: n) | Tok.Keyword(name: n) if n == "if":
            return 10
        Tok.Ident(name: n) | Tok.Keyword(name: n):
            return 1
    return 0

def classify_char(c: char) -> i64:
    match c:
        '0'..='9':
            return 1
        'a'..<'z':
            return 2
        _:
            return 0
    return 0

def classify_int(n: i64) -> i64:
    r: i64 = match n:
        0..<10: 1
        10..=99: 2
        _: 3
    return r

def take_rest(xs: darray[i64]) -> i64:
    can Memory.Allocate, Abort.Panic:
        match xs:
            [first, ...rest]:
                total: mutable i64 = first * 1000
                for v in rest:
                    total <- total + v
                return total
            _:
                return -1
        return -1

def rest_only(xs: darray[i64]) -> i64:
    can Memory.Allocate, Abort.Panic:
        match xs:
            [...rest]:
                total: mutable i64 = 0
                for v in rest:
                    total <- total + v
                return total
        return -1

def whole_and_parts(t: Tok) -> i64:
    match t:
        Tok.Num(value: v, _) as whole:
            keep: Tok = whole
            match keep:
                Tok.Num(value: v2, width: w2):
                    return v + v2 + w2
                _:
                    return -1
        _:
            return -2
    return -3

def key_only(e: Ev) -> i64:
    match e:
        Ev{key: k, _}:
            return k
        _:
            return -1
    return -1

@test
def guards_dispatch_by_value() -> void:
    can Abort.Panic, Memory.Allocate:
        if classify_guarded(Tok.Num(500, 1)) != 3:
            panic("guarded arm should win at 500")
        if classify_guarded(Tok.Num(5, 1)) != 2:
            panic("failed guard must fall to the next arm")
        if classify_guarded(Tok.Keyword("if")) != 10:
            panic("guarded or-alternative broken")
        if classify_guarded(Tok.Ident("x")) != 1:
            panic("unguarded or-arm broken")

@test
def range_patterns() -> void:
    can Abort.Panic, Memory.Allocate:
        if classify_char('5') != 1 or classify_char('9') != 1:
            panic("inclusive digit range broken")
        if classify_char('m') != 2 or classify_char('z') != 0:
            panic("exclusive ..< must exclude the hi bound")
        if classify_int(0) != 1 or classify_int(9) != 1:
            panic("0..<10 broken")
        if classify_int(10) != 2 or classify_int(99) != 2 or classify_int(100) != 3:
            panic("10..=99 bounds broken")
        if classify_int(-5) != 3:
            panic("negative falls to wildcard")

@test
def rest_bindings() -> void:
    can Abort.Panic, Memory.Allocate:
        xs: mutable darray[i64] = [5, 1, 2, 3]
        if take_rest(xs) != 5006:
            panic("bound rest view broken")
        ys: mutable darray[i64] = [7]
        if take_rest(ys) != 7000:
            panic("empty rest must bind an empty view")
        if rest_only(xs) != 11:
            panic("[...rest]-only pattern broken")

@test
def as_and_struct_rest() -> void:
    can Abort.Panic, Memory.Allocate:
        if whole_and_parts(Tok.Num(20, 4)) != 44:
            panic("as-binding broken")
        e: Ev = Ev{key: 42, x: 1, y: 2}
        if key_only(e) != 42:
            panic("struct brace rest broken")
`
	exit, stdout, stderr := runStressProgram(t, "docs122_patterns", body)
	assertAllPassed(t, exit, stdout, stderr,
		"guards_dispatch_by_value", "range_patterns", "rest_bindings", "as_and_struct_rest")
}

// A guarded arm never discharges exhaustiveness (§5.1): a match whose only catch-all
// carries a guard is non-exhaustive and must be rejected at compile time.
func TestDocs122GuardedArmDoesNotCover(t *testing.T) {
	body := `enum Color:
    Red
    Blue

def f(c: Color, n: i64) -> i64:
    match c:
        Color.Red:
            return 1
        Color.Blue if n > 0:
            return 2
    return 0
`
	exit, _, stderr := compileStressProgram(t, "docs122_guard_cover", body)
	if exit == 0 {
		t.Fatalf("expected non-exhaustive error for guarded coverage, got clean compile")
	}
	if !strings.Contains(stderr, "non-exhaustive") && !strings.Contains(stderr, "missing") {
		t.Fatalf("expected a non-exhaustiveness diagnostic, got:\n%s", stderr)
	}
}
