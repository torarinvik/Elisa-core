package semantic

import (
	"elisacore/src/ast"
	"maps"
	"reflect"
	"slices"
)

func cloneAnalyzerValueForParallel(a *Analyzer) reflect.Value {
	return cloneAnalyzerValueForParallelMode(a, false)
}

func cloneAnalyzerValueForBodyWorker(a *Analyzer) reflect.Value {
	return cloneAnalyzerValueForParallelMode(a, true)
}

func cloneAnalyzerValueForParallelMode(a *Analyzer, useMemoOverlays bool) reflect.Value {
	return cloneAnalyzerValueForParallelPolicy(a, useMemoOverlays, false)
}

// A specialization imports exactly the current/private declarations through
// specializationContext.copyDecl. Its analyzer never queries unrelated function
// analyses; public Result queries retain canonical fallback after publication.
func cloneAnalyzerValueForSpecialization(a *Analyzer) reflect.Value {
	if a != nil && (a.exprTypeOverlay != nil || a.exprFactsOverlay != nil || a.iterBinderElementOverlay != nil || a.noStorageOverlay != nil || a.storeScanParamFactsOverlay != nil || a.storeScanBinderOverlay != nil || a.optimizationSymbolIdentityOverlay != nil) {
		panic("specialization construction requires a canonical analyzer, not another construction overlay")
	}
	return cloneAnalyzerValueForParallelPolicy(a, true, true)
}

var parallelAnalyzerFieldNames = func() []string {
	t := reflect.TypeFor[Analyzer]()
	names := make([]string, t.NumField())
	for i := range names {
		names[i] = t.Field(i).Name
	}
	return names
}()

