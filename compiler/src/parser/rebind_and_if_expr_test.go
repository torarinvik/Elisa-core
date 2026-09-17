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

// docs/119 §4.1: a diverging branch (`return`/`panic`/`break`/`continue` tail) unifies
// with anything, so it is not an "`if`/`else` branch used as a value must end in an
// expression" error. (`break`/`continue` are then E5 in the analyzer, not the parser.)
func TestIfValueDivergingBranchParses(t *testing.T) {
	for _, src := range []string{
		"def f(n: i64) -> i64:\n    r: i64 =\n        if n > 0:\n            n\n        else:\n            return -1\n    return r\n",
		"def f(n: i64) -> i64:\n    r: i64 =\n        if n > 0:\n            k: i64 = n\n            return k\n        elif n == 0:\n            0\n        else:\n            panic(\"neg\")\n    return r\n",
		"def f(n: i64) -> i64:\n    r: i64 =\n        if n > 0:\n            n\n        else:\n            if n < -5:\n                return 1\n            else:\n                return 2\n    return r\n",
	} {
		_, errs, _ := parseSourceWithNotices(t, src)
		if len(errs) != 0 {
			t.Fatalf("unexpected parser errors for a diverging branch: %v\n%s", errs, src)
		}
	}
}

// docs/119 §1.6/§4.2: a statement `match` arm may put its body on the arm line
// (`_: base`), exactly like an expression match arm.
func TestStatementMatchArmSingleLineBody(t *testing.T) {
	_, errs, _ := parseSourceWithNotices(t, "def f(n: i64) -> i64:\n    base: i64 = 1\n    v: i64 =\n        match n:\n            0: 7\n            _: base\n    return v\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors for single-line statement match arms: %v", errs)
	}
}

// docs/119 §5.2 rule 6 (E9) and §6.3: `rebind` and a `|capture|` header may not share a
// construct, and a loop header names each accumulator or capture once.
func TestRebindCaptureAndHeaderNameRules(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"rebind_inline_loop_capture",
			"def f(xs: darray[i64]) -> i64:\n    total: mutable i64 = 0\n    seen: mutable i64 = 0\n    rebind total = for x in xs |acc = 0, seen| -> acc:\n        seen <- seen + 1\n        acc <- acc + x\n    return total\n",
			"may not appear on the same construct (docs/119 E9)"},
		{"rebind_block_loop_capture",
			"def f(xs: darray[i64]) -> i64:\n    total: mutable i64 = 0\n    seen: mutable i64 = 0\n    rebind total =\n        for x in xs |acc = 0, seen| -> acc:\n            seen <- seen + 1\n            acc <- acc + x\n    return total\n",
			"may not appear on the same construct (docs/119 E9)"},
		{"header_declares_and_captures",
			"def f(xs: darray[i64]) -> i64:\n    total: mutable i64 = 0\n    r: i64 = for x in xs |total = 0, total| -> total:\n        total <- total + x\n    return r\n",
			"loop header both declares and captures \"total\""},
		{"header_captures_twice",
			"def f(xs: darray[i64]) -> i64:\n    seen: mutable i64 = 0\n    r: i64 = for x in xs |acc = 0, seen, seen| -> acc:\n        seen <- seen + 1\n    return r\n",
			"loop header captures \"seen\" twice"},
	}
	for _, tc := range cases {
		_, errs, _ := parseSourceWithNotices(t, tc.src)
		found := false
		for _, e := range errs {
			found = found || strings.Contains(e, tc.want)
		}
		if !found {
			t.Fatalf("%s: expected %q, got: %v", tc.name, tc.want, errs)
		}
	}
	// A capture on a loop AFTER leading statements is its own construct, and a rebind
	// target that threads what a header would have captured is the sanctioned spelling.
	for _, src := range []string{
		"def f(xs: darray[i64]) -> i64:\n    total: mutable i64 = 0\n    seen: mutable i64 = 0\n    rebind total =\n        base = 1\n        for x in xs |acc = base, seen| -> acc:\n            seen <- seen + 1\n            acc <- acc + x\n    return total\n",
		"def f(xs: darray[i64]) -> i64:\n    total: mutable i64 = 0\n    seen: mutable i64 = 0\n    rebind total, seen = for x in xs |acc = 0, n = seen| -> acc, n:\n        n <- n + 1\n        acc <- acc + x\n    return total + seen\n",
	} {
		if _, errs, _ := parseSourceWithNotices(t, src); len(errs) != 0 {
			t.Fatalf("unexpected parser errors: %v\n%s", errs, src)
		}
	}
}
