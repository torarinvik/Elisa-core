package parser

import (
	"strings"
	"testing"
)

// docs/119 §4/§5 parser-level tests: `if` used as a value must have an `else` (E7);
// `rebind` parses a target list of bare (existing) and `name: T` (fresh) targets.

func TestIfValueRequiresElse(t *testing.T) {
	// A bare block whose tail `if` has no `else` has no value on the false path.
	_, errs, _ := parseSourceWithNotices(t, "def f(n: i64) -> i64:\n    r: i64 =\n        if n > 0:\n            1\n    return r\n")
	if len(errs) == 0 {
		t.Fatalf("expected an E7 error for a value `if` without `else`")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "must have a final `else`") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the E7 diagnostic, got: %v", errs)
	}
}

func TestIfValueWithElseParses(t *testing.T) {
	_, errs, _ := parseSourceWithNotices(t, "def f(n: i64) -> i64:\n    r: i64 =\n        if n > 0:\n            1\n        else:\n            0\n    return r\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
}

func TestRebindMixedTargetsParse(t *testing.T) {
	// bare target (existing mutable, reassigned) + `name: T` (fresh binding).
	src := "def f(p: mutable i64, v: i64) -> i64:\n    rebind p, applied: i64 =\n        p + v, v\n    return p + applied\n"
	_, errs, _ := parseSourceWithNotices(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
}

func TestRebindAsIdentifierStillWorks(t *testing.T) {
	// `rebind` is a soft keyword: a variable named `rebind` (followed by an operator,
	// not an identifier) must still parse.
	src := "def f() -> i64:\n    rebind: i64 = 5\n    rebind <- rebind + 1\n    return rebind\n"
	_, errs, _ := parseSourceWithNotices(t, src)
	if len(errs) != 0 {
		t.Fatalf("`rebind` as an identifier must still parse, got: %v", errs)
	}
}

// docs/120 §10: `return if cond: … else: …` is a value-if whose branch value is
// returned — distinct from the bare postfix guard `return if cond` (which has no
// `:`). A branch may carry statements (e.g. a `rebind` claim) before its tail tuple.
func TestReturnIfValueFormParses(t *testing.T) {
	src := "struct L:\n    pos: mutable i64\n" +
		"def rest(l: lmut L) -> (t: i64, l: lmut L):\n    l.pos <- l.pos + 1\n    return 9, l\n" +
		"def op(l: lmut L, m: bool) -> (t: i64, l: lmut L):\n" +
		"    return if m:\n        1, l\n    else:\n        rebind r: i64, l = l.rest()\n        r, l\n"
	_, errs, _ := parseSourceWithNotices(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors for return-if value form: %v", errs)
	}
}

// The bare postfix guard `return if cond` (no `:`) must still parse as a guard, not
// be swallowed by the value-if path.
func TestReturnIfBareGuardStillParses(t *testing.T) {
	src := "def f(x: i64) -> void:\n    return if x > 5\n    _ = x\n"
	_, errs, _ := parseSourceWithNotices(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors for bare return guard: %v", errs)
	}
}

// A signed return value followed by a guard must remain a statement guard. The
// stage1 expression parser can consume the postfix `if` while parsing `-1`,
// so this is an explicit stage0 parity anchor for that spelling.
func TestSignedReturnIfGuardStillParses(t *testing.T) {
	src := "def f(value: i64, stop: bool) -> i64:\n    return -1 if stop\n    return value\n"
	_, errs, _ := parseSourceWithNotices(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors for signed return guard: %v", errs)
	}
}
