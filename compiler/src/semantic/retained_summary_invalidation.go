package semantic

import (
	"reflect"
	"sort"

	"elisacore/src/ast"
)

// retainedInferredFunctionSummary is the body-derived part of a function type
// that can affect callers. Keep the snapshot independent of the mutable type:
// a retained update rewrites these fields in place.
type retainedInferredFunctionSummary struct {
	returnProvenance             regionRefState
	returnProvenanceKnown        bool
	returnBorrowedOwnerRefs      borrowedOwnerRefSummary
	returnBorrowedOwnerRefsKnown bool
	returnIsolation              ReturnIsolationSummary
	returnIsolationKnown         bool
	sinkParams                   []bool
	sinkParamsKnown              bool
	freshReturnShapeParams       []string
	returnsOwnedRegion           bool
	regionPolymorphic            bool
}

func snapshotRetainedInferredFunctionSummary(ft *FuncType) (retainedInferredFunctionSummary, bool) {
	if ft == nil {
		return retainedInferredFunctionSummary{}, false
	}
	isolation := ft.ReturnIsolation
	isolation.AliasLocations = append([]string(nil), isolation.AliasLocations...)
	isolation.AliasParamIndices = append([]int(nil), isolation.AliasParamIndices...)
	isolation.AliasMutableParamIndices = append([]int(nil), isolation.AliasMutableParamIndices...)
	return retainedInferredFunctionSummary{
		returnProvenance:             cloneRegionRefState(ft.ReturnProvenance),
		returnProvenanceKnown:        ft.ReturnProvenanceKnown,
		returnBorrowedOwnerRefs:      cloneBorrowedOwnerRefSummary(ft.ReturnBorrowedOwnerRefs),
		returnBorrowedOwnerRefsKnown: ft.ReturnBorrowedOwnerRefsKnown,
		returnIsolation:              isolation,
		returnIsolationKnown:         ft.ReturnIsolationKnown,
		sinkParams:                   append([]bool(nil), ft.SinkParams...),
		sinkParamsKnown:              ft.SinkParamsKnown,
		freshReturnShapeParams:       append([]string(nil), ft.FreshReturnShapeParams...),
		returnsOwnedRegion:           ft.ReturnsOwnedRegion,
		regionPolymorphic:            ft.RegionPolymorphic,
	}, true
}

func (s retainedInferredFunctionSummary) equal(other retainedInferredFunctionSummary) bool {
	return reflect.DeepEqual(s, other)
}

func (r *RetainedAnalysis) snapshotReturnBorrowInvalidationSummaries(a *Analyzer) map[*ast.FuncDecl]retainedInferredFunctionSummary {
	if r == nil || a == nil || len(r.rbInvalid) == 0 {
		return nil
	}
	before := make(map[*ast.FuncDecl]retainedInferredFunctionSummary, len(r.rbInvalid))
	for fn := range r.rbInvalid {
		if sym := a.funcDeclSymbols[fn]; sym != nil {
			if ft, ok := sym.Type.(*FuncType); ok {
				if summary, known := snapshotRetainedInferredFunctionSummary(ft); known {
					before[fn] = summary
				}
			}
		}
	}
	return before
}

// reanalyzeReturnBorrowSummaryCallers refreshes function types that consumed a
// changed return-borrow summary. Changed recursive bodies enter their current
// SCC even when the first summary comparison is masked by the old recursive
// assumption; acyclic callers are reanalyzed only when a summary changes.

