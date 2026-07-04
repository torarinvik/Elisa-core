//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/119 §2.2/§6.2 (E4): a value block (an ExprBlock) is pure over outer state — it
// may not write back into an enclosing binding. Threading an update out is `rebind`'s
// job (§5). Writes to the block's own locals/accumulators stay legal.

func TestValueBlockOuterAssignIsE4(t *testing.T) {
	requireOneErrorContaining(t, `
def f() -> i64:
    outer: mutable i64 = 0
    x: i64 =
        outer <- 5
        7
    return x + outer
`, `value block may not mutate the outer binding "outer"`)
}

func TestValueBlockOuterAugAssignIsE4(t *testing.T) {
	requireOneErrorContaining(t, `
def f() -> i64:
    outer: mutable i64 = 0
    x: i64 =
        outer += 5
        7
    return x + outer
`, `value block may not mutate the outer binding "outer"`)
}

func TestValueBlockOuterMutationInsideLoopIsE4(t *testing.T) {
	// A write buried in a loop body inside the value block still escapes the block.
	requireOneErrorContaining(t, `
def f(xs: darray[i64]) -> i64:
    outer: mutable i64 = 0
    x: i64 =
        for v in xs:
            outer <- outer + v
        7
    return x + outer
`, `value block may not mutate the outer binding "outer"`)
}

func TestValueBlockLocalMutationStaysLegal(t *testing.T) {
	// Writing the block's own accumulator (declared inside the block) is fine — this is
	// exactly the loop-expression header shape.
	errs := semanticErrorsFor(t, `
def f(xs: darray[i64]) -> i64:
    sum: i64 =
        acc: mutable i64 = 0
        for v in xs:
            acc <- acc + v
        acc
    return sum
`)
	for _, e := range errs {
		if strings.Contains(e, "E4") || strings.Contains(e, "value block may not mutate") {
			t.Fatalf("E4 must not fire on a block-local accumulator, got: %v", errs)
		}
	}
}
