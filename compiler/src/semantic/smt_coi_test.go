package semantic

import (
	"strings"
	"testing"

	"elisacore/src/smt"
)

func openCOISolver(t *testing.T, rlimit int64) smt.Solver {
	t.Helper()
	s, err := smt.Open(smt.Options{ResourceLimit: rlimit})
	if err != nil {
		t.Skipf("no SMT solver: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// pigeonhole over an uninterpreted predicate: unsat but expensive, and shares no symbol with x.
const coiHeavyFacts = `(declare-fun p (Int Int) Bool)
(assert (forall ((i Int)) (=> (and (<= 0 i) (<= i 7)) (or (p i 0) (p i 1) (p i 2) (p i 3) (p i 4) (p i 5) (p i 6)))))
(assert (forall ((i Int) (j Int) (h Int)) (=> (and (<= 0 i) (< i j) (<= j 7) (<= 0 h) (<= h 6)) (not (and (p i h) (p j h))))))
`

// An irrelevant heavy hypothesis no longer spends the budget: under a budget the full query
// exhausts (Unknown), the cone-of-influence query decides the goal at once.
func TestSMTConeOfInfluenceDropsIrrelevantHeavyHypothesis(t *testing.T) {
	solver := openCOISolver(t, 20000)
	goal := "(assert (not (> x 0)))"
	query := "(declare-const x Int)\n" + coiHeavyFacts + "(assert (>= x 0))\n" + goal + "\n"
	if res, _ := solver.Check(query); res != smt.Unknown {
		t.Fatalf("precondition: the full query should exhaust a 20k budget, got %s", res)
	}
	sliced, dropped, ok := smtSliceQuery(query, goal)
	if !ok || strings.Contains(sliced, "forall") || !strings.Contains(dropped, "forall") {
		t.Fatalf("the pigeonhole facts must be sliced away; ok=%v\nsliced:\n%s", ok, sliced)
	}
	if res, _ := solver.Check(sliced); res != smt.Sat {
		t.Fatalf("the goal's cone alone is decided within the same budget (x = 0); got %s", res)
	}
	a := &Analyzer{}
	// x = 0 refutes the goal; the dropped pigeonhole part is undecided at 20k, so the honest
	// verdict is Unknown, not a fabricated Sat — and certainly not a proof.
	if res, _ := a.smtCheckSliced(solver, query, goal, []string{"x"}); res == smt.Unsat {
		t.Fatal("an unprovable goal must not prove")
	}
	provable := "(assert (not (>= x 0)))"
	query2 := "(declare-const x Int)\n" + coiHeavyFacts + "(assert (> x 3))\n" + provable + "\n"
	if res, _ := a.smtCheckSliced(solver, query2, provable, nil); res != smt.Unsat {
		t.Fatalf("the goal's own cone proves it; got %s", res)
	}
}

// A dead path whose contradiction lies entirely outside the goal's cone still proves: the dropped
// hypotheses are checked for unsatisfiability on their own.
func TestSMTConeOfInfluenceKeepsDisconnectedDeadPathProof(t *testing.T) {
	solver := openCOISolver(t, 500000)
	goal := "(assert (not (> x 100)))"
	query := "(declare-const x Int)\n(declare-const y Int)\n(declare-const v (Array Int Int))\n" +
		"(assert (forall ((i Int)) (> (select v i) y)))\n(assert (< (select v 2) y))\n(assert (>= x 0))\n" + goal + "\n"
	sliced, _, ok := smtSliceQuery(query, goal)
	if !ok || strings.Contains(sliced, "select") {
		t.Fatalf("the array facts are disconnected from x and must be dropped; ok=%v\n%s", ok, sliced)
	}
	a := &Analyzer{}
	if res, _ := a.smtCheckSliced(solver, query, goal, nil); res != smt.Unsat {
		t.Fatalf("the disconnected contradiction makes the path dead; want unsat, got %s", res)
	}
	// Negative: the same shape with a satisfiable disconnected part does not prove.
	satQuery := strings.Replace(query, "(assert (< (select v 2) y))", "(assert (> (select v 2) y))", 1)
	if res, _ := a.smtCheckSliced(solver, satQuery, goal, nil); res == smt.Unsat {
		t.Fatal("a satisfiable disconnected part must not prove the goal")
	}
}

// A fact linked to the goal only through a chain of shared symbols (array, quantifier, fresh
// svarg names) stays in the cone, so the goal still proves; an unrelated fact is dropped.
func TestSMTConeOfInfluenceFollowsTransitiveChains(t *testing.T) {
	solver := openCOISolver(t, 500000)
	goal := "(assert (not (> x 6)))"
	query := "(declare-const x Int)\n(declare-const |v_svarg$0_len| Int)\n(declare-const v_svarg$0 (Array Int Int))\n(declare-const k Int)\n(declare-const w Int)\n" +
		"(assert (= x (+ (select v_svarg$0 k) 1)))\n" +
		"(assert (forall ((i Int)) (=> (< i |v_svarg$0_len|) (> (select v_svarg$0 i) 5))))\n" +
		"(assert (< k |v_svarg$0_len|))\n" +
		"(assert (> w 1000))\n" + goal + "\n"
	sliced, dropped, ok := smtSliceQuery(query, goal)
	if !ok || !strings.Contains(sliced, "forall") || !strings.Contains(sliced, "(< k ") || strings.Contains(sliced, "(> w 1000)") || !strings.Contains(dropped, "(> w 1000)") {
		t.Fatalf("chain facts must stay and only w's fact drop; ok=%v\nsliced:\n%s\ndropped:\n%s", ok, sliced, dropped)
	}
	a := &Analyzer{}
	if res, _ := a.smtCheckSliced(solver, query, goal, nil); res != smt.Unsat {
		t.Fatalf("the chained facts prove x > 6; got %s", res)
	}
}

// Shapes the symbol analysis does not model decline slicing instead of guessing.
func TestSMTConeOfInfluenceDeclinesUnmodeledShapes(t *testing.T) {
	goal := "(assert (not (> x 0)))"
	for name, q := range map[string]string{
		"sort":        "(declare-sort S 0)\n(declare-const x Int)\n(declare-const s S)\n(assert (forall ((a S) (b S)) (= a b)))\n" + goal + "\n",
		"unbalanced":  "(declare-const x Int)\n(assert (> x 0)\n" + goal + "\n",
		"ground_goal": "(declare-const x Int)\n(assert (> x 0))\n(assert (not (> 1 0)))\n",
	} {
		g := goal
		if name == "ground_goal" {
			g = "(assert (not (> 1 0)))"
		}
		if _, _, ok := smtSliceQuery(q, g); ok {
			t.Errorf("%s: slicing must decline", name)
		}
	}
}
