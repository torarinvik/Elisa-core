package semantic

import (
	"elisacore/src/ast"
)

// analyzeFunctionBodyStmts analyzes a function body as a top-level statement list, threading the
// docs/107 early-return size-guard fall-through facts across sibling statements: a guard clause
// `if base.size[mem] < N: return` leaves `size >= N` proven for the statements that follow it. Facts
// are scoped to this body and dropped on return. (Nested blocks get the same treatment in
// analyzeBlockInScope; function bodies are iterated directly here, so they need the same threading.)
func (a *Analyzer) analyzeFunctionBodyStmts(body []ast.Stmt) {
	a.visitFunctionBodyStmts(body, a.analyzeStmt)
}

func (a *Analyzer) visitFunctionBodyStmts(body []ast.Stmt, visit func(ast.Stmt)) {
	a.visitFunctionBodyWithElementScan(body, visit, false)
}

func (a *Analyzer) visitFunctionBodyWithElementScan(body []ast.Stmt, visit func(ast.Stmt), closedPrimitive bool) {
	savedSizeGuards := len(a.overlaySizeGuards)
	savedElementReads, savedElementStates, savedBinderLoops, savedLoop := a.currentElementReads, a.currentElementStates, a.currentElementBinderLoops, a.currentElementLoop
	savedExtendSources := a.currentElementExtendSources
	if closedPrimitive {
		// The validated cached grammar has no identifiers, element accesses,
		// containers, calls or loops. Its element-read summary is exactly empty.
		a.currentElementReads = map[*ast.IndexExpr]bool{}
		a.currentElementBinderLoops = map[*ast.IterForStmt]bool{}
		a.currentElementLoop = nil
		a.currentElementExtendSources = map[ast.Expr]bool{}
	} else {
		a.currentElementReads, a.currentElementBinderLoops, a.currentElementLoop = a.scanLocalContainerElementReads(body)
	}
	a.currentElementStates = map[*Symbol]regionRefState{}
	defer func() {
		a.currentElementReads, a.currentElementStates, a.currentElementBinderLoops, a.currentElementLoop = savedElementReads, savedElementStates, savedBinderLoops, savedLoop
		a.currentElementExtendSources = savedExtendSources
	}()
	for _, stmt := range body {
		visit(stmt)
		a.applyOverlayFallthroughGuard(stmt)
	}
	a.overlaySizeGuards = a.overlaySizeGuards[:savedSizeGuards]
}

// analyzeFunc analyzes a function body with its type parameters left OPAQUE (the
// template pass). See analyzeFuncWithTypeArgs for the monomorphized re-analysis.
func (a *Analyzer) analyzeFunc(fn *ast.FuncDecl) {
	if a != nil && a.retained != nil {
		a.retained.noteFuncNameUsageFunctionAnalysis(fn)
	}
	a.invalidateRetainedFunctionIndexBoundsProofs(fn)
	if r := a.retained; r != nil && fn != nil && r.depth == 0 {
		r.depth++
		before := len(a.diagnostics)
		defer func() {
			r.depth--
			r.windows[fn] = [2]int{before, len(a.diagnostics)}
		}()
	}
	if fn != nil && !a.suppressDiagnostics && a.convergeDepth == 0 && !a.ambientFillAnalyzed[fn] && a.summaryAnalyzable(fn) && a.summaryRelevant(fn) && a.funcIsCyclic(fn) {
		a.convergeSummaries(fn)
	}
	if fn != nil {
		for _, regionParam := range fn.RegionParams {
			a.checkProgramRegionShadow(regionParam, fn)
		}
	}
	a.analyzeFuncWithTypeArgs(fn, nil)
}

