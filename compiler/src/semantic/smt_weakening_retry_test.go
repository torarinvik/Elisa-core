package semantic

import (
	"elisacore/src/smt"
	"sync"
	"testing"
	"time"
)

type weakeningRetrySolver struct {
	smt.Solver
	first   smt.Result
	budgets []int64
	queries []string
	full    int
}

func (s *weakeningRetrySolver) CheckValues(string, []string) (smt.Result, map[string]string, time.Duration) {
	s.full++
	return smt.Unknown, nil, 0
}
func (s *weakeningRetrySolver) CheckBudgetValues(query string, _ []string, budget int64) (smt.Result, map[string]string) {
	s.budgets = append(s.budgets, budget)
	s.queries = append(s.queries, query)
	if budget == smtDeadPathBudget {
		return s.first, nil
	}
	return smt.Unsat, nil
}
func (s *weakeningRetrySolver) CheckBudget(string, int64) smt.Result { return smt.Unknown }

func TestSMTWeakeningRetryOnlyAfterUnknown(t *testing.T) {
	goal := "(assert (not (>= x 0)))"
	query := "(declare-const x Int)\n(declare-fun p (Int) Bool)\n(assert (and (= x 3) (forall ((i Int)) (p i))))\n" + goal
	for _, tc := range []struct {
		first, want smt.Result
		wantBudgets int
	}{
		{smt.Sat, smt.Unknown, 1},
		{smt.Unknown, smt.Unsat, 2},
	} {
		solver := &weakeningRetrySolver{first: tc.first}
		got, _ := (&Analyzer{}).smtCheckSliced(solver, query, goal, nil)
		if got != tc.want || len(solver.budgets) != tc.wantBudgets || solver.full == 0 {
			t.Fatalf("first=%v: got %v, budgets %v, full checks %d", tc.first, got, solver.budgets, solver.full)
		}
		if len(solver.queries) == 2 && solver.queries[0] != solver.queries[1] {
			t.Fatal("full-budget retry changed the weakened assertions")
		}
	}
}

type weakeningHighSatSolver struct {
	smt.Solver
	budgets []int64
	full    int
}

func (s *weakeningHighSatSolver) CheckValues(string, []string) (smt.Result, map[string]string, time.Duration) {
	s.full++
	return smt.Unknown, nil, 0
}
func (s *weakeningHighSatSolver) CheckBudgetValues(_ string, _ []string, budget int64) (smt.Result, map[string]string) {
	s.budgets = append(s.budgets, budget)
	if budget == smtDeadPathBudget {
		return smt.Unknown, nil
	}
	return smt.Sat, map[string]string{"v_x": "1"}
}
func (s *weakeningHighSatSolver) CheckBudget(string, int64) smt.Result { return smt.Unknown }

func TestSMTHighBudgetWeakeningSatSharedWithoutModel(t *testing.T) {
	shared := &sync.Map{}
	dir := t.TempDir()
	makeAnalyzer := func() *Analyzer {
		return &Analyzer{analyzerProofSolverState: analyzerProofSolverState{
			smtProofCache: &smtProofCache{directory: dir, identity: "same"},
		},
			parallel: &parallelWorkerState{smtFlights: shared}}
	}
	goal := "(assert (not (>= v_x 0)))"
	query := "(declare-const v_x Int)\n(declare-fun p (Int) Bool)\n(assert (and (= v_x 3) (forall ((i Int)) (p i))))\n" + goal
	first, second := &weakeningHighSatSolver{}, &weakeningHighSatSolver{}
	r, model := makeAnalyzer().smtCheckSliced(first, query, goal, nil)
	if r != smt.Unknown || model != nil || len(first.budgets) != 2 || first.full == 0 {
		t.Fatalf("first: %v %v %v", r, model, first.budgets)
	}
	r, model = makeAnalyzer().smtCheckSliced(second, query, goal, nil)
	if r != smt.Unknown || model != nil || len(second.budgets) != 0 || second.full == 0 {
		t.Fatalf("second: %v %v %v", r, model, second.budgets)
	}
}
