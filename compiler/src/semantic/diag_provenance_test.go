package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// ---------------------------------------------------------------------------
// FactWithProvenance
// ---------------------------------------------------------------------------

func TestFactWithProvenanceString(t *testing.T) {
	cases := []struct {
		fact string
		prov FactProvenance
		want string
	}{
		{"0 <= i", FactProvenanceRangeBound, "0 <= i  (from: range bound)"},
		{"n > 0", FactProvenanceRequires, "n > 0  (from: requires)"},
		{"x < len(a)", FactProvenanceBranchGuard, "x < len(a)  (from: branch guard)"},
		{"v >= 0", FactProvenanceWhere, "v >= 0  (from: where refinement)"},
		{"k != 0", FactProvenanceAssert, "k != 0  (from: assert)"},
		{"q > 1", FactProvenanceUnknown, "q > 1  (from: unknown)"},
	}
	for _, tc := range cases {
		fp := FactWithProvenance{Fact: tc.fact, Provenance: tc.prov}
		got := fp.String()
		if got != tc.want {
			t.Errorf("FactWithProvenance{%q, %q}.String() = %q, want %q", tc.fact, tc.prov, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// proofDiagnosticWithProvenance.Format / FormatEnsure
// ---------------------------------------------------------------------------

func TestProofDiagnosticWithProvenanceFormat(t *testing.T) {
	d := proofDiagnosticWithProvenance{
		Goal: "n >= 0",
		RelevantFacts: []FactWithProvenance{
			{Fact: "n >= 0", Provenance: FactProvenanceRangeBound},
			{Fact: "x > 0", Provenance: FactProvenanceBranchGuard},
		},
		Suggestion:     "add `assert n >= 0` before the call",
		Counterexample: "n = -1",
	}
	out := d.Format("myFunc")

	checkContains(t, "Format", out, []string{
		`precondition of "myFunc" could not be proven statically at this call`,
		"goal: n >= 0",
		"known facts:",
		"n >= 0  (from: range bound)",
		"x > 0  (from: branch guard)",
		"suggestion: add `assert n >= 0` before the call",
		"it can fail when n = -1",
	})
}

func TestProofDiagnosticWithProvenanceFormatEnsure(t *testing.T) {
	d := proofDiagnosticWithProvenance{
		Goal: "result > 0",
		RelevantFacts: []FactWithProvenance{
			{Fact: "result > 0", Provenance: FactProvenanceRequires},
		},
		Suggestion: "strengthen the function body",
	}
	out := d.FormatEnsure("compute")

	checkContains(t, "FormatEnsure", out, []string{
		`ensure postcondition of "compute" could not be proven statically at this point`,
		"goal: result > 0",
		"result > 0  (from: requires)",
		"suggestion: strengthen the function body",
	})
}

func TestProofDiagnosticWithProvenanceNoFacts(t *testing.T) {
	d := proofDiagnosticWithProvenance{
		Goal:       "x >= 0",
		Suggestion: "add `assert x >= 0`",
	}
	out := d.Format("f")
	if strings.Contains(out, "known facts:") {
		t.Errorf("expected no 'known facts:' section when RelevantFacts is empty, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// classifySMTFactProvenance
// ---------------------------------------------------------------------------

func TestClassifySMTFactProvenanceRequires(t *testing.T) {
	sf := smtFact{
		Expr: &ast.Ident{Name: "n"},
		Deps: map[string]bool{"__requires__": true, "n": true},
	}
	got := classifySMTFactProvenance(sf)
	if got != FactProvenanceRequires {
		t.Errorf("got %q, want %q", got, FactProvenanceRequires)
	}
}

func TestClassifySMTFactProvenanceWhere(t *testing.T) {
	sf := smtFact{
		Expr: &ast.Ident{Name: "v"},
		Deps: map[string]bool{"__where__": true, "v": true},
	}
	got := classifySMTFactProvenance(sf)
	if got != FactProvenanceWhere {
		t.Errorf("got %q, want %q", got, FactProvenanceWhere)
	}
}

func TestClassifySMTFactProvenanceAssert(t *testing.T) {
	sf := smtFact{
		Expr: &ast.Ident{Name: "k"},
		Deps: map[string]bool{"__assert__": true, "k": true},
	}
	got := classifySMTFactProvenance(sf)
	if got != FactProvenanceAssert {
		t.Errorf("got %q, want %q", got, FactProvenanceAssert)
	}
}

func TestClassifySMTFactProvenanceBranchGuard_NoDeps(t *testing.T) {
	sf := smtFact{
		Expr: &ast.Ident{Name: "x"},
		Deps: nil,
	}
	got := classifySMTFactProvenance(sf)
	if got != FactProvenanceBranchGuard {
		t.Errorf("got %q, want %q", got, FactProvenanceBranchGuard)
	}
}

func TestClassifySMTFactProvenanceBranchGuard_NoSentinel(t *testing.T) {
	sf := smtFact{
		Expr: &ast.Ident{Name: "y"},
		Deps: map[string]bool{"y": true, "z": true},
	}
	got := classifySMTFactProvenance(sf)
	if got != FactProvenanceBranchGuard {
		t.Errorf("got %q, want %q", got, FactProvenanceBranchGuard)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func checkContains(t *testing.T, label, out string, substrings []string) {
	t.Helper()
	for _, s := range substrings {
		if !strings.Contains(out, s) {
			t.Errorf("%s output missing %q\ngot:\n%s", label, s, out)
		}
	}
}
