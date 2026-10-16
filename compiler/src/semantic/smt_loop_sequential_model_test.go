//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func loopLintCount(errs []string) int {
	n := 0
	for _, e := range errs {
		if strings.Contains(e, "invariant") || strings.Contains(e, "decreases") {
			n++
		}
	}
	return n
}

// A loop body with body-local lets, a call-valued update and an `if` is modeled sequentially: the
// invariant is proven inductive, the measure decreases, and inv ∧ ¬cond reaches the ensure.
func TestLoopSequentialModelProves(t *testing.T) {
	src := `
module M:
    public:
        def gb(left: i64, right: i64) -> i64:
            ensure result >= left
            ensure result >= right
            return left if left > right else right

        def fnd(t: sview) -> usize:
            ensure result <= t.len
            return t.len

        def lb(facts: darray[sview]) -> i64:
            ensure result >= 0
            greatest: mutable i64 = 0
            index: mutable usize = 0
            while index < facts.count |index, facts, greatest|:
                decreases facts.count - index
                invariant index <= facts.count
                invariant greatest >= 0
                fact: sview = facts[index]
                marker: usize = fnd(fact)
                if marker < fact.len:
                    greatest <- gb(greatest, 5)
                index <- index + 1
            return greatest
`
	result := analyzeContractStrict(t, "seq_model.elisa", src)
	if n := loopLintCount(result.Errors()) + ensureFailuresFor(result.Errors(), "lb"); n != 0 {
		t.Fatalf("expected full proof, got %v", result.Errors())
	}
}

// SOUNDNESS: a guard reads the value written earlier in the same iteration, not the pre-state. Read
// as the pre-state, `i > old` would be `i > i` and the false invariant `c == 0` would
// be "preserved".
func TestLoopSequentialModelGuardSeesEarlierWrite(t *testing.T) {
	src := `
def g(n: i64) -> i64:
    requires n > 3 and n < 100
    ensure result == 0
    c: mutable i64 = 0
    i: mutable i64 = 0
    while i < n |i, c|:
        invariant c == 0
        old: i64 = i
        i <- i + 1
        if i > old:
            c <- 1
    return c
`
	result := analyzeContractStrict(t, "seq_model_guard.elisa", src)
	if ensureFailuresFor(result.Errors(), "g") == 0 {
		t.Fatalf("false ensure must stay unproven; got %v", result.Errors())
	}
	found := false
	for _, e := range result.Errors() {
		if strings.Contains(e, "invariant") {
			found = true
		}
	}
	if !found {
		t.Fatalf("false invariant must stay unproven; got %v", result.Errors())
	}
}

// SOUNDNESS: a callee with a mutable reference parameter may write a loop variable, so the model
// declines and the (false) invariant is not proven.
func TestLoopSequentialModelRejectsMutableRefCallee(t *testing.T) {
	src := `
module M:
    public:
        def reset(x: mutable i64&) -> i64:
            x <- 0
            return 1

        def h(n: i64) -> i64:
            requires n > 2 and n < 100
            ensure result == n
            c: mutable i64 = 0
            i: mutable i64 = 0
            while i < n |i, c|:
                invariant c == i
                one: i64 = reset(c)
                c <- c + one
                i <- i + 1
            return c
`
	result := analyzeContractStrict(t, "seq_model_ref.elisa", src)
	if ensureFailuresFor(result.Errors(), "h") == 0 {
		t.Fatalf("ensure through a mutating callee must stay unproven; got %v", result.Errors())
	}
}
