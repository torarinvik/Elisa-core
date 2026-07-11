package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

// analyzeFunctionBodyStmts analyzes a function body as a top-level statement list, threading the
// docs/107 early-return size-guard fall-through facts across sibling statements: a guard clause
// `if base.size[mem] < N: return` leaves `size >= N` proven for the statements that follow it. Facts
// are scoped to this body and dropped on return. (Nested blocks get the same treatment in
// analyzeBlockInScope; function bodies are iterated directly here, so they need the same threading.)
func (a *Analyzer) analyzeFunctionBodyStmts(body []ast.Stmt) {
	savedSizeGuards := len(a.overlaySizeGuards)
	for _, stmt := range body {
		a.analyzeStmt(stmt)
		a.applyOverlayFallthroughGuard(stmt)
	}
	a.overlaySizeGuards = a.overlaySizeGuards[:savedSizeGuards]
}

func (a *Analyzer) analyzeFunc(fn *ast.FuncDecl) {
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
	// Deferred storage-view errors are scoped per function: a nested function resolves its own
	// pending uses at its checkRegionLifetimes; restore the enclosing function's pending afterward.
	savedPending := a.pendingStorageViewErrors
	a.pendingStorageViewErrors = nil
	defer func() { a.pendingStorageViewErrors = savedPending }()
	savedScope := a.currentScope
	savedReturn := a.currentReturn
	savedFuncDecl := a.currentFuncDecl
	savedFuncType := a.currentFuncType
	savedReturnFreshStatus := a.returnFreshShapeStatus
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedRegionRefs := a.currentRegionRefs
	savedAffineValues := a.currentAffineValues
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
	a.currentRegionMarks = map[*Symbol]regionMarkState{}
	a.currentRegionRefs = map[*Symbol]regionRefState{}
	a.currentStructInteriorRegionTaint = map[*Symbol]string{}
	a.currentStructLocalAllocRegion = map[*Symbol]string{}
	a.currentAffineValues = map[affineValueKey]affineValueState{}
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
	a.currentFuncDecl = fn
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
	explicitDecls := a.expandedFuncDeclParams(fn)
	allParamDecls := append([]ast.ParamDecl(nil), explicitDecls...)
	a.withGenericParams(fn.GenericParams, nil, func() {
		a.withRegionParams(fn.RegionParams, func() {
			a.withPermissionParams(fn.PermissionParams, func() {
				a.withShapeParams(fnType.ShapeParams, func() {
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
						if state, ok := a.abstractParamRegionRefState(ptype, i, map[string]bool{}); ok {
							a.recordResolvedRegionRefBinding(sym, state)
						}
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
					a.seedParamRefinementFacts(a.expandedFuncDeclParams(fn))
					a.seedParamWhereRefinementFacts(a.expandedFuncDeclParams(fn))
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
					a.analyzeFunctionBodyStmts(fn.Body)
					a.verifyLemmaEnsures(fn)
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
	a.checkNarrowableHandleWidths(fn)
	a.checkRegionLifetimes(fn)
	if fnType != nil {
		if summary, ok := abstractParamOnlyRegionRefState(a.currentReturnProvenance); ok {
			fnType.ReturnProvenance = summary
		} else {
			fnType.ReturnProvenance = regionRefState{}
		}
		fnType.ReturnProvenanceKnown = true
		if hasBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs) {
			fnType.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs)
		} else {
			fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
		}
		fnType.ReturnBorrowedOwnerRefsKnown = true
		fnType.FreshReturnShapeParams = mergeShapeParamNames(fnType.FreshReturnShapeParams, inferredFreshReturnShapeParams(a.returnFreshShapeStatus))
		inferredRefs := canonicalizePermissionRefs(a.currentFunctionUsedPermissionRefs)
		inferredPermissions := permissionFamiliesFromRefs(inferredRefs)
		fnType.PermissionRefs = mergePermissionRefs(fnType.DeclaredPermissionRefs, inferredRefs)
		fnType.Permissions = mergePermissionFamilies(fnType.DeclaredPermissions, inferredPermissions)
		// Trusted-runtime ENCAPSULATION: the stdlib implements safe abstractions with
		// raw-memory/panic internals, so those implementation details are not propagated
		// into the function's public signature (like Rust's `std` not being `unsafe` to
		// call). Ordinary user code is still required to declare explicit low-level
		// allocation, panic, and Unsafe grants.
		if a.enforceUnsafePermissions && fn != nil && isRuntimeStdPermissionInternal(fn.Pos().File) {
			fnType.PermissionRefs = filterOutTrustedStdlibPermissionRefs(fnType.PermissionRefs)
			fnType.Permissions = permissionFamiliesFromRefs(fnType.PermissionRefs)
		}
		a.checkHotContract(fn, fnType)
		a.checkPerfContracts(fn, fnType)
		a.checkLawContract(fn, fnType)
		a.checkFunctionLevelFulfills(fn, fnType)
		a.checkTermination(fn, fnType)
		a.finalizeFunctionAnalysis(fn, fnType)
	}
	a.finishFunctionProgressSummary(fn, a.currentFunctionUsedPermissionRefs)
	a.reportUnconsumedProtocolValues()
	a.currentScope = savedScope
	a.currentReturn = savedReturn
	a.currentFuncDecl = savedFuncDecl
	a.currentFuncType = savedFuncType
	a.returnFreshShapeStatus = savedReturnFreshStatus
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentAffineValues = savedAffineValues
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

func (a *Analyzer) inferFuncReturnProvenance(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || fnType.ReturnProvenanceKnown {
		return
	}
	if a.returnProvenanceInProgress[fn] {
		return
	}
	a.returnProvenanceInProgress[fn] = true
	defer delete(a.returnProvenanceInProgress, fn)

	savedScope := a.currentScope
	savedReturn := a.currentReturn
	savedFuncDecl := a.currentFuncDecl
	savedFuncType := a.currentFuncType
	savedReturnFreshStatus := a.returnFreshShapeStatus
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedRegionRefs := a.currentRegionRefs
	savedAffineValues := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	savedStorageViewDeps := a.currentStorageViewDeps
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	savedFunctionPermissions := a.currentFunctionUsedPermissions
	savedFunctionPermissionRefs := a.currentFunctionUsedPermissionRefs
	savedFunctionGuardedIndexes := a.currentFunctionGuardedIndexes
	savedFunctionExpectsVectorize := a.currentFunctionExpectsVectorize
	savedReturnProvenance := a.currentReturnProvenance
	savedReturnBorrowedOwnerRefs := a.currentReturnBorrowedOwnerRefs
	savedConservativeCallWidenings := a.currentConservativeCallWidenings
	savedSuppressDiagnostics := a.suppressDiagnostics
	savedSuppressOptimizationFacts := a.suppressOptimizationFacts
	savedNamespace := a.currentNamespace
	savedUsings := a.currentUsings

	a.currentScope = NewScope(a.globalScope)
	a.currentReturn = fnType.Return
	a.currentFuncDecl = nil
	a.currentFuncType = nil
	a.returnFreshShapeStatus = freshReturnTracker(fnType.Return)
	a.currentRegions = map[*Symbol]regionState{}
	a.currentRegionMarks = map[*Symbol]regionMarkState{}
	a.currentRegionRefs = map[*Symbol]regionRefState{}
	a.currentStructInteriorRegionTaint = map[*Symbol]string{}
	a.currentStructLocalAllocRegion = map[*Symbol]string{}
	a.currentAffineValues = map[affineValueKey]affineValueState{}
	a.currentBorrowedOwnerRefs = map[*Symbol]borrowedOwnerRefState{}
	a.currentFunctionValues = map[*Symbol]*FuncType{}
	a.currentSpecializedValueTypes = map[*Symbol]Type{}
	a.currentValueBindings = map[*Symbol]ast.Expr{}
	a.currentStorageViewDeps = map[*Symbol]storageViewDependencyState{}
	a.currentPackedVariantViews = map[*Symbol]*PackedVariantViewType{}
	a.currentPackedStores = map[string]*PackedEnumStoreType{}
	a.currentPackedStoreResolutions = map[*Symbol]packedStoreResolution{}
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	a.currentFunctionGuardedIndexes = nil
	a.currentReturnProvenance = regionRefState{}
	a.currentReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	a.currentConservativeCallWidenings = nil
	a.suppressDiagnostics = true
	a.suppressOptimizationFacts = true
	if fnType != nil {
		if idx := strings.LastIndex(fnType.Name, "."); idx >= 0 {
			a.currentNamespace = fnType.Name[:idx]
		} else {
			a.currentNamespace = ""
		}
		a.currentUsings = nil
	}

	a.withGenericParams(fn.GenericParams, nil, func() {
		a.withRegionParams(fn.RegionParams, func() {
			a.withPermissionParams(fn.PermissionParams, func() {
				a.withShapeParams(fnType.ShapeParams, func() {
					for i, param := range a.expandedFuncDeclParams(fn) {
						var ptype Type = invalidType
						if i < len(fnType.Params) {
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
						if state, ok := a.abstractParamRegionRefState(ptype, i, map[string]bool{}); ok {
							a.recordResolvedRegionRefBinding(sym, state)
						}
					}
					a.defineRegionParamValueSymbols(fn)
					a.seedParamRefinementFacts(a.expandedFuncDeclParams(fn))
					a.seedParamWhereRefinementFacts(a.expandedFuncDeclParams(fn))
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
					a.analyzeFunctionBodyStmts(fn.Body)
				})
			})
		})
	})

	if hasRegionProvenance(a.currentReturnProvenance) {
		fnType.ReturnProvenance = cloneRegionRefState(a.currentReturnProvenance)
	} else {
		fnType.ReturnProvenance = regionRefState{}
	}
	fnType.ReturnProvenanceKnown = true
	if hasBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs) {
		fnType.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs)
	} else {
		fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	}
	fnType.ReturnBorrowedOwnerRefsKnown = true

	a.currentScope = savedScope
	a.currentReturn = savedReturn
	a.currentFuncDecl = savedFuncDecl
	a.currentFuncType = savedFuncType
	a.returnFreshShapeStatus = savedReturnFreshStatus
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentAffineValues = savedAffineValues
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	a.currentStorageViewDeps = savedStorageViewDeps
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
	a.currentFunctionUsedPermissions = savedFunctionPermissions
	a.currentFunctionUsedPermissionRefs = savedFunctionPermissionRefs
	a.currentFunctionGuardedIndexes = savedFunctionGuardedIndexes
	a.currentFunctionExpectsVectorize = savedFunctionExpectsVectorize
	a.currentReturnProvenance = savedReturnProvenance
	a.currentReturnBorrowedOwnerRefs = savedReturnBorrowedOwnerRefs
	a.currentConservativeCallWidenings = savedConservativeCallWidenings
	a.suppressDiagnostics = savedSuppressDiagnostics
	a.suppressOptimizationFacts = savedSuppressOptimizationFacts
	a.currentNamespace = savedNamespace
	a.currentUsings = savedUsings
}

func (a *Analyzer) inferFuncReturnBorrowedOwnerRefs(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || fnType.ReturnBorrowedOwnerRefsKnown {
		return
	}
	if a.returnBorrowedOwnerRefInProgress[fn] {
		return
	}
	a.returnBorrowedOwnerRefInProgress[fn] = true
	defer delete(a.returnBorrowedOwnerRefInProgress, fn)

	savedScope := a.currentScope
	savedReturn := a.currentReturn
	savedFuncDecl := a.currentFuncDecl
	savedFuncType := a.currentFuncType
	savedReturnFreshStatus := a.returnFreshShapeStatus
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedRegionRefs := a.currentRegionRefs
	savedAffineValues := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	savedStorageViewDeps := a.currentStorageViewDeps
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	savedFunctionPermissions := a.currentFunctionUsedPermissions
	savedFunctionPermissionRefs := a.currentFunctionUsedPermissionRefs
	savedFunctionGuardedIndexes := a.currentFunctionGuardedIndexes
	savedFunctionExpectsVectorize := a.currentFunctionExpectsVectorize
	savedReturnProvenance := a.currentReturnProvenance
	savedReturnBorrowedOwnerRefs := a.currentReturnBorrowedOwnerRefs
	savedConservativeCallWidenings := a.currentConservativeCallWidenings
	savedSuppressDiagnostics := a.suppressDiagnostics
	savedSuppressOptimizationFacts := a.suppressOptimizationFacts
	savedNamespace := a.currentNamespace
	savedUsings := a.currentUsings

	a.currentScope = NewScope(a.globalScope)
	a.currentReturn = fnType.Return
	a.currentFuncDecl = nil
	a.currentFuncType = nil
	a.returnFreshShapeStatus = freshReturnTracker(fnType.Return)
	a.currentRegions = map[*Symbol]regionState{}
	a.currentRegionMarks = map[*Symbol]regionMarkState{}
	a.currentRegionRefs = map[*Symbol]regionRefState{}
	a.currentStructInteriorRegionTaint = map[*Symbol]string{}
	a.currentStructLocalAllocRegion = map[*Symbol]string{}
	a.currentAffineValues = map[affineValueKey]affineValueState{}
	a.currentBorrowedOwnerRefs = map[*Symbol]borrowedOwnerRefState{}
	a.currentFunctionValues = map[*Symbol]*FuncType{}
	a.currentSpecializedValueTypes = map[*Symbol]Type{}
	a.currentValueBindings = map[*Symbol]ast.Expr{}
	a.currentStorageViewDeps = map[*Symbol]storageViewDependencyState{}
	a.currentPackedVariantViews = map[*Symbol]*PackedVariantViewType{}
	a.currentPackedStores = map[string]*PackedEnumStoreType{}
	a.currentPackedStoreResolutions = map[*Symbol]packedStoreResolution{}
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	a.currentFunctionGuardedIndexes = nil
	a.currentReturnProvenance = regionRefState{}
	a.currentReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	a.currentConservativeCallWidenings = nil
	a.suppressDiagnostics = true
	a.suppressOptimizationFacts = true
	if fnType != nil {
		if idx := strings.LastIndex(fnType.Name, "."); idx >= 0 {
			a.currentNamespace = fnType.Name[:idx]
		} else {
			a.currentNamespace = ""
		}
		a.currentUsings = nil
	}

	a.withGenericParams(fn.GenericParams, nil, func() {
		a.withRegionParams(fn.RegionParams, func() {
			a.withPermissionParams(fn.PermissionParams, func() {
				a.withShapeParams(fnType.ShapeParams, func() {
					for i, param := range a.expandedFuncDeclParams(fn) {
						var ptype Type = invalidType
						if i < len(fnType.Params) {
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
						if state, ok := a.abstractParamRegionRefState(ptype, i, map[string]bool{}); ok {
							a.recordResolvedRegionRefBinding(sym, state)
						}
					}
					a.defineRegionParamValueSymbols(fn)
					a.seedParamRefinementFacts(a.expandedFuncDeclParams(fn))
					a.seedParamWhereRefinementFacts(a.expandedFuncDeclParams(fn))
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
					a.analyzeFunctionBodyStmts(fn.Body)
				})
			})
		})
	})

	if hasBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs) {
		fnType.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs)
	} else {
		fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	}
	fnType.ReturnBorrowedOwnerRefsKnown = true

	a.currentScope = savedScope
	a.currentReturn = savedReturn
	a.currentFuncDecl = savedFuncDecl
	a.currentFuncType = savedFuncType
	a.returnFreshShapeStatus = savedReturnFreshStatus
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentAffineValues = savedAffineValues
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	a.currentStorageViewDeps = savedStorageViewDeps
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
	a.currentFunctionUsedPermissions = savedFunctionPermissions
	a.currentFunctionUsedPermissionRefs = savedFunctionPermissionRefs
	a.currentFunctionGuardedIndexes = savedFunctionGuardedIndexes
	a.currentFunctionExpectsVectorize = savedFunctionExpectsVectorize
	a.currentReturnProvenance = savedReturnProvenance
	a.currentReturnBorrowedOwnerRefs = savedReturnBorrowedOwnerRefs
	a.currentConservativeCallWidenings = savedConservativeCallWidenings
	a.suppressDiagnostics = savedSuppressDiagnostics
	a.suppressOptimizationFacts = savedSuppressOptimizationFacts
	a.currentNamespace = savedNamespace
	a.currentUsings = savedUsings
}

// analyzeRequiresClauses type-checks a function's value-contract preconditions in the parameter
// scope (no `result` binding). Each must be bool. Analysis attaches types so the backend can emit
// the entry checks (debug builds only). Called from each body-analysis branch before the body.
func (a *Analyzer) analyzeRequiresClauses(fn *ast.FuncDecl) {
	// Reset the cross-variable bound-equality relation at every function entry. The relation is a
	// global analyzer field (like currentIndexBounds) but a top-level `n = xs.count` binding records
	// into it without a save/restore, so it must be cleared per function to avoid leaking equalities
	// from one function body into the next. This is the single per-function entry hook (called
	// unconditionally before each body, and nowhere else).
	a.currentBoundEqual = nil
	a.ghostReadAllowed++ // `requires` is a spec position: ghost vars/functions are readable here.
	defer func() { a.ghostReadAllowed-- }()
	for i, req := range fn.Requires {
		if req == nil {
			continue
		}
		reqType := a.analyzeSpecClauseExpr(req, "requires")
		if reqType != nil && !IsBoolType(reqType) {
			a.errorf(req.Pos(), "requires clause must be bool, got %s", reqType)
		}
		// A `requires n == xs.count` precondition is an ASSUMPTION inside the body (the callee may rely
		// on it; callers must establish it — enforced by the runtime check and brick 86-5's static
		// discharge). Seed it as a bound equality so `for i in 0..<n: xs[i]` discharges in the body.
		a.collectBoundEqualitiesForCondition(req, true)
		// Likewise seed the interval prover from a numeric-comparison precondition (`requires
		// alignment >= 1`, `requires n < cap`): the clause holds throughout the body, so recording
		// its range fact lets tier-1/tier-2 AND the SMT modulo-soundness gate (provablyPositive) see
		// the bound — which is what makes a guarded `value % alignment` use the precise Euclidean
		// model rather than an opaque symbol. Reuses the branch-fact narrower (truthy = assumed).
		a.seedRangeFactsFromCondition(req)
		if proof := proofAt(fn.RequiresProofs, i); proof != nil {
			a.analyzeScopedProofGoal(proof.Pos(), proof.Goal, proof.Proof, true, "", "requires by scoped")
		}
	}
}

// seedRangeFactsFromCondition records the integer range fact implied by a boolean precondition onto
// the function-entry scope. A bare comparison (`x >= 1`) narrows directly; a conjunction (`a >= 0 and
// a < n`) seeds each side. Anything else is ignored (sound: fewer facts only forgoes proofs). This is
// the `requires`-clause analogue of the branch-flow narrowing already done for `if` guards.
func (a *Analyzer) seedRangeFactsFromCondition(cond ast.Expr) {
	switch n := cond.(type) {
	case *ast.ParenExpr:
		a.seedRangeFactsFromCondition(n.Inner)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			a.seedRangeFactsFromCondition(n.Left)
			a.seedRangeFactsFromCondition(n.Right)
		case lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_GT, lexer.TOKEN_GTEQ, lexer.TOKEN_EQEQ:
			a.gatherNumericRangeRefinement(a.currentScope, n, true)
		}
	}
}

// analyzeEnsureClauses type-checks a function's value-contract postconditions in a child scope that
// binds `result` to the return type (so `ensure result >= 0` works). Each must be bool. The backend
// emits the checks at every return (debug builds only). `old(...)` (the entry-time value of an
// expression) is recognized here via inEnsureContext and captured at function entry by the backend.
func (a *Analyzer) analyzeEnsureClauses(fn *ast.FuncDecl, fnType *FuncType) {
	if len(fn.EnsureValues) == 0 {
		return
	}
	saved := a.currentScope
	scope := NewScope(saved)
	if fnType != nil && fnType.Return != nil && !isVoidType(fnType.Return) {
		scope.Define(&Symbol{Name: "result", Kind: SymbolLocal, Type: fnType.Return})
	}
	a.currentScope = scope
	a.inEnsureContext = true
	a.ghostReadAllowed++ // `ensure` is a contract: ghost body locals are readable here.
	defer func() { a.inEnsureContext = false; a.ghostReadAllowed-- }()
	for i, e := range fn.EnsureValues {
		if e == nil {
			continue
		}
		t := a.analyzeSpecClauseExpr(e, "ensure")
		if t != nil && !IsBoolType(t) {
			a.errorf(e.Pos(), "ensure clause must be bool, got %s", t)
		}
		if proof := proofAt(fn.EnsureProofs, i); proof != nil {
			a.analyzeScopedProofGoal(proof.Pos(), proof.Goal, proof.Proof, true, "", "ensure by scoped")
		}
	}
	a.currentScope = saved
}

// analyzeSpecClauseExpr type-checks a `requires`/`ensure` clause expression AND enforces that it is
// pure / side-effect-free — defense-in-depth mirroring the `where`/`refine` predicate and `law` body
// purity checks. A spec-position call `p(x)` is canonicalized by the verifier to a single
// deterministic uninterpreted symbol; that canonicalization is only SOUND when `p` is pure. An
// effectful or non-deterministic call (reading a mutable global, `random()`, `time()`, IO, mutation,
// allocation, …) would make the same syntactic clause denote different values at different points,
// so it must be rejected.
//
// The check reuses the existing effect-accumulation machinery rather than re-deriving purity: every
// effect a sub-expression performs is recorded into a.currentFunctionUsedPermissionRefs (the same set
// the law-purity check and @hot contract are judged against). We snapshot that set, analyze the
// clause, and reject if the clause introduced ANY effect. Pure user-helper calls (`ensure result ==
// sorted(xs)`) record no effects and are therefore allowed — exactly the law-body rule. Spec-position
// effects are not attributed to the enclosing function, so the snapshot is restored afterwards.
func (a *Analyzer) analyzeSpecClauseExpr(expr ast.Expr, clause string) Type {
	if expr == nil {
		return nil
	}
	savedRefs := a.currentFunctionUsedPermissionRefs
	start := len(savedRefs)
	t := a.analyzeExpr(expr)
	if introduced := a.currentFunctionUsedPermissionRefs[start:]; len(introduced) > 0 {
		a.errorf(expr.Pos(), "%s clause must be pure but uses the `%s` effect; a contract may not perform effects (no IO, allocation, mutation, time, or randomness) — an effectful or non-deterministic clause is unsound because the verifier treats a spec-position call as a single deterministic value", clause, lawEffectName(introduced[0]))
	}
	// Spec-position effects are verification-only and must not leak into the enclosing function's
	// inferred effect set; restore the snapshot regardless of outcome.
	a.currentFunctionUsedPermissionRefs = savedRefs
	return t
}

// exprReferencesGhostField reports whether an analyzed contract expression reads a `ghost` struct
// field. Such a clause is verification-only: the ghost field is erased in codegen, so a runtime
// check over it would dereference a field that does not exist at runtime. The clause is kept for
// static discharge but must be dropped from the backend's runtime-check set — mirroring the
// ghost-invariant split in analyzeStructInvariants. Walks the contract-expression shapes.
func (a *Analyzer) exprReferencesGhostField(expr ast.Expr) bool {
	switch n := expr.(type) {
	case nil:
		return false
	case *ast.ParenExpr:
		return a.exprReferencesGhostField(n.Inner)
	case *ast.UnaryExpr:
		return a.exprReferencesGhostField(n.Operand)
	case *ast.BinaryExpr:
		return a.exprReferencesGhostField(n.Left) || a.exprReferencesGhostField(n.Right)
	case *ast.TernaryExpr:
		return a.exprReferencesGhostField(n.Cond) || a.exprReferencesGhostField(n.Value) || a.exprReferencesGhostField(n.Alt)
	case *ast.IndexExpr:
		return a.exprReferencesGhostField(n.Object) || a.exprReferencesGhostField(n.Index) || a.exprReferencesGhostField(n.Fallback)
	case *ast.CallExpr:
		if a.exprReferencesGhostField(n.Func) {
			return true
		}
		for _, arg := range n.Args {
			if a.exprReferencesGhostField(arg) {
				return true
			}
		}
		return false
	case *ast.FieldExpr:
		if st, ok := stripRefForBounds(a.exprTypes[n.Object]).(*StructType); ok && st != nil {
			if f, ok := st.Fields[n.Field]; ok && f.Ghost {
				return true
			}
		}
		return a.exprReferencesGhostField(n.Object)
	default:
		return false
	}
}

// stripGhostFieldContractsForRuntime drops ghost-field-referencing `requires`/`ensure` clauses from
// the codegen-visible contract slices of the current function, AFTER static discharge has run over
// them. The backend emits debug runtime checks from FuncDecl.Requires/EnsureValues; a clause over an
// erased ghost field has no runtime representation, so leaving it in produces a "no field <ghost>"
// codegen error under non-strict / -emit test. Soundness: this runs post-discharge, so a false
// ghost-referencing refinement is still reported statically; only the runtime check is suppressed.
func (a *Analyzer) stripGhostFieldContractsForRuntime() {
	fn := a.currentFuncDecl
	if fn == nil {
		return
	}
	if len(fn.EnsureValues) != 0 {
		kept := fn.EnsureValues[:0:0]
		for _, e := range fn.EnsureValues {
			if a.exprReferencesGhostField(e) {
				continue
			}
			kept = append(kept, e)
		}
		fn.EnsureValues = kept
	}
	if len(fn.Requires) != 0 {
		kept := fn.Requires[:0:0]
		for _, r := range fn.Requires {
			if a.exprReferencesGhostField(r) {
				continue
			}
			kept = append(kept, r)
		}
		fn.Requires = kept
	}
}

func proofAt(proofs []*ast.ProofBlockStmt, i int) *ast.ProofBlockStmt {
	if i < 0 || i >= len(proofs) {
		return nil
	}
	return proofs[i]
}
