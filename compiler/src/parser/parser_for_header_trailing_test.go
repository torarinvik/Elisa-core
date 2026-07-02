package parser

import (
	"strings"
	"testing"
)

// The for-header slice sub-parse must consume its whole token slice. Unconsumed
// trailing tokens used to be silently dropped, letting junk in a for-loop header
// compile as a no-op (the `step 2` silent-miscompile class, c697356f). Any leftover
// junk is now a parse error at the first unconsumed token.
func TestParseForHeaderTrailingJunkRejected(t *testing.T) {
	_, errs := parseSourceFile(t, "def f() -> void:\n    for i in 0..<10 stride 2:\n        pass\n")
	if len(errs) == 0 {
		t.Fatal("expected trailing-junk diagnostic, got none (silent truncation regressed)")
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "unexpected trailing tokens in for-loop header") {
		t.Fatalf("expected the trailing-tokens diagnostic, got: %v", errs)
	}
}

// Junk after the iterable of a non-range loop is caught the same way.
func TestParseForIterableHeaderTrailingJunkRejected(t *testing.T) {
	_, errs := parseSourceFile(t, "def f(xs: darray[int]) -> void:\n    for x in xs bogus:\n        pass\n")
	if len(errs) == 0 {
		t.Fatal("expected trailing-junk diagnostic on an iterable header, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "unexpected trailing tokens in for-loop header") {
		t.Fatalf("expected the trailing-tokens diagnostic, got: %v", errs)
	}
}

// Legitimate complex headers must not trip the check: calls with arguments,
// `where` filters, `by par` markers, and range strides all consume cleanly.
func TestParseForHeaderLegitimateFormsUnaffected(t *testing.T) {
	sources := []string{
		"def f(xs: darray[int]) -> void:\n    for x in slice_of(xs, 0, 3):\n        pass\n",
		"def f(xs: darray[int]) -> void:\n    for x in xs where x > 0:\n        pass\n",
		"def f(n: int) -> void:\n    for i in 0..<n..2:\n        pass\n",
		"def f(n: int) -> void:\n    for i in 0..<n by par:\n        pass\n",
	}
	for _, src := range sources {
		if _, errs := parseSourceFile(t, src); len(errs) != 0 {
			t.Fatalf("legitimate header tripped the trailing check:\n%s\ngot: %v", src, errs)
		}
	}
}
