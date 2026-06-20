//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS (audit, cluster B): a `requires` constrains a parameter's ENTRY value. Once the parameter
// is mutated it no longer holds, so re-asserting it unconditionally proves false postconditions about
// the new value. These three shapes all reproduced as silent accepts and must now report a strict error.
func TestRequiresDroppedAfterMutation(t *testing.T) {
	cases := []struct{ name, src string }{
		{"scalar", `
def f(m: mutable i64) -> i64:
    requires m > 100
    ensure result > 100
    m <- 0
    return m
`},
		{"struct_field", `
struct S:
    x: mutable i64
def f(s: mutable S&) -> i64:
    requires s.x == 5
    ensure result == 5
    s.x <- s.x + 1
    return s.x
`},
		{"call_site", `
def needs_pos(x: i64) -> i64:
    requires x > 0
    return x
def caller(p: mutable i64) -> i64:
    requires p > 0
    p <- 0 - 5
    return needs_pos(p)
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := strings.Join(analyzeContractStrict(t, "req_mut_"+tc.name+".elisa", tc.src).Errors(), "\n")
			if !strings.Contains(errs, "could not be proven statically") {
				t.Fatalf("the requires is invalidated by the mutation; the false postcondition must NOT prove, got: %v", errs)
			}
		})
	}
}

// COMPLETENESS: the fix must not blanket-drop a `requires` over a mutable root — only after an ACTUAL
// mutation. A mutable param (or a writable-pointee field) that is never mutated keeps its precondition,
// so the legitimate postcondition still proves with no runtime check.
func TestRequiresSurvivesWhenUnmutated(t *testing.T) {
	cases := []struct{ name, src string }{
		{"scalar_unmutated", `
def f(p: mutable i64) -> i64:
    requires p > 100
    ensure result > 50
    return p
`},
		{"field_unmutated", `
struct S:
    x: mutable i64
def f(s: mutable S&) -> i64:
    requires s.x == 5
    ensure result == 5
    return s.x
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if errs := analyzeContractStrict(t, "req_unmut_"+tc.name+".elisa", tc.src).Errors(); len(errs) != 0 {
				t.Fatalf("an unmutated requires should still discharge the postcondition, got: %v", errs)
			}
		})
	}
}
