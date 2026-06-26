package semantic

import (
	"sort"
	"strings"
)

// hypothesisFact is the ONE common type for a logical hypothesis carried by the verifier. It unifies
// the previously divergent SMT-fact sources (per-scope integer range facts in `rangeFacts` and the
// flow-local assert facts in `smtAssertFacts`) behind a single contract:
//
//   - hypotheses(tr) renders the fact into zero or more SMT `(assert …)` lines for the hypothesis list.
//   - deps() reports the structural roots whose mutation invalidates the fact.
//
// Storage stays where it was (each scope still owns its `rangeFacts` map and `smtAssertFacts` slice —
// those are mutated in many places and rewriting them would not be behavior-preserving). The win is at
// the rendering and invalidation BOUNDARY: both kinds now expose one interface, a single renderer
// (renderHypothesisFacts) walks the scope chain identically for both, and a single dependency
// predicate (factInvalidatedBy) decides invalidation. This removes the divergence the audit flagged
// while keeping the produced hypotheses, the invalidated set, and the proof outcomes byte-identical.
//
// The interface intentionally leaves room for richer propositions (e.g. relational or quantified facts)
// to be added later by implementing the same two methods.
type hypothesisFact interface {
	// hypotheses renders this fact into SMT `(assert …)` lines (each newline-terminated). It may emit
	// zero lines (an open/empty range fact, or an assert whose expression cannot be translated).
	hypotheses(tr *smtTranslator) []string
	// deps reports the structural roots that, when mutated, invalidate this fact. A nil/empty map means
	// "no identifiable root" — a conservative caller clears such facts on any unattributable mutation.
	deps() map[string]bool
}

// rangeHypothesisFact wraps a single per-scope integer range fact (`name` bounded by `r`). Its only
// dependency is the subject variable itself: a range fact is a concrete interval snapshot with no live
// symbolic dependence on other variables (docs/90 brick 90-11), so only a write to `name` stales it.
type rangeHypothesisFact struct {
	name string
	r    numRange
}

func (f rangeHypothesisFact) hypotheses(tr *smtTranslator) []string {
	if tr == nil || (!f.r.loKnown && !f.r.hiKnown) {
		return nil
	}
	v := smtVar(f.name)
	tr.decls[f.name] = true
	var out []string
	if f.r.loKnown {
		out = append(out, "(assert (>= "+v+" "+smtInt(f.r.lo)+"))\n")
	}
	if f.r.hiKnown {
		out = append(out, "(assert (<= "+v+" "+smtInt(f.r.hi)+"))\n")
	}
	return out
}

func (f rangeHypothesisFact) deps() map[string]bool {
	if f.name == "" {
		return nil
	}
	return map[string]bool{f.name: true}
}

// assertHypothesisFact wraps a flow-local assert fact (a branch guard, proven invariant/assertion, or
// exact-assignment equality). It renders by translating its boolean expression and carries the dep roots
// already computed at record time.
type assertHypothesisFact struct {
	fact smtFact
}

func (f assertHypothesisFact) hypotheses(tr *smtTranslator) []string {
	if tr == nil || f.fact.Expr == nil {
		return nil
	}
	if h, ok := tr.boolTerm(f.fact.Expr, nil); ok {
		return []string{"(assert " + h + ")\n"}
	}
	return nil
}

func (f assertHypothesisFact) deps() map[string]bool { return f.fact.Deps }

// collectScopeRangeFacts yields a scope's range facts as unified hypothesis facts, in sorted name order.
// Sorting keeps the emitted hypothesis text (and thus the discharge cache key — brick 5) deterministic.
func collectScopeRangeFacts(sc *Scope) []hypothesisFact {
	if sc == nil || len(sc.rangeFacts) == 0 {
		return nil
	}
	names := make([]string, 0, len(sc.rangeFacts))
	for name := range sc.rangeFacts {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]hypothesisFact, 0, len(names))
	for _, name := range names {
		out = append(out, rangeHypothesisFact{name: name, r: sc.rangeFacts[name]})
	}
	return out
}

// collectScopeAssertFacts yields a scope's assert facts as unified hypothesis facts, in record order.
func collectScopeAssertFacts(sc *Scope) []hypothesisFact {
	if sc == nil || len(sc.smtAssertFacts) == 0 {
		return nil
	}
	out := make([]hypothesisFact, 0, len(sc.smtAssertFacts))
	for _, fact := range sc.smtAssertFacts {
		out = append(out, assertHypothesisFact{fact: fact})
	}
	return out
}

// renderHypothesisFacts walks the active scope chain (honoring the closed-world wall) and renders every
// fact produced by `collect` for each scope into one SMT hypothesis blob. This is the SINGLE rendering
// path both range-fact and assert-fact hypotheses now flow through. `dedupeByName` reproduces the
// range-fact shadowing rule (a closer scope's fact shadows an outer one) and must stay false for assert
// facts, which are emitted per-occurrence.
func (a *Analyzer) renderHypothesisFacts(tr *smtTranslator, collect func(*Scope) []hypothesisFact, dedupeByName bool) string {
	if a == nil || a.currentScope == nil || tr == nil {
		return ""
	}
	var b strings.Builder
	var seen map[string]bool
	if dedupeByName {
		seen = map[string]bool{}
	}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		closedHere := sc.closedWorld
		for _, f := range collect(sc) {
			if dedupeByName {
				if rf, ok := f.(rangeHypothesisFact); ok {
					if seen[rf.name] {
						continue // a closer scope's fact shadows an outer one
					}
					seen[rf.name] = true
				}
			}
			for _, line := range f.hypotheses(tr) {
				b.WriteString(line)
			}
		}
		if closedHere {
			break
		}
	}
	return b.String()
}

// factInvalidatedBy reports whether a fact's dependency roots intersect the mutated-root set. A fact with
// no identifiable roots is never invalidated here; conservative whole-clear is handled by callers.
func factInvalidatedBy(f hypothesisFact, mutationRoots map[string]bool) bool {
	d := f.deps()
	if len(d) == 0 {
		return false
	}
	return depsIntersect(d, mutationRoots)
}
