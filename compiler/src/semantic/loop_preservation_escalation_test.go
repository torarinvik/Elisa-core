package semantic

import (
	"strings"
	"testing"
	"time"

	"elisacore/src/smt"
)

type loopPreservationEscalationSolver struct {
	highBudget   int64
	highResult   smt.Result
	goal         string
	transient    uint64
	standardRuns int
	budgets      []int64
	highQueries  []string
}

func (s *loopPreservationEscalationSolver) Check(string) (smt.Result, time.Duration) {
	return smt.Unknown, 0
}

func (s *loopPreservationEscalationSolver) CheckValues(_ string, _ []string) (smt.Result, map[string]string, time.Duration) {
	s.standardRuns++
	s.transient++
	return smt.Unknown, nil, 0
}

func (s *loopPreservationEscalationSolver) CheckBudgetValues(query string, _ []string, budget int64) (smt.Result, map[string]string) {
	s.budgets = append(s.budgets, budget)
	if budget >= s.highBudget && strings.Contains(query, "(assert (not "+s.goal+"))") {
		s.highQueries = append(s.highQueries, query)
		return s.highResult, nil
	}
	s.transient++
	return smt.Unknown, nil
}

func (s *loopPreservationEscalationSolver) CheckBudget(query string, budget int64) smt.Result {
	result, _ := s.CheckBudgetValues(query, nil, budget)
	return result
}

func (s *loopPreservationEscalationSolver) UnknownCacheEpoch() (uint64, uint64) {
	return 0, s.transient
}

func (*loopPreservationEscalationSolver) Stats() smt.Stats { return smt.Stats{} }
func (*loopPreservationEscalationSolver) Close() error     { return nil }

func TestReportedLoopPreservationEscalatesOnlyAStillUnprovenStrategy(t *testing.T) {
	t.Setenv("ELISA_SMT_RLIMIT", "500000")

	for _, tc := range []struct {
		name       string
		goal       string
		highResult smt.Result
		wantProof  bool
	}{
		{name: "unknown becomes proof at higher budget", goal: "(= 1 1)", highResult: smt.Unsat, wantProof: true},
		{name: "false obligation stays rejected", goal: "(= 1 2)", highResult: smt.Sat, wantProof: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			solver := &loopPreservationEscalationSolver{
				highBudget: smtResourceLimit() * smtEscalationFactor,
				highResult: tc.highResult,
				goal:       tc.goal,
			}
			a := &Analyzer{
				smtEnabled:               true,
				smtSolver:                solver,
				smtProofCacheInitialized: true,
			}
			const hyps = "(assert (forall ((k Int)) (= k k)))\n"
			prove := func() (bool, bool, string) {
				tr := a.newSMTTranslator(nil)
				proven, counterexample := a.smtCheckQuery(tr, hyps, tc.goal)
				return proven, false, counterexample
			}

			proven, guarded, _ := a.proveLoopPreservationReported(prove)
			if proven != tc.wantProof || guarded {
				t.Fatalf("reported preservation result=(%v, guarded=%v), want proof=%v", proven, guarded, tc.wantProof)
			}
			if solver.standardRuns == 0 || solver.transient == 0 {
				t.Fatal("the ordinary-budget preservation query did not return Unknown first")
			}
			if len(solver.highQueries) != 1 {
				t.Fatalf("expected one complete high-budget retry, got %d (budgets %v)", len(solver.highQueries), solver.budgets)
			}
			if budget := solver.budgets[len(solver.budgets)-1]; budget != solver.highBudget {
				t.Fatalf("last solver budget=%d, want escalated budget %d", budget, solver.highBudget)
			}
			if !strings.Contains(solver.highQueries[0], "(forall ((k Int)) (= k k))") {
				t.Fatal("high-budget retry did not preserve the original hypotheses")
			}
			if a.smtEscalation != 0 {
				t.Fatalf("escalation depth leaked after retry: %d", a.smtEscalation)
			}
		})
	}
}

func TestReportedLoopPreservationDoesNotEscalateSuccessAndKeepsGuardedRetry(t *testing.T) {
	t.Run("already proven", func(t *testing.T) {
		a := &Analyzer{smtEnabled: true}
		calls := 0
		proven, guarded, _ := a.proveLoopPreservationReported(func() (bool, bool, string) {
			calls++
			return true, true, ""
		})
		if !proven || !guarded || calls != 1 {
			t.Fatalf("success result=(%v, guarded=%v), calls=%d; want one guarded proof", proven, guarded, calls)
		}
	})

	t.Run("guarded retry success", func(t *testing.T) {
		a := &Analyzer{smtEnabled: true}
		calls := 0
		proven, guarded, _ := a.proveLoopPreservationReported(func() (bool, bool, string) {
			calls++
			if a.smtEscalation > 0 {
				return true, true, ""
			}
			return false, false, ""
		})
		if !proven || !guarded || calls != 2 || a.smtEscalation != 0 {
			t.Fatalf("retry result=(%v, guarded=%v), calls=%d, depth=%d; want guarded retry success", proven, guarded, calls, a.smtEscalation)
		}
	})
}
