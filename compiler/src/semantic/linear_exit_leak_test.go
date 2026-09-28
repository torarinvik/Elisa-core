package semantic

import (
	"fmt"
	"strings"
	"testing"
)

// A linear local still live when control leaves its scope early (return, break,
// continue) is dropped unconsumed. (Lock guards, released by their block on every
// exit, are covered by the compiler_parallel_fixture CLI test.)
func TestLinearValueLeakOnEarlyExit(t *testing.T) {
	pos := `# A linear local still live when control leaves its scope early -- 'continue',
# 'break', 'return' (also from inside a loop or a match arm) -- is dropped
# without being consumed. Both compilers used to accept every one of these.
linear struct Guard:
    v: i64
def make() -> Guard:
    return Guard{v: 1}
def release(g: Guard) -> void:
    _ = move g
def leak_continue(n: i64) -> void:
    for i in 0..<3:
        g: Guard = make()
        if i == n:
            continue
        release(move g)
def leak_break(n: i64) -> void:
    for i in 0..<3:
        g: Guard = make()
        if i == n:
            break
        release(move g)
def leak_return(n: i64) -> void:
    g: Guard = make()
    if n == 0:
        return
    release(move g)
def leak_return_in_loop(n: i64) -> void:
    for i in 0..<3:
        g: Guard = make()
        if i == n:
            return
        release(move g)
def leak_return_in_arm(n: i64) -> i64:
    g: Guard = make()
    match n:
        0:
            return 0
        _:
            release(move g)
    return 1
def main() -> i32:
    return 0
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "linear_exit_leak_pos.elisa", pos)
	all := strings.Join(result.Errors(), "\n")
	for _, line := range []int{12, 18, 23, 29, 34} {
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

	neg := `# Early exits that consume every live linear local first, or that leave only a
# scope the local does not belong to.
linear struct Guard:
    v: i64
def make() -> Guard:
    return Guard{v: 1}
def release(g: Guard) -> void:
    _ = move g
def pass_back(n: i64) -> Guard:
    g: Guard = make()
    if n == 0:
        return move g
    release(move g)
    return make()
def released_before_return(n: i64) -> i64:
    g: Guard = make()
    if n == 0:
        release(move g)
        return 0
    release(move g)
    return 1
def inner_break_keeps_outer(n: i64) -> void:
    for i in 0..<3:
        g: Guard = make()
        for j in 0..<3:
            if j == n:
                break
        release(move g)
def released_then_continue(n: i64) -> void:
    for i in 0..<3:
        g: Guard = make()
        if i == n:
            release(move g)
            continue
        release(move g)
def both_continue(n: i64) -> void:
    for i in 0..<3:
        g: Guard = make()
        if i == n:
            release(move g)
            continue
        else:
            release(move g)
            continue
def outer_decl_break(n: i64) -> void:
    g: Guard = make()
    for i in 0..<3:
        if i == n:
            break
    release(move g)
def match_return(n: i64) -> i64:
    g: Guard = make()
    match n:
        0:
            release(move g)
            return 0
        _:
            release(move g)
    return 1
def main() -> i32:
    return 0
`
	analyzeTreeTestSource(t, "linear_exit_leak_neg.elisa", neg)
}
