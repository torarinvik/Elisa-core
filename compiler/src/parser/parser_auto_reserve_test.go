package parser

import (
	"strings"
	"testing"

	"elisacore/src/unparse"
)

// Auto-reservation inserts `xs.reserve(n)` before a `for i in 0..<n:` loop that fills a freshly
// declared inferred-region darray, so the fill never reallocates.
func TestAutoReserveInsertsBeforeCountingFill(t *testing.T) {
	file, errs := parseSourceFile(t, "def foo(n: usize) -> i64:\n    xs: mutable darray[i64] = []\n    for i in 0..<n:\n        xs.push(i.i64())\n    return xs[0]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	formatted := unparse.FormatDecl(file.Decls[0])
	if !strings.Contains(formatted, "xs.reserve(n)") {
		t.Fatalf("expected an auto-inserted `xs.reserve(n)`, got:\n%s", formatted)
	}
	// The reserve must precede the loop.
	if strings.Index(formatted, "xs.reserve(n)") > strings.Index(formatted, "for i in") {
		t.Fatalf("reserve must come before the fill loop, got:\n%s", formatted)
	}
}

func TestAutoReserveInsertsBeforeCountingExtendFill(t *testing.T) {
	file, errs := parseSourceFile(t, "def foo(n: usize, chunks: darray[darray[i64]]) -> i64:\n    xs: mutable darray[i64] = []\n    for i in 0..<n:\n        xs.extend(chunks[i])\n    return xs[0]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	formatted := unparse.FormatDecl(file.Decls[0])
	if !strings.Contains(formatted, "xs.reserve(n)") {
		t.Fatalf("expected an auto-inserted `xs.reserve(n)` before extend fill, got:\n%s", formatted)
	}
	if strings.Index(formatted, "xs.reserve(n)") > strings.Index(formatted, "for i in") {
		t.Fatalf("reserve must come before the extend fill loop, got:\n%s", formatted)
	}
}

func TestAutoReserveCountsListPushElements(t *testing.T) {
	file, errs := parseSourceFile(t, "def foo(n: usize) -> i64:\n    xs: mutable darray[i64] = []\n    for i in 0..<n:\n        xs.push([1, 2, 3])\n    return xs[0]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	formatted := unparse.FormatDecl(file.Decls[0])
	if !strings.Contains(formatted, "xs.reserve((n * 3))") {
		t.Fatalf("expected auto-reserve to account for list-push element count, got:\n%s", formatted)
	}
	if strings.Index(formatted, "xs.reserve((n * 3))") > strings.Index(formatted, "for i in") {
		t.Fatalf("reserve must come before the list-push fill loop, got:\n%s", formatted)
	}
}

func TestAutoReserveFoldsMultiplePushesPerIteration(t *testing.T) {
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
	if !strings.Contains(formatted, "xs.reserve((n * 2))") {
		t.Fatalf("expected auto-reserve to fold two pushes per iteration, got:\n%s", formatted)
	}
}

func TestAutoReserveInfersNestedCountingGrowthProduct(t *testing.T) {
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
	if !strings.Contains(formatted, "xs.reserve((n * m))") {
		t.Fatalf("expected nested counting fill to reserve the product n * m, got:\n%s", formatted)
	}
}

// v1 fires only for the side-effect-free counting shape. A `for x in coll:` fill (no statically
// derivable pure bound at parse time) and a non-empty / explicitly-regioned darray are skipped.
func TestAutoReserveSkipsNonCountingAndIneligible(t *testing.T) {
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
