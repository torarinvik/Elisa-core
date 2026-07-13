package main

import "testing"

// Regression: a plain `assert(COND)` (no `by:` proof block) is NOT a call to a function named
// `assert` — the analyzer claims it (discharges it through the proof ladder and
// records COND as a downstream flow fact), and the backend must lower it as a debug-gated check
// (erased at higher opt), mirroring `assert … by:`. It previously fell through to ordinary call
// emission and crashed with `unknown identifier "assert" during LLVM lowering`.
//
// This covers both halves that broke: (1) it must COMPILE at all, and (2) the asserted fact must be
// load-bearing — here it is the only thing that discharges `needs_pos`'s `requires x >= 1` for a
// value whose sign the prover cannot otherwise know.
const plainAssertLoweringBody = `
def needs_pos(x: i64) -> i64:
    requires x >= 1
    return x * 2

def via_assert(v: i64) -> i64:
    assert(v >= 1)
    return needs_pos(v)

@test
def plain_assert_lowering() -> void:
    can Abort.Panic:
        if via_assert(3) != 6:
            panic("assert-guarded call")
`

func TestPlainAssertLowering(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "plain_assert_lowering", plainAssertLoweringBody)
	assertAllPassed(t, exit, stdout, stderr, "plain_assert_lowering")
}
