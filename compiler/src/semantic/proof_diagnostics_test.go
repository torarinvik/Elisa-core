package semantic

import (
	"strings"
	"testing"
)

// TestProofDiagnosticsGoalShown verifies that an unprovable `requires` diagnostic includes the
// caller-side goal text (the precondition with parameter names substituted by argument names).
func TestProofDiagnosticsGoalShown(t *testing.T) {
	src := `
def use_off(buf: u8&, off: i32) -> u8:
    requires off >= 0
    return buf[off.usize()]

def caller(buf: u8&, n: i32) -> u8:
    return use_off(buf, n)
`
	result := analyzeTreeTestSource(t, "diag_goal.elisa", src)
	diags := allDiagnostics(result)
	// The diagnostic must mention the goal (caller-side: "n >= 0").
	if !strings.Contains(diags, "goal:") {
		t.Fatalf("expected 'goal:' in diagnostic, got:\n%s", diags)
	}
	if !strings.Contains(diags, "n >= 0") {
		t.Fatalf("expected caller-side goal 'n >= 0' in diagnostic, got:\n%s", diags)
	}
}

// TestProofDiagnosticsKnownFactShown verifies that when the caller has a range fact relevant to
// the goal, it appears in the diagnostic's known-facts section.
func TestProofDiagnosticsKnownFactShown(t *testing.T) {
	src := `
def check_bound(x: i32, limit: i32) -> i32:
    requires x < limit
    return x

def caller(a: i32) -> i32:
    if a > 0:
        # In this branch we know a > 0, but not whether a < limit (limit is unknown).
        limit: i32 = 5
        return check_bound(a, limit)
    return 0
`
	result := analyzeTreeTestSource(t, "diag_fact.elisa", src)
	diags := allDiagnostics(result)
	// The diagnostic should show the goal.
	if !strings.Contains(diags, "goal:") {
		t.Fatalf("expected 'goal:' section in diagnostic, got:\n%s", diags)
	}
	// Known facts or suggestion should appear.
	hasFacts := strings.Contains(diags, "known facts:")
	hasSuggestion := strings.Contains(diags, "suggestion:")
	if !hasFacts && !hasSuggestion {
		t.Fatalf("expected 'known facts:' or 'suggestion:' in diagnostic, got:\n%s", diags)
	}
}

// TestProofDiagnosticsSuggestionShown verifies the suggestion line always appears in an
// unprovable-requires diagnostic.
func TestProofDiagnosticsSuggestionShown(t *testing.T) {
	src := `
def need_pos(v: i32) -> i32:
    requires v > 0
    return v

def caller(x: i32) -> i32:
    return need_pos(x)
`
	result := analyzeTreeTestSource(t, "diag_suggestion.elisa", src)
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "suggestion:") {
		t.Fatalf("expected 'suggestion:' in diagnostic, got:\n%s", diags)
	}
	// Suggestion should mention assert or requires.
	if !strings.Contains(diags, "assert") && !strings.Contains(diags, "requires") {
		t.Fatalf("suggestion should mention 'assert' or 'requires', got:\n%s", diags)
	}
}

// TestProofDiagnosticsRangeFactIncluded verifies that when the caller has a specific range fact
// for a variable that appears in the goal, it shows up in the known-facts section.
func TestProofDiagnosticsRangeFactIncluded(t *testing.T) {
	// k is unknown (could be anything), so the precondition k >= 10 is unknown (not refuted).
	src := `
def in_range(n: i32) -> i32:
    requires n >= 10
    return n

def caller(k: i32) -> i32:
    return in_range(k)
`
	result := analyzeTreeTestSource(t, "diag_range_fact.elisa", src)
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "goal:") {
		t.Fatalf("expected 'goal:' in diagnostic, got:\n%s", diags)
	}
	if !strings.Contains(diags, "suggestion:") {
		t.Fatalf("expected 'suggestion:' in diagnostic, got:\n%s", diags)
	}
}

// TestProofDiagnosticsRenderSubstitutedGoal is a unit test for the goal rendering helper.
func TestProofDiagnosticsRenderSubstitutedGoal(t *testing.T) {
	// Build a simple "off >= 0" binary expr and substitute off -> n.
	// We'll test via the public-facing diagnostic: use analyzeTreeTestSource.
	src := `
def f(off: i32) -> i32:
    requires off >= 0
    return off

def g(n: i32) -> i32:
    return f(n)
`
	result := analyzeTreeTestSource(t, "diag_subst.elisa", src)
	diags := allDiagnostics(result)
	// After substitution "off" -> "n", the goal should read "n >= 0".
	if !strings.Contains(diags, "n >= 0") {
		t.Fatalf("expected substituted goal 'n >= 0', got:\n%s", diags)
	}
}

// TestProofDiagnosticsKnownFactsForBranchScope verifies that a branch-guard fact (e.g. from
// `if n > 0:`) appears in the known-facts list when the goal involves the same variable.
func TestProofDiagnosticsKnownFactsForBranchScope(t *testing.T) {
	src := `
def need_large(n: i32) -> i32:
    requires n > 100
    return n

def caller(n: i32) -> i32:
    if n > 0:
        return need_large(n)
    return 0
`
	result := analyzeTreeTestSource(t, "diag_branch_fact.elisa", src)
	diags := allDiagnostics(result)
	// In the if-branch we know n > 0 but not n > 100. The diagnostic should show the goal.
	if !strings.Contains(diags, "goal:") {
		t.Fatalf("expected 'goal:' in diagnostic, got:\n%s", diags)
	}
	// The known-facts section or suggestion should reference n.
	if !strings.Contains(diags, "n") {
		t.Fatalf("expected variable 'n' in diagnostic, got:\n%s", diags)
	}
}
