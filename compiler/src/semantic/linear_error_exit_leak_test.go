package semantic

import (
	"fmt"
	"strings"
	"testing"
)

// Error paths leave the function too: a propagating 'try', a 'raise', and a
// recovery that returns or raises drop every live linear local. A recovery that
// leaves the function consumes nothing on the fall-through path.
func TestLinearValueLeakOnErrorExit(t *testing.T) {
	pos := `# A linear local still live when an error path leaves the function -- a
# propagating 'try', a 'raise' (statement or guarded), 'try ... else return',
# 'else raise', or an 'else err:' block that returns -- is dropped unconsumed.
error E:
    Bad
linear struct Guard:
    v: i64
def make() -> Guard:
    return Guard{v: 1}
def release(g: Guard) -> void:
    _ = move g
def step(n: i64) -> i64 error[E]:
    raise E.Bad if n < 0
    return n
def leak_try(n: i64) -> i64 error[E]:
    g: Guard = make()
    v: i64 = try step(n)
    release(move g)
    return v
def leak_raise_guard(n: i64) -> i64 error[E]:
    g: Guard = make()
    raise E.Bad if n < 0
    release(move g)
    return n
def leak_raise(n: i64) -> i64 error[E]:
    g: Guard = make()
    if n < 0:
        raise E.Bad
    release(move g)
    return n
def leak_else_return(n: i64) -> i64 error[E]:
    g: Guard = make()
    v: i64 = try step(n) else return 0
    release(move g)
    return v
def leak_else_raise(n: i64) -> i64 error[E]:
    g: Guard = make()
    v: i64 = try step(n) else raise E.Bad
    release(move g)
    return v
def leak_else_block(n: i64) -> i64 error[E]:
    g: Guard = make()
    v: i64 = try step(n) else err:
        return 0
    release(move g)
    return v
def main() -> i32:
    return 0
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "linear_error_exit_leak_pos.elisa", pos)
	all := strings.Join(result.Errors(), "\n")
	for _, line := range []int{16, 21, 26, 32, 37, 42} {
		want := fmt.Sprintf(":%d:", line)
		found := false
		for _, e := range result.Errors() {
			if strings.Contains(e, want) && strings.Contains(e, `linear value "g" must be consumed before scope exit`) {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing leak at line %d in:\n%s", line, all)
		}
	}

	neg := `# Error exits that consume every live linear local first; a recovery that
# leaves the function does not consume anything on the fall-through path.
error E:
    Bad
linear struct Guard:
    v: i64
def make() -> Guard:
    return Guard{v: 1}
def release(g: Guard) -> void:
    _ = move g
def step(n: i64) -> i64 error[E]:
    raise E.Bad if n < 0
    return n
def try_before(n: i64) -> i64 error[E]:
    v: i64 = try step(n)
    g: Guard = make()
    release(move g)
    return v
def raise_after_release(n: i64) -> i64 error[E]:
    g: Guard = make()
    release(move g)
    raise E.Bad if n < 0
    return n
def else_value(n: i64) -> i64 error[E]:
    g: Guard = make()
    v: i64 = try step(n) else 0
    release(move g)
    return v
def else_return_moves(n: i64) -> Guard error[E]:
    g: Guard = make()
    v: i64 = try step(n) else return move g
    return move g
def else_block_releases(n: i64) -> i64 error[E]:
    g: Guard = make()
    v: i64 = try step(n) else err:
        release(move g)
        return 0
    release(move g)
    return v
def main() -> i32:
    return 0
`
	analyzeTreeTestSource(t, "linear_error_exit_leak_neg.elisa", neg)
}