func (r *RetainedAnalysis) reanalyzeReturnBorrowSummaryCallers(a *Analyzer, before map[*ast.FuncDecl]retainedInferredFunctionSummary, declarationDiagnostics int) bool {
	if r == nil || a == nil || len(before) == 0 || len(r.rbInvalid) == 0 {
		return true
	}
	changedSummary := map[*ast.FuncDecl]bool{}
	var roots []*ast.FuncDecl
	for _, fn := range r.rbChanged {
		old, hadOld := before[fn]
		current, hasCurrent := retainedFunctionSummary(a, fn)
		if !hadOld || !hasCurrent || !old.equal(current) {
			changedSummary[fn] = true
			roots = append(roots, fn)
		}
	}
	rootSet := make(map[*ast.FuncDecl]bool, len(roots))
	for _, fn := range roots {
		rootSet[fn] = true
	}
	// A changed recursive body can appear to keep its old summary when its first
	// ordinary body pass reads that same old summary at the recursive call. Force
	// the current SCC through reset/fixpoint before using equality as a cutoff.
	for _, fn := range r.rbChanged {
		if rootSet[fn] || !retainedSummaryMayCarryBorrowedOutput(a, fn) || !a.funcIsCyclic(fn) {
			continue
		}
		changedSummary[fn] = true
		roots = append(roots, fn)
		rootSet[fn] = true
	}
	if len(roots) == 0 {
		return true
	}
	// Rebuild the complete candidate-call graph from the current typed declarations.
	// rbDeps only records summaries actually consulted by the previous pass; a body
	// edit can add or remove an edge before its owner has queried that summary again.
	// Using the analyzer's conservative may-call edges discovers newly closed SCCs
	// and drops stale edges from the old body at the same time.
	r.rbCallers = r.currentReturnBorrowCallers(a)
	affected := returnBorrowCallerClosure(roots, r.rbCallers)
	for fn := range affected {
		if r.rbInvalid[fn] {
			continue
		}
		// A newly discovered caller was not in the pre-edit invalidation closure.
		// Preserve its old dependency set for cutoff restoration, but force the late
		// pass and summary refresh to record dependencies from its reanalyzed body.
		r.rbInvalid[fn] = true
		if deps := r.rbDeps[fn]; deps != nil {
			r.rbStaleDeps[fn] = deps
			delete(r.rbDeps, fn)
		}
		if summary, known := retainedFunctionSummary(a, fn); known {
			before[fn] = summary
		}
	}
	r.LastStats.Invalidated = len(r.rbInvalid)
	components, componentCyclic := r.returnBorrowSummaryComponents(affected)
	cyclic := false
	for _, isCyclic := range componentCyclic {
		cyclic = cyclic || isCyclic
	}
	if cyclic {
		return r.reanalyzeReturnBorrowSummaryComponents(a, before, affected, components, componentCyclic, declarationDiagnostics, changedSummary)
	}
	var order []*ast.FuncDecl
	for _, component := range components {
		order = append(order, component...)
	}
	needsReanalysis := map[*ast.FuncDecl]bool{}
	for _, fn := range order {
		if needsReanalysis[fn] {
			if scoped, live := r.retainedScopedFunction(fn); live && fn.Body != nil {
				r.reanalyzeSummaryDependentFunction(a, fn, scoped, declarationDiagnostics)
			}
		}
		old, hadOld := before[fn]
		current, hasCurrent := retainedFunctionSummary(a, fn)
		changedSummary[fn] = !hadOld || !hasCurrent || !old.equal(current)
		if changedSummary[fn] {
			for _, caller := range r.rbCallers[fn] {
				if affected[caller] {
					needsReanalysis[caller] = true
				}
			}
		}
	}
	return true
}

func (r *RetainedAnalysis) currentReturnBorrowCallers(a *Analyzer) map[*ast.FuncDecl][]*ast.FuncDecl {
	callers := map[*ast.FuncDecl][]*ast.FuncDecl{}
	if r == nil || a == nil {
		return callers
	}
	var functions []*ast.FuncDecl
	seen := map[*ast.FuncDecl]bool{}
	for _, sd := range r.decls {
		for _, fn := range bodyFunctions(sd.Decl) {
			fn = a.readDecl(fn)
			if fn == nil || fn.Body == nil || seen[fn] {
				continue
			}
			seen[fn] = true
			functions = append(functions, fn)
		}
	}
	sort.Slice(functions, func(i, j int) bool {
		left, right := functions[i].Pos(), functions[j].Pos()
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Offset != right.Offset {
			return left.Offset < right.Offset
		}
		return functions[i].Name < functions[j].Name
	})
	for _, caller := range functions {
		for _, callee := range a.callEdgesOf(caller) {
			callee = a.readDecl(callee)
			if callee == nil || callee.Body == nil || a.funcDeclSymbols[callee] == nil {
				continue
			}
			callers[callee] = append(callers[callee], caller)
		}
	}
	return callers
}

