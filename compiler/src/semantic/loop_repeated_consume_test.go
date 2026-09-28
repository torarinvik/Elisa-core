package semantic

import (
	"strings"
	"testing"
)

// A value live on loop entry and consumed on the back edge is consumed again by
// the next iteration; a `break` carries its consumptions past the loop.
func TestLoopRepeatedConsumeAndBreakState(t *testing.T) {
	pos := `
# A 'break' carries its consumptions to the code after the loop: the value
# is gone on that path even though the branch that consumed it never falls
# through, and the enclosing loop's back edge sees it too.
affine struct Handle:
    v: i64

def sink(h: Handle) -> i64:
    return h.v

def use_after_break(c: bool) -> i64:
    t: Handle = Handle{v: 1}
    for i in 0..<3:
        if c:
            sink(move t)
            break
    return sink(move t)

def outer_repeats() -> i64:
    t: Handle = Handle{v: 1}
    total: mutable i64 = 0
    for i in 0..<3:
        for j in 0..<3:
            total <- total + sink(move t)
            break
    return total

def while_break(c: bool) -> i64:
    t: Handle = Handle{v: 1}
    while c:
        if c:
            sink(move t)
            break
    return sink(move t)

def main() -> i32:
    return 0
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "loop_break_state_pos.elisa", pos)
	all := strings.Join(result.Errors(), "\n")
	for _, want := range []string{
		`:17:22-23: linear value "t" cannot be used`,
		`:22:5-8: linear handle value "t" declared outside the loop is consumed on every iteration`,
		`:34:22-23: linear value "t" cannot be used`,
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %q in:\n%s", want, all)
		}
	}
	comprehension := `
affine struct Handle:
    v: i64
def take(h: Handle) -> i64:
    return h.v
def main() -> i32:
    h: Handle = Handle{v: 1}
    xs: darray[i64] = [take(move h) for i in 0..<3]
    return xs.count.i32()
`
	result = analyzeTreeTestSourceWithSemanticErrors(t, "loop_repeat_comprehension.elisa", comprehension)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, `linear handle value "h" declared outside the loop is consumed on every iteration`) {
		t.Fatalf("comprehension repeated consume not reported:\n%s", all)
	}
	neg := `
# A loop body that always leaves by 'return' hands nothing to the code after
# the loop; a 'break' after a reinitialization carries a live value.
affine struct Handle:
    v: i64

def sink(h: Handle) -> i64:
    return h.v

def return_in_while(c: bool) -> i64:
    t: Handle = Handle{v: 1}
    while c:
        return sink(move t)
    return sink(move t)

def reinit_then_break(c: bool) -> i64:
    t: mutable Handle = Handle{v: 1}
    for i in 0..<3:
        if c:
            sink(move t)
            t <- Handle{v: 2}
            break
    return sink(move t)

def break_before_move() -> i64:
    t: Handle = Handle{v: 1}
    for i in 0..<3:
        if i == 1:
            break
    return sink(move t)

def main() -> i32:
    return 0
`
	analyzeTreeTestSource(t, "loop_break_state_neg.elisa", neg)
}
