package smt

import "testing"

// A query the solver rejects mid-batch (here an Int symbol used as a Bool) prints an `(error ...)`
// line BEFORE its check-sat answer. Without per-batch framing that answer stays buffered and is read
// as the verdict of the NEXT query. This pins both directions:
//   - the erroring query itself is Unknown (it proved nothing), and
//   - the following satisfiable query is Sat — never the stale `unsat` left by the bad query, which
//     would be a false proof.
func TestSolverErrorDoesNotLeakIntoNextQuery(t *testing.T) {
	z3Available(t)
	s, err := Open(Options{})
	if err != nil {
		t.Fatalf("open solver: %v", err)
	}
	defer s.Close()
	// Ill-sorted assertion, then an unsatisfiable remainder: z3 reports the error and then `unsat`.
	bad := "(declare-const r Int)\n(assert (not r))\n(assert false)"
	if got, _ := s.Check(bad); got != Unknown {
		t.Fatalf("erroring query must be Unknown, got %s", got)
	}
	// Satisfiable: the obligation `x > 0` does not hold for every x.
	if got, _ := s.Check("(declare-const x Int)\n(assert (not (> x 0)))"); got != Sat {
		t.Fatalf("query after a solver error must get its own verdict (sat), got %s", got)
	}
	// And a genuinely valid obligation still proves afterwards.
	if got, _ := s.Check("(declare-const y Int)\n(assert (> y 0))\n(assert (not (>= y 0)))"); got != Unsat {
		t.Fatalf("expected unsat after recovery, got %s", got)
	}
}

// CheckValues must stay framed too: a model fetch after an error-free Sat still returns values, and an
// erroring CheckValues does not desynchronize the following query.
func TestSolverCheckValuesFramingAfterError(t *testing.T) {
	z3Available(t)
	s, err := Open(Options{})
	if err != nil {
		t.Fatalf("open solver: %v", err)
	}
	defer s.Close()
	if got, _, _ := s.CheckValues("(declare-const r Int)\n(assert (not r))\n(assert false)", []string{"r"}); got != Unknown {
		t.Fatalf("erroring CheckValues must be Unknown, got %s", got)
	}
	got, vals, _ := s.CheckValues("(declare-const x Int)\n(assert (= x 7))", []string{"x"})
	if got != Sat || vals["x"] != "7" {
		t.Fatalf("expected sat with x=7, got %s %v", got, vals)
	}
}
