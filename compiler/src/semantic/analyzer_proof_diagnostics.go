package semantic

import (
	"fmt"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

// proofDiagnostic holds the components of a rich, human-readable proof-failure diagnostic.
// It is constructed by buildRequiresFailureDiagnostic and consumed by the caller to emit
// the final proofLint message.
type proofDiagnostic struct {
	// Goal is the rendered precondition that could not be proven (after parameter substitution).
	Goal string
	// RelevantFacts is a short (≤5) list of known scope facts that mention at least one variable
	// appearing in the goal. Best-effort, may be empty.
	RelevantFacts []string
	// Suggestion is a concrete actionable hint (e.g. "add `assert n >= 0` before the call").
	Suggestion string
	// Counterexample is the optional SMT counterexample string (may be "").
	Counterexample string
}

// Format returns a multi-line human-readable string for use in a proofLint/errorf call.
// The first line is the primary message; subsequent lines are indented detail lines.
func (d proofDiagnostic) Format(declName string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("precondition of %q could not be proven statically at this call\n", declName))
	b.WriteString(fmt.Sprintf("  goal: %s\n", d.Goal))
	if len(d.RelevantFacts) > 0 {
		b.WriteString("  known facts:\n")
		for _, f := range d.RelevantFacts {
			b.WriteString(fmt.Sprintf("    - %s\n", f))
		}
	}
	b.WriteString(fmt.Sprintf("  suggestion: %s", d.Suggestion))
	if d.Counterexample != "" {
		b.WriteString(fmt.Sprintf("\n  (it can fail when %s)", d.Counterexample))
	}
	return b.String()
}

