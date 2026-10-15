package smt

import "testing"

// pigeonhole(n): n+1 pigeons in n holes, unsat but costly; a fixed instance has a fixed resource cost.
const rlimitHardQuery = `(declare-fun p (Int Int) Bool)
(assert (forall ((i Int)) (=> (and (<= 0 i) (<= i 7)) (or (p i 0) (p i 1) (p i 2) (p i 3) (p i 4) (p i 5) (p i 6)))))
(assert (forall ((i Int) (j Int) (h Int)) (=> (and (<= 0 i) (< i j) (<= j 7) (<= 0 h) (<= h 6)) (not (and (p i h) (p j h))))))`

// ResourceLimit is the deterministic budget: the same query under the same budget gets the same
// verdict every time, and a budget too small to decide it yields Unknown, never a wrong verdict.
func TestSolverResourceLimitIsDeterministicBudget(t *testing.T) {
	z3Available(t)
	tiny, err := Open(Options{ResourceLimit: 1000})
	if err != nil {
		t.Fatalf("open solver: %v", err)
	}
	defer tiny.Close()
	for i := 0; i < 3; i++ {
		if got, _ := tiny.Check(rlimitHardQuery); got != Unknown {
			t.Fatalf("run %d: a 1000-unit budget cannot decide pigeonhole; want unknown, got %s", i, got)
		}
	}
	// Budgets are per query, not cumulative: a cheap query after exhausted ones is still decided.
	if got, _ := tiny.Check("(declare-const x Int)\n(assert (> x 0))\n(assert (not (>= x 0)))"); got != Unsat {
		t.Fatalf("a trivial query must still be decided under the per-query budget, got %s", got)
	}
	ample, err := Open(Options{ResourceLimit: 6_000_000})
	if err != nil {
		t.Fatalf("open solver: %v", err)
	}
	defer ample.Close()
	if got, _ := ample.Check("(declare-const x Int)\n(declare-const y Int)\n(assert (> x 1))\n(assert (> y 1))\n(assert (not (> (* x y) x)))"); got != Unsat {
		t.Fatalf("expected unsat under the production budget, got %s", got)
	}
}
