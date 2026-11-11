package semantic

import (
	"crypto/sha256"
	"elisacore/src/smt"
	"encoding/hex"
	"strings"
)

// smtDeadPathBudget bounds the side check that the hypotheses outside the goal's cone are
// contradictory on their own (z3 rlimit units).
const smtDeadPathBudget = 20000

// smtVerdict names the solver result type for analyzer fields declared outside this file.
type smtVerdict = smt.Result

type smtBudgetedSolver interface {
	CheckBudget(assertions string, rlimit int64) smt.Result
	CheckBudgetValues(assertions string, vars []string, rlimit int64) (smt.Result, map[string]string)
}

// smtAfterSatAbstractionBudget bounds the full Int<->BV query once its bit-vector abstraction
// is definitively SAT: the abstraction already has a model, so only real bit semantics beyond
// the AND bound lemma could still prove the goal. A lower budget only forgoes such proofs.
const smtAfterSatAbstractionBudget = 50000

// smtCheckTransformedProof reuses only definitive UNSAT for a complete transformed
// query. SAT models and Unknown results must follow the existing solver path.
// The outer query initializes the cache; nil keeps caching fully disabled.
func (a *Analyzer) smtCheckTransformedProof(query string, check func() (smt.Result, map[string]string)) (smt.Result, map[string]string) {
	return a.smtCheckTransformedVerdict(query, false, check)
}

// A model-free caller uses SAT only as a boolean and never consumes its model.
// It can therefore share SAT across alpha-equivalent queries without remapping
// model names. Other transformed-query callers share only UNSAT.
func (a *Analyzer) smtCheckTransformedVerdict(query string, shareSat bool, check func() (smt.Result, map[string]string)) (smt.Result, map[string]string) {
	var exactFlight *smtFlight
	var exactFlightKey string
	var keys smtProofKeys
	var hit bool
	if a.smtProofCache != nil {
		exact := a.smtProofCache.key(query)
		if a.parallel != nil && a.parallel.smtFlights != nil {
			exactFlightKey = "\x00transformed-early-exact\x00" + exact
			if shareSat {
				exactFlightKey = "\x00model-free\x00" + exactFlightKey
			}
			shared, found, owned := a.awaitSharedSMTQuery(exactFlightKey)
			if found {
				a.smtStats.CacheHits++
				if shared.proven {
					return smt.Unsat, nil
				}
				if shareSat {
					return smt.Sat, nil
				}
			}
			exactFlight = owned
			defer a.releaseSMTFlight(exactFlightKey, exactFlight)
		}
		keys, hit = a.smtProofCache.lookupProofWithExact(query, exact)
	}
	resolveExact := func(result smt.Result) {
		if exactFlight != nil && (result == smt.Unsat || shareSat && result == smt.Sat) {
			a.resolveSMTFlight(exactFlightKey, smtQueryResult{proven: result == smt.Unsat})
		}
	}
	if hit {
		a.smtStats.CacheHits++
		a.smtStats.PersistentCacheHits++
		resolveExact(smt.Unsat)
		return smt.Unsat, nil
	}
	// Distinct outer queries can produce the same transformed obligation.
	// Share only definitive proofs; SAT models and Unknown remain local.
	var flightKey string
	var flight *smtFlight
	if a.smtProofCache != nil && a.parallel != nil && a.parallel.smtFlights != nil {
		// The proof cache already validates alpha-equivalence for UNSAT records.
		// Share that same key when available; otherwise use the exact digest.
		// Separate namespaces prevent an exact key being interpreted as alpha.
		if keys.alpha != "" {
			flightKey = "\x00transformed-alpha\x00" + keys.alpha
		} else {
			flightKey = "\x00transformed-unsat\x00" + keys.exact
		}
		if shareSat {
			flightKey = "\x00weakening-verdict\x00" + flightKey
		}
		shared, ok, owned := a.awaitSharedSMTQuery(flightKey)
		if ok {
			a.smtStats.CacheHits++
			if shared.proven {
				resolveExact(smt.Unsat)
				return smt.Unsat, nil
			}
			if shareSat {
				resolveExact(smt.Sat)
				return smt.Sat, nil
			}
		}
		flight = owned
		defer a.releaseSMTFlight(flightKey, flight)
	}
	result, model := check()
	resolveExact(result)
	if result == smt.Unsat {
		a.smtProofCache.publishProof(keys)
		if flight != nil {
			a.resolveSMTFlight(flightKey, smtQueryResult{proven: true})
		}
	} else if result == smt.Sat && shareSat && flight != nil {
		a.resolveSMTFlight(flightKey, smtQueryResult{proven: false})
	}
	return result, model
}

