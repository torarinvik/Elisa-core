package main

import "testing"

func TestFiniteQuantifierRuntime(t *testing.T) {
	exit, stdout, stderr := runStressProgram(t, "finite_quantifiers", `
def all_small(n: i64) -> bool:
    return forall i in 0..<n: i < 3

def contains_two(n: i64) -> bool:
    return exists i in 0..<n: i == 2

def short_circuit_all() -> bool can[Abort.Panic]:
    return forall i in 0..<3: i > 0 and 10 / (2 - i) > 0

def short_circuit_exists() -> bool can[Abort.Panic]:
    return exists i in 0..<3: i == 0 or 10 / (2 - i) > 0

@test
def finite_quantifiers() -> void:
    can Abort.Panic:
        assert(all_small(-1))
        assert(not contains_two(-1))
        assert(not short_circuit_all())
        assert(short_circuit_exists())
        assert(all_small(0))
        assert(all_small(3))
        assert(not all_small(4))
        assert(not contains_two(0))
        assert(not contains_two(2))
        assert(contains_two(3))
`)
	assertAllPassed(t, exit, stdout, stderr, "finite_quantifiers")
}
