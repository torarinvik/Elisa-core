package semantic

import "elisacore/src/ast"

// finalizeFuncDisjointParams retains the cold least-fixpoint semantics while
// narrowing retained updates to the changed callee set and its dependents.
func (a *Analyzer) finalizeFuncDisjointParams(decls []scopedDecl) {
	if a == nil {
		return
	}
	wasReady := a.disjointIndexesReady
	a.ensureDisjointIndexes()
	// Cold analysis uses the full solver, which performs its own single name-usage scan.
	// Avoid doing that whole-program walk twice before the retained indexes are ready.
	if !wasReady || a.retained == nil {
		a.finalizeFuncDisjointParamsFull(decls)
		for fn := range a.disjointDirtyCallees {
			delete(a.disjointDirtyCallees, fn)
		}
		return
	}
	if len(a.disjointCallSites) == 0 {
		// A retained removal of the last call site still has to retract old proofs, but
		// it no longer needs a whole-program name-usage scan to mark every prior candidate
		// ineligible. Body removal already marks its affected callees dirty.
		for fn, previous := range a.disjointEligibility {
			if previous {
				a.markDisjointDirty(fn)
			}
			a.disjointEligibility[fn] = false
		}
		a.recomputeDirtyFuncDisjointParams(map[*ast.FuncDecl]bool{})
		return
	}
	nameCounts, valueUsedNames := scanFuncNameUsage(decls)
	currentEligibility := map[*ast.FuncDecl]bool{}
	for _, scoped := range decls {
		fn, ok := scoped.Decl.(*ast.FuncDecl)
		if !ok || fn == nil {
			continue
		}
		_, hadEligibility := a.disjointEligibility[fn]
		if len(a.disjointCallSites[fn]) == 0 && !hadEligibility {
			continue
		}
		eligible := len(a.disjointCallSites[fn]) != 0 && nameCounts[fn.Name] == 1 && !valueUsedNames[fn.Name] && a.containerHeadersStable(fn)
		currentEligibility[fn] = eligible
		if previous, known := a.disjointEligibility[fn]; !known || previous != eligible {
			a.markDisjointDirty(fn)
		}
	}
	for fn, eligible := range currentEligibility {
		a.disjointEligibility[fn] = eligible
	}

	a.recomputeDirtyFuncDisjointParams(currentEligibility)
}

// ensureDisjointIndexes rebuilds the caller-owned and reverse dependency indexes
// once after cold analysis or a disk restore. Subsequent retained updates mutate
// only entries owned by changed functions.
func (a *Analyzer) ensureDisjointIndexes() {
	if a == nil || a.disjointIndexesReady {
		return
	}
	a.disjointCallSitesByCaller = map[*ast.FuncDecl]map[*ast.FuncDecl][]callDisjointObservation{}
	a.disjointDependents = map[*ast.FuncDecl]map[*ast.FuncDecl]bool{}
	for callee, observations := range a.disjointCallSites {
		for _, observation := range observations {
			caller := observation.enclosing
			if caller == nil {
				continue
			}
			if a.disjointCallSitesByCaller[caller] == nil {
				a.disjointCallSitesByCaller[caller] = map[*ast.FuncDecl][]callDisjointObservation{}
			}
			a.disjointCallSitesByCaller[caller][callee] = append(a.disjointCallSitesByCaller[caller][callee], observation)
			if observationHasDependentEvidence(observation) {
				if a.disjointDependents[caller] == nil {
					a.disjointDependents[caller] = map[*ast.FuncDecl]bool{}
				}
				a.disjointDependents[caller][callee] = true
			}
		}
	}
	a.disjointIndexesReady = true
}

func observationHasDependentEvidence(observation callDisjointObservation) bool {
	for _, evidence := range observation.evidence {
		if evidence.depend {
			return true
		}
	}
	return false
}

func (a *Analyzer) markDisjointDirty(fn *ast.FuncDecl) {
	if a == nil || fn == nil {
		return
	}
	if a.disjointDirtyCallees == nil {
		a.disjointDirtyCallees = map[*ast.FuncDecl]bool{}
	}
	a.disjointDirtyCallees[fn] = true
}

func (a *Analyzer) recordDisjointCallObservation(callee *ast.FuncDecl, observation callDisjointObservation) {
	if a == nil || callee == nil {
		return
	}
	if a.disjointCallSites == nil {
		a.disjointCallSites = map[*ast.FuncDecl][]callDisjointObservation{}
	}
	if a.disjointCallSitesByCaller == nil {
		a.disjointCallSitesByCaller = map[*ast.FuncDecl]map[*ast.FuncDecl][]callDisjointObservation{}
	}
	if a.disjointDependents == nil {
		a.disjointDependents = map[*ast.FuncDecl]map[*ast.FuncDecl]bool{}
	}
	a.disjointCallSites[callee] = append(a.disjointCallSites[callee], observation)
	caller := observation.enclosing
	if caller != nil {
		if a.disjointCallSitesByCaller[caller] == nil {
			a.disjointCallSitesByCaller[caller] = map[*ast.FuncDecl][]callDisjointObservation{}
		}
		a.disjointCallSitesByCaller[caller][callee] = append(a.disjointCallSitesByCaller[caller][callee], observation)
		if observationHasDependentEvidence(observation) {
			if a.disjointDependents[caller] == nil {
				a.disjointDependents[caller] = map[*ast.FuncDecl]bool{}
			}
			a.disjointDependents[caller][callee] = true
		}
	}
	a.markDisjointDirty(callee)
}

