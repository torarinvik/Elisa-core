package semantic

import (
	"fmt"
	"os"
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/smt"
)

// ceOpElisa maps an SMT arithmetic operator to its Elisa infix symbol for readable labels.
func ceOpElisa(op string) string {
	switch op {
	case "div":
		return "/"
	case "mod":
		return "%"
	case "*":
		return "*"
	case "+":
		return "+"
	case "-":
		return "-"
	default:
		return ""
	}
}

// smtCheckGoal lowers a boolean obligation expression into the VC IR, then discharges it via the brick-4
// splitter: a conjunctive goal is proven conjunct-by-conjunct against a shared hypothesis context, and a
// goal that constant-folds to `true` concludes with NO solver call. The third return reports whether the
// goal could be lowered/translated at all (false ⇒ outside the fragment, the caller declines).
func (a *Analyzer) smtCheckGoal(tr *smtTranslator, goalExpr ast.Expr, env map[string]string, extraHyps string) (proven bool, counterexample string, lowered bool) {
	goal, ok := tr.lowerVCFormula(goalExpr, env)
	if !ok {
		// Generalize the goal's untranslatable propositional atoms (analyzer_vc_ir.go): proving the
		// goal for EVERY value of such an atom proves it for the atom's actual value.
		tr.generalizeGoalAtoms = true
		goal, ok = tr.lowerVCFormula(goalExpr, env)
		tr.generalizeGoalAtoms = false
		if !ok {
			return false, "", false
		}
	}
	savedGoalLine := a.smtLogGoalLine
	if goalExpr != nil {
		a.smtLogGoalLine = goalExpr.Pos().Line
	}
	p, ce := a.smtDischargeFormula(tr, goal, extraHyps)
	a.smtLogGoalLine = savedGoalLine
	if !p {
		if qce := a.quantifierCounterexampleAny(tr, goalExpr, env, extraHyps); qce != "" {
			ce = qce
		}
	}
	return p, ce, true
}

func (a *Analyzer) smtQuantifierCounterexample(tr *smtTranslator, goalExpr ast.Expr, env map[string]string, extraHyps string) string {
	q, ok := stripOptimizationParens(goalExpr).(*ast.QuantifierExpr)
	if !ok || q == nil || q.Exists || q.In != nil || len(q.Vars) == 0 {
		return ""
	}
	qenv := make(map[string]string, len(env)+len(q.Vars))
	for k, v := range env {
		qenv[k] = v
	}
	for _, v := range q.Vars {
		sym := "q_" + v
		qenv[v] = sym
		tr.auxDecls = append(tr.auxDecls, "(declare-const "+sym+" Int)\n")
		tr.ceExprs[v] = sym
	}
	tr.collectQuantifierWitnessSelects(q.Body, qenv)
	body, ok := tr.boolTerm(q.Body, qenv)
	if !ok {
		return ""
	}
	requires := a.smtRequiresHypotheses(tr)
	locals := a.smtImmutableLocalHypotheses(tr)
	others := a.smtAssertHypotheses(tr) + a.smtFlowFactHypotheses(tr) + a.smtProtocolLawHypotheses(tr) + a.smtNonNullRefParamHypotheses(tr) + extraHyps
	context := smtPreambleContext(tr.factPreamble()) + requires + others + body
	if len(tr.boolLocalRefs) != 0 {
		context = smtBoolMacroContext(tr, context)
		boolDemandContext := context + sliceImmutableLocalHypotheses(locals, context)
		locals += a.smtImmutableBoolLocalHypotheses(tr, boolDemandContext)
		context = smtPreambleContext(tr.factPreamble()) + requires + others + body
		context = smtBoolMacroContext(tr, context)
	}
	hyps := requires + sliceImmutableLocalHypotheses(locals, context) + others
	proven, ce := a.smtCheckQuery(tr, hyps, body)
	if proven {
		return ""
	}
	return ce
}

