package semantic

import (
	"elisacore/src/ast"
	"strings"
)

func (a *Analyzer) inferFuncReturnBorrowedOwnerRefs(fn *ast.FuncDecl, fnType *FuncType) {
	a.addProofReportOrigin(fn)
	proofProbe := a.beginProofReportProbe()
	defer proofProbe.restore()
	restoreProofOwner := a.enterIndexProofOwner(fn, false)
	defer restoreProofOwner()
	fn = a.readDecl(fn)
	if fn == nil || fnType == nil || fnType.ReturnBorrowedOwnerRefsKnown {
		return
	}
	if a.scalarReturnHasNoReferenceProvenance(fnType) {
		fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
		fnType.ReturnBorrowedOwnerRefsKnown = true
		return
	}
	original := a.canonicalFuncDecl(fn)
	if a.returnBorrowedOwnerRefInProgress[original] {
		return
	}
	a.returnBorrowedOwnerRefInProgress[original] = true
	defer delete(a.returnBorrowedOwnerRefInProgress, original)
	private, release, ok := a.privateReturnInferenceDecl(fn)
	if !ok {
		return
	}
	defer release()
	fn = private
	defer a.enterFunctionBoundsFacts()()

	// This pass can use a call's concrete callback types. Its summary belongs to
	// that call, but its expression annotations must not replace the template's
	// permission rows or change later code generation/authority validation.
	restoreExprTypes := a.preserveFunctionExprTypes(fn)
	defer restoreExprTypes()

	restoreFreshShapes := a.enterFreshShapeScope(fn, nil, "infer-owner")
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
	a.storeLivedCache = nil
	a.currentFuncType = nil
	a.returnFreshShapeStatus = freshReturnTracker(fnType.Return)
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
		// Restore the declaration's own `using` imports: without them unqualified
		// names (e.g. a match pattern's enum name made visible by a file-level
		// `using`) silently fail to resolve in this suppressed-diagnostics pass,
		// poisoning cached expression types with <invalid>.
		a.currentUsings = append([]string(nil), a.funcDeclUsings[fn]...)
	}

	a.withSourceGenericParams(fn, fn.GenericParams, nil, func() {
		a.withSourceRegionParams(fn, fn.RegionParams, func() {
			a.withSourcePermissionParams(fn, fn.PermissionParams, func() {
				a.withSourceShapeParams(fn, fnType.ShapeParams, func() {
					for i, param := range a.readOnlyExpandedFuncDeclParams(fn) {
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
					a.seedParamRefinementFacts(a.readOnlyExpandedFuncDeclParams(fn))
					a.seedParamWhereRefinementFacts(a.readOnlyExpandedFuncDeclParams(fn))
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
