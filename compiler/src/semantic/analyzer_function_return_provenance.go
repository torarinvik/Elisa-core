package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"strings"
)

// Pointer-free results need no lazy reference-provenance walk. The ordinary
// body pass still checks statements and diagnostics. Unresolved, owning and
// generic types remain conservative; a bounded recursion also rejects cycles.
func (a *Analyzer) scalarReturnHasNoReferenceProvenance(fnType *FuncType) bool {
	if a == nil || fnType == nil || fnType.ReturnRegion != "" {
		return false
	}
	if result, ok := fnType.Return.(*ErrorUnionType); ok {
		// A concrete error union carries only its value or an error payload.
		// An opaque error-set parameter may add reference-carrying payloads.
		if result == nil || result.Errors == nil || result.Errors.HasParams() || !a.returnTypeHasNoReferences(result.Value, 1) {
			return false
		}
		for _, payloads := range result.Errors.Payloads {
			for _, payload := range payloads {
				if !a.returnTypeHasNoReferences(payload, 1) {
					return false
				}
			}
		}
		return true
	}
	return a.returnTypeHasNoReferences(fnType.Return, 0)
}

func (a *Analyzer) returnTypeHasNoReferences(t Type, depth int) bool {
	if a == nil || depth > 16 {
		return false
	}
	switch typ := t.(type) {
	case *BuiltinType:
		if typ == nil {
			return false
		}
		switch typ.Name {
		case "void", "bool", "char", "int", "i8", "i16", "i32", "i64", "i128", "isize", "u8", "u16", "u32", "u64", "u128", "usize", "f16", "f32", "f64":
			return true
		}
	case *ConstEnumType:
		return typ != nil && a.returnTypeHasNoReferences(typ.Storage, depth+1)
	case *ArrayType:
		return typ != nil && a.returnTypeHasNoReferences(typ.Elem, depth+1)
	case *OptionalType:
		return typ != nil && a.returnTypeHasNoReferences(typ.Value, depth+1)
	case *TupleType:
		if typ == nil {
			return false
		}
		for _, field := range typ.Fields {
			if !a.returnTypeHasNoReferences(field.Type, depth+1) {
				return false
			}
		}
		return true
	case *EnumType:
		// A base enum accepts every refinement case, whose payloads may add references.
		// Keep hierarchies conservative rather than proving only the base's own variants.
		if typ == nil || typ.Packed || typ.StoreBackedPlain || typ.Parent != nil || len(typ.Children) != 0 ||
			(typ.Decl != nil && (typ.Decl.Parent != "" || typ.Decl.StoreRegion != "")) {
			return false
		}
		for _, field := range typ.Common {
			if !a.returnTypeHasNoReferences(field.Type, depth+1) {
				return false
			}
		}
		for _, variant := range typ.Variants {
			if variant == nil {
				continue
			}
			for _, payload := range variant.Payload {
				if !a.returnTypeHasNoReferences(payload, depth+1) {
					return false
				}
			}
		}
		return true
	case *AggregateStateType:
		return typ != nil && a.returnTypeHasNoReferences(typ.Base, depth+1)
	case *StructType:
		if typ == nil || typ.Decl == nil || typ.Store || typ.Resource || typ.Affine || typ.DropHook != "" || typ.HasPackedGroups || typ.RegionOwner != "" || len(typ.RegionParams) != 0 || len(typ.GenericParams) != 0 || len(typ.TypeParams) != 0 || len(typ.Fields) < len(typ.Decl.Fields) {
			return false
		}
		for _, field := range typ.Fields {
			if !a.returnTypeHasNoReferences(field.Type, depth+1) {
				return false
			}
		}
		return true
	case *GenericInstanceType:
		if typ == nil || typ.Region != "" {
			return false
		}
		base, ok := typ.Base.(*StructType)
		if !ok || base == nil || base.Decl == nil || base.Store || base.Resource || base.Affine || base.DropHook != "" || base.HasPackedGroups || base.RegionOwner != "" || len(base.Fields) < len(base.Decl.Fields) {
			return false
		}
		params := genericParamsForStructType(base)
		if len(params) != len(typ.Args) {
			return false
		}
		for _, param := range params {
			if param.Kind != ast.GenericParamType && param.Kind != ast.GenericParamRegion {
				return false
			}
		}
		bindings := genericBindingsForStructInstance(base, typ.Args)
		regionBindings := regionBindingsForStructInstance(base, typ.Args)
		for _, declaredField := range base.Decl.Fields {
			field, ok := base.Fields[declaredField.Name]
			if !ok {
				return false
			}
			fieldType := field.Type
			if len(bindings) != 0 || len(regionBindings) != 0 {
				fieldType = a.substituteType(fieldType, bindings, nil, regionBindings, nil)
			}
			if !a.returnTypeHasNoReferences(fieldType, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

func (a *Analyzer) directParamReturnProvenance(fn *ast.FuncDecl, fnType *FuncType) (regionRefState, bool) {
	if a == nil || fn == nil || fnType == nil || fnType.ReturnRegion != "" || len(fn.Body) != 1 {
		return regionRefState{}, false
	}
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		return regionRefState{}, false
	}
	expr, ok := unwrapReturnProvenanceWrappers(ret.Value)
	if !ok {
		return regionRefState{}, false
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return regionRefState{}, false
	}
	params := a.readOnlyExpandedFuncDeclParams(fn)
	for i, param := range params {
		if param.Name != ident.Name || i >= len(fnType.Params) || !SameType(fnType.Params[i], fnType.Return) {
			continue
		}
		if state, carriesRefs := a.abstractParamRegionRefState(fnType.Params[i], i, map[string]bool{}); carriesRefs {
			return state, true
		}
		if a.returnTypeHasNoReferences(fnType.Return, 0) {
			return regionRefState{}, true
		}
		return regionRefState{}, false
	}
	return regionRefState{}, false
}

func (a *Analyzer) directParamFieldReturnProvenance(fn *ast.FuncDecl, fnType *FuncType) (regionRefState, bool) {
	if a == nil || fn == nil || fnType == nil || fnType.ReturnRegion != "" || len(fn.Body) != 1 {
		return regionRefState{}, false
	}
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		return regionRefState{}, false
	}
	value, ok := unwrapReturnProvenanceWrappers(ret.Value)
	if !ok {
		return regionRefState{}, false
	}
	field, ok := value.(*ast.FieldExpr)
	if !ok || field.Safe || field.Object == nil || field.Field == "" {
		return regionRefState{}, false
	}
	object, ok := unwrapReturnProvenanceWrappers(field.Object)
	if !ok {
		return regionRefState{}, false
	}
	ident, ok := object.(*ast.Ident)
	if !ok {
		return regionRefState{}, false
	}
	params := a.readOnlyExpandedFuncDeclParams(fn)
	for i, param := range params {
		if param.Name != ident.Name || i >= len(fnType.Params) {
			continue
		}
		paramType := fnType.Params[i]
		structType := StripAggregateStateType(stripRefForBounds(paramType))
		switch tt := structType.(type) {
		case *StructType:
			if tt == nil {
				return regionRefState{}, false
			}
		case *GenericInstanceType:
			if tt == nil {
				return regionRefState{}, false
			}
			if _, isStruct := tt.Base.(*StructType); !isStruct {
				return regionRefState{}, false
			}
		default:
			return regionRefState{}, false
		}
		fieldType, ok := a.lookupResolvedFieldType(structType, field.Field)
		if !ok || fieldType == nil || IsInvalidType(fieldType) || !SameType(fieldType, fnType.Return) {
			return regionRefState{}, false
		}
		if state, carriesRefs := a.abstractParamRegionRefState(paramType, i, map[string]bool{}); carriesRefs {
			if projected, ok := projectRegionFieldState(state, field.Field); ok {
				return projected, true
			}
		}
		if a.returnTypeHasNoReferences(fnType.Return, 0) {
			return regionRefState{}, true
		}
		return regionRefState{}, false
	}
	return regionRefState{}, false
}

func unwrapReturnProvenanceWrappers(expr ast.Expr) (ast.Expr, bool) {
	for {
		switch n := expr.(type) {
		case *ast.CanExpr:
			expr = n.Expr
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.MoveExpr:
			expr = n.Operand
		default:
			goto unwrapped
		}
		if expr == nil {
			return nil, false
		}
	}

unwrapped:
	return expr, true
}

func reusableDeclReturnProvenance(fn *ast.FuncDecl, source, target *FuncType) (regionRefState, bool) {
	if fn == nil || source == nil || target == nil || source == target || !source.ReturnProvenanceKnown ||
		len(fn.GenericParams) != 0 || len(fn.TypeParams) != 0 || len(fn.RegionParams) != 0 || len(fn.InferredRegionParams) != 0 || len(fn.PermissionParams) != 0 ||
		source.Name != target.Name || source.ReturnRegion != target.ReturnRegion || len(source.Params) != len(target.Params) ||
		source.Return == nil || target.Return == nil || !reflect.DeepEqual(source.Return, target.Return) {
		return regionRefState{}, false
	}
	for i := range source.Params {
		if source.Params[i] == nil || target.Params[i] == nil || !reflect.DeepEqual(source.Params[i], target.Params[i]) {
			return regionRefState{}, false
		}
	}
	return cloneRegionRefState(source.ReturnProvenance), true
}

// privateReturnInferenceDecl keeps speculative body reanalysis off the shared
// AST during parallel analysis. Even a declaration last owned by this worker
// may already have been published and imported by another worker.
//
// Serial analysis and specialization views already have exclusive ownership.
// For a regular body worker, copy from the pre-fork snapshot rather than from
// a possibly-mutating live declaration. Failure is a diagnostic, never a quiet
// loss of provenance or a fallback to mutating shared syntax.
func (a *Analyzer) privateReturnInferenceDecl(fn *ast.FuncDecl) (*ast.FuncDecl, func(), bool) {
	noop := func() {}
	if a == nil || fn == nil {
		return nil, noop, false
	}
	p := a.parallel
	if p == nil || p.specialization != nil {
		return fn, noop, true
	}
	// Use a new snapshot even if a quiet-analysis shadow exists: a nested
	// inference must not rekey or mutate the body its caller is still using.
	original := a.canonicalFuncDecl(fn)
	shadow := p.copyDecl(original)
	if shadow == nil || shadow == original || shadow == fn {
		a.appendDiagnostic(Diagnostic{Pos: fn.Pos(), Severity: DiagnosticSeverityError, Message: "cannot isolate function syntax for parallel return-provenance analysis"})
		return nil, noop, false
	}
	if p.original == nil {
		p.original = map[*ast.FuncDecl]*ast.FuncDecl{}
	}
	p.original[shadow] = original
	a.rekeyFuncDeclEntries(fn, shadow, false)
	return shadow, func() {
		a.rekeyFuncDeclEntries(shadow, fn, true)
		delete(p.original, shadow)
	}, true
}

func (a *Analyzer) inferFuncReturnProvenance(fn *ast.FuncDecl, fnType *FuncType) {
	a.addProofReportOrigin(fn)
	proofProbe := a.beginProofReportProbe()
	defer proofProbe.restore()
	restoreProofOwner := a.enterIndexProofOwner(fn, false)
	defer restoreProofOwner()
	fn = a.readDecl(fn)
	if fn == nil || fnType == nil || fnType.ReturnProvenanceKnown {
		return
	}
	if a.scalarReturnHasNoReferenceProvenance(fnType) {
		fnType.ReturnProvenance = regionRefState{}
		fnType.ReturnProvenanceKnown = true
		return
	}
	original := a.canonicalFuncDecl(fn)
	if a.returnProvenanceInProgress[original] {
		return
	}
	a.returnProvenanceInProgress[original] = true
	defer delete(a.returnProvenanceInProgress, original)
	// Reuse a completed declaration summary before taking a speculative syntax
	// snapshot. The snapshot is needed only when the body must be reanalyzed; the
	// matching path below reads declaration metadata and copies the already
	// published provenance into this function view.
	if symbol := a.funcDeclSymbols[fn]; symbol != nil {
		if sourceType, ok := symbol.Type.(*FuncType); ok {
			sourceType = a.privateFuncType(fn, sourceType)
			if state, ok := reusableDeclReturnProvenance(fn, sourceType, fnType); ok {
				fnType.ReturnProvenance = state
				fnType.ReturnProvenanceKnown = true
				return
			}
		}
	}
	if state, ok := a.directParamReturnProvenance(fn, fnType); ok {
		fnType.ReturnProvenance = withPackedStoreProvenanceSummary(state)
		fnType.ReturnProvenanceKnown = true
		return
	}
	if state, ok := a.directParamFieldReturnProvenance(fn, fnType); ok {
		fnType.ReturnProvenance = withPackedStoreProvenanceSummary(state)
		fnType.ReturnProvenanceKnown = true
		return
	}
	private, release, ok := a.privateReturnInferenceDecl(fn)
	if !ok {
		return
	}
	defer release()
	fn = private
	defer a.enterFunctionBoundsFacts()()

	restoreFreshShapes := a.enterFreshShapeScope(fn, nil, "infer-provenance")
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
		a.currentUsings = append([]string(nil), a.declarationUsings(fn)...)
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

	declaredReturnProvenance := fnType.ReturnProvenance
	declaredReturnProvenanceKnown := fnType.ReturnRegion != "" && fnType.ReturnProvenanceKnown
	if !declaredReturnProvenanceKnown {
		if hasRegionProvenance(a.currentReturnProvenance) {
			fnType.ReturnProvenance = cloneRegionRefState(a.currentReturnProvenance)
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
