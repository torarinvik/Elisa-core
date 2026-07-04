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