func retainedFunctionSummary(a *Analyzer, fn *ast.FuncDecl) (retainedInferredFunctionSummary, bool) {
	if a == nil || fn == nil {
		return retainedInferredFunctionSummary{}, false
	}
	sym := a.funcDeclSymbols[fn]
	if sym == nil {
		return retainedInferredFunctionSummary{}, false
	}
	ft, _ := sym.Type.(*FuncType)
	return snapshotRetainedInferredFunctionSummary(ft)
}

// retainedSummaryMayCarryBorrowedOutput covers the body-derived return facts
// compared by retainedInferredFunctionSummary. Unlike summaryRelevant, this
// includes direct reference and closure returns, not only reference-bearing
// containers and writable container parameters.
func retainedSummaryMayCarryBorrowedOutput(a *Analyzer, fn *ast.FuncDecl) bool {
	if a == nil || fn == nil {
		return false
	}
	sym := a.funcDeclSymbols[fn]
	if sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return false
	}
	if fnType.Return != nil && !isVoidType(fnType.Return) &&
		(a.typeCanContainRegionRefs(fnType.Return, map[string]bool{}) ||
			a.typeCarriesBorrowedStorage(fnType.Return, map[Type]bool{}) ||
			valueTypeCarriesClosure(fnType.Return, map[Type]bool{}) || containsTypeParam(fnType.Return)) {
		return true
	}
	for _, param := range fnType.Params {
		if a.returnBorrowWritableParam(param) &&
			(a.typeCanContainRegionRefs(param, map[string]bool{}) ||
				a.typeCarriesBorrowedStorage(param, map[Type]bool{}) || containsTypeParam(param)) {
			return true
		}
	}
	return false
}

// returnBorrowSummaryComponents returns affected SCCs in callee-first order.
// Members of each component retain source order for deterministic reanalysis.
func (r *RetainedAnalysis) returnBorrowSummaryComponents(affected map[*ast.FuncDecl]bool) ([][]*ast.FuncDecl, []bool) {
	if len(affected) == 0 {
		return nil, nil
	}
	funcs := make([]*ast.FuncDecl, 0, len(affected))
	seen := map[*ast.FuncDecl]bool{}
	for _, sd := range r.decls {
		for _, fn := range bodyFunctions(sd.Decl) {
			if affected[fn] && !seen[fn] {
				seen[fn] = true
				funcs = append(funcs, fn)
			}
		}
	}
	var missing []*ast.FuncDecl
	for fn := range affected {
		if fn != nil && !seen[fn] {
			missing = append(missing, fn)
		}
	}
	sort.Slice(missing, func(i, j int) bool {
		left, right := missing[i].Pos(), missing[j].Pos()
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Offset != right.Offset {
			return left.Offset < right.Offset
		}
		return missing[i].Name < missing[j].Name
	})
	funcs = append(funcs, missing...)
	index := make(map[*ast.FuncDecl]int, len(funcs))
	for i, fn := range funcs {
		index[fn] = i
	}
	graph := &bodyGraph{deps: make([][]int, len(funcs))}
	for callee, callers := range r.rbCallers {
		calleeIndex, inAffected := index[callee]
		if !inAffected {
			continue
		}
		for _, caller := range callers {
			callerIndex, inAffected := index[caller]
			if !inAffected {
				continue
			}
			graph.deps[callerIndex] = append(graph.deps[callerIndex], calleeIndex)
		}
	}
	for i := range graph.deps {
		sort.Ints(graph.deps[i])
	}
	indices := graph.sccs()
	components := make([][]*ast.FuncDecl, len(indices))
	componentCyclic := make([]bool, len(indices))
	for componentIndex, component := range indices {
		components[componentIndex] = make([]*ast.FuncDecl, 0, len(component))
		componentCyclic[componentIndex] = len(component) > 1
		for _, i := range component {
			components[componentIndex] = append(components[componentIndex], funcs[i])
			for _, dep := range graph.deps[i] {
				if dep == i {
					componentCyclic[componentIndex] = true
				}
			}
		}
	}
	return components, componentCyclic
}

const (
	retainedSummaryCycleMinRounds = 8
	retainedSummaryCycleMaxRounds = 128
)

