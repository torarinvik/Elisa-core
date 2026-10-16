//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Call preconditions inside a later ensure clause may assume the earlier clauses (left-to-right
// well-definedness), never the later ones.
func TestSMTEnsureClauseWellDefinednessOrder(t *testing.T) {
	src := `
def g(x: usize, n: usize) -> usize:
    requires x <= n
    ensure result <= n
    return x

def f(n: usize) -> usize:
    ensure result <= n
    ensure g(result, n) <= n
    return 0

def bad(n: usize) -> usize:
    ensure g(result, n) <= n
    ensure result <= n
    return 0

`
	result := analyzeContractStrict(t, "ensure_order.elisa", src)
	ordered, reversed := 0, 0
	for _, e := range result.Errors() {
		if containsAll(e, "precondition of \"g\"", "ensure_order.elisa:9:") {
			ordered++
		}
		if containsAll(e, "precondition of \"g\"", "ensure_order.elisa:13:") {
			reversed++
		}
	}
	if ordered != 0 {
		t.Fatalf("f: the earlier clause should discharge g's precondition; got %v", result.Errors())
	}
	if reversed == 0 {
		t.Fatalf("bad: a LATER clause must not discharge an earlier clause's call precondition; got %v", result.Errors())
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
