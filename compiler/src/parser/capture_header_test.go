package parser

import "testing"

// docs/119 §6: a `|capture|` header (bare names, or mixed with `name = init` decls)
// is detected by a closing pipe immediately followed by `:`/`->`. A bitwise `|` in a
// condition/iterable/range must never be misread as a header.

func TestCaptureHeaderParses(t *testing.T) {
	// mixed private accumulator + capture, value form
	src := "def f(xs: darray[i64]) -> i64:\n    total: mutable i64 = 0\n    r: i64 =\n        for x in xs |acc = 0, total| -> acc:\n            total <- total + x\n            acc <- acc + 1\n    return total + r\n"
	_, errs, _ := parseSourceWithNotices(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
}

func TestBitwiseOrNotMisreadAsCaptureHeader(t *testing.T) {
	// `(a | b)` in a condition, `(a | b | c)` in a while, `(a | b)` in a range — all
	// bitwise, none a header.
	src := "def f(a: i64, b: i64, c: i64, xs: darray[i64]) -> i64:\n" +
		"    total: mutable i64 = 0\n" +
		"    for x in xs:\n" +
		"        if x < (a | b):\n" +
		"            total <- total + 1\n" +
		"    while total < (a | b | c):\n" +
		"        total <- total + 1\n" +
		"    for y in 0..<(a | b):\n" +
		"        total <- total + y\n" +
		"    return total\n"
	_, errs, _ := parseSourceWithNotices(t, src)
	if len(errs) != 0 {
		t.Fatalf("bitwise `|` must not be misread as a capture header, got: %v", errs)
	}
}