// smtCheckSliced decides a query through its cone of influence: the sliced query first, then —
// only if that does not prove — whether the dropped hypotheses are unsatisfiable by themselves
// (a dead path), under a small budget. See smtSliceQuery for why both unsat cases prove the full
// query and why a sat sliced query plus a sat dropped part is the full query's sat.
func (a *Analyzer) smtCheckSliced(solver smt.Solver, query, goalAssert string, vars []string) (smt.Result, map[string]string) {
	weakening := smtWeakeningAttempt{}
	res, model := a.smtCheckSlicedOnce(solver, query, goalAssert, vars, &weakening)
	if res != smt.Unknown {
		return res, model
	}
	budgeted, ok := solver.(smtBudgetedSolver)
	if !ok {
		return res, model
	}
	// An undecided query whose quantified hypotheses drown a simple arithmetic goal: the
	// quantifier-free weakening ran first under the small dead-path budget; give it the
	// query budget now. Dropping hypotheses only weakens, so only its unsat is used.
	stripped := smtStripAbstractionOnly(query)
	if weakening.query != "" && !weakening.sat {
		a.smtSetLogLabelForQuery(solver, "quantifier-weakening-full", goalAssert, weakening.query)
		weak, _ := a.smtCheckTransformedVerdict(weakening.query+"; full budget\n", true, func() (smt.Result, map[string]string) {
			return budgeted.CheckBudgetValues(weakening.query, vars, smtResourceLimit())
		})
		if weak == smt.Unsat {
			return smt.Unsat, nil
		}
	}
	if a.smtEscalation == 0 {
		return res, model
	}
	// withSMTEscalation: retry the complete query under the larger budget.
	a.smtSetLogLabelForQuery(solver, "escalated", goalAssert, stripped)
	res, model = a.smtCheckTransformedProof(stripped+"; escalated\n", func() (smt.Result, map[string]string) {
		return budgeted.CheckBudgetValues(stripped, vars, smtResourceLimit()*smtEscalationFactor)
	})
	if res != smt.Unknown {
		return res, model
	}
	// Retry the complete query using an alternate Z3 search strategy before
	// replaying history. Definitive results apply to the same assertions.
	if alternate, ok := solver.(interface {
		CheckAlternateRelevancy(string, []string, int64) (smt.Result, map[string]string, bool)
	}); ok {
		a.smtSetLogLabelForQuery(solver, "escalated-relevancy", goalAssert, stripped)
		r, m, supported := alternate.CheckAlternateRelevancy(stripped, vars, smtResourceLimit()*smtEscalationFactor)
		if supported && r != smt.Unknown {
			return r, m
		}
	}
	// Some proofs need lemmas a session learns from the body's earlier obligations. Replay them
	// in one session, then ask again. The history is this body's own asking order, so the
	// verdict is reproducible; an unsat is cached like any other proof.
	warm, ok := solver.(smtHistorySolver)
	history := a.smtHistoryBefore(query)
	if !ok || len(history) == 0 {
		return res, model
	}
	a.smtSetLogLabelForQuery(solver, "escalated-history", goalAssert, stripped)
	return a.smtCheckTransformedProof(stripped+"; escalated after history "+smtHistoryDigest(history)+"\n", func() (smt.Result, map[string]string) {
		return warm.CheckAfterHistory(history, stripped, vars, smtResourceLimit()*smtEscalationFactor)
	})
}

type smtHistorySolver interface {
	CheckAfterHistory(history []string, assertions string, vars []string, rlimit int64) (smt.Result, map[string]string)
}

// smtHistoryBefore is the body's history up to (excluding) the query's latest entry.
func (a *Analyzer) smtHistoryBefore(query string) []string {
	if a.smtHistoryFor != a.currentFuncDecl {
		return nil
	}
	history := a.smtHistory
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] == query {
			return history[:i]
		}
	}
	return history
}

