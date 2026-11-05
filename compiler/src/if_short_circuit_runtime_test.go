package main

import "testing"

func TestIfConditionShortCircuitRuntime(t *testing.T) {
	fixture := `
def disjunction(n: i64) -> i64 can[Abort.Panic]:
    if n == 0 or 10 / n > 0: return 1
    return 2

def conjunction(n: i64) -> i64 can[Abort.Panic]:
    if n != 0 and 10 / n > 0: return 1
    return 2

@test
def if_short_circuit() -> void:
    can Abort.Panic:
        assert(disjunction(0) == 1)
        assert(disjunction(2) == 1)
        assert(disjunction(-2) == 2)
        assert(conjunction(0) == 2)
        assert(conjunction(2) == 1)
        assert(conjunction(-2) == 2)
`
	for _, opt := range []string{"-O0", "-O3"} {
		t.Run(opt, func(t *testing.T) {
			exit, stdout, stderr := runStressProgram(t, "if_short_circuit", fixture, opt)
			assertAllPassed(t, exit, stdout, stderr, "if_short_circuit")
		})
	}
}
