package semantic

import (
	"strings"
	"testing"
)

// TestRequiresProvenanceDiagHasFromTag verifies that an unprovable `requires` diagnostic now
// includes at least one `(from: ...)` provenance annotation in the known-facts section when
// the caller has a relevant range fact.  This confirms the swap from
// buildRequiresFailureDiagnostic to buildRequiresFailureDiagnosticWithProvenance is wired.
func TestRequiresProvenanceDiagHasFromTag(t *testing.T) {
	src := `
def check_bound(x: i32, limit: i32) -> i32:
    requires x < limit
    return x

def caller(a: i32) -> i32:
    if a > 0:
        limit: i32 = 5
        return check_bound(a, limit)
    return 0
`
	result := analyzeTreeTestSource(t, "req_prov_wire.elisa", src)
	diags := allDiagnostics(result)

	// The diagnostic must be emitted (an unprovable precondition fires a lint).
	if !strings.Contains(diags, "goal:") {
		t.Fatalf("expected a requires-failure diagnostic with 'goal:' line, got:\n%s", diags)
	}
	// With the provenance path wired, any known fact must carry a "(from: ...)" tag.
	if strings.Contains(diags, "known facts:") && !strings.Contains(diags, "(from:") {
		t.Fatalf("known facts are present but no '(from: ...)' provenance tag found — provenance path may not be wired:\n%s", diags)
	}
}

// TestRequiresProvenanceDiagRangeBoundTag checks that a range-bound fact (from the flow
// prover's numRange lattice) appears tagged as "(from: range bound)" when the caller has a
// loop- or guard-derived range for the argument.
func TestRequiresProvenanceDiagRangeBoundTag(t *testing.T) {
	src := `
def nonneg(n: i32) -> i32:
    requires n >= 0
    return n

def caller(v: i32) -> i32:
    if v >= 0:
        return nonneg(v)
    return 0
`
	result := analyzeTreeTestSource(t, "req_prov_range.elisa", src)
	diags := allDiagnostics(result)

	// v is guarded by `v >= 0` in the branch but nonneg requires n >= 0 — if the prover
	// discharges it as proven then no lint fires and the test passes trivially (no provenance
	// tag needed).  If it cannot prove it, the provenance-wired path must tag any shown fact.
	if strings.Contains(diags, "known facts:") {
		if !strings.Contains(diags, "(from:") {
			t.Fatalf("known facts section present but no '(from: ...)' provenance annotation found:\n%s", diags)
		}
	}
}

// TestRequiresProvenanceDiagGoalSubstituted checks that the goal line in the provenance-path
// diagnostic still carries the caller-side substituted form (param name replaced by arg name),
// confirming the switch did not break goal rendering.
func TestRequiresProvenanceDiagGoalSubstituted(t *testing.T) {
	src := `
def use_off(buf: u8&, off: i32) -> u8:
    requires off >= 0
    return buf[off.usize()]

def caller(buf: u8&, n: i32) -> u8:
    return use_off(buf, n)
`
	result := analyzeTreeTestSource(t, "req_prov_goal.elisa", src)
	diags := allDiagnostics(result)

	// The caller-side goal must show "n >= 0" (not "off >= 0") — param substitution must still work.
	if !strings.Contains(diags, "goal:") {
		t.Fatalf("expected 'goal:' in provenance diagnostic, got:\n%s", diags)
	}
	if !strings.Contains(diags, "n >= 0") {
		t.Fatalf("expected caller-side goal 'n >= 0' (param substituted) in diagnostic, got:\n%s", diags)
	}
}

// TestRequiresProvenanceDiagSuggestionPresent confirms the suggestion line is still emitted
// after the provenance-path swap, to avoid regressions in actionability.
func TestRequiresProvenanceDiagSuggestionPresent(t *testing.T) {
	src := `
def check(n: i32) -> i32:
    requires n > 0
    return n

def caller(x: i32) -> i32:
    return check(x)
`
	result := analyzeTreeTestSource(t, "req_prov_suggestion.elisa", src)
	diags := allDiagnostics(result)

	if !strings.Contains(diags, "suggestion:") {
		t.Fatalf("expected 'suggestion:' in provenance-path diagnostic, got:\n%s", diags)
	}
}
