package semantic

import (
	"elisacore/src/ast"
	"strings"
)

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
	savedFunctionTreeStores := a.currentFunctionUsedTreeStores
	savedTreeOwner := a.currentTreeAllocOwner
	savedAllocExpr := a.currentAllocExpr
	savedFunctionPermissions := a.currentFunctionUsedPermissions
	savedFunctionPermissionRefs := a.currentFunctionUsedPermissionRefs
	savedProgressSummary := a.currentProgressSummary
	savedTrustedNonProgressDepth := a.currentTrustedNonProgressDepth
	savedTrustedAssumeProgressDepth := a.currentTrustedAssumeProgressDepth
	savedImplicitScopes := a.currentImplicitScopes
	savedReturnProvenance := a.currentReturnProvenance
	savedReturnBorrowedOwnerRefs := a.currentReturnBorrowedOwnerRefs
	savedConservativeCallWidenings := a.currentConservativeCallWidenings
	savedRegionFactTransforms := a.currentRegionFactTransforms
	a.currentScope = NewScope(a.globalScope)
	a.currentRegions = map[*Symbol]regionState{}
	a.currentRegionMarks = map[*Symbol]regionMarkState{}
	a.currentRegionRefs = map[*Symbol]regionRefState{}
	a.currentAffineValues = map[affineValueKey]affineValueState{}
	a.currentBorrowedOwnerRefs = map[*Symbol]borrowedOwnerRefState{}
	a.currentFunctionValues = map[*Symbol]*FuncType{}
	a.currentSpecializedValueTypes = map[*Symbol]Type{}
	a.currentValueBindings = map[*Symbol]ast.Expr{}
	a.currentStorageViewDeps = map[*Symbol]storageViewDependencyState{}
	a.currentPackedVariantViews = map[*Symbol]*PackedVariantViewType{}
	a.currentPackedStores = map[string]*PackedEnumStoreType{}
	a.currentPackedStoreResolutions = map[*Symbol]packedStoreResolution{}
	a.currentTreeAllocOwner = treeAllocOwnerBinding{}
	a.currentAllocExpr = nil
	a.currentFunctionUsedTreeStores = map[string]*TreeStoreType{}
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	a.currentProgressSummary = a.beginFunctionProgressSummary(fn)
	a.currentTrustedNonProgressDepth = 0
	a.currentTrustedAssumeProgressDepth = 0
	a.currentReturnProvenance = regionRefState{}
	a.currentReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	a.currentFuncDecl = fn
	a.currentFuncType = fnType
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
	if implicitDecls, _ := a.expandImplicitParamDecls(explicitDecls, fn.ImplicitParams, fn.ImplicitBundles, fn.ImplicitItemOrder, sym.Name); len(implicitDecls) != 0 {
		allParamDecls = append(allParamDecls, implicitDecls...)
	}
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
					savedBodyTreeOwner := a.currentTreeAllocOwner
					if owner, ok := a.inferFunctionTreeAllocOwnerFromParams(allParamDecls, fnType); ok {
						a.currentTreeAllocOwner = owner
					}
					savedBodyImplicitScopes := a.currentImplicitScopes
					if bindings := a.implicitBindingsForCurrentFunction(fnType); len(bindings) != 0 {
						a.currentImplicitScopes = pushExprBindingScope(savedBodyImplicitScopes, bindings)
					}
					a.withLocalParamPackFrame(func() {
						for _, stmt := range fn.Body {
							a.analyzeStmt(stmt)
						}
					})
					a.currentImplicitScopes = savedBodyImplicitScopes
					a.currentTreeAllocOwner = savedBodyTreeOwner
				})
			})
		})
	})
	if fnType != nil && !blockDefinitelyExits(fn.Body) {
		a.validateCurrentFuncPoststates()
	}
	a.checkSentinelIndex(fn)
	a.checkAllocationChurn(fn)
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
		appendInferredTreeStoreParams(fnType, a.currentFunctionUsedTreeStores)
		inferredRefs := canonicalizePermissionRefs(a.currentFunctionUsedPermissionRefs)
		inferredPermissions := permissionFamiliesFromRefs(inferredRefs)
		fnType.PermissionRefs = mergePermissionRefs(fnType.DeclaredPermissionRefs, inferredRefs)
		fnType.Permissions = mergePermissionFamilies(fnType.DeclaredPermissions, inferredPermissions)
		// Trusted-runtime ENCAPSULATION: the stdlib implements safe abstractions with
		// raw-memory internals, so its Unsafe.* effects are not propagated into the function's
		// public signature (like Rust's `std` not being `unsafe` to call). This keeps the
		// runtime's effect types stable when EnforceUnsafePermissions is on for every build
		// path, while ordinary user code is still required to declare its Unsafe grants.
		if a.enforceUnsafePermissions && fn != nil && isRuntimeStdPermissionInternal(fn.Pos().File) {
			fnType.PermissionRefs = filterOutUnsafePermissionRefs(fnType.PermissionRefs)
			fnType.Permissions = filterOutPermissionFamily(fnType.Permissions, "Unsafe")
		}
		a.checkHotContract(fn, fnType)
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
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
	a.currentFunctionUsedTreeStores = savedFunctionTreeStores
	a.currentTreeAllocOwner = savedTreeOwner
	a.currentAllocExpr = savedAllocExpr
	a.currentFunctionUsedPermissions = savedFunctionPermissions
	a.currentFunctionUsedPermissionRefs = savedFunctionPermissionRefs
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
					a.withLocalParamPackFrame(func() {
						for _, stmt := range fn.Body {
							a.analyzeStmt(stmt)
						}
					})
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
					a.withLocalParamPackFrame(func() {
						for _, stmt := range fn.Body {
							a.analyzeStmt(stmt)
						}
					})
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
	a.currentReturnProvenance = savedReturnProvenance
	a.currentReturnBorrowedOwnerRefs = savedReturnBorrowedOwnerRefs
	a.currentConservativeCallWidenings = savedConservativeCallWidenings
	a.suppressDiagnostics = savedSuppressDiagnostics
	a.suppressOptimizationFacts = savedSuppressOptimizationFacts
	a.currentNamespace = savedNamespace
	a.currentUsings = savedUsings
}