// buildRequiresFailureDiagnostic constructs a rich proof-failure diagnostic for an unprovable
// `requires` clause. It renders the goal under the caller's argument substitution, collects a
// short list of scope facts (range facts + SMT assert facts) that touch the goal's variables,
// and proposes a concrete fix. This is purely diagnostic — it changes nothing about the proof
// outcome, raises no new errors, and performs no solving.
func (a *Analyzer) buildRequiresFailureDiagnostic(req ast.Expr, subst map[string]ast.Expr, counterexample string) proofDiagnostic {
	// 1. Render the goal after substituting callee param names with caller argument text.
	goal := renderSubstitutedGoal(req, subst)

	// 2. Collect variables that appear in the goal so we can filter relevant facts.
	goalVars := collectExprIdents(req, subst)

	// 3. Gather relevant range facts.
	var facts []string
	seen := map[string]bool{}
	rangeFacts := a.visibleRangeFacts()
	for name, r := range rangeFacts {
		if !goalVars[name] {
			continue
		}
		rendered := renderRangeFact(name, r)
		if rendered != "" && !seen[rendered] {
			facts = append(facts, rendered)
			seen[rendered] = true
		}
		if len(facts) >= 5 {
			break
		}
	}

	// 4. Gather relevant SMT assert facts (branch conditions, asserts) up to the cap.
	if len(facts) < 5 && a.currentScope != nil {
		for sc := a.currentScope; sc != nil; sc = sc.Parent {
			for _, sf := range sc.smtAssertFacts {
				if sf.Expr == nil {
					continue
				}
				// Include only if the fact references at least one goal variable.
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
					facts = append(facts, rendered)
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

	// 5. Propose a suggestion.
	suggestion := buildSuggestion(req, subst, counterexample)

	return proofDiagnostic{
		Goal:           goal,
		RelevantFacts:  facts,
		Suggestion:     suggestion,
		Counterexample: counterexample,
	}
}

// renderSubstitutedGoal renders the requires clause with callee param names replaced by the
// caller's argument text, so the diagnostic shows the caller-side form (e.g. `n >= 0` instead
// of `off >= 0`).
func renderSubstitutedGoal(expr ast.Expr, subst map[string]ast.Expr) string {
	if expr == nil {
		return "<nil>"
	}
	// Build a textual substitution: replace param idents with their argument text.
	raw := unparse.FormatExpr(expr)
	for paramName, argExpr := range subst {
		if argExpr == nil {
			continue
		}
		argText := unparse.FormatExpr(argExpr)
		// Simple word-boundary replacement to avoid partial matches.
		raw = replaceIdentInText(raw, paramName, argText)
	}
	return raw
}

// replaceIdentInText replaces whole-word occurrences of `old` with `new` in `s`.
func replaceIdentInText(s, old, newText string) string {
	if old == "" {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], old) {
			before := i > 0 && isIdentChar(s[i-1])
			after := i+len(old) < len(s) && isIdentChar(s[i+len(old)])
			if !before && !after {
				b.WriteString(newText)
				i += len(old)
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// collectExprIdents returns the set of identifier names that appear in expr (after resolving
// substituted parameter names to the argument expressions' identifiers).
func collectExprIdents(expr ast.Expr, subst map[string]ast.Expr) map[string]bool {
	out := map[string]bool{}
	collectIdents(expr, subst, out)
	return out
}

func collectIdents(expr ast.Expr, subst map[string]ast.Expr, out map[string]bool) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.Ident:
		if arg, ok := subst[n.Name]; ok {
			// This ident is a parameter; collect the arg's idents instead.
			collectIdents(arg, nil, out)
		} else {
			out[n.Name] = true
		}
	case *ast.ParenExpr:
		collectIdents(n.Inner, subst, out)
	case *ast.BinaryExpr:
		collectIdents(n.Left, subst, out)
		collectIdents(n.Right, subst, out)
	case *ast.UnaryExpr:
		collectIdents(n.Operand, subst, out)
	case *ast.FieldExpr:
		collectIdents(n.Object, subst, out)
	}
}

// renderRangeFact formats a numRange fact for a named variable into a readable bound string.
func renderRangeFact(name string, r numRange) string {
	switch {
	case r.loKnown && r.hiKnown:
		if r.lo == r.hi {
			return fmt.Sprintf("%s == %d", name, r.lo)
		}
		return fmt.Sprintf("%d <= %s <= %d", r.lo, name, r.hi)
	case r.loKnown:
		return fmt.Sprintf("%s >= %d", name, r.lo)
	case r.hiKnown:
		return fmt.Sprintf("%s <= %d", name, r.hi)
	default:
		return ""
	}
}

// smtFactVars returns the set of identifier names referenced by an smtFact's Deps map.
func smtFactVars(sf smtFact) map[string]bool {
	if sf.Deps != nil {
		return sf.Deps
	}
	// Fall back to scanning the expression.
	out := map[string]bool{}
	collectIdents(sf.Expr, nil, out)
	return out
}

// FormatEnsure returns a multi-line human-readable string for an unprovable postcondition,
// framed for `ensure` (goal over result/old) rather than a precondition at a call site.
func (d proofDiagnostic) FormatEnsure(funcName string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("ensure postcondition of %q could not be proven statically at this point\n", funcName))
	b.WriteString(fmt.Sprintf("  goal: %s\n", d.Goal))
	if len(d.RelevantFacts) > 0 {
		b.WriteString("  known facts:\n")
		for _, f := range d.RelevantFacts {
			b.WriteString(fmt.Sprintf("    - %s\n", f))
		}
	}
	b.WriteString(fmt.Sprintf("  suggestion: %s", d.Suggestion))
	if d.Counterexample != "" {
		b.WriteString(fmt.Sprintf("\n  (it can fail when %s)", d.Counterexample))
	}
	return b.String()
}

// buildEnsureFailureDiagnostic constructs a rich proof-failure diagnostic for an unprovable
// `ensure` clause. It renders the postcondition goal (with `result` substituted by the return
// expression where available), collects a short list of scope facts that touch the goal's
// variables, and proposes a concrete fix. Like buildRequiresFailureDiagnostic this is purely
// diagnostic — it changes nothing about the proof outcome and performs no solving.
func (a *Analyzer) buildEnsureFailureDiagnostic(clause ast.Expr, subst map[string]ast.Expr, counterexample string) proofDiagnostic {
	// 1. Render the goal with result/old substituted so the diagnostic shows the return-side form.
	goal := renderSubstitutedGoal(clause, subst)

	// 2. Collect variables that appear in the goal.
	goalVars := collectExprIdents(clause, subst)

	// 3. Gather relevant range facts.
	var facts []string
	seen := map[string]bool{}
	rangeFacts := a.visibleRangeFacts()
	for name, r := range rangeFacts {
		if !goalVars[name] {
			continue
		}
		rendered := renderRangeFact(name, r)
		if rendered != "" && !seen[rendered] {
			facts = append(facts, rendered)
			seen[rendered] = true
		}
		if len(facts) >= 5 {
			break
		}
	}

	// 4. Gather relevant SMT assert facts (branch conditions, asserts) up to the cap.
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
					facts = append(facts, rendered)
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

	// 5. Propose a suggestion framed for postconditions.
	suggestion := buildEnsureSuggestion(clause, subst, counterexample)

	return proofDiagnostic{
		Goal:           goal,
		RelevantFacts:  facts,
		Suggestion:     suggestion,
		Counterexample: counterexample,
	}
}

// buildEnsureSuggestion returns a concrete, actionable suggestion for fixing an unprovable
// ensure/postcondition obligation.
func buildEnsureSuggestion(clause ast.Expr, subst map[string]ast.Expr, counterexample string) string {
	goal := renderSubstitutedGoal(clause, subst)
	if counterexample != "" {
		return fmt.Sprintf("strengthen the function body so %q holds on all return paths, or add `requires` bounds to rule out the failing case", goal)
	}
	return fmt.Sprintf("add `assert %s` before the return to surface the fact to the prover, or strengthen the `requires` preconditions so the postcondition is reachable", goal)
}

// ---------------------------------------------------------------------------
// Provenance-annotated diagnostics (additive — does NOT modify existing fns)
// ---------------------------------------------------------------------------

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
	for name, r := range rangeFacts {
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
	for name, r := range rangeFacts {
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

// buildSuggestion returns a concrete, actionable suggestion for fixing the unprovable obligation.
func buildSuggestion(req ast.Expr, subst map[string]ast.Expr, counterexample string) string {
	// Build the caller-side goal text for the suggestion.
	goal := renderSubstitutedGoal(req, subst)

	// If there's a counterexample, suggest narrowing the caller inputs.
	if counterexample != "" {
		return fmt.Sprintf("add `assert %s` before the call to rule out the failing case, or strengthen the precondition of the caller", goal)
	}
	// Generic: suggest asserting the goal or adding a requires to the caller.
	return fmt.Sprintf("add `assert %s` before the call to make the fact visible to the prover, or add `requires %s` to the calling function", goal, goal)
}
