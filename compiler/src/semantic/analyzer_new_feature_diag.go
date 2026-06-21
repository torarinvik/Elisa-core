package semantic

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

// Diagnostic completeness for the recently-added verification features (docs/98 family).
//
// These helpers are ADVISORY ONLY: they run AFTER a proof has already declined and only enrich the
// message with a concrete witness. They never re-decide acceptance — only `unsat` concludes a proof,
// and nothing here can flip a declined obligation to accepted or vice versa.
//
// They cover the three features the existing counterexample/proof-hole machinery did not yet explain:
//  1. lexicographic `decreases (a, b)` — which component failed and its entry→call transition,
//  2. quantified struct invariants — the violating index/element witness (reuses the array-quantifier
//     counterexample extraction),
//  3. set/dict quantifiers `forall x in s` / `forall (k,v) in d` — a concrete violating element/key
//     witness (reuses the SMT model readback used for array quantifiers).

// lexicographicDecreaseDiagnostic explains why a lexicographic `decreases` measure tuple could not be
// proven to strictly descend at a recursive call. It identifies the FIRST component the lexicographic
// rule was forced to rely on (every strictly-earlier component proven unchanged) yet which did not
// strictly decrease, and renders the entry→call transition for that component as the witness.
//
// The transition is rendered with concrete integer values when a side const-folds (e.g. `m=3 -> m=3`);
// otherwise the symbolic forms are shown (e.g. `m -> m`). Reusing measureDiffIsZero / the affine prover
// keeps this consistent with the actual proof attempt — the component this names is exactly the one
// proveMeasureDecreases gave up on.
//
// Returns "" when no informative component can be singled out (the caller then omits the suffix).
func (a *Analyzer) lexicographicDecreaseDiagnostic(measures []ast.Expr, subst map[string]ast.Expr) string {
	measures = decreaseMeasureComponents(measures)
	if len(measures) == 0 {
		return ""
	}
	// The lexicographic rule decreases iff the FIRST component that is not provably unchanged strictly
	// decreases (and is bounded below). So the deciding component is the first one whose every strictly-
	// earlier component is provably unchanged AND which is not itself provably unchanged. That is the
	// component the descent rests on; if it fails to strictly decrease (or stay >= 0), it is the witness.
	// If every component is provably unchanged the measure simply does not move — report the last one.
	for k, measure := range measures {
		earlierUnchanged := true
		for j := 0; j < k; j++ {
			if !a.measureDiffIsZero(measures[j], subst) {
				earlierUnchanged = false
				break
			}
		}
		if !earlierUnchanged {
			continue
		}
		isLast := k == len(measures)-1
		// A provably-unchanged non-last component is a valid lexicographic "stay" — the descent is
		// deferred to a later component, so this one is not the failure. Skip it.
		if !isLast && a.measureDiffIsZero(measure, subst) {
			continue
		}
		// Deciding component. If it actually strictly decreases and is bounded, the real failure is
		// elsewhere (shouldn't happen when proveMeasureDecreases failed, but stay defensive).
		if a.measureStrictlyDecreases(measure, subst) && a.measureBoundedBelow(measure) {
			continue
		}
		comp := unparse.FormatExpr(measure)
		entry, call := a.measureTransitionStrings(measure, subst)
		reason := "non-decreasing"
		if a.measureStrictlyDecreases(measure, subst) && !a.measureBoundedBelow(measure) {
			reason = "not bounded below by 0"
		}
		if len(measures) == 1 {
			return "measure `" + comp + "` " + reason + " across the recursive call: " + entry + " -> " + call
		}
		return "lexicographic decreases: component `" + comp + "` " + reason + " across the recursive call: " + entry + " -> " + call
	}
	return ""
}

// measureTransitionStrings renders the entry-side and call-side values of a measure component as the
// `name=value` / `name` witness fragments. Concrete integers are used when a side const-folds (or its
// affine form pins a single value); otherwise the symbolic expression text is used.
func (a *Analyzer) measureTransitionStrings(measure ast.Expr, subst map[string]ast.Expr) (entry, call string) {
	comp := unparse.FormatExpr(measure)
	entry = comp
	if form, ok := a.affineOf(measure, a.currentScope); ok {
		if v, isConst := a.affineSingletonValue(form); isConst {
			entry = comp + "=" + strconv.FormatInt(v, 10)
		}
	}
	call = comp
	if callExpr, ok := substituteLemmaEnsure(measure, subst); ok {
		callText := unparse.FormatExpr(callExpr)
		call = callText
		if form, ok := a.substitutedAffine(measure, subst); ok {
			if v, isConst := a.affineSingletonValue(form); isConst {
				call = callText + "=" + strconv.FormatInt(v, 10)
			}
		}
	}
	return entry, call
}