// analyzeFuncWithTypeArgs is analyzeFunc with the generic parameters bound to CONCRETE
// arguments. Passing nil reproduces the template pass exactly.
//
// Type-directed typing rules cannot be decided on a template: `T& + n` is pointer
// arithmetic while `i64& + n` is value arithmetic (scalarRefValueContextOperandType), so a
// body analyzed once with T opaque records types that are wrong for the instantiation.
// Re-analyzing per instantiation is what makes the instantiated type govern — the most
// specific type wins, as monomorphization requires.
func (a *Analyzer) analyzeFuncWithTypeArgs(fn *ast.FuncDecl, typeArgs []Type) {
	if a != nil && a.retained != nil {
		a.retained.noteFuncNameUsageFunctionAnalysis(fn)
	}
	defer a.beginFunctionProofContribution(fn, typeArgs)()
	restoreProofOwner := a.enterIndexProofOwner(fn, false)
	defer restoreProofOwner()
	a.invalidateRetainedFunctionIndexBoundsProofs(fn)
	sym, ok := a.symbolForFuncDecl(fn)
	if !ok || sym == nil {
		a.errorf(fn.Pos(), "internal error: missing function symbol for %q", fn.Name)
		return
	}
	fnType, _ := sym.Type.(*FuncType)
	if fnType == nil {
		a.errorf(fn.Pos(), "internal error: function %q does not resolve to a function type", fn.Name)
		return
	}
	fnType = a.privateFuncType(fn, fnType)
	defer a.enterFunctionBoundsFacts()()
	// Deferred storage-view errors are scoped per function: a nested function resolves its own
	// pending uses at its checkRegionLifetimes; restore the enclosing function's pending afterward.
	if a.funcAnalysisActive == nil {
		a.funcAnalysisActive = map[*ast.FuncDecl]bool{}
	}
	wasActive := a.funcAnalysisActive[fn]
	a.funcAnalysisActive[fn] = true
	// NUL-termination proofs are keyed by local name, so they belong to one body:
	// a `path.push(0)` in another function proves nothing about this one's `path`.
	outerNulTerminated := a.nulTerminatedBuffers
	a.nulTerminatedBuffers = map[string]bool{}
	defer func() {
		a.nulTerminatedBuffers = outerNulTerminated
		if !wasActive {
			delete(a.funcAnalysisActive, fn)
		}
	}()
	savedPending := a.pendingStorageViewErrors
	a.pendingStorageViewErrors = nil
	defer func() { a.pendingStorageViewErrors = savedPending }()
	restoreFreshShapes := a.enterFreshShapeScope(fn, typeArgs, "body")
	defer restoreFreshShapes()
	savedScope := a.currentScope
	savedReturn := a.currentReturn
	savedFuncDecl := a.currentFuncDecl
	savedFuncType := a.currentFuncType
	savedReturnFreshStatus := a.returnFreshShapeStatus
	savedRegions := a.currentRegions
	savedRegionsBorrowed := a.currentRegionsBorrowed
	savedStructLocalAllocRegion := a.currentStructLocalAllocRegion
	savedStructInteriorRegionTaint := a.currentStructInteriorRegionTaint
	savedRegionMarks := a.currentRegionMarks
	savedRegionRefs := a.currentRegionRefs
	savedRegionRefsBorrowed := a.currentRegionRefsBorrowed
	savedAffineValues := a.currentAffineValues
	savedDropLocals := a.currentDropLocals
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	savedStorageViewDeps := a.currentStorageViewDeps
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	savedAllocExpr := a.currentAllocExpr
	savedFunctionPermissions := a.currentFunctionUsedPermissions
	savedFunctionPermissionRefs := a.currentFunctionUsedPermissionRefs
	savedFunctionGuardedIndexes := a.currentFunctionGuardedIndexes
	savedFunctionExpectsVectorize := a.currentFunctionExpectsVectorize
	savedProgressSummary := a.currentProgressSummary
	savedTrustedNonProgressDepth := a.currentTrustedNonProgressDepth
	savedTrustedAssumeProgressDepth := a.currentTrustedAssumeProgressDepth
	savedImplicitScopes := a.currentImplicitScopes
	savedReturnProvenance := a.currentReturnProvenance
	savedReturnBorrowedOwnerRefs := a.currentReturnBorrowedOwnerRefs
	savedConservativeCallWidenings := a.currentConservativeCallWidenings
	savedRegionFactTransforms := a.currentRegionFactTransforms
	// Mutable-alias access tracking is per-function: borrows are function-local, so a fresh
	// function must start with an empty access/binding map. Failing to reset these leaked
	// borrows (e.g. an outer function's `r = a.begin` read) into later functions, where they
	// caused spurious overlap conflicts (a write to param `a` "conflicting" with a stale
	// `a.begin` borrow) and false Unsafe.Alias inference. Save + reset + restore so nested
	// functions still see the enclosing state on return.
	savedAliasAccesses := a.currentAliasAccesses
	savedAliasBindings := a.currentAliasBindings
	savedAliasCarriers := a.currentAliasCarriers
	savedAliasCarrierFieldOverrides := a.currentAliasCarrierFieldOverrides
	a.currentAliasAccesses = nil
	a.currentAliasBindings = nil
	a.currentAliasCarriers = nil
	a.currentAliasCarrierFieldOverrides = nil
	a.currentScope = NewScope(a.globalScope)
	a.currentRegions = map[*Symbol]regionState{}
	a.currentRegionsBorrowed = false
	a.currentRegionMarks = map[*Symbol]regionMarkState{}
	a.currentRegionRefs = map[*Symbol]regionRefState{}
	a.currentRegionRefsBorrowed = false
	a.currentStructInteriorRegionTaint = map[*Symbol]string{}
	a.currentStructLocalAllocRegion = map[*Symbol]string{}
	a.currentAffineValues = map[affineValueKey]affineValueState{}
	a.currentDropLocals = nil
	a.currentBorrowedOwnerRefs = map[*Symbol]borrowedOwnerRefState{}
	a.currentFunctionValues = map[*Symbol]*FuncType{}
	a.currentSpecializedValueTypes = map[*Symbol]Type{}
	a.currentValueBindings = map[*Symbol]ast.Expr{}
	a.currentStorageViewDeps = map[*Symbol]storageViewDependencyState{}
	a.currentPackedVariantViews = map[*Symbol]*PackedVariantViewType{}
	a.currentPackedStores = map[string]*PackedEnumStoreType{}
	a.currentPackedStoreResolutions = map[*Symbol]packedStoreResolution{}
	a.currentAllocExpr = nil
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	a.currentFunctionGuardedIndexes = nil
	a.currentProgressSummary = a.beginFunctionProgressSummary(fn)
	a.currentTrustedNonProgressDepth = 0
	a.currentTrustedAssumeProgressDepth = 0
	a.currentReturnProvenance = regionRefState{}
	a.currentReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	a.beginReturnBorrowSummary(fn)
	a.currentFuncDecl = fn
	a.storeLivedCache = nil
	a.currentFuncType = fnType
	if fn != nil && (fn.IsGhost || fn.IsLaw) {
		a.ghostReadAllowed++
		defer func() { a.ghostReadAllowed-- }()
	}
	if fn != nil && fn.IsLemma {
		// Track the lemma while its body is analyzed so a self/mutual-recursive lemma call inside it
		// does NOT inject this lemma's own (not-yet-proven) ensure as a fact — that would be circular
		// reasoning (assuming what we are trying to prove). Inductive lemmas are a future extension
		// gated on a verified `decreases` measure.
		if a.lemmasInAnalysis == nil {
			a.lemmasInAnalysis = map[*ast.FuncDecl]bool{}
		}
		a.lemmasInAnalysis[fn] = true
		defer delete(a.lemmasInAnalysis, fn)
	}
	savedSawPlainValueReturn := a.currentFuncSawPlainValueReturn
	a.currentFuncSawPlainValueReturn = false
	defer func() { a.currentFuncSawPlainValueReturn = savedSawPlainValueReturn }()
	a.currentConservativeCallWidenings = map[*Symbol][]conservativeCallWidening{}
	a.currentRegionFactTransforms = nil
	if fnType != nil {
		a.currentReturn = fnType.Return
		a.returnFreshShapeStatus = freshReturnTracker(fnType.Return)
	}
	allParamDecls := fn.Params
	ordinaryParams := ordinaryExplicitParams(allParamDecls)
	if !ordinaryParams {
		allParamDecls = a.expandedFuncDeclParams(fn)
	}
	// MONOMORPHIZATION: bind the signature to the concrete arguments before the parameter
	// symbols are defined from it. Binding only the type-parameter SCOPE is not enough —
	// parameter symbols take their types from fnType, so without this substitution the body
	// still sees `T&` and every type-directed rule decides as if T were unknown.
	if len(typeArgs) != 0 && fnType != nil {
		if bindings := funcTypeParamBindings(fn, typeArgs); len(bindings) != 0 {
			if specialized, ok := a.substituteType(fnType, bindings, nil, nil, nil).(*FuncType); ok && specialized != nil {
				fnType = specialized
				a.currentFuncType = fnType
				a.currentReturn = fnType.Return
			}
		}
	}
	a.withSourceGenericParams(fn, fn.GenericParams, typeArgs, func() {
		a.withSourceRegionParams(fn, fn.RegionParams, func() {
			a.withSourcePermissionParams(fn, fn.PermissionParams, func() {
				a.withSourceShapeParams(fn, fnType.ShapeParams, func() {
					for i, param := range allParamDecls {
						var ptype Type = invalidType
						if fnType != nil && i < len(fnType.Params) {
							ptype = fnType.Params[i]
						}
						symType := ptype
						if ref, ok := ptype.(*RefType); ok && !ref.Mutable && isLegacyOutParamName(param.Name) {
							cloned := cloneRefType(ref)
							cloned.Mutable = true
							symType = cloned
						}
						sym := &Symbol{Name: param.Name, Kind: SymbolParam, Type: symType, Node: fn, ParamIndex: i, Mutable: a.paramIsMutable(param)}
						a.defineLocal(sym, param.Position)
						a.bindActivePackedStoreType(ptype)
						a.recordValueBinding(sym, nil)
						a.recordBorrowedOwnerRefParam(sym)
						if isOwnedTypeExpr(param.Type) {
							a.registerOwnedStoreOwner(sym)
						}
						if state, ok := a.abstractParamRegionRefStateForBinding(ptype, i); ok {
							a.recordResolvedRegionRefBinding(sym, state)
						}
						// docs/126 §2: a drop-typed parameter was MOVED in by the caller,
						// so this frame owns the obligation and releases it on the way out.
						// (Only the real body pass registers these — the provenance and
						// borrowed-owner inference passes re-walk the same body and must
						// not double-enter the drop ledger.)
						a.noteDropParam(fn, sym, symType)
					}
					if fnType != nil && fnType.RegionPolymorphic {
						a.defineRegionPolymorphicParamSymbol(fn, fnType)
					}
					a.defineRegionParamValueSymbols(fn)
					if fnType != nil {
						a.defineImplicitPackedStoreParamSymbols(fn, fnType)
					}
					savedBodyImplicitScopes := a.currentImplicitScopes
					if bindings := a.implicitBindingsForCurrentFunction(fnType); len(bindings) != 0 {
						a.currentImplicitScopes = pushExprBindingScope(savedBodyImplicitScopes, bindings)
					}
					if ordinaryParams {
						a.seedParamRefinementFacts(allParamDecls)
						a.seedParamWhereRefinementFacts(allParamDecls)
					} else {
						// Keep expansion diagnostics in their original order for malformed declarations.
						a.seedParamRefinementFacts(a.readOnlyExpandedFuncDeclParams(fn))
						a.seedParamWhereRefinementFacts(a.readOnlyExpandedFuncDeclParams(fn))
					}
					a.seedRequiresAsAssertFacts(fn)
					savedChangesPaths, savedHasChanges := a.currentChangesPaths, a.currentHasChanges
					savedPreservesPaths, savedHasPreserves := a.currentPreservesPaths, a.currentHasPreserves
					a.currentChangesPaths = a.resolveFramePaths(fn.Changes, "changes")
					a.currentPreservesPaths = a.resolveFramePaths(fn.Preserves, "preserves")
					a.currentHasChanges = len(fn.Changes) != 0
					a.currentHasPreserves = len(fn.Preserves) != 0
					a.currentFunctionExpectsVectorize = fulfillsVectorizes(fn)
					a.expandFulfills(fn)
					a.checkFrameConsistency(fn)
					defer func() {
						a.currentChangesPaths, a.currentHasChanges = savedChangesPaths, savedHasChanges
						a.currentPreservesPaths, a.currentHasPreserves = savedPreservesPaths, savedHasPreserves
					}()
					a.analyzeParamWhereRefinements(fn)
					a.analyzeRequiresClauses(fn)
					a.analyzeEnsureClauses(fn, fnType)
					a.analyzeReturnWhereRefinement(fn, fnType)
					a.analyzeIncrementalFunctionBody(fn, typeArgs)
					a.verifyLemmaEnsures(fn)
					// docs/126 D1: close the drop ledger while the affine state is still
					// live — records the fall-through drops and folds each destructor's
					// effects into this function's inferred effect set (below, line ~263).
					a.finalizeImplicitDrops()
					a.currentImplicitScopes = savedBodyImplicitScopes
				})
			})
		})
	})
	if fnType != nil && !blockDefinitelyExits(fn.Body) {
		a.validateCurrentFuncPoststates()
	}
	a.checkSentinelIndex(fn)
	a.checkFlowComplexity(fn)
	a.checkAllocationChurn(fn)
	a.checkUnfoldableClassifier(fn)
	a.checkPoolChurn(fn)
	a.checkTaskGroupChurn(fn)
	a.checkLockChurn(fn)
	a.checkAtomicHotLoops(fn)
	a.checkAwaitHotLoops(fn)
	a.checkUnreservedCountingFills(fn)
	a.checkPushLoopExtendable(fn)
	a.checkDiscardedLoopValues(fn)
	a.checkNarrowableHandleWidths(fn)
	a.checkRegionLifetimes(fn)
	if fnType != nil {
		// An explicit `-> T @r` contract is authoritative. Body inference may
		// fail to observe the dependency when the returned value is rebuilt in a
		// helper or a generic branch, but replacing the declared contract with an
		// empty summary would make callers lose the lifetime guarantee.
		declaredReturnProvenance := fnType.ReturnProvenance
		declaredReturnProvenanceKnown := fnType.ReturnRegion != "" && fnType.ReturnProvenanceKnown
		if !declaredReturnProvenanceKnown {
			if summary, ok := abstractParamOnlyRegionRefState(a.currentReturnProvenance); ok {
				fnType.ReturnProvenance = summary
			} else {
				fnType.ReturnProvenance = regionRefState{}
			}
		}
		fnType.ReturnProvenanceKnown = true
		if declaredReturnProvenanceKnown {
			fnType.ReturnProvenance = declaredReturnProvenance
		}
		if hasBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs) {
			fnType.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs)
		} else {
			fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
		}
		fnType.ReturnBorrowedOwnerRefsKnown = true
		fnType.FreshReturnShapeParams = mergeShapeParamNames(fnType.FreshReturnShapeParams, inferredFreshReturnShapeParams(a.returnFreshShapeStatus))
		inferredRefs := canonicalizePermissionRefs(a.currentFunctionUsedPermissionRefs)
		inferredPermissions := permissionFamiliesFromRefs(inferredRefs)
		// Only explicit trusted scopes suppress inferred effects. Source identity
		// must not erase effects authorized by an ordinary can scope.
		fnType.PermissionRefs = mergePermissionRefs(fnType.DeclaredPermissionRefs, inferredRefs)
		fnType.Permissions = mergePermissionFamilies(fnType.DeclaredPermissions, inferredPermissions)
		a.checkHotContract(fn, fnType)
		a.checkPerfContracts(fn, fnType)
		a.checkLawContract(fn, fnType)
		a.checkFunctionLevelFulfills(fn, fnType)
		a.checkTermination(fn, fnType)
		a.finalizeFunctionAnalysis(fn, fnType)
	}
	a.finishFunctionProgressSummary(fn, a.currentFunctionUsedPermissionRefs)
	a.reportUnconsumedProtocolValues()
	a.finishReturnBorrowSummary(fn)
	if fn != nil && (!a.suppressDiagnostics || a.provisionalSummaryFn == fn) {
		if a.ambientFillAnalyzed == nil {
			a.ambientFillAnalyzed = map[*ast.FuncDecl]bool{}
		}
		a.ambientFillAnalyzed[fn] = true
		a.finishReturnElementSummary(fn)
	}
	a.currentScope = savedScope
	a.currentReturn = savedReturn
	a.currentFuncDecl = savedFuncDecl
	a.storeLivedCache = nil
	a.currentFuncType = savedFuncType
	a.returnFreshShapeStatus = savedReturnFreshStatus
	a.currentRegions = savedRegions
	a.currentRegionsBorrowed = savedRegionsBorrowed
	a.currentStructLocalAllocRegion = savedStructLocalAllocRegion
	a.currentStructInteriorRegionTaint = savedStructInteriorRegionTaint
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentRegionRefsBorrowed = savedRegionRefsBorrowed
	a.currentAffineValues = savedAffineValues
	a.currentDropLocals = savedDropLocals
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	a.currentStorageViewDeps = savedStorageViewDeps
	a.currentAliasAccesses = savedAliasAccesses
	a.currentAliasBindings = savedAliasBindings
	a.currentAliasCarriers = savedAliasCarriers
	a.currentAliasCarrierFieldOverrides = savedAliasCarrierFieldOverrides
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
	a.currentAllocExpr = savedAllocExpr
	a.currentFunctionUsedPermissions = savedFunctionPermissions
	a.currentFunctionUsedPermissionRefs = savedFunctionPermissionRefs
	a.currentFunctionGuardedIndexes = savedFunctionGuardedIndexes
	a.currentFunctionExpectsVectorize = savedFunctionExpectsVectorize
	a.currentProgressSummary = savedProgressSummary
	a.currentTrustedNonProgressDepth = savedTrustedNonProgressDepth
	a.currentTrustedAssumeProgressDepth = savedTrustedAssumeProgressDepth
	a.currentImplicitScopes = savedImplicitScopes
	a.currentReturnProvenance = savedReturnProvenance
	a.currentReturnBorrowedOwnerRefs = savedReturnBorrowedOwnerRefs
	a.currentConservativeCallWidenings = savedConservativeCallWidenings
	a.currentRegionFactTransforms = savedRegionFactTransforms
}
