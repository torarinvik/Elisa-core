package semantic

import (
	"strings"
	"testing"
)

// Three ways a port compiled and silently returned the wrong answer: a nested `x = e` that
// shadows instead of updating, a by-value array parameter written as if it were a pointer, and a
// body local redeclaring a parameter hidden by the synthesized auto-region body wrap.
func TestSilentBindingDiagnostics(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"nested untyped shadow of a local", `
def total(values: i64[4]&) -> i64:
    sum: mutable i64 = 0
    for index in 0..<4:
        sum = sum + values[index]
    sum
`, `"sum" = ... declares a new local that shadows the outer local "sum"`},
		{"nested untyped shadow of a parameter", `
def clamp(limit: i64, value: i64) -> i64:
    if value < 0:
        limit = 0
    value if value < limit else limit
`, `shadows the outer parameter "limit"`},
		{"by-value array parameter written and never read", `
def fill(values: mutable i64[4], seed: i64) -> void:
    for index in 0..<4:
        values[index] <- seed
`, `parameter "values" is a by-value array copy that is written but never read back`},
		{"parameter redeclared in a wrapped body", `
struct Affine:
    scale: mutable i64

struct Aggregate:
    forward: mutable Affine

def compose(after: Affine, before: Affine) -> Affine:
    after

def range_of(tree: view[Aggregate], low: usize) -> Aggregate:
    tree[low]

def path(tree: view[Aggregate], head: view[usize], to_value: usize, start: Affine) -> Affine:
    right: mutable Affine = start
    to_value: mutable usize = to_value
    while head[to_value] != 0:
        part: Aggregate = range_of(tree, to_value)
        right <- compose(right, part.forward)
        to_value <- head[to_value]
    right
`, `duplicate local "to_value" (already defined as param)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "silent_binding.elisa", tc.src, AnalyzeOptions{})
			if all := allDiagnostics(result); !strings.Contains(all, tc.want) {
				t.Fatalf("expected %q, got:\n%s", tc.want, all)
			}
		})
	}
}

func TestSilentBindingDiagnosticsAcceptIntendedForms(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "silent_binding_ok.elisa", `
def total(values: i64[4]&) -> i64:
    sum: mutable i64 = 0
    for index in 0..<4:
        sum <- sum + values[index]
        if values[index] < 0:
            sum: i64 = -values[index]
            doubled = sum * 2
            return doubled
    sum

def sorted_middle(values: mutable i64[3]) -> i64:
    if values[0] > values[1]:
        first: i64 = values[0]
        values[0] <- values[1]
        values[1] <- first
    values[1]

def fill(values: mutable i64[4]&, seed: i64) -> void:
    for index in 0..<4:
        values[index] <- seed
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	for _, unwanted := range []string{"declares a new local", "by-value array copy", "duplicate local"} {
		if strings.Contains(all, unwanted) {
			t.Fatalf("intended form rejected (%s):\n%s", unwanted, all)
		}
	}
}
