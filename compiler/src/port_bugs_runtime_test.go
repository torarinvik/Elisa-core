package main

import (
	"strings"
	"testing"
)

// Bugs found porting benchmarks (2026-10-13): a two-variant `const enum` (BitInt storage) in a
// match/compare crashed type lowering; a `mutable usize&` index was rejected; `"lit".sview()` and
// `c.sview()` (cstr) missed the `cstr?` cast hook. Run at -O0 and -O3.
const portBugsRuntimeBody = `
const enum Kind2:
    A
    B

const enum Kind3:
    X
    Y
    Z

def kind2_val(k: Kind2) -> i64:
    return match k:
        Kind2.A: 1
        Kind2.B: 10

def kind3_val(k: Kind3) -> i64:
    return match k:
        Kind3.X: 1
        Kind3.Y: 2
        Kind3.Z: 3

def next_byte(text: darray[u8]&, cursor: mutable usize&) -> u8:
    c: u8 = text[cursor]
    cursor <- cursor + 1
    return c

@test
def port_bugs() -> void:
    can Abort.Panic, Memory.Allocate:
        kinds: mutable darray[Kind2] = []
        kinds.push(Kind2.B)
        if kind2_val(kinds[0]) != 10 or kind2_val(Kind2.A) != 1:
            panic("two-variant const enum match")
        if kind3_val(Kind3.Z) != 3:
            panic("three-variant const enum match")
        text: darray[u8] = [7, 8, 9]
        i: mutable usize = 1
        a: u8 = next_byte(&text, &i)
        b: u8 = next_byte(&text, &i)
        if a != 8 or b != 9 or i != 3:
            panic("mutable usize& index")
        lit: sview = "abc".sview()
        c: cstr = "hello"
        hv: sview = c.sview()
        if string_view_len(lit) != 3 or string_view_len(hv) != 5 or lit != "abc":
            panic("sview cast")
`

func TestPortBugsRuntime(t *testing.T) {
	t.Parallel()
	for _, opt := range []string{"-O0", "-O3"} {
		exit, stdout, stderr := runStressProgram(t, "port_bugs", portBugsRuntimeBody, opt)
		assertAllPassed(t, exit, stdout, stderr, "port_bugs")
	}
}

// docs/78/99: a plain `assert` is debug-gated and erased above -O0, so a condition that writes
// through a `mutable` reference (or performs an observable effect) would only run in debug builds.
func TestAssertSideEffectWarned(t *testing.T) {
	t.Parallel()
	body := `
def bump(x: mutable i64&) -> bool:
    x <- x + 1
    return true

@test
def assert_effect() -> void:
    v: mutable i64 = 41
    assert bump(&v)
`
	// Warning tier: asserts are erased above -O0, so the hazard is named, but the program builds.
	exit, stdout, stderr := compileStressProgram(t, "assert_effect", body)
	if exit != 0 || !strings.Contains(stdout+stderr, "warning: assert condition must be pure but calls \"bump\"") {
		t.Fatalf("expected assert purity warning, exit=%d\n%s%s", exit, stdout, stderr)
	}
	pure := `
def is_pos(x: i64) -> bool:
    return x > 0

@test
def assert_pure() -> void:
    v: i64 = 41
    assert is_pos(v)
`
	exit, stdout, stderr = runStressProgram(t, "assert_pure", pure)
	assertAllPassed(t, exit, stdout, stderr, "assert_pure")
}