// smtDischargeFormulaWithHyps discharges a lowered VC-IR formula against a CALLER-SUPPLIED hypothesis
// block (already built from `tr`), rather than the analyzer's standard set. It is the loop-preservation
// prover's bridge into the central VC IR: that prover deliberately assumes ONLY the loop condition +
// invariants (loop variables free, no ambient assert/flow facts), so it cannot route through smtCheckVC
// (which layers the standard block). Routing through the IR gains the smart-constructor folder — a
// goal that constant-folds to `true` is valid under ANY hypotheses and concludes with NO solver call,
// and structural comparisons/arithmetic are normalized — while keeping emission byte-identical for
// every atom (opaque leaves emit their already-translated string verbatim), so the proof verdict is
// preserved for goals the folder does not touch.
func (a *Analyzer) smtDischargeFormulaWithHyps(tr *smtTranslator, goal vcFormula, hyps string) (bool, string) {
	if isVCTrue(goal) {
		a.smtStats.Attempts++
		a.smtStats.Proven++
		return true, ""
	}
	return a.smtCheckQuery(tr, hyps, emitVCFormula(goal))
}

// recordSMTAttempt preserves profile and trace accounting for cache hits and solver calls.
func (a *Analyzer) recordSMTAttempt(query string) {
	a.smtStats.Attempts++
	if a.smtTraceOn() {
		fmt.Fprintf(os.Stderr, "=== SMT query (%s)\n%s", a.currentFuncName(), query)
	}
}

// smtCheckQuery is the innermost discharge primitive: given the full hypothesis block (already built
// from `tr`) and an `obligation`, it lays out `factPreamble + hyps + (assert (not obligation))`, checks
// exact verdict caches before starting the solver, updates the stats, and returns proven (only on
// `unsat`) plus a counterexample on `sat`.
// smtCheckVC layers the standard hypothesis set on top; the loop-preservation prover uses this directly
// with its OWN hypothesis set (loop variables free, no ambient facts) — so the check protocol itself is
// shared while each site keeps control of which facts it assumes. `factPreamble()` is emitted last so
// every symbol the hypotheses and obligation introduced is declared.
func (a *Analyzer) smtCheckQuery(tr *smtTranslator, hyps string, obligation string) (bool, string) {
	return a.smtCheckQueryWithLogLabel(tr, hyps, obligation, "query", obligation)
}

func (a *Analyzer) smtCheckQueryWithLogLabel(tr *smtTranslator, hyps string, obligation string, label string, logObligation string) (bool, string) {
	if !a.smtEnabled || a.smtUnavailable {
		return false, ""
	}
	if tr == nil {
		// Preserve the legacy availability probe for an invalid translator, which
		// previously happened before this nil check.
		_ = a.openSMT()
		return false, ""
	}
	// A cached verdict still means the SMT tier handled this obligation, even when
	// no long-lived solver process needs to be started for the query.
	a.smtStats.Enabled = true
	goal := "(assert (not " + obligation + "))"
	query := tr.takeFactPreamble() + hyps + goal + "\n" + tr.counterexampleExprDefinitions()
	query = smtDeduplicateAssertions(query, goal)
	a.noteSMTHistory(query)
	key := a.smtVerdictKey(query)
	hit, ok, flight, proofKeys := a.smtCachedQuery(key)
	if ok {
		return hit.proven, hit.ce
	}
	defer a.releaseSMTFlight(key, flight)
	persistentHits := a.smtStats.PersistentCacheHits
	var solver smtSolverHandle
	var resourceBefore, transientBefore, snapshotBefore uint64
	var canCertify, snapshotClassified bool
	res, model, sharedProof := a.smtShareOuterProof(proofKeys, func() (smt.Result, map[string]string) {
		solver = a.openSMT()
		if solver == nil {
			return smt.Unknown, nil
		}
		a.recordSMTAttempt(query)
		a.smtSetLogLabelForQuery(solver, label, logObligation, query)
		resourceBefore, transientBefore, canCertify = smtUnknownCacheEpoch(solver)
		snapshotBefore, snapshotClassified = smtSnapshotUnknownEpoch(solver)
		return a.smtCheckSliced(solver, query, goal, tr.declaredSMTVars())
	})
	if solver == nil && !sharedProof {
		return false, ""
	}
	if sharedProof {
		a.recordSMTAttempt(query)
	}
	if res == smt.Unsat {
		a.smtStats.Proven++
		if !sharedProof && a.smtStats.PersistentCacheHits == persistentHits {
			a.smtStats.SolverProven++
		}
		a.cacheSMTQuery(key, smtQueryResult{proven: true})
		if !sharedProof {
			if key != query {
				a.smtProofCache.publish(query)
			}
			a.smtProofCache.publishProof(proofKeys)
		}
		return true, ""
	}
	a.smtStats.Declined++
	if res == smt.Sat {
		ce := tr.counterexample(model)
		a.cacheSMTQuery(key, smtQueryResult{proven: false, ce: ce})
		a.smtProofCache.publishDeclined(key, res, ce)
		return false, ce
	}
	resourceAfter, transientAfter, _ := smtUnknownCacheEpoch(solver)
	snapshotAfter, _ := smtSnapshotUnknownEpoch(solver)
	stableHistory := snapshotClassified && snapshotAfter > snapshotBefore
	resourceCertified := smtIndependent && canCertify && resourceAfter > resourceBefore && transientAfter == transientBefore && !stableHistory
	if resourceCertified {
		a.smtProofCache.publishResourceDeclined(key)
		a.cacheSMTQuery(key, smtQueryResult{proven: false})
	} else if smtIndependent && canCertify && stableHistory && transientAfter == transientBefore {
		// The whole program snapshot binds the history, unlike a query-only key.
		// Do not publish this decline into either query cache.
	} else {
		// Do not publish an analysis snapshot whose conservative fallback could
		// become stale when this query succeeds in a later process. The epochs
		// span the whole sliced/escalated check above, so a later resource-limited
		// attempt cannot conceal an earlier transient failure.
		markTransientSMT()
		a.smtSnapshotTainted = true
	}
	// Every other Unknown is retryable, including unclassified test/third-party solvers.

	return false, ""
}

