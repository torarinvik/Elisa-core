package semantic

import (
	"elisacore/src/smt"
	"strings"
	"testing"
	"time"
)

type quantifiedTimeoutSolver struct{ smt.Solver }

func (s *quantifiedTimeoutSolver) CheckValues(q string, vars []string) (smt.Result, map[string]string, time.Duration) {
	if strings.Contains(q, "forall") {
		return smt.Unknown, nil, 0
	}
	return s.Solver.CheckValues(q, vars)
}
func (s *quantifiedTimeoutSolver) CheckBudget(q string, limit int64) smt.Result {
	r, _ := s.CheckBudgetValues(q, nil, limit)
	return r
}
func (s *quantifiedTimeoutSolver) CheckBudgetValues(q string, vars []string, limit int64) (smt.Result, map[string]string) {
	return s.Solver.(smtBudgetedSolver).CheckBudgetValues(q, vars, limit)
}

func TestSMTQuantifierWeakeningRecoversScalarProof(t *testing.T) {
	solver := &quantifiedTimeoutSolver{Solver: openCOISolver(t, 500000)}
	goal := "(assert (not (>= x 0)))"
	query := "(declare-const x Int)\n(declare-fun p (Int) Bool)\n(assert (forall ((i Int)) (=> (< i x) (p i))))\n(assert (= x 3))\n" + goal + "\n"
	a := &Analyzer{}
	if r, _ := a.smtCheckSliced(solver, query, goal, nil); r != smt.Unsat {
		t.Fatalf("weakened scalar query should prove despite quantified timeout: %v", r)
	}
}

func TestSMTQuantifierWeakeningFallsBackForNeededHypotheses(t *testing.T) {
	solver := &budgetRecorder{Solver: openCOISolver(t, 500000)}
	goal := "(assert (not (>= (select a 0) 0)))"
	query := "(declare-const a (Array Int Int))\n(assert (forall ((i Int)) (>= (select a i) 0)))\n" + goal + "\n"
	a := &Analyzer{}
	if r, _ := a.smtCheckSliced(solver, query, goal, nil); r != smt.Unsat {
		t.Fatalf("full quantified fact should prove: %v", r)
	}
	if solver.sessionChecks == 0 || len(solver.budgets) == 0 {
		t.Fatalf("expected bounded weakening and full fallback: %+v", solver)
	}
	wrong := "(assert (not (> (select a 0) 0)))"
	if r, _ := a.smtCheckSliced(solver, strings.Replace(query, goal, wrong, 1), wrong, nil); r == smt.Unsat {
		t.Fatal("nonnegative does not imply positive")
	}
}

func TestSMTQuantifierWeakeningKeepsQuantifiedGoalAndRejectsMalformedInput(t *testing.T) {
	goal := "(assert (not (forall ((i Int)) (> i 0))))"
	if _, ok := smtWithoutQuantifiedHypotheses("(assert (forall ((j Int)) (> j 0)))\n"+goal, goal); ok {
		t.Fatal("quantified goals must use the full query")
	}
	goal = "(assert (not (> x 0)))"
	for _, query := range []string{"(assert (forall ((i Int)) (> i 0))", "(declare-const x Int)"} {
		if _, ok := smtWithoutQuantifiedHypotheses(query, goal); ok {
			t.Fatal("malformed or missing goal must decline")
		}
	}
}
