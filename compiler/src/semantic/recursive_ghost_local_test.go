//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestRecursiveGhostEquationSubstitutesImmutableLocal(t *testing.T) {
	src := `
ghost def accumulate(n: i64, state: i64) -> i64:
    requires n >= 0
    requires n <= 1000
    requires state >= -1000
    requires state <= 1000 - n
    ensure result == state + n
    decreases n
    if n == 0:
        return state
    next_state: i64 = state + 1
    return accumulate(n - 1, next_state)
`
	r := analyzeContractStrict(t, "recursive_ghost_local.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("recursive ghost equation should substitute the immutable local and prove its ensure, got: %v", errs)
	}
}

func TestRecursiveGhostLocalDoesNotHideNondecreasingCall(t *testing.T) {
	src := `
ghost def bad(n: i64, state: i64) -> i64:
    requires n >= 0
    decreases n
    if n == 0:
        return state
    next_state: i64 = state + 1
    return bad(n, next_state)
`
	r := analyzeContractStrict(t, "recursive_ghost_local_bad_decrease.elisa", src)
	if !strings.Contains(strings.Join(r.Errors(), "\n"), "decreases") {
		t.Fatalf("an immutable local must not hide a nondecreasing recursive argument, got: %v", r.Errors())
	}
}