// Equivalent complete obligations may be analyzed concurrently by different
// workers. Reuse only definitive proofs: SAT models are named for the original
// query and Unknown must remain retryable. The distinct flight namespace avoids
// nesting this outer check with the transformed-query flights it may invoke.
func (a *Analyzer) smtShareOuterProof(keys smtProofKeys, check func() (smt.Result, map[string]string)) (smt.Result, map[string]string, bool) {
	key := keys.alpha
	namespace := "\x00outer-alpha-proof\x00"
	if key == "" {
		key = keys.exact
		namespace = "\x00outer-exact-proof\x00"
	}
	if key == "" || a.parallel == nil || a.parallel.smtFlights == nil {
		result, model := check()
		return result, model, false
	}
	flightKey := namespace + key
	shared, hit, flight := a.awaitSharedSMTQuery(flightKey)
	if hit && shared.proven {
		a.smtStats.CacheHits++
		return smt.Unsat, nil, true
	}
	defer a.releaseSMTFlight(flightKey, flight)
	result, model := check()
	if result == smt.Unsat && flight != nil {
		a.resolveSMTFlight(flightKey, smtQueryResult{proven: true})
	}
	return result, model, false
}

func smtSnapshotUnknownEpoch(solver smt.Solver) (uint64, bool) {
	classified, ok := solver.(interface{ SnapshotUnknownEpoch() uint64 })
	if !ok {
		return 0, false
	}
	return classified.SnapshotUnknownEpoch(), true
}

func smtUnknownCacheEpoch(solver smt.Solver) (uint64, uint64, bool) {
	classified, ok := solver.(interface{ UnknownCacheEpoch() (uint64, uint64) })
	if !ok {
		return 0, 0, false
	}
	resource, transient := classified.UnknownCacheEpoch()
	return resource, transient, true
}

// smtEscalationFactor multiplies the per-query budget for an obligation whose failure would be
// reported (withSMTEscalation). Measured on the self-host: 8x proves 10 of the 12 queries that
// independent solving leaves undecided but a warm session proved.
const smtEscalationFactor = 8

// smtIndependent selects reset-per-check solving (smt.Options.Independent). ELISA_SMT_SESSION=1
// restores the shared push/pop session, for comparison only.
var smtIndependent = os.Getenv("ELISA_SMT_SESSION") != "1"

// smtVerdictKey names a query's verdict under the current escalation: an escalated attempt
// runs under a larger budget, so its verdict is cached separately.
func (a *Analyzer) smtVerdictKey(query string) string {
	if a.smtEscalation == 0 {
		return query
	}
	return query + "; escalated x" + strconv.Itoa(smtEscalationFactor) + "\n"
}

// smtHistoryLimit bounds the obligations a warm-session retry replays.
const smtHistoryLimit = 4096

