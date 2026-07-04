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

func TestValueBlockMutatingCallOnOuterIsE4(t *testing.T) {
	// Passing an outer var as `mutable T&` inside a value block is a hidden write.
	requireOneErrorContaining(t, `
def bump(x: mutable i64&) -> void:
    x <- x + 1

def f() -> i64:
    outer: mutable i64 = 0
    r: i64 =
        for i in 0..<3 |acc = 0| -> acc:
            bump(outer)
            acc <- acc + 1
    return r + outer
`, `may not mutate the outer binding "outer" through a call`)
}

func TestValueBlockMutatingBuiltinMethodOnOuterIsE4(t *testing.T) {
	// A mutating builtin collection method models its receiver as a value, but a
	// `.push` on an uncaptured outer container is still a hidden write.
	requireOneErrorContaining(t, `
def f() -> i64:
    xs: mutable darray[i64] = []
    r: i64 =
        for i in 0..<3 |acc = 0| -> acc:
            xs.push(i)
            acc <- acc + 1
    return r
`, `may not mutate the outer binding "xs" through a call`)
}

func TestValueBlockCapturedMutatingCallStaysLegal(t *testing.T) {
	// A captured container licenses mutating-method calls on it.
	errs := semanticErrorsFor(t, `
def f() -> i64:
    xs: mutable darray[i64] = []
    r: i64 =
        for i in 0..<3 |acc = 0, xs| -> acc:
            xs.push(i)
            acc <- acc + 1
    return r
`)
	for _, e := range errs {
		if strings.Contains(e, "value block may not mutate") {
			t.Fatalf("a captured container must license mutating calls, got: %v", errs)
		}
	}
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
