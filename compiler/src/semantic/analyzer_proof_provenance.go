package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"fmt"
	"sort"
	"strings"
)

// FactProvenance describes which part of the program contributed a known fact.
type FactProvenance string

const (
	// FactProvenanceRequires means the fact originates from a `requires` clause recorded as a
	// hypothesis after proving it at the call site.
	FactProvenanceRequires FactProvenance = "requires"
	// FactProvenanceWhere means the fact originates from a `where` refinement predicate on a type
	// or named refinement alias.
	FactProvenanceWhere FactProvenance = "where refinement"
	// FactProvenanceBranchGuard means the fact is a branch condition (if/while/assert guard) that
	// is known to hold on the current control-flow path.
	FactProvenanceBranchGuard FactProvenance = "branch guard"
	// FactProvenanceRangeBound means the fact is a numeric range bound inferred from a loop bound
	// or comparison guard.
	FactProvenanceRangeBound FactProvenance = "range bound"
	// FactProvenanceAssert means the fact was explicitly stated via an `assert` statement.
	FactProvenanceAssert FactProvenance = "assert"
	// FactProvenanceUnknown is the fallback when the origin cannot be determined.
	FactProvenanceUnknown FactProvenance = "unknown"
)

// FactWithProvenance pairs a rendered fact string with its FactProvenance tag.
type FactWithProvenance struct {
	Fact       string
	Provenance FactProvenance
}

// String renders the pair in the canonical diagnostic form: `FACT  (from: PROVENANCE)`.
func (fp FactWithProvenance) String() string {
	return fmt.Sprintf("%s  (from: %s)", fp.Fact, fp.Provenance)
}

// proofDiagnosticWithProvenance is like proofDiagnostic but each known fact carries an explicit
// FactProvenance tag identifying which clause or guard produced it.
// Constructed by buildRequiresFailureDiagnosticWithProvenance /
// buildEnsureFailureDiagnosticWithProvenance; the existing proofDiagnostic variants are unchanged.
type proofDiagnosticWithProvenance struct {
	Goal           string
	RelevantFacts  []FactWithProvenance
	Suggestion     string
	Counterexample string
}

// Format returns a multi-line human-readable string identical in structure to proofDiagnostic.Format
// but with each fact annotated `(from: PROVENANCE)`.
func (d proofDiagnosticWithProvenance) Format(declName string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("precondition of %q could not be proven statically at this call\n", declName))
	b.WriteString(fmt.Sprintf("  goal: %s\n", d.Goal))
	if len(d.RelevantFacts) > 0 {
		b.WriteString("  known facts:\n")
		for _, f := range d.RelevantFacts {
			b.WriteString(fmt.Sprintf("    - %s\n", f.String()))
		}
	}
	b.WriteString(fmt.Sprintf("  suggestion: %s", d.Suggestion))
	if d.Counterexample != "" {
		b.WriteString(fmt.Sprintf("\n  (it can fail when %s)", d.Counterexample))
	}
	return b.String()
}

// FormatEnsure is the ensure/postcondition variant of Format for proofDiagnosticWithProvenance.
func (d proofDiagnosticWithProvenance) FormatEnsure(funcName string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("ensure postcondition of %q could not be proven statically at this point\n", funcName))
	b.WriteString(fmt.Sprintf("  goal: %s\n", d.Goal))
	if len(d.RelevantFacts) > 0 {
		b.WriteString("  known facts:\n")
		for _, f := range d.RelevantFacts {
			b.WriteString(fmt.Sprintf("    - %s\n", f.String()))
		}
	}
	b.WriteString(fmt.Sprintf("  suggestion: %s", d.Suggestion))
	if d.Counterexample != "" {
		b.WriteString(fmt.Sprintf("\n  (it can fail when %s)", d.Counterexample))
	}
	return b.String()
}

// buildRequiresFailureDiagnosticWithProvenance is the provenance-annotated variant of
// buildRequiresFailureDiagnostic.  It collects the same facts but tags each one with a
// FactProvenance describing its origin.  The existing buildRequiresFailureDiagnostic is unchanged.
//
// Provenance assignment:
//   - Range facts (from the flow prover's numRange lattice) → FactProvenanceRangeBound.
//   - SMT assert facts → heuristic classification via classifySMTFactProvenance.
func (a *Analyzer) buildRequiresFailureDiagnosticWithProvenance(req ast.Expr, subst map[string]ast.Expr, counterexample string) proofDiagnosticWithProvenance {
	goal := renderSubstitutedGoal(req, subst)
	goalVars := collectExprIdents(req, subst)

	var facts []FactWithProvenance
	seen := map[string]bool{}

	// Range facts.
	rangeFacts := a.visibleRangeFacts()
	for _, name := range sortedRangeFactNames(rangeFacts) {
		r := rangeFacts[name]
		if !goalVars[name] {
			continue
		}
		rendered := renderRangeFact(name, r)
		if rendered != "" && !seen[rendered] {
			facts = append(facts, FactWithProvenance{Fact: rendered, Provenance: FactProvenanceRangeBound})
			seen[rendered] = true
		}
		if len(facts) >= 5 {
			break
		}
	}

	// SMT assert facts with heuristic provenance tagging.
	if len(facts) < 5 && a.currentScope != nil {
		for sc := a.currentScope; sc != nil; sc = sc.Parent {
			for _, sf := range sc.smtAssertFacts {
				if sf.Expr == nil {
					continue
				}
				factVars := smtFactVars(sf)
				relevant := false
				for v := range factVars {
					if goalVars[v] {
						relevant = true
						break
					}
				}
				if !relevant {
					continue
				}
				rendered := unparse.FormatExpr(sf.Expr)
				if rendered != "" && !seen[rendered] {
					prov := classifySMTFactProvenance(sf)
					facts = append(facts, FactWithProvenance{Fact: rendered, Provenance: prov})
					seen[rendered] = true
				}
				if len(facts) >= 5 {
					break
				}
			}
			if sc.closedWorld {
				break
			}
			if len(facts) >= 5 {
				break
			}
		}
	}

	suggestion := buildSuggestion(req, subst, counterexample)
	return proofDiagnosticWithProvenance{
		Goal:           goal,
		RelevantFacts:  facts,
		Suggestion:     suggestion,
		Counterexample: counterexample,
	}
}

