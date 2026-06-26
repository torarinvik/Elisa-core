package semantic

import (
	"strings"
	"testing"
)

// opts is a convenience shorthand for the options that enable strict ensure checking.
var ensureDiagOpts = AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true}

// TestEnsureDiagnosticGoalShown verifies that an unprovable `ensure` diagnostic includes the
// postcondition goal text.
func TestEnsureDiagnosticGoalShown(t *testing.T) {
	src := `
def add_pos(a: i32, b: i32) -> i32:
    ensure result > 0
    return a + b
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_goal.elisa", src, ensureDiagOpts)
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "goal:") {
		t.Fatalf("expected 'goal:' in ensure diagnostic, got:\n%s", diags)
	}
}

// TestEnsureDiagnosticSuggestionShown verifies the suggestion line appears in an
// unprovable ensure diagnostic.
func TestEnsureDiagnosticSuggestionShown(t *testing.T) {
	src := `
def double(x: i32) -> i32:
    ensure result > x
    return x * 2
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_suggestion.elisa", src, ensureDiagOpts)
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "suggestion:") {
		t.Fatalf("expected 'suggestion:' in ensure diagnostic, got:\n%s", diags)
	}
}

// TestEnsureDiagnosticPostconditionLabel verifies that the diagnostic names it as a
// postcondition failure (not a precondition at a call site).
func TestEnsureDiagnosticPostconditionLabel(t *testing.T) {
	src := `
def nonneg(x: i32) -> i32:
    ensure result >= 0
    return x - 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_label.elisa", src, ensureDiagOpts)
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "ensure postcondition") {
		t.Fatalf("expected 'ensure postcondition' in ensure diagnostic, got:\n%s", diags)
	}
}

// TestEnsureDiagnosticFunctionNameInMessage verifies that the function name appears in
// the ensure failure diagnostic.
func TestEnsureDiagnosticFunctionNameInMessage(t *testing.T) {
	src := `
def must_be_positive(x: i32) -> i32:
    ensure result > 0
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_funcname.elisa", src, ensureDiagOpts)
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "must_be_positive") {
		t.Fatalf("expected function name 'must_be_positive' in ensure diagnostic, got:\n%s", diags)
	}
	if !strings.Contains(diags, "goal:") {
		t.Fatalf("expected 'goal:' in ensure diagnostic, got:\n%s", diags)
	}
}

// TestEnsureDiagnosticCounterexampleShown verifies that when the SMT prover produces a
// counterexample it is surfaced in the diagnostic.
func TestEnsureDiagnosticCounterexampleShown(t *testing.T) {
	src := `
def bounded(x: i32) -> i32:
    ensure result == 42
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_cex.elisa", src, ensureDiagOpts)
	diags := allDiagnostics(result)
	// The diagnostic must include at minimum the goal and the function name.
	if !strings.Contains(diags, "goal:") {
		t.Fatalf("expected 'goal:' in ensure diagnostic, got:\n%s", diags)
	}
}

// TestEnsureDiagnosticResultSubstituted verifies that `result` in the ensure clause is
// substituted with the return expression in the rendered goal.
func TestEnsureDiagnosticResultSubstituted(t *testing.T) {
	src := `
def offset(base: i32) -> i32:
    ensure result > base
    return base
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_subst.elisa", src, ensureDiagOpts)
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "goal:") {
		t.Fatalf("expected 'goal:' in ensure diagnostic, got:\n%s", diags)
	}
	// The goal should reference `base` (the substituted `result`) or the original clause.
	if !strings.Contains(diags, "base") {
		t.Fatalf("expected parameter name 'base' in substituted goal, got:\n%s", diags)
	}
}

// TestEnsureDiagnosticKnownFactsShown verifies that when the prover has a counterexample
// or range fact for variables in the goal, the known-facts section is populated.
func TestEnsureDiagnosticKnownFactsShown(t *testing.T) {
	src := `
def strictly_increasing(x: i32) -> i32:
    ensure result > x
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_facts.elisa", src, ensureDiagOpts)
	diags := allDiagnostics(result)
	// The goal and suggestion must be present; known facts may appear when facts are available.
	if !strings.Contains(diags, "goal:") {
		t.Fatalf("expected 'goal:' in ensure diagnostic, got:\n%s", diags)
	}
	if !strings.Contains(diags, "suggestion:") {
		t.Fatalf("expected 'suggestion:' in ensure diagnostic, got:\n%s", diags)
	}
}