// noteSMTHistory records an obligation query of the current body in asking order. The record
// does not depend on which verdicts were cached, so a retry replays the same session cold or warm.
func (a *Analyzer) noteSMTHistory(query string) {
	if a.smtHistoryFor != a.currentFuncDecl {
		a.smtHistory, a.smtHistoryFor = a.smtHistory[:0], a.currentFuncDecl
	}
	if a.smtEscalation == 0 && len(a.smtHistory) < smtHistoryLimit {
		a.smtHistory = append(a.smtHistory, query)
	}
}

// trySMTProveRequiresReported is trySMTProveRequires for an obligation that is reported when it
// stays unproven; only then is it retried under withSMTEscalation.
func (a *Analyzer) trySMTProveRequiresReported(clause ast.Expr, subst map[string]ast.Expr) (bool, string) {
	proven, counterexample := a.trySMTProveRequires(clause, subst)
	if !proven {
		a.withSMTEscalation(func() bool {
			proven, counterexample = a.trySMTProveRequires(clause, subst)
			return proven
		})
	}
	return proven, counterexample
}

// withSMTEscalation re-runs a failed obligation check with every full query that stays undecided
// retried under smtEscalationFactor times the budget. Callers use it only on the path that would
// report the obligation as unproven, so the larger budget is paid only where a proof matters.
func (a *Analyzer) withSMTEscalation(prove func() bool) bool {
	if !a.smtEnabled || a.smtUnavailable || a.smtEscalation > 0 {
		return false
	}
	a.smtEscalation++
	defer func() { a.smtEscalation-- }()
	return prove()
}

// smtCachedQuery returns a cached exact verdict, if available. Query layout is part of the cache key, so
// callers may use this for the historical raw-query form as well as the standard negate-and-check form.
func (a *Analyzer) smtCachedQuery(query string) (smtQueryResult, bool, *smtFlight, smtProofKeys) {
	// Query cache (brick 5): an identical query has the same deterministic verdict, so serve it without a
	// solver call. Attempts and Proven/Declined still count (the obligation was discharged), but no solver
	// time accrues — the cache hit is the whole point. SolverProven is left to mean "proven by an actual
	// solver run", so a cached proof does NOT bump it.
	if hit, ok := a.smtQueryCache[query]; ok {
		a.recordSMTAttempt(query)
		a.smtStats.CacheHits++
		if hit.proven {
			a.smtStats.Proven++
		} else {
			a.smtStats.Declined++
		}
		return hit, true, nil, smtProofKeys{}
	}
	hit, ok, flight := a.awaitSharedSMTQuery(query)
	if ok {
		a.recordSMTAttempt(query)
		a.smtStats.CacheHits++
		if hit.proven {
			a.smtStats.Proven++
		} else {
			a.smtStats.Declined++
		}
		a.cacheSMTQuery(query, hit)
		return hit, true, nil, smtProofKeys{}
	}
	if !a.smtProofCacheInitialized {
		a.smtProofCacheInitialized = true
		a.smtProofCache = newSMTProofCache(os.Getenv("ELISACORE_SMT_CACHE_DIR"), a.smtBinary)
	}
	proofKeys, proven := a.smtProofCache.lookupProof(query)
	if proven {
		a.recordSMTAttempt(query)
		a.smtStats.CacheHits++
		a.smtStats.PersistentCacheHits++
		a.smtStats.Proven++
		result := smtQueryResult{proven: true}
		a.cacheSMTQuery(query, result)
		return result, true, nil, smtProofKeys{}
	}
	if ce, ok := a.smtProofCache.declined(query); ok {
		a.recordSMTAttempt(query)
		a.smtStats.CacheHits++
		a.smtStats.PersistentCacheHits++
		a.smtStats.Declined++
		result := smtQueryResult{proven: false, ce: ce}
		a.cacheSMTQuery(query, result)
		return result, true, nil, smtProofKeys{}
	}
	return smtQueryResult{}, false, flight, proofKeys
}

