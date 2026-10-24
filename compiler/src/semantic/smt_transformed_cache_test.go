package semantic

import (
	"elisacore/src/smt"
	"strings"
	"testing"
	"time"
)

type transformedCountingSolver struct {
	smt.Solver
	checks int
}

func (s *transformedCountingSolver) CheckValues(q string, vars []string) (smt.Result, map[string]string, time.Duration) {
	s.checks++
	return s.Solver.CheckValues(q, vars)
}
func (s *transformedCountingSolver) CheckBudgetValues(q string, vars []string, budget int64) (smt.Result, map[string]string) {
	s.checks++
	return s.Solver.(smtBudgetedSolver).CheckBudgetValues(q, vars, budget)
}
func (s *transformedCountingSolver) CheckBudget(q string, budget int64) smt.Result {
	r, _ := s.CheckBudgetValues(q, nil, budget)
	return r
}

func TestSMTTransformedProofSurvivesNewSolverAndChangedOuterQuery(t *testing.T) {
	for _, goal := range []string{"(assert (not (>= x 0)))", "(assert (not (forall ((j Int)) (= (+ x j) (+ 3 j)))))"} {
		t.Run(goal, func(t *testing.T) {
			cache := &smtProofCache{directory: t.TempDir(), identity: "transformed-test"}
			query := "(declare-const x Int)\n(declare-fun p (Int) Bool)\n(assert (= x 3))\n(assert (forall ((i Int)) (p (+ i 0))))\n" + goal + "\n"
			first := &transformedCountingSolver{Solver: openCOISolver(t, 500000)}
			a := &Analyzer{smtProofCache: cache}
			if result, _ := a.smtCheckSliced(first, query, goal, nil); result != smt.Unsat {
				t.Fatalf("first proof: %v", result)
			}
			if first.checks == 0 {
				t.Fatal("first proof did not run a solver")
			}
			changed := strings.Replace(query, "(+ i 0)", "(+ i 1)", 1)
			if cache.contains(changed) {
				t.Fatal("outer query should not have been published")
			}
			second := &transformedCountingSolver{Solver: openCOISolver(t, 500000)}
			b := &Analyzer{smtProofCache: &smtProofCache{directory: cache.directory, identity: cache.identity}}
			if result, _ := b.smtCheckSliced(second, changed, goal, nil); result != smt.Unsat {
				t.Fatalf("reused proof: %v", result)
			}
			if second.checks != 0 || b.smtStats.PersistentCacheHits != 1 {
				t.Fatalf("transformed proof missed: checks=%d stats=%+v", second.checks, b.smtStats)
			}
		})
	}
}

func TestSMTTransformedCacheOnlyReusesUnsatAndSeparatesIdentity(t *testing.T) {
	cache := &smtProofCache{directory: t.TempDir(), identity: "one"}
	a := &Analyzer{smtProofCache: cache}
	for _, result := range []smt.Result{smt.Sat, smt.Unknown} {
		calls := 0
		for i := 0; i < 2; i++ {
			got, model := a.smtCheckTransformedProof("(assert true)", func() (smt.Result, map[string]string) { calls++; return result, map[string]string{"v": "7"} })
			if got != result || model["v"] != "7" {
				t.Fatal("non-proof changed")
			}
		}
		if calls != 2 {
			t.Fatal("non-proof cached")
		}
	}
	cache.publish("(assert false)")
	for _, other := range []*smtProofCache{cache, {directory: cache.directory, identity: "two"}} {
		b := &Analyzer{smtProofCache: other}
		calls := 0
		query := "(assert true)"
		if other.identity == "two" {
			query = "(assert false)"
		}
		got, _ := b.smtCheckTransformedProof(query, func() (smt.Result, map[string]string) { calls++; return smt.Unknown, nil })
		if calls != 1 || got != smt.Unknown {
			t.Fatal("different query or identity reused a proof")
		}
	}
}