func retainedSummaryCycleRoundLimit(componentSize int) int {
	limit := componentSize * 4
	if limit < retainedSummaryCycleMinRounds {
		limit = retainedSummaryCycleMinRounds
	}
	if limit > retainedSummaryCycleMaxRounds {
		limit = retainedSummaryCycleMaxRounds
	}
	return limit
}

func (r *RetainedAnalysis) reanalyzeReturnBorrowSummaryComponents(a *Analyzer, before map[*ast.FuncDecl]retainedInferredFunctionSummary, affected map[*ast.FuncDecl]bool, components [][]*ast.FuncDecl, componentCyclic []bool, declarationDiagnostics int, changedSummary map[*ast.FuncDecl]bool) bool {
	needsReanalysis := map[*ast.FuncDecl]bool{}
	for componentIndex, component := range components {
		trigger := false
		for _, fn := range component {
			trigger = trigger || changedSummary[fn] || needsReanalysis[fn]
		}
		if !trigger {
			continue
		}
		if componentCyclic[componentIndex] {
			if !r.reanalyzeReturnBorrowSummaryCycle(a, component, declarationDiagnostics) {
				return false
			}
		} else if len(component) != 0 && needsReanalysis[component[0]] {
			fn := component[0]
			if scoped, live := r.retainedScopedFunction(fn); live && fn.Body != nil {
				r.reanalyzeSummaryDependentFunction(a, fn, scoped, declarationDiagnostics)
			}
		}
		for _, fn := range component {
			old, hadOld := before[fn]
			current, hasCurrent := retainedFunctionSummary(a, fn)
			changedSummary[fn] = !hadOld || !hasCurrent || !old.equal(current)
			if !changedSummary[fn] {
				continue
			}
			for _, caller := range r.rbCallers[fn] {
				if affected[caller] {
					needsReanalysis[caller] = true
				}
			}
		}
	}
	return true
}

func (r *RetainedAnalysis) reanalyzeReturnBorrowSummaryCycle(a *Analyzer, component []*ast.FuncDecl, declarationDiagnostics int) bool {
	for _, fn := range component {
		resetRetainedCycleFunctionSummary(a, fn)
	}
	// Return-borrow queries can cache a transitive result rooted outside this SCC.
	// Its cut-sensitive entry may still contain summaries from before the edit.
	a.returnBorrowRootMemos = nil
	a.returnBorrowRootMemosFor = nil
	a.returnBorrowQueryMemo = nil
	a.returnBorrowCutLog = nil
	limit := retainedSummaryCycleRoundLimit(len(component))
	for round := 0; round < limit; round++ {
		beforeRound := make(map[*ast.FuncDecl]retainedInferredFunctionSummary, len(component))
		for _, fn := range component {
			if summary, known := retainedFunctionSummary(a, fn); known {
				beforeRound[fn] = summary
			}
		}
		for _, fn := range component {
			if scoped, live := r.retainedScopedFunction(fn); live && fn.Body != nil {
				r.reanalyzeSummaryDependentFunction(a, fn, scoped, declarationDiagnostics)
			}
		}
		stable := true
		for _, fn := range component {
			prior, hadPrior := beforeRound[fn]
			current, hasCurrent := retainedFunctionSummary(a, fn)
			if !hadPrior || !hasCurrent || !prior.equal(current) {
				stable = false
				break
			}
		}
		if stable {
			return true
		}
	}
	return false
}

func resetRetainedCycleFunctionSummary(a *Analyzer, fn *ast.FuncDecl) {
	if a == nil || fn == nil {
		return
	}
	sym := a.funcDeclSymbols[fn]
	if sym == nil {
		return
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return
	}
	if fn.ReturnRegion == "" {
		fnType.ReturnProvenance = regionRefState{}
		fnType.ReturnProvenanceKnown = true
	}
	fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	fnType.ReturnBorrowedOwnerRefsKnown = true
	fnType.SinkParams = nil
	fnType.SinkParamsKnown = false
	fnType.ReturnIsolation = ReturnIsolationSummary{Known: true, Isolated: true}
	fnType.ReturnIsolationKnown = true
	fnType.FreshReturnShapeParams = knownFreshReturnShapeParams(fnType.Name, fnType.Return)
	fnType.ReturnsOwnedRegion = false
	delete(a.returnBorrowFuncAnalyzed, fn)
	delete(a.returnBorrowFuncSummaries, fn)
	delete(a.returnBorrowCycleAssume, fn)
}

