package semantic

import (
	"elisacore/src/smt"
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