func cloneAnalyzerValueForParallelPolicy(a *Analyzer, useMemoOverlays, ownedFunctionAnalyses bool) reflect.Value {
	if a == nil {
		return reflect.Value{}
	}
	source := reflect.ValueOf(a).Elem()
	cloned := reflect.New(source.Type()).Elem()
	cloned.Set(source)
	shareDeclarationMaps := true
	// Preserve deepCloneValue's value policy if OptimizationFacts ever grows
	// inline maps/slices: that schema keeps the ordinary reflective copy.
	useExprFactsOverlay := ownedFunctionAnalyses && !typeHoldsMapInline(reflect.TypeFor[OptimizationFacts]())
	for i := 0; i < source.NumField(); i++ {
		field := source.Field(i)
		name := parallelAnalyzerFieldNames[i]
		if ownedFunctionAnalyses && name == "proofLifecycle" {
			// The private construction journal contains only concrete work;
			// publication retains the canonical Result's full public report.
			settable(cloned.Field(i)).Set(reflect.ValueOf(a.specializationProofReportLifecycle()))
			continue
		}
		if name == "proofLifecycle" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(a.forkProofReportLifecycle()))
			continue
		}
		if ownedFunctionAnalyses && name == "proofReport" {
			// Concrete producers append only construction-local observations.
			// Published specialization views inherit the complete canonical
			// Result report and summary, then discard this analyzer. Borrowing
			// its event prefix would copy the whole program on the first append.
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if ownedFunctionAnalyses && name == "visibleCandidates" {
			// This memo contains only candidate names derived from lexical
			// context and aliases. Rebuild the few private lookups on demand
			// instead of copying every context/name table in the program.
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if ownedFunctionAnalyses && name == "headerFunctionNames" {
			// The published counts map is immutable. A worker that needs a new
			// index replaces its own whole value, so sharing the current map avoids
			// copying every function name for each specialization.
			continue
		}
		if ownedFunctionAnalyses && (name == "diagnostics" || name == "reportedDiagnostics") {
			// Reanalysis must re-emit its own errors, even when position/text
			// matches an inherited canonical diagnostic. Skip before cloning
			// either the inherited slice or its potentially large dedup map.
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if name == "optimizationSymbolIdentityOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerOptimizationSymbolIdentityOverlay(a.optimizationSymbolIdentityOverlay)))
			continue
		}
		if ownedFunctionAnalyses && name == "optimizationSymbolIDs" {
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if name == "storeScanBinderOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerStoreScanBinderOverlay(a.storeScanBinderOverlay)))
			continue
		}
		if name == "storeScanParamFactsOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerStoreScanParamFactsOverlay(a.storeScanParamFactsOverlay)))
			continue
		}
		if name == "noStorageOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerNoStorageOverlay(a.noStorageOverlay)))
			continue
		}
		if ownedFunctionAnalyses && name == "storeScanBinderTypes" {
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && (name == "storeScanNoStorageCache" || name == "containerRegionMemo" || name == "storeScanCache") {
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "storeScanSharesCache" {
			// This memo contains immutable type-property results. Inherit settled
			// rows through noStorageOverlay and keep new rows private to this
			// specialization instead of copying the whole program cache.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "compilerBuiltinHelperCalls" {
			// copyDecl rekeys only the owned call facts needed by this
			// specialization. Keep the whole-program canonical table as a
			// read-only overlay base instead of cloning every helper call.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "returnElementStmtUnknown" {
			// copyDecl rekeys only owned return rows. Inherit the remaining
			// whole-program facts through the overlay base instead of copying all.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "indexBoundsRuntimeSites" {
			// Runtime guard sites are observational telemetry. Specialization
			// re-analyzes its private syntax and publishes the canonical summary,
			// so start with a private empty ledger instead of copying every site.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "storeScanParamWriteCache" {
			// This reachability memo is shared as an immutable base. Keep only
			// private rows produced while analyzing the concrete specialization.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if name == "iterBinderElementOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerIterBinderElementOverlay(a.iterBinderElementOverlay)))
			continue
		}
		if ownedFunctionAnalyses && (name == "iterBinderElementStates" || name == "iterBindingSources") {
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if name == "exprFactsOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerExprFactsOverlay(a.exprFactsOverlay)))
			continue
		}
		if useExprFactsOverlay && (name == "exprFacts" || name == "symbolFacts") {
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if name == "exprTypeOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerExprTypeOverlay(a.exprTypeOverlay)))
			continue
		}
		if ownedFunctionAnalyses && name == "exprTypes" {
			// Private observations only: canonical reads use the immutable base.
			// Skip its entire map before reflective cloning or typed cloning.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "storeScanParamFactsCache" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneStoreScanParamFactsConstructionMap(a.storeScanParamFactsCache, specializationParamFactsCopySchemaValid)))
			continue
		}
		if ownedFunctionAnalyses && name == "functionAnalyses" {
			// Skip BEFORE deepCloneValue: cloning then clearing still performs
			// a whole-program map traversal in the serialized specialization path.
			settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			continue
		}
		if ownedFunctionAnalyses && name == "regionStacks" {
			// copyDecl imports and isolates assignments for owned region syntax
			// from the canonical source. Private analysis only writes fresh rows;
			// published queries inherit foreign assignments through the root view.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && (name == "storeScanCallWriteCache" || name == "storageViewCalleeWriteCache") {
			// The call-write cache is AST-keyed, so copyDecl imports only rows for
			// syntax owned by the private declaration. Storage-view summaries use
			// composite declaration keys; specialization reads go through private
			// shadows and recompute those rows on demand. Keep this map private so
			// fresh memo entries cannot mutate the canonical analyzer.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "returnElementStmtStates" {
			// copyDecl imports and isolates statement facts for owned ReturnStmt
			// syntax. Unrelated functions' rows are never queried during private
			// specialization analysis, so avoid cloning the whole-program map.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "storeScanBoundCache" {
			// Settled name sets are read-only; only publish new rows privately.
			settable(cloned.Field(i)).Set(reflect.ValueOf(maps.Clone(a.storeScanBoundCache)))
			continue
		}
		if ownedFunctionAnalyses && name == "funcDeclUsings" {
			// Usings are installed as fresh slices and only read (or copied into a
			// resolution context). Keep the index private for declaration edits,
			// while sharing its immutable per-declaration rows.
			settable(cloned.Field(i)).Set(reflect.ValueOf(maps.Clone(a.funcDeclUsings)))
			continue
		}
		if name == "retained" {
			// The retained analysis owns the whole analyzer; a worker records into a
			// shell of its own (analyzeDeclsParallel), merged back after the join.
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if useMemoOverlays && (name == "smtSolver" || name == "smtStats") {
			// A parent's live solver serializes all checks behind its mutex.
			// Workers own lazy solver sessions and account only their own work.
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if useMemoOverlays && name == "storeScanTaintCache" {
			// Published taint sets are read-only. Only the cache's outer map
			// receives new entries, so give each worker an independent map
			// while preserving the settled sets (and their internal sharing).
			settable(cloned.Field(i)).Set(reflect.ValueOf(maps.Clone(a.storeScanTaintCache)))
			continue
		}
		if useMemoOverlays && name == "storeFlowSummaries" {
			if ownedFunctionAnalyses {
				settable(cloned.Field(i)).Set(reflect.ValueOf(cloneStoreFlowSummariesForSpecialization(a.storeFlowSummaries)))
			} else {
				settable(cloned.Field(i)).Set(reflect.ValueOf(cloneStoreFlowSummariesForBodyWorker(a.storeFlowSummaries)))
			}
			continue
		}
		// These caches contain lazily populated maps/slices behind pointers.
		// Rebuild them privately rather than sharing mutable pointer values.
		if useMemoOverlays && parallelBodyWorkerRebuildMap(name) {
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if useMemoOverlays && parallelBodyMemoMap(name) {
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if shareDeclarationMaps && field.Kind() == reflect.Map && !field.IsNil() && parallelDeclarationMapIsReadOnly(name) {
			continue
		}
		if ownedFunctionAnalyses && cloneSpecializationStoreScanBoolSetCache(cloned.Addr().Interface().(*Analyzer), a, name) {
			continue
		}
		if ownedFunctionAnalyses && cloneSpecializationNestedSetField(cloned.Addr().Interface().(*Analyzer), a, name) {
			continue
		}
		if ownedFunctionAnalyses && name == "returnElementSummaries" {
			cloned.Addr().Interface().(*Analyzer).returnElementSummaries = cloneSpecializationFrozenReturnElementSummaries(a.returnElementSummaries)
			continue
		}
		if ownedFunctionAnalyses && name == "storeFlowSCC" {
			cloned.Addr().Interface().(*Analyzer).storeFlowSCC = cloneSpecializationFuncDeclSliceMap(a.storeFlowSCC, specializationGraphCacheSchema("storeFlowSCC"))
			continue
		}
		if ownedFunctionAnalyses && name == "callEdgeCache" {
			cloned.Addr().Interface().(*Analyzer).callEdgeCache = cloneSpecializationFuncDeclSliceMap(a.callEdgeCache, specializationGraphCacheSchema("callEdgeCache"))
			continue
		}
		if ownedFunctionAnalyses && name == "disjointCallSitesByCaller" {
			cloned.Addr().Interface().(*Analyzer).disjointCallSitesByCaller = cloneSpecializationDisjointCallSites(a.disjointCallSitesByCaller)
			continue
		}
		if ownedFunctionAnalyses && name == "returnBorrowFuncSummaries" {
			cloned.Addr().Interface().(*Analyzer).returnBorrowFuncSummaries = cloneSpecializationReturnBorrowSummaries(a.returnBorrowFuncSummaries)
			continue
		}
		if ownedFunctionAnalyses && name == "paramStoreTargets" {
			cloned.Addr().Interface().(*Analyzer).paramStoreTargets = cloneSpecializationParamStoreTargets(a.paramStoreTargets)
			continue
		}
		if ownedFunctionAnalyses && name == "storeScanFilledCache" {
			// Filled-local rows are completed fixed-point results. The scanner
			// only reads them after publication, so copy the mutable cache header
			// and share its immutable result sets.
			settable(cloned.Field(i)).Set(reflect.ValueOf(maps.Clone(a.storeScanFilledCache)))
			continue
		}
		if ownedFunctionAnalyses && cloneSpecializationOpaqueMap(cloned.Addr().Interface().(*Analyzer), a, name, field) {
			continue
		}
		settable(cloned.Field(i)).Set(deepCloneValue(settable(field)))
	}
	if ownedFunctionAnalyses {
		worker := cloned.Addr().Interface().(*Analyzer)
		worker.optimizationSymbolIdentityOverlay = &analyzerOptimizationSymbolIdentityOverlay{base: a.optimizationSymbolIDs}
		worker.storeScanBinderOverlay = &analyzerStoreScanBinderOverlay{base: a.storeScanBinderTypes}
		if specializationParamFactsCopySchemaValid {
			worker.storeScanParamFactsOverlay = &analyzerStoreScanParamFactsOverlay{base: a.storeScanParamFactsCache}
		}
		worker.noStorageOverlay = &analyzerNoStorageOverlay{
			base:             a.storeScanNoStorageCache,
			viewOnlyBase:     a.storeScanViewOnlyCache,
			sharesBase:       a.storeScanSharesCache,
			paramWrites:      a.storeScanParamWriteCache,
			helperCalls:      a.compilerBuiltinHelperCalls,
			returnUnknown:    a.returnElementStmtUnknown,
			containerRegions: a.containerRegionMemo,
			storeModes:       a.storeScanCache,
		}
		worker.iterBinderElementOverlay = &analyzerIterBinderElementOverlay{base: a.iterBinderElementStates, sources: a.iterBindingSources}
		worker.exprTypeOverlay = &analyzerExprTypeOverlay{base: a.exprTypes}
		if useExprFactsOverlay {
			worker.exprFactsOverlay = &analyzerExprFactsOverlay{base: a.exprFacts, symbols: a.symbolFacts}
		}
	}
	return cloned
}

// These settled caches are read-only after publication. Specializations copy
// the mutable outer maps while sharing immutable rows, or use a read-through
// base for the type-only view predicate cache.
func cloneSpecializationStoreScanBoolSetCache(dst, src *Analyzer, field string) bool {
	switch field {
	case "storeScanViewCache":
		dst.storeScanViewCache = maps.Clone(src.storeScanViewCache)
	case "storeScanScalarCache":
		dst.storeScanScalarCache = maps.Clone(src.storeScanScalarCache)
	case "storeScanReadOnlyCache":
		dst.storeScanReadOnlyCache = maps.Clone(src.storeScanReadOnlyCache)
	case "storeScanViewOnlyCache":
		if src.storeScanViewOnlyCache != nil {
			dst.storeScanViewOnlyCache = make(map[Type]bool)
		}
	default:
		return false
	}
	return true
}

func cloneStoreFlowSummariesForBodyWorker(src map[*ast.FuncDecl]*storeFlowSummary) map[*ast.FuncDecl]*storeFlowSummary {
	if src == nil {
		return nil
	}
	out := make(map[*ast.FuncDecl]*storeFlowSummary, len(src))
	memo := make(map[*storeFlowSummary]*storeFlowSummary, len(src))
	for decl, summary := range src {
		if summary == nil {
			out[decl] = nil
			continue
		}
		if prior := memo[summary]; prior != nil {
			out[decl] = prior
			continue
		}
		cloned := *summary
		cloned.retKind = slices.Clone(summary.retKind)
		cloned.reachKind = slices.Clone(summary.reachKind)
		for i, row := range summary.reachKind {
			cloned.reachKind[i] = slices.Clone(row)
		}
		memo[summary] = &cloned
		out[decl] = &cloned
	}
	return out
}

// These caches can contain worker read-shadow identities or transient solver
// state. Keep their worker copies out of unit exports and root merges. Warm graph
// entries built before the fork contain canonical declarations and immutable
// slices, so cloning their outer maps avoids repeating whole-graph walks.
