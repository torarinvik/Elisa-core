package parser

import (
	"strings"
	"testing"

	"elisacore/src/unparse"
)

// Auto-reservation now lives in semantic analysis as compiler-synthesized loop preludes. The
// parser preserves source structure so formatting and source maps do not show optimization-only
// statements.
func TestParserPreservesCountingFillWithoutAutoReserveRewrite(t *testing.T) {
	file, errs := parseSourceFile(t, "def foo(n: usize) -> i64:\n    xs: mutable darray[i64] = []\n    for i in 0..<n:\n        xs.push(i.i64())\n    return xs[0]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	formatted := unparse.FormatDecl(file.Decls[0])
	if strings.Contains(formatted, "xs.reserve") {
		t.Fatalf("parser must not insert auto-reserve statements, got:\n%s", formatted)
	}
}

func TestParserPreservesCountingExtendFillWithoutAutoReserveRewrite(t *testing.T) {
	file, errs := parseSourceFile(t, "def foo(n: usize, chunks: darray[darray[i64]]) -> i64:\n    xs: mutable darray[i64] = []\n    for i in 0..<n:\n        xs.extend(chunks[i])\n    return xs[0]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	formatted := unparse.FormatDecl(file.Decls[0])
	if strings.Contains(formatted, "xs.reserve") {
		t.Fatalf("parser must not insert auto-reserve statements, got:\n%s", formatted)
	}
}

func TestParserPreservesListPushFillWithoutAutoReserveRewrite(t *testing.T) {
	file, errs := parseSourceFile(t, "def foo(n: usize) -> i64:\n    xs: mutable darray[i64] = []\n    for i in 0..<n:\n        xs.push([1, 2, 3])\n    return xs[0]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	formatted := unparse.FormatDecl(file.Decls[0])
	if strings.Contains(formatted, "xs.reserve") {
		t.Fatalf("parser must not insert auto-reserve statements, got:\n%s", formatted)
	}
}

func TestParserPreservesMultiplePushFillWithoutAutoReserveRewrite(t *testing.T) {
	file, errs := parseSourceFile(t, `def foo(n: usize) -> i64:
    xs: mutable darray[i64] = []
    for i in 0..<n:
        xs.push(i.i64())
        xs.push((i + 1).i64())
    return xs[0]
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	formatted := unparse.FormatDecl(file.Decls[0])
	if strings.Contains(formatted, "xs.reserve") {
		t.Fatalf("parser must not insert auto-reserve statements, got:\n%s", formatted)
	}
}

func TestParserPreservesNestedCountingFillWithoutAutoReserveRewrite(t *testing.T) {
	file, errs := parseSourceFile(t, `def foo(n: usize, m: usize) -> i64:
    xs: mutable darray[i64] = []
    for i in 0..<n:
        for j in 0..<m:
            xs.push(j.i64())
    return xs[0]
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	formatted := unparse.FormatDecl(file.Decls[0])
	if strings.Contains(formatted, "xs.reserve") {
		t.Fatalf("parser must not insert auto-reserve statements, got:\n%s", formatted)
	}
}

func TestParserPreservesNonCountingAndIneligibleFillsWithoutAutoReserveRewrite(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"for-in", "def g(src: darray[i64]&) -> i64:\n    ys: mutable darray[i64] = []\n    for x in src:\n        ys.push(x)\n    return ys[0]\n"},
		{"non-zero-start", "def h(n: usize) -> i64:\n    zs: mutable darray[i64] = []\n    for i in 1..<n:\n        zs.push(i.i64())\n    return zs[0]\n"},
		{"no-push", "def k(n: usize) -> i64:\n    ws: mutable darray[i64] = []\n    for i in 0..<n:\n        ws.reserve(i)\n    return ws[0]\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, errs := parseSourceFile(t, tc.src)
			if len(errs) != 0 {
				t.Fatalf("unexpected parse errors: %v", errs)
			}
			formatted := unparse.FormatDecl(file.Decls[0])
			// none of these should gain an auto `reserve(<bound>)` before a loop
			if strings.Contains(formatted, ".reserve(n)") {
				t.Fatalf("%s: must not auto-reserve, got:\n%s", tc.name, formatted)
			}
		})
	}
}
