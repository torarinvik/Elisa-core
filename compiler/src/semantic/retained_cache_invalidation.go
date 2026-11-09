package semantic

import "elisacore/src/ast"

// invalidateRetainedBodyCaches drops body-dependent caches before replay.
func (r *RetainedAnalysis) invalidateRetainedBodyCaches(a *Analyzer, changed []scopedDecl, removedLive []*ast.FuncDecl, addedScoped []scopedDecl, splices []retainedSplice, structOnly map[*ast.FuncDecl]bool, marks map[string][][2]int, bodyEnvironmentUnchanged bool) {
	r.invalidateReturnBorrow(changed, marks)
	a.invalidateStorageViewCalleeWriteCaches()
	a.regionPointeesMemo = nil
	// Binder and scan indexes describe syntax; replacing a body invalidates both.
	for _, sd := range changed {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok {
			delete(a.storeFlowBinderIndexes, fn)
			delete(a.storeScanIndexCache, fn)
		}
	}
	for _, fn := range removedLive {
		delete(a.storeFlowBinderIndexes, fn)
		delete(a.storeScanIndexCache, fn)
	}
	// Parameter-write reachability and its local facts depend on function bodies
	// and call resolution. A retained semantic edit can change any transitive
	// path, so invalidate both caches before re-analyzing edited bodies.
	if !bodyEnvironmentUnchanged || len(splices) != 0 || len(removedLive) != 0 {
		a.storeScanParamWriteCache = nil
		a.storeScanParamFactsCache = nil
		// Alias closures also depend on resolved callees and their types, even
		// when the caller's syntax index itself remains valid.
		a.invalidateStoreScanParamAliases()
	}
	if len(changed) != 0 || len(removedLive) != 0 {
		// A body edit can change flow through every transitive caller and can
		// split or join call cycles. These results predate the replacement bodies.
		// Every body whose call resolution an added or removed name changes mentions that
		// name and is re-analyzed, so it is among the changed functions; a removed function
		// seeds its callers.
		fns := make([]*ast.FuncDecl, 0, len(changed)+len(removedLive))
		for _, sd := range changed {
			if fn, ok := sd.Decl.(*ast.FuncDecl); ok && !structOnly[fn] {
				fns = append(fns, fn)
			}
		}
		fns = append(fns, removedLive...)
		a.invalidateStoreFlowSummariesFrom(fns)
		a.storeFlowSCC = nil
		a.cyclicFuncSet = nil
		if len(addedScoped) != 0 || len(removedLive) != 0 {
			// New/removed overloads also change name-based edge resolution in
			// otherwise unchanged bodies.
			a.callDeclsByName = nil
			a.callEdgeCache = nil
		} else {
			for _, sd := range changed {
				if fn, ok := sd.Decl.(*ast.FuncDecl); ok {
					delete(a.callEdgeCache, fn)
				}
			}
		}
	}
}

// rebaseWindows maps the recorded diagnostic windows onto the compacted list.
func (r *RetainedAnalysis) rebaseWindows(drop map[int]bool) {
	if len(drop) == 0 {
		return
	}
	maxK := 0
	for _, w := range r.windows {
		if w[1] > maxK {
			maxK = w[1]
		}
	}
	// below[k] counts dropped indices smaller than k.
	below := make([]int, maxK+1)
	for k := 1; k <= maxK; k++ {
		below[k] = below[k-1]
		if drop[k-1] {
			below[k]++
		}
	}
	remap := func(k int) int { return k - below[k] }
	for fn, w := range r.windows {
		if drop[w[0]] {
			delete(r.windows, fn)
			continue
		}
		r.windows[fn] = [2]int{remap(w[0]), remap(w[1])}
	}
}