// buildEnsureFailureDiagnosticWithProvenance is the provenance-annotated variant of
// buildEnsureFailureDiagnostic.  The existing function is unchanged.
func (a *Analyzer) buildEnsureFailureDiagnosticWithProvenance(clause ast.Expr, subst map[string]ast.Expr, counterexample string) proofDiagnosticWithProvenance {
	goal := renderSubstitutedGoal(clause, subst)
	goalVars := collectExprIdents(clause, subst)

	var facts []FactWithProvenance
	seen := map[string]bool{}

	rangeFacts := a.visibleRangeFacts()
	for _, name := range sortedRangeFactNames(rangeFacts) {
		r := rangeFacts[name]
		if !goalVars[name] {
			continue
		}
		rendered := renderRangeFact(name, r)
		if rendered != "" && !seen[rendered] {
			facts = append(facts, FactWithProvenance{Fact: rendered, Provenance: FactProvenanceRangeBound})
			seen[rendered] = true
		}
		if len(facts) >= 5 {
			break
		}
	}

	if len(facts) < 5 && a.currentScope != nil {
		for sc := a.currentScope; sc != nil; sc = sc.Parent {
			for _, sf := range sc.smtAssertFacts {
				if sf.Expr == nil {
					continue
				}
				factVars := smtFactVars(sf)
				relevant := false
				for v := range factVars {
					if goalVars[v] {
						relevant = true
						break
					}
				}
				if !relevant {
					continue
				}
				rendered := unparse.FormatExpr(sf.Expr)
				if rendered != "" && !seen[rendered] {
					prov := classifySMTFactProvenance(sf)
					facts = append(facts, FactWithProvenance{Fact: rendered, Provenance: prov})
					seen[rendered] = true
				}
				if len(facts) >= 5 {
					break
				}
			}
			if sc.closedWorld {
				break
			}
			if len(facts) >= 5 {
				break
			}
		}
	}

	suggestion := buildEnsureSuggestion(clause, subst, counterexample)
	return proofDiagnosticWithProvenance{
		Goal:           goal,
		RelevantFacts:  facts,
		Suggestion:     suggestion,
		Counterexample: counterexample,
	}
}

// classifySMTFactProvenance applies a heuristic to tag an smtFact with its most likely origin.
//
// Heuristic rules (applied in order):
//  1. A `requires` hypothesis pushed by smtAssertHypotheses after a proven call is typically a
//     BinaryExpr whose top-level structure matches a `requires` predicate.  We detect this by
//     checking if the Deps map contains the sentinel key "__requires__".
//  2. A fact injected by the `where`-refinement machinery carries "__where__" in Deps.
//  3. An explicit `assert` statement-fact carries "__assert__" in Deps.
//  4. Everything else is classified as a branch guard (the dominant source of SMT facts).
func classifySMTFactProvenance(sf smtFact) FactProvenance {
	if sf.Deps != nil {
		if sf.Deps["__requires__"] {
			return FactProvenanceRequires
		}
		if sf.Deps["__where__"] {
			return FactProvenanceWhere
		}
		if sf.Deps["__assert__"] {
			return FactProvenanceAssert
		}
	}
	return FactProvenanceBranchGuard
}

// ---------------------------------------------------------------------------

// assertCallForm renders the runtime-assert CALL that seeds the prover: `assert(<goal>)`. It must be
// the parenthesized call form — a bare `assert COND` parses as a static (compile-time) assertion and
// does NOT inject a fact. A single fully-enclosing paren layer on the goal (added by the expression
// unparser) is stripped so the result reads `assert(x >= 0)`, not `assert((x >= 0))`.
func assertCallForm(goal string) string {
	return "assert(" + stripOuterParens(goal) + ")"
}

// stripOuterParens removes one paren pair iff it fully encloses the whole string; otherwise returns
// the input unchanged (e.g. `(a) + (b)` is left alone).
func stripOuterParens(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '(' || s[len(s)-1] != ')' {
		return s
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && i != len(s)-1 {
				return s // the opening paren closes before the end: not fully enclosing
			}
		}
	}
	return strings.TrimSpace(s[1 : len(s)-1])
}

// buildSuggestion returns a concrete, actionable suggestion for fixing the unprovable obligation.
func buildSuggestion(req ast.Expr, subst map[string]ast.Expr, counterexample string) string {
	// Build the caller-side goal text for the suggestion.
	goal := renderSubstitutedGoal(req, subst)

	// If there's a counterexample, suggest narrowing the caller inputs.
	if counterexample != "" {
		return fmt.Sprintf("add `%s` before the call to rule out the failing case, or strengthen the precondition of the caller", assertCallForm(goal))
	}
	// Generic: suggest asserting the goal or adding a requires to the caller.
	return fmt.Sprintf("add `%s` before the call to make the fact visible to the prover, or add `requires %s` to the calling function", assertCallForm(goal), goal)
}

// sortedRangeFactNames returns the subjects of a visibleRangeFacts map in name order.
// The known-facts lists above are capped (5 entries), so walking the map directly made
// both the ORDER and the SELECTION of facts in a proof diagnostic vary run to run.
func sortedRangeFactNames(facts map[string]numRange) []string {
	names := make([]string, 0, len(facts))
	for name := range facts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