// smtCheckRawQueryWithLogLabel preserves checks whose historical query was a satisfiability test rather
// than a negated obligation. It shares the exact-query caches and accounting but calls Solver.Check
// directly, so special checks such as requires-vacuity retain their original query and resource budget.
func (a *Analyzer) smtCheckRawQueryWithLogLabel(query string, label string, logObligation string) (bool, string) {
	if !a.smtEnabled || a.smtUnavailable {
		return false, ""
	}
	a.smtStats.Enabled = true
	hit, ok, flight, proofKeys := a.smtCachedQuery(query)
	if ok {
		return hit.proven, hit.ce
	}
	defer a.releaseSMTFlight(query, flight)
	solver := a.openSMT()
	if solver == nil {
		return false, ""
	}
	a.recordSMTAttempt(query)
	a.smtSetLogLabel(solver, label, logObligation)
	res, _ := solver.Check(query)
	if res == smt.Unsat {
		a.smtStats.Proven++
		a.smtStats.SolverProven++
		a.cacheSMTQuery(query, smtQueryResult{proven: true})
		a.smtProofCache.publishProof(proofKeys)
		return true, ""
	}
	if res == smt.Sat {
		a.smtStats.Declined++
		// Raw-query SAT checks do not collect a model. Do not cache this decline: the same SMT-LIB
		// text can also be a regular negated obligation whose diagnostic needs a counterexample.
		return false, ""
	}
	a.smtStats.Declined++
	if res == smt.Unknown {
		// Raw checks do not cache Unknown, even when the solver reports a
		// resource limit, so a retained snapshot must retry this analysis.
		markTransientSMT()
		a.smtSnapshotTainted = true
	}
	return false, ""
}

// smtQueryResult is a memoized solver verdict: whether the obligation was proven, plus the
// counterexample text rendered on a `sat` decline (empty otherwise).
type smtQueryResult struct {
	proven bool
	ce     string
}

func (a *Analyzer) cacheSMTQuery(query string, result smtQueryResult) {
	if a.smtQueryCache == nil {
		a.smtQueryCache = map[string]smtQueryResult{}
	}
	a.smtQueryCache[query] = result
	a.resolveSMTFlight(query, result)
}

// trySMTProveRequires discharges a precondition clause with the solver after the linear clause prover
// declined. The clause references the callee's parameters; each is translated to its caller argument
// term (populating the caller's free variables), and the clause obligation is checked against the
// caller's facts. Returns true only on `unsat` of the negation (a sound proof).
func (a *Analyzer) trySMTProveRequires(clause ast.Expr, subst map[string]ast.Expr) (bool, string) {
	return a.trySMTProveRequiresWithPath(clause, subst, nil)
}

// trySMTProveRequiresWithPath proves a clause under the enclosing function contract plus an optional
// pure control-flow path. The extra path is used for recursive termination calls collected before
// ordinary body analysis has seeded branch facts into the live scope.
func (a *Analyzer) trySMTProveRequiresWithPath(clause ast.Expr, subst map[string]ast.Expr, path ast.Expr, formalParamSets ...[]ast.ParamDecl) (bool, string) {
	proven, counterexample, _ := a.trySMTProveRequiresCore(clause, subst, path, formalParamSets...)
	return proven, counterexample
}

// trySMTProveCallRequires proves one callee `requires` clause at an ordinary call site. It keeps the
// established all-arguments binding; only when that binding declines (some argument the clause never
// mentions lies outside the SMT fragment, e.g. a byte read of a view) does it retry with the selective
// binding that recursive obligations use. The retry receives the callee's full formal list, so a
// formal the clause mentions but the call does not bind still declines rather than resolving to a
// same-named caller variable; unmentioned formals cannot affect the clause's value.
func (a *Analyzer) trySMTProveCallRequires(clause ast.Expr, subst map[string]ast.Expr, path ast.Expr, params []ast.ParamDecl, callee *ast.FuncDecl) (bool, string) {
	proven, counterexample, envDeclined := a.trySMTProveRequiresCoreWithCallee(clause, subst, path, callee)
	if proven || !envDeclined || len(params) == 0 {
		return proven, counterexample
	}
	proven, counterexample, _ = a.trySMTProveRequiresCoreWithCallee(clause, subst, path, callee, params)
	return proven, counterexample
}

// trySMTProveRequiresCore is trySMTProveRequiresWithPath that also reports whether the argument
// binding (not the solver) declined.
func (a *Analyzer) trySMTProveRequiresCore(clause ast.Expr, subst map[string]ast.Expr, path ast.Expr, formalParamSets ...[]ast.ParamDecl) (bool, string, bool) {
	return a.trySMTProveRequiresCoreWithCallee(clause, subst, path, nil, formalParamSets...)
}