func smtHistoryDigest(history []string) string {
	hash := sha256.New()
	for _, entry := range history {
		hash.Write([]byte(entry))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type smtWeakeningAttempt struct {
	query string
	sat   bool
}

func (a *Analyzer) smtCheckSlicedOnce(solver smt.Solver, query, goalAssert string, vars []string, weakening *smtWeakeningAttempt) (smt.Result, map[string]string) {
	// Bit-vector abstraction first (analyzer_smt_bv_abstract.go): an unsat weakening proves.
	budgeted, canBudget := solver.(smtBudgetedSolver)
	check := func(q string) (smt.Result, map[string]string) {
		res, model, _ := solver.CheckValues(q, vars)
		return res, model
	}
	checkBudget := int64(0) // 0 = the session budget; mirrors which call `check` makes
	if abstracted, ok := smtAbstractBitvectorReads(query); ok {
		a.smtSetLogLabelForQuery(solver, "bv-abstract", goalAssert, abstracted)
		res, _ := a.smtCheckTransformedVerdict(abstracted, true, func() (smt.Result, map[string]string) {
			result, model, _ := solver.CheckValues(abstracted, nil)
			return result, model
		})
		if res == smt.Unsat {
			return smt.Unsat, nil
		}
		a.smtSetLogLabelForQuery(solver, "query", goalAssert, query)
		if res == smt.Sat && canBudget && smtOnlyAndBitvectorOps(query) {
			checkBudget = smtAfterSatAbstractionBudget
			check = func(q string) (smt.Result, map[string]string) {
				return budgeted.CheckBudgetValues(q, vars, smtAfterSatAbstractionBudget)
			}
		}
	}
	query = smtStripAbstractionOnly(query)
	// Weakening and slicing only act on quantified hypotheses. Text without a quantifier
	// spelling has no quantifier atom, so both would decline after re-parsing every command.
	if !strings.Contains(query, "forall") && !strings.Contains(query, "exists") {
		return check(query)
	}
	if !canBudget {
		return check(query)
	}
	commands, balanced := smtSplitTopLevel(query)
	if !balanced {
		return check(query)
	}
	// The ladder's next step is fixed by the query alone: the sliced query when the dropped
	// part carries quantifiers, else the full query. When a weakening pre-check runs first,
	// start that next check on a helper process now so a failed pre-check costs no latency.
	// Independent checks reset first, so the helper's verdict and Unknown classification are
	// exactly the ones a direct check would produce (smt.Prefetch); unused speculation is
	// discarded without touching any cache or epoch.
	sliced, dropped, ok := smtSliceQueryCommands(commands, goalAssert, true)
	// Slice only when the dropped part carries quantifiers: quantifier instantiation is what burns
	// the budget on irrelevant facts. A quantifier-free remainder is cheap inside the full query,
	// and slicing it would only add a second solver round trip.
	if ok && !strings.Contains(dropped, "(forall ") && !strings.Contains(dropped, "(exists ") {
		ok = false
	}
	next := query
	if ok {
		next = sliced
	}
	weaker, weakenOK := smtWithoutQuantifiedHypothesesCommands(query, goalAssert, commands)
	// Related quantified callee clauses can overwhelm an otherwise simple
	// arithmetic proof. Try a bounded weakening first, accepting UNSAT only.
	if weakenOK {
		prefetcher, canPrefetch := solver.(smt.Prefetcher)
		var prefetch *smt.Prefetch
		weakening.query = weaker
		a.smtSetLogLabelForQuery(solver, "quantifier-weakening", goalAssert, weaker)
		budget := int64(smtDeadPathBudget)
		if a.smtEscalation > 0 {
			// A reported obligation: its cheap weakening also gets the larger budget.
			budget *= smtEscalationFactor
		}
		res, model := a.smtCheckTransformedVerdict(weaker, true, func() (smt.Result, map[string]string) {
			// Cache and shared-flight hits never reach this callback. Starting
			// speculation here avoids solving a full query already proven by
			// the cached weakening, while overlapping both checks on a miss.
			if canPrefetch {
				prefetch = prefetcher.StartPrefetch(next, vars, checkBudget, a.smtLogLabelText("query", goalAssert, query))
			}
			return budgeted.CheckBudgetValues(weaker, vars, budget)
		})
		if prefetch != nil {
			defer func() {
				if prefetch != nil {
					prefetcher.DiscardPrefetch(prefetch)
				}
			}()
			direct := check
			check = func(q string) (smt.Result, map[string]string) {
				if prefetch != nil && q == next {
					taken := prefetch
					prefetch = nil
					if res, model, ok := prefetcher.TakePrefetch(taken, q, vars, checkBudget); ok {
						return res, model
					}
				}
				return direct(q)
			}
		}
		if res == smt.Unsat {
			return res, model
		}
		// A definite SAT answer for these same weakened assertions is
		// conclusive even if the full query later returns unknown.
		if res == smt.Sat {
			weakening.sat = true
		}
		a.smtSetLogLabelForQuery(solver, "query", goalAssert, query)
	}
	if !ok {
		return check(query)
	}
	res, model := a.smtCheckTransformedProof(sliced, func() (smt.Result, map[string]string) { return check(sliced) })
	if res == smt.Unsat {
		return res, model
	}
	// The same out-of-cone hypotheses recur across a function's obligations: memoize the
	// definitive dead-path verdicts (unknown is retried, as in the query cache).
	if a.smtDeadPathCache == nil {
		a.smtDeadPathCache = map[string]smt.Result{}
	}
	deadPath, cached := a.smtDeadPathCache[dropped]
	if !cached {
		a.smtSetLogLabelForQuery(solver, "deadpath", goalAssert, dropped)
		deadPath, _ = a.smtCheckTransformedProof(dropped, func() (smt.Result, map[string]string) {
			return budgeted.CheckBudget(dropped, smtDeadPathBudget), nil
		})
		if deadPath != smt.Unknown {
			a.smtDeadPathCache[dropped] = deadPath
		}
	}
	if deadPath == smt.Unsat {
		return smt.Unsat, nil
	}
	if res == smt.Sat && deadPath != smt.Sat {
		// The full query is sat only if the dropped part is too; undecided ⇒ undecided (no model).
		return smt.Unknown, nil
	}
	return res, model
}
