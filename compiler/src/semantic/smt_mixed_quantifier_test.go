package semantic

import (
	"elisacore/src/smt"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSMTMixedQuantifierWeakeningRetainsBranchBounds(t *testing.T) {
	solver := &quantifiedTimeoutSolver{Solver: openCOISolver(t, 500000)}
	goal := "(assert (not (<= x 10)))"
	query := "(declare-const x Int)\n(declare-const branch Bool)\n(declare-fun p (Int) Bool)\n(assert (or (and branch (<= x 9) (forall ((i Int)) (p i))) (and (not branch) (= x 10) (exists ((i Int)) (p i)))))\n" + goal
	a := &Analyzer{}
	if result, _ := a.smtCheckSliced(solver, query, goal, nil); result != smt.Unsat {
		t.Fatalf("scalar branch bounds were lost: %v", result)
	}
	wrong := "(assert (not (< x 10)))"
	if result, _ := a.smtCheckSliced(solver, strings.Replace(query, goal, wrong, 1), wrong, nil); result == smt.Unsat {
		t.Fatal("non-strict bounds incorrectly proved a strict bound")
	}
}

func TestSMTMixedQuantifierConsequencesAreWeaker(t *testing.T) {
	z3, err := exec.LookPath("z3")
	if err != nil {
		t.Skip("z3 unavailable")
	}
	quantifiers := []string{"(forall ((i Int)) (p i))", "(exists ((i Int)) (p i))"}
	var formulas []string
	for _, q := range quantifiers {
		formulas = append(formulas, q, "(not "+q+")", "(and flag "+q+")", "(or flag "+q+")", "(not (and flag "+q+"))", "(not (or flag "+q+"))", "(not (not (and flag "+q+")))", "(= flag "+q+")", "(not (= flag "+q+"))", "(ite "+q+" flag (not flag))", "(=> flag "+q+")")
	}
	formulas = append(formulas, "(or (and flag (> x 0) (forall ((i Int)) (p i))) (and (not flag) (= x 0) (exists ((i Int)) (p i))))")
	var script strings.Builder
	script.WriteString("(declare-const flag Bool)\n(declare-const x Int)\n(declare-fun p (Int) Bool)\n")
	for _, formula := range formulas {
		original := "(assert " + formula + ")"
		weakened, ok := smtQuantifierFreeConsequence(original)
		if !ok {
			weakened = "(assert true)" // declining retains the old whole-assertion drop
		}
		if strings.Contains(weakened, "forall") || strings.Contains(weakened, "exists") {
			t.Fatalf("quantifier escaped: %s", weakened)
		}
		body := strings.TrimSuffix(strings.TrimPrefix(weakened, "(assert "), ")")
		fmt.Fprintf(&script, "(push 1)\n%s\n(assert (not %s))\n(check-sat)\n(pop 1)\n", original, body)
	}
	cmd := exec.Command(z3, "-in")
	cmd.Stdin = strings.NewReader(script.String())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("z3: %v: %s", err, output)
	}
	answers := strings.Fields(string(output))
	if len(answers) != len(formulas) {
		t.Fatalf("unexpected solver output: %s", output)
	}
	for i, answer := range answers {
		if answer != "unsat" {
			t.Fatalf("original did not imply weakening for %s: %s", formulas[i], answer)
		}
	}
}

func TestSMTMixedWeakeningKeepsGoalAndQuotedTokens(t *testing.T) {
	goal := "(assert (not flag))"
	query := "(declare-const flag Bool)\n(declare-const |name (with) spaces| Bool)\n(declare-fun p (Int) Bool)\n(assert (or (and flag |name (with) spaces| (forall ((i Int)) (p i))) flag))\n" + goal + "\n(get-model)"
	weaker, ok := smtWithoutQuantifiedHypotheses(query, goal)
	if !ok || !strings.Contains(weaker, goal) || !strings.HasSuffix(weaker, "(get-model)\n") || !strings.Contains(weaker, "|name (with) spaces|") {
		t.Fatal("goal, trailing command, or quoted symbol changed")
	}
	for _, bad := range []string{"(assert (and flag (forall ((i Int)) (p i)))", "(assert flag flag)", "(check-sat)"} {
		if _, ok := smtQuantifierFreeConsequence(bad); ok {
			t.Fatalf("malformed or non-assert command accepted: %s", bad)
		}
	}
}

type mixedWeakeningFallbackSolver struct {
	smt.Solver
	verdict    smt.Result
	fullChecks int
	queries    []string
	budgets    []int64
}

func (s *mixedWeakeningFallbackSolver) CheckValues(query string, vars []string) (smt.Result, map[string]string, time.Duration) {
	s.fullChecks++
	s.queries = append(s.queries, query)
	return s.Solver.CheckValues(query, vars)
}
func (s *mixedWeakeningFallbackSolver) CheckBudgetValues(query string, vars []string, budget int64) (smt.Result, map[string]string) {
	s.budgets = append(s.budgets, budget)
	return s.verdict, nil
}
func (s *mixedWeakeningFallbackSolver) CheckBudget(query string, budget int64) smt.Result {
	s.budgets = append(s.budgets, budget)
	return s.verdict
}

func TestSMTMixedWeakeningSatAndUnknownUseFullQuery(t *testing.T) {
	for _, verdict := range []smt.Result{smt.Sat, smt.Unknown} {
		solver := &mixedWeakeningFallbackSolver{Solver: openCOISolver(t, 500000), verdict: verdict}
		goal := "(assert (not (>= x 0)))"
		query := "(declare-const x Int)\n(declare-fun p (Int) Bool)\n(assert (and (= x 3) (forall ((i Int)) (p i))))\n" + goal
		a := &Analyzer{}
		if result, _ := a.smtCheckSliced(solver, query, goal, nil); result != smt.Unsat {
			t.Fatalf("full query failed after weakening verdict %v: %v", verdict, result)
		}
		if solver.fullChecks == 0 || len(solver.budgets) == 0 {
			t.Fatal("weakening verdict bypassed the full-query fallback")
		}
		if solver.budgets[0] != smtDeadPathBudget {
			t.Fatal("weakening budget changed")
		}
		fullQuerySeen := false
		for _, checked := range solver.queries {
			fullQuerySeen = fullQuerySeen || checked == query
		}
		if !fullQuerySeen {
			t.Fatal("fallback did not check the original query")
		}

	}
}