// removeDisjointCallSitesForCaller drops exactly the body-owned evidence. The
// affected callee buckets are filtered in place; unrelated callers are retained.
func (a *Analyzer) removeDisjointCallSitesForCaller(caller *ast.FuncDecl) {
	if a == nil || caller == nil {
		return
	}
	pending := a.pendingDisjointCalls[:0]
	for _, request := range a.pendingDisjointCalls {
		if request.fn != caller {
			pending = append(pending, request)
		}
	}
	for i := len(pending); i < len(a.pendingDisjointCalls); i++ {
		a.pendingDisjointCalls[i] = pendingDisjointCall{}
	}
	a.pendingDisjointCalls = pending
	for call := range a.callArgDisjointByFunc[caller] {
		delete(a.callArgDisjoint, call)
	}
	delete(a.callArgDisjointByFunc, caller)
	for dependent := range a.disjointDependents[caller] {
		// These outputs may have used the old caller proof. Mark them before the
		// old reverse edges are removed, so the changed body cannot hide a path.
		a.markDisjointDirty(dependent)
	}
	for callee := range a.disjointCallSitesByCaller[caller] {
		observations := a.disjointCallSites[callee]
		kept := observations[:0]
		for _, observation := range observations {
			if observation.enclosing != caller {
				kept = append(kept, observation)
			}
		}
		for i := len(kept); i < len(observations); i++ {
			observations[i] = callDisjointObservation{}
		}
		if len(kept) == 0 {
			delete(a.disjointCallSites, callee)
		} else {
			a.disjointCallSites[callee] = kept
		}
		a.markDisjointDirty(callee)
	}
	delete(a.disjointCallSitesByCaller, caller)
	delete(a.disjointDependents, caller)
}

func (a *Analyzer) removeDisjointFunction(fn *ast.FuncDecl) {
	if a == nil || fn == nil {
		return
	}
	a.removeDisjointCallSitesForCaller(fn)
	for _, observation := range a.disjointCallSites[fn] {
		caller := observation.enclosing
		if caller == nil {
			continue
		}
		if callees := a.disjointCallSitesByCaller[caller]; callees != nil {
			delete(callees, fn)
			if len(callees) == 0 {
				delete(a.disjointCallSitesByCaller, caller)
			}
		}
		if dependents := a.disjointDependents[caller]; dependents != nil {
			delete(dependents, fn)
			if len(dependents) == 0 {
				delete(a.disjointDependents, caller)
			}
		}
	}
	delete(a.disjointCallSites, fn)
	delete(a.disjointEligibility, fn)
	delete(a.funcDisjointParams, fn)
	a.markDisjointDirty(fn)
}

func (a *Analyzer) recomputeDirtyFuncDisjointParams(currentEligibility map[*ast.FuncDecl]bool) {
	affected := map[*ast.FuncDecl]bool{}
	queue := make([]*ast.FuncDecl, 0, len(a.disjointDirtyCallees))
	for fn := range a.disjointDirtyCallees {
		if !affected[fn] {
			affected[fn] = true
			queue = append(queue, fn)
		}
	}
	for head := 0; head < len(queue); head++ {
		for dependent := range a.disjointDependents[queue[head]] {
			if !affected[dependent] {
				affected[dependent] = true
				queue = append(queue, dependent)
			}
		}
	}
	for fn := range a.disjointDirtyCallees {
		delete(a.disjointDirtyCallees, fn)
	}
	if len(affected) == 0 {
		return
	}
	for fn := range affected {
		delete(a.funcDisjointParams, fn)
	}
	for fn, info := range a.solveFuncDisjointParams(affected, affected, currentEligibility) {
		a.funcDisjointParams[fn] = info
	}
}

func (a *Analyzer) solveFuncDisjointParams(targets, affected, eligible map[*ast.FuncDecl]bool) map[*ast.FuncDecl]*FuncDisjointParamInfo {
	proven := map[*ast.FuncDecl]map[[2]int]bool{}
	contributes := func(observation callDisjointObservation, pair [2]int) bool {
		evidence, ok := observation.evidence[pair]
		if !ok {
			return false
		}
		if evidence.base {
			return true
		}
		if evidence.depend {
			var dependencyPairs map[[2]int]bool
			if affected[observation.enclosing] {
				dependencyPairs = proven[observation.enclosing]
			} else if dependency := a.funcDisjointParams[observation.enclosing]; dependency != nil {
				dependencyPairs = dependency.DistinctPairs
			}
			return dependencyPairs[orderedPair(evidence.depP, evidence.depQ)]
		}
		return false
	}

	for changed := true; changed; {
		changed = false
		for decl := range targets {
			if !eligible[decl] {
				continue
			}
			observations := a.disjointCallSites[decl]
			if len(observations) == 0 {
				continue
			}
			for pair := range observations[0].evidence {
				if proven[decl][pair] {
					continue
				}
				allContribute := true
				for _, observation := range observations {
					if !contributes(observation, pair) {
						allContribute = false
						break
					}
				}
				if allContribute {
					if proven[decl] == nil {
						proven[decl] = map[[2]int]bool{}
					}
					proven[decl][pair] = true
					changed = true
				}
			}
		}
	}

	result := map[*ast.FuncDecl]*FuncDisjointParamInfo{}
	for decl, pairs := range proven {
		if len(pairs) == 0 {
			continue
		}
		observations := a.disjointCallSites[decl]
		if len(observations) == 0 {
			continue
		}
		result[decl] = &FuncDisjointParamInfo{
			DistinctPairs: pairs,
			SelfNoalias:   selfNoaliasFromPairs(observations[0].containerParams, pairs),
		}
	}
	return result
}
