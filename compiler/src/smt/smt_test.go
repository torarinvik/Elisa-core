package smt

import (
	"os/exec"
	"testing"
)

// z3Available skips the test when no solver is on PATH, so CI without z3 stays green (the SMT tier is
// optional by design).
func z3Available(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; SMT harness test skipped")
	}
}

func TestSolverUnsatProvenObligation(t *testing.T) {
	z3Available(t)
	s, err := Open(Options{})
	if err != nil {
		t.Fatalf("open solver: %v", err)
	}
	defer s.Close()
	// Negation of `x > 0 implies x >= 0` (always true) is unsatisfiable → the obligation is proven.
	got, _ := s.Check("(declare-const x Int)\n(assert (> x 0))\n(assert (not (>= x 0)))")
	if got != Unsat {
		t.Fatalf("expected unsat (obligation proven), got %s", got)
	}
}

func TestSolverSatCounterexample(t *testing.T) {
	z3Available(t)
	s, err := Open(Options{})
	if err != nil {
		t.Fatalf("open solver: %v", err)
	}
	defer s.Close()
	// Negation of `x >= 0 implies x > 0` is satisfiable (x == 0) → the obligation is refuted.
	got, _ := s.Check("(declare-const x Int)\n(assert (>= x 0))\n(assert (not (> x 0)))")
	if got != Sat {
		t.Fatalf("expected sat (counterexample exists), got %s", got)
	}
}

// The headline win over the linear prover: a NON-LINEAR fact. `x > 1 and y > 1 implies x*y > x` is
// valid, but var*var is outside the affine fragment. z3 proves it.
func TestSolverNonlinearProof(t *testing.T) {
	z3Available(t)
	s, err := Open(Options{})
	if err != nil {
		t.Fatalf("open solver: %v", err)
	}
	defer s.Close()
	got, _ := s.Check("(declare-const x Int)\n(declare-const y Int)\n(assert (> x 1))\n(assert (> y 1))\n(assert (not (> (* x y) x)))")
	if got != Unsat {
		t.Fatalf("expected unsat (nonlinear obligation proven), got %s", got)
	}
}

// The incremental scopes are isolated: a fact asserted inside one Check must not leak into the next.
func TestSolverScopesAreIsolated(t *testing.T) {
	z3Available(t)
	s, err := Open(Options{})
	if err != nil {
		t.Fatalf("open solver: %v", err)
	}
	defer s.Close()
	if got, _ := s.Check("(declare-const x Int)\n(assert (= x 1))\n(assert (= x 2))"); got != Unsat {
		t.Fatalf("first query should be unsat, got %s", got)
	}
	// If scope 1 leaked, a bare `true` would inherit the contradiction. It must be sat.
	if got, _ := s.Check("(assert true)"); got != Sat {
		t.Fatalf("second query should be sat (scopes isolated), got %s", got)
	}
	if st := s.Stats(); st.Queries != 2 {
		t.Fatalf("expected 2 queries recorded, got %d", st.Queries)
	}
}