// affineSingletonValue reports the single concrete value of an affine form — either it has no symbolic
// terms (a literal constant) or the current facts pin it to a point interval [v, v].
func (a *Analyzer) affineSingletonValue(form affineForm) (int64, bool) {
	allZero := true
	for _, coeff := range form.terms {
		if coeff != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return form.c, true
	}
	if r := a.boundAffine(form, a.currentScope); r.loKnown && r.hiKnown && r.lo == r.hi {
		return r.lo, true
	}
	return 0, false
}

// setDictQuantifierCounterexample renders a concrete violating element/key witness for a failed
// `forall x in s` (set) or `forall (k, v) in d` (dict) container quantifier, reusing the same SMT model
// readback used for array quantifiers (the auxDecls / ceExprs path). Advisory only — invoked only after
// the obligation has already declined.
//
// Returns "" when the goal is not a container `forall`, the body lies outside the SMT fragment, or the
// solver could not produce a model — the caller then falls back to the existing counterexample text.
func (a *Analyzer) setDictQuantifierCounterexample(tr *smtTranslator, goalExpr ast.Expr, env map[string]string, extraHyps string) string {
	q, ok := stripOptimizationParens(goalExpr).(*ast.QuantifierExpr)
	if !ok || q == nil || q.Exists || q.In == nil || len(q.Vars) != 1 || tr == nil {
		return ""
	}
	// Mirror boolTerm's `n.In` translation EXACTLY (the container is modeled as an array; the binder is
	// `(select arr idx)` over a free index). To obtain a concrete witness we leave that index FREE and
	// add the membership guard `0 <= idx < len`, then ask the solver for an assignment that violates the
	// body. The binder element value and the index are registered for counterexample readback. This is a
	// faithful witness for the same model the proof used — advisory only, never re-deciding the proof.
	arr, ok := tr.arrayTermEnv(q.In, env)
	if !ok {
		return ""
	}
	binder := q.Vars[0]
	idxSym := "q_" + binder + "_idx"
	elemTerm := "(select " + arr + " " + idxSym + ")"
	qenv := make(map[string]string, len(env)+1)
	for k, v := range env {
		qenv[k] = v
	}
	qenv[binder] = elemTerm
	tr.auxDecls = append(tr.auxDecls, "(declare-const "+idxSym+" Int)\n")
	// Report the violating element value under the binder's source name, and its index position.
	tr.ceExprs[binder] = elemTerm
	tr.ceExprs[binder+"@idx"] = idxSym
	tr.collectQuantifierWitnessSelects(q.Body, qenv)
	body, ok := tr.boolTerm(q.Body, qenv)
	if !ok {
		return ""
	}
	lenSym := arr + "_len"
	tr.lenDecls[lenSym] = true
	guard := "(and (<= 0 " + idxSym + ") (< " + idxSym + " " + lenSym + "))"
	// smtCheckQuery asserts `(not obligation)`. With obligation `(=> guard body)`, its negation is
	// `(and guard (not body))` — an IN-RANGE element that VIOLATES the body, i.e. the witness. The
	// returned SAT model names idxSym and the binder element value.
	obligation := "(=> " + guard + " " + body + ")"
	hyps := a.smtRequiresHypotheses(tr) + a.smtImmutableLocalHypotheses(tr) + a.smtAssertHypotheses(tr) + a.smtFlowFactHypotheses(tr) + extraHyps
	proven, ce := a.smtCheckQuery(tr, hyps, obligation)
	if proven {
		// No in-range element violates the body — no witness to report.
		return ""
	}
	return ce
}

// quantifierCounterexampleAny is the unified entry point: it tries the array-index quantifier extractor
// first, then the container (set/dict, `in`) extractor. Used by smtCheckGoal so EVERY quantifier shape
// — array indices (incl. `invariant forall i in self.items.indices: P`), set elements, and dict
// key/value pairs — gets a concrete witness on a decline. Advisory only.
func (a *Analyzer) quantifierCounterexampleAny(tr *smtTranslator, goalExpr ast.Expr, env map[string]string, extraHyps string) string {
	if ce := a.smtQuantifierCounterexample(tr, goalExpr, env, extraHyps); ce != "" {
		return ce
	}
	return a.setDictQuantifierCounterexample(tr, goalExpr, env, extraHyps)
}