func (r *RetainedAnalysis) retainedScopedFunction(fn *ast.FuncDecl) (scopedDecl, bool) {
	if r == nil || fn == nil {
		return scopedDecl{}, false
	}
	for _, sd := range r.decls {
		if sd.Decl == fn {
			return sd, true
		}
		for _, member := range bodyFunctions(sd.Decl) {
			if member == fn {
				return scopedDecl{Decl: fn, Namespace: sd.Namespace, Usings: sd.Usings, Private: sd.Private}, true
			}
		}
	}
	return scopedDecl{}, false
}

func (r *RetainedAnalysis) reanalyzeSummaryDependentFunction(a *Analyzer, fn *ast.FuncDecl, scoped scopedDecl, declarationDiagnostics int) {
	if r == nil || a == nil || fn == nil {
		return
	}
	start := declarationDiagnostics
	if start < 0 {
		start = 0
	}
	if start > r.bodyEnd {
		start = r.bodyEnd
	}
	end := r.bodyEnd
	if end > len(a.diagnostics) {
		end = len(a.diagnostics)
	}
	owners := retainedDiagnosticOwners(r.decls, nil, func(decl ast.Decl) ast.Decl { return decl })
	drop := map[int]bool{}
	for i := start; i < end; i++ {
		if owners.owner(a.diagnostics[i].Pos) == fn {
			drop[i] = true
		}
	}
	delete(r.windows, fn)
	if len(drop) != 0 {
		kept := make([]Diagnostic, 0, len(a.diagnostics)-len(drop))
		for i, diagnostic := range a.diagnostics {
			if !drop[i] {
				kept = append(kept, diagnostic)
			}
		}
		a.diagnostics = kept
		r.rebaseWindows(drop)
		r.bodyEnd -= len(drop)
		a.reportedDiagnostics = make(map[diagnosticIdentity]bool, len(kept))
		for _, diagnostic := range kept {
			a.reportedDiagnostics[diagnosticIdentity{Pos: diagnostic.Pos, Severity: diagnostic.Severity, Message: diagnostic.Message}] = true
		}
		a.warnOnceSeen = nil
	}
	delete(a.ambientFillAnalyzed, fn)
	delete(a.storeFlowBinderIndexes, fn)
	delete(a.storeScanIndexCache, fn)
	clearRetainedSummaryDependentExprFacts(a, fn)
	// Reanalyze this caller against the declaration rows used by a cold body pass.
	// A changed callee may already have a newly inferred effect row from the body
	// update, while cold body analysis sees its declaration row until the permission
	// solver runs. Let that solver publish transitive effects after all bodies have
	// been refreshed, rather than baking the temporary row into caller facts.
	if r.perms.summaryReanalyzed == nil {
		r.perms.summaryReanalyzed = map[*ast.FuncDecl]bool{}
	}
	r.perms.summaryReanalyzed[fn] = true
	// This caller may no longer defer any checks after its callee summaries
	// change. Discard the old frame before reanalysis so an absent replacement
	// cannot leave obsolete pending nodes in the late pass.
	frames := a.returnBorrowLateFrames[:0]
	for _, late := range a.returnBorrowLateFrames {
		if late.frame != nil && late.frame.fn == fn {
			continue
		}
		frames = append(frames, late)
	}
	a.returnBorrowLateFrames = frames
	r.perms.withInitialRows(a, r.decls, func() {
		a.analyzeScopedDecl(scoped)
	}, fn)
	r.bodyEnd = len(a.diagnostics)
}

// clearRetainedSummaryDependentExprFacts drops only facts owned by fn's current
// syntax before reanalysis. Recursive summary probing suppresses optimization
// fact writes, so otherwise a fact from the previous inferred callee summary can
// survive when the refreshed expression no longer has that fact.
func clearRetainedSummaryDependentExprFacts(a *Analyzer, fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(a.exprFacts) == 0 {
		return
	}
	walkStoreScanNodes(funcScanStmts(fn), func(node any) {
		if expr, ok := node.(ast.Expr); ok {
			delete(a.exprFacts, expr)
		}
	})
}
