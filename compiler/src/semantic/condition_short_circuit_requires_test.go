//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestConditionShortCircuitRequires(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		failures   int
	}{
		{"or_list", "    return if:\n        start >= total\n        not span_fits(start, total)\n", 0},
		{"and", "    if start < total and span_fits(start, total):\n        return\n", 0},
		{"unsafe_or", "    return if:\n        start < total\n        not span_fits(start, total)\n", 1},
		{"unsafe_and", "    if start >= total and span_fits(start, total):\n        return\n", 1},
		{"no_leak", "    if start < total and span_fits(start, total):\n        pass\n    valid: bool = span_fits(start, total)\n", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `def span_fits(start: usize, total: usize) -> bool:
    requires start < total
    ensure result
    return true
def caller(start: usize, total: usize) -> void:
` + tc.body
			result := analyzeContractStrict(t, tc.name+".elisa", source)
			count := 0
			for _, diagnostic := range result.Errors() {
				if strings.Contains(diagnostic, `precondition of "span_fits"`) {
					count++
				} else {
					t.Errorf("unexpected diagnostic: %v", diagnostic)
				}
			}
			if count != tc.failures {
				t.Fatalf("got %d precondition failures, want %d: %v", count, tc.failures, result.Errors())
			}
		})
	}
}

func TestConditionShortCircuitRequiresInvalidatesMutation(t *testing.T) {
	source := `def positive(x: usize) -> bool:
    requires x > 0
    return true
def erase(x: mutable usize&) -> bool:
    x <- 0
    return true
def caller(start: usize) -> void:
    x: mutable usize = start
    if x > 0 and erase(x) and positive(x):
        return
`
	result := analyzeContractStrict(t, "condition_mutation.elisa", source)
	if errors := result.Errors(); len(errors) != 1 || !strings.Contains(errors[0], `precondition of "positive"`) {
		t.Fatalf("the guard fact must not survive erase(x): %v", result.Errors())
	}
}
