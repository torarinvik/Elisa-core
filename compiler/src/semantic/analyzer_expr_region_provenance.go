package semantic

import (
	"llcontext/src/ast"
)

func (a *Analyzer) regionRefStateForExpr(expr ast.Expr) (regionRefState, bool) {
	if expr == nil {
		return regionRefState{}, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.regionRefStateForExpr(n.Inner)
	case *ast.CastExpr:
		return a.regionRefStateForExpr(n.Operand)
	case *ast.MoveExpr:
		return a.regionRefStateForExpr(n.Operand)
	case *ast.AddrOfExpr:
		return a.regionRefStateForExpr(n.Operand)
	case *ast.Ident:
		if a.currentScope == nil {
			return regionRefState{}, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok {
			return regionRefState{}, false
		}
		stateSym := sym
		state, ok := a.currentRegionRefs[sym]
		if (!ok || !hasRegionProvenance(state)) && sym != nil {
			root := symbolAliasRoot(sym)
			if root != nil && root != sym {
				stateSym = root
				state, ok = a.currentRegionRefs[root]
			}
		}
		if !ok {
			return regionRefState{}, false
		}
		state = a.canonicalizeStoredRegionRefBinding(stateSym, state)
		return state, true
	case *ast.AllocExpr:
		if n.Owner != nil {
			if _, _, _, ok := a.treeAllocConstructorInfo(n.Value); ok {
				if isTreeAllocPermExpr(n.Owner) {
					return regionRefState{}, false
				}
				if owner, _, ownerOK := a.classifyTreeAllocOwnerExpr(n.Owner); ownerOK && owner.Kind != treeAllocOwnerNone {
					return regionRefState{}, false
				}
			}
			if _, _, ok := a.treeExactAllocConstructorInfo(n.Value); ok {
				if isTreeAllocPermExpr(n.Owner) {
					return regionRefState{}, false
				}
				if owner, _, ownerOK := a.classifyTreeAllocOwnerExpr(n.Owner); ownerOK && owner.Kind != treeAllocOwnerNone {
					return regionRefState{}, false
				}
			}
			if ownerType := a.exprTypes[n.Owner]; ownerType != nil {
				if _, ok := ownerType.(*PackedEnumStoreType); ok {
					ownerState, ownerOK := a.regionRefStateForExpr(n.Owner)
					if !ownerOK {
						return regionRefState{}, false
					}
					callExpr, ok := n.Value.(*ast.CallExpr)
					if !ok {
						return ownerState, true
					}
					enumType, variant, ok := a.enumConstructorCall(callExpr)
					if !ok || enumType == nil || variant == nil {
						return ownerState, true
					}
					orderedArgs, _, ok := a.resolvePackedEnumConstructorArgs(callExpr, enumType, variant)
					if !ok {
						return ownerState, true
					}
					fieldStates := map[string]regionRefState{}
					states := []regionRefState{ownerState}
					for i := 0; i < len(orderedArgs) && i < len(variant.Payload); i++ {
						fieldState, ok := a.regionRefStateForExpr(orderedArgs[i])
						if !ok || !hasRegionProvenance(fieldState) {
							continue
						}
						key := moveBindVariantFieldKey(variant, i)
						fieldStates[key] = fieldState
						states = append(states, fieldState)
					}
					return mergeRegionRefStatesWithExplicitFields(states, fieldStates)
				}
			}
			if ownerType := a.analyzeExpr(n.Owner); ownerType != nil {
				if _, ok := ownerType.(*PackedEnumStoreType); ok {
					return a.regionRefStateForExpr(n.Owner)
				}
			}
		}
		ident, ok := n.Owner.(*ast.Ident)
		if !ok {
			return regionRefState{}, false
		}
		sym, state := a.lookupRegionState(ident.Name)
		if sym == nil || state.Destroyed {
			return regionRefState{}, false
		}
		return regionRefStateFromDependency(sym, state.Generation), true
	case *ast.StructLitExpr:
		actual := a.exprTypes[n]
		fields, ok := a.resolvedStructFields(actual)
		if !ok {
			return regionRefState{}, false
		}
		args := n.LoweredArgs()
		fieldStates := map[string]regionRefState{}
		unionStates := make([]regionRefState, 0, len(fields))
		for i, field := range fields {
			if i >= len(args) {
				break
			}
			if args[i] == nil {
				continue
			}
			fieldState, ok := a.regionRefStateForExpr(args[i])
			if !ok || !hasRegionProvenance(fieldState) {
				continue
			}
			fieldStates[field.Name] = fieldState
			unionStates = append(unionStates, fieldState)
		}
		if len(unionStates) == 0 {
			return regionRefState{}, false
		}
		return mergeRegionRefStatesWithExplicitFields(unionStates, fieldStates)
	case *ast.RecordUpdateExpr:
		actual := a.exprTypes[n]
		fields, ok := a.resolvedStructFields(actual)
		if !ok {
			return regionRefState{}, false
		}
		baseState, hasBaseState := a.regionRefStateForExpr(n.Base)
		args := n.LoweredArgs()
		fieldStates := map[string]regionRefState{}
		unionStates := make([]regionRefState, 0, len(fields))
		for i, field := range fields {
			var (
				fieldState regionRefState
				hasState   bool
			)
			if i < len(args) && args[i] != nil {
				fieldState, hasState = a.regionRefStateForExpr(args[i])
			} else if hasBaseState {
				fieldState, hasState = projectRegionFieldState(baseState, field.Name)
			}
			if !hasState || !hasRegionProvenance(fieldState) {
				continue
			}
			fieldStates[field.Name] = fieldState
			unionStates = append(unionStates, fieldState)
		}
		if len(unionStates) == 0 {
			return regionRefState{}, false
		}
		return mergeRegionRefStatesWithExplicitFields(unionStates, fieldStates)
	case *ast.ListLitExpr:
		elemStates := make([]regionRefState, 0, len(n.Elems))
		fieldStates := map[string]regionRefState{}
		for i, elem := range n.Elems {
			if state, ok := a.regionRefStateForExpr(elem); ok && hasRegionProvenance(state) {
				elemStates = append(elemStates, state)
				fieldStates[regionIndexFieldKey(int64(i))] = state
			}
		}
		merged, ok := mergeRegionRefStates(elemStates...)
		if !ok {
			return regionRefState{}, false
		}
		if len(fieldStates) != 0 {
			fieldStates[regionAnyIndexFieldKey()] = cloneRegionRefState(merged)
			merged.Fields = fieldStates
			merged.PackedStoreSummaryKnown = false
		}
		return withPackedStoreProvenanceSummary(merged), true
	case *ast.FieldExpr:
		if enumType, variant, ok := a.enumConstructorInfoFromFieldExpr(n); ok && enumType != nil && variant != nil && enumType.Packed && len(variant.Payload) == 0 {
			if state, ok := a.activePackedStoreRegionState(enumType); ok {
				return state, true
			}
			return regionRefState{}, false
		}
		if n.Field == "tags" {
			if storeType, ok := a.exprTypes[n.Object].(*PackedEnumStoreType); ok && IsFrozenPackedEnumStoreType(storeType) {
				return a.regionRefStateForExpr(n.Object)
			}
		}
		state, ok := a.regionRefStateForExpr(n.Object)
		if !ok {
			return regionRefState{}, false
		}
		return projectRegionFieldState(state, n.Field)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return a.regionRefStateForRecoveredExpr(&ast.IndexExpr{Position: n.Position, Object: n.Object, Index: n.Index}, n.Fallback)
		}
		if viewType, ok := a.exprTypes[n.Object].(*DArrayViewType); ok && viewType.SurfaceName == "packedtags" {
			return a.regionRefStateForExpr(n.Object)
		}
		resultType := a.exprTypes[n]
		if resultType == nil || !a.typeCanContainRegionRefs(resultType, map[string]bool{}) {
			return regionRefState{}, false
		}
		state, ok := a.regionRefStateForExpr(n.Object)
		if !ok || !hasRegionDependencies(state) {
			if !ok || len(state.Fields) == 0 {
				return regionRefState{}, false
			}
		}
		if fieldState, ok := projectRegionIndexState(state, n.Index, a.evalConstExpr); ok {
			return fieldState, true
		}
		return cloneRegionRefState(state), true
	case *ast.SliceExpr:
		if viewType, ok := a.exprTypes[n.Object].(*DArrayViewType); ok && viewType.SurfaceName == "packedtags" {
			return a.regionRefStateForExpr(n.Object)
		}
		resultType := a.exprTypes[n]
		if resultType == nil || !a.typeCanContainRegionRefs(resultType, map[string]bool{}) {
			return regionRefState{}, false
		}
		state, ok := a.regionRefStateForExpr(n.Object)
		if !ok || !hasRegionProvenance(state) {
			return regionRefState{}, false
		}
		return summarizeRegionIndexStates(state)
	case *ast.TryExpr:
		return a.regionRefStateForRecoveredExpr(n.Value, n.Fallback)
	case *ast.CatchExpr:
		return regionRefState{}, false
	case *ast.UnwrapElseExpr:
		return a.regionRefStateForRecoveredExpr(n.Value, n.Fallback)
	case *ast.TernaryExpr:
		left, leftOK := a.regionRefStateForExpr(n.Value)
		right, rightOK := a.regionRefStateForExpr(n.Alt)
		if !leftOK && !rightOK {
			return regionRefState{}, false
		}
		if leftOK && rightOK {
			return mergeRegionRefStates(left, right)
		}
		if leftOK {
			return cloneRegionRefState(left), true
		}
		return cloneRegionRefState(right), true
	case *ast.CallExpr:
		if state, ok := a.regionRefStateForProofCarryingViewCall(n); ok {
			return state, true
		}
		if freezeStoreArg, ok := a.freezeStoreArg(n); ok {
			return a.regionRefStateForExpr(freezeStoreArg)
		}
		if _, ok := a.packedStoreConstructorCall(n); ok {
			return regionRefState{}, false
		}
		if _, ok := a.treeStoreConstructorCall(n); ok {
			return regionRefState{}, false
		}
		if enumType, variant, ok := a.enumConstructorCall(n); ok && enumType != nil && variant != nil {
			states := make([]regionRefState, 0, len(n.Args))
			fieldStates := map[string]regionRefState{}
			if enumType.Packed {
				orderedArgs, commonArgs, ok := a.resolvePackedEnumConstructorArgs(n, enumType, variant)
				if !ok {
					return regionRefState{}, false
				}
				for i := 0; i < len(orderedArgs) && i < len(variant.Payload); i++ {
					state, ok := a.regionRefStateForExpr(orderedArgs[i])
					if !ok || !hasRegionProvenance(state) {
						continue
					}
					states = append(states, state)
					fieldStates[moveBindVariantFieldKey(variant, i)] = state
				}
				for name, arg := range commonArgs {
					state, ok := a.regionRefStateForExpr(arg)
					if !ok || !hasRegionProvenance(state) {
						continue
					}
					states = append(states, state)
					fieldStates[name] = state
				}
			} else {
				orderedArgs, ok := a.resolveEnumConstructorArgs(n, enumType, variant)
				if !ok {
					return regionRefState{}, false
				}
				for _, arg := range orderedArgs {
					if state, ok := a.regionRefStateForExpr(arg); ok && hasRegionProvenance(state) {
						states = append(states, state)
					}
				}
				for i := 0; i < len(orderedArgs) && i < len(variant.Payload); i++ {
					if state, ok := a.regionRefStateForExpr(orderedArgs[i]); ok && hasRegionProvenance(state) {
						fieldStates[moveBindVariantFieldKey(variant, i)] = state
					}
				}
			}
			return mergeRegionRefStatesWithExplicitFields(states, fieldStates)
		}
		fnType, _ := a.exprTypes[n.Func].(*FuncType)
		if fnType == nil {
			if analyzed := a.analyzeExpr(n.Func); analyzed != nil {
				fnType, _ = analyzed.(*FuncType)
			}
		}
		if fnType != nil {
			if !fnType.ReturnProvenanceKnown {
				if a.suppressLazyFuncSummaryInference {
					return regionRefState{}, false
				}
				a.inferFuncReturnProvenanceForExpr(n.Func, fnType)
			}
			if fnType.ReturnProvenanceKnown {
				return a.instantiateReturnProvenance(fnType.ReturnProvenance, n.Args)
			}
		}
		return regionRefState{}, false
	default:
		return regionRefState{}, false
	}
}

func (a *Analyzer) regionRefStateForProofCarryingViewCall(call *ast.CallExpr) (regionRefState, bool) {
	if a == nil || call == nil || len(call.Args) == 0 {
		return regionRefState{}, false
	}
	sourceState, ok := a.regionRefStateForExpr(call.Args[0])
	if !ok || !hasRegionProvenance(sourceState) {
		return regionRefState{}, false
	}
	summarized, summaryOK := summarizeRegionIndexStates(sourceState)
	if !summaryOK {
		summarized = cloneRegionRefState(sourceState)
	}
	switch callIdentName(call) {
	case "readonly":
		return cloneRegionRefState(sourceState), true
	case "split_at":
		state := cloneRegionRefState(summarized)
		state.Fields = map[string]regionRefState{
			"left":  cloneRegionRefState(summarized),
			"right": cloneRegionRefState(summarized),
		}
		state.PackedStoreSummaryKnown = false
		return withPackedStoreProvenanceSummary(state), true
	case "chunks_exact":
		state := cloneRegionRefState(summarized)
		state.Fields = map[string]regionRefState{
			"source": cloneRegionRefState(summarized),
		}
		state.PackedStoreSummaryKnown = false
		return withPackedStoreProvenanceSummary(state), true
	case "reduce_sum":
		return regionRefState{}, true
	default:
		return regionRefState{}, false
	}
}

func (a *Analyzer) regionRefStateForRecoveredExpr(value ast.Expr, fallback ast.Expr) (regionRefState, bool) {
	valueState, valueOK := a.regionRefStateForExpr(value)
	if fallback == nil || a.exprDefinitelyNever(fallback) {
		if !valueOK {
			return regionRefState{}, false
		}
		return cloneRegionRefState(valueState), true
	}
	fallbackState, fallbackOK := a.regionRefStateForExpr(fallback)
	if !valueOK && !fallbackOK {
		return regionRefState{}, false
	}
	if valueOK && fallbackOK {
		return mergeRegionRefStates(valueState, fallbackState)
	}
	if valueOK {
		return cloneRegionRefState(valueState), true
	}
	return cloneRegionRefState(fallbackState), true
}

func (a *Analyzer) exprDefinitelyNever(expr ast.Expr) bool {
	if a == nil || expr == nil {
		return false
	}
	t := a.exprTypes[expr]
	if t == nil {
		return false
	}
	return IsNeverType(t)
}

func (a *Analyzer) inferFuncReturnProvenanceForExpr(expr ast.Expr, fnType *FuncType) {
	if fnType == nil || fnType.ReturnProvenanceKnown || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.inferFuncReturnProvenanceForExpr(n.Inner, fnType)
	case *ast.FieldExpr:
		if fieldExpr, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok {
			a.inferFuncReturnProvenanceForExpr(fieldExpr, fnType)
		}
	case *ast.Ident:
		if a.inferFuncReturnProvenanceForLocalIdent(n, fnType) {
			return
		}
		if a.globalScope == nil {
			return
		}
		sym, _, ok := a.lookupVisibleGlobal(n.Name)
		if !ok {
			return
		}
		if sourceType, ok := sym.Type.(*FuncType); ok && sourceType != fnType {
			a.ensureFunctionValueTypeSummaries(n, sourceType)
			if sourceType.ReturnProvenanceKnown {
				fnType.ReturnProvenance = cloneRegionRefState(sourceType.ReturnProvenance)
				fnType.ReturnProvenanceKnown = true
				return
			}
		}
		fnDecl, _ := sym.Node.(*ast.FuncDecl)
		if fnDecl == nil {
			return
		}
		a.inferFuncReturnProvenance(fnDecl, fnType)
	case *ast.SpecializeExpr:
		a.inferFuncReturnProvenanceForExpr(n.Operand, fnType)
	}
}

func (a *Analyzer) inferFuncReturnProvenanceForLocalIdent(ident *ast.Ident, fnType *FuncType) bool {
	if ident == nil || fnType == nil || a.currentScope == nil {
		return false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return false
	}
	if sourceType, ok := a.currentFunctionValueTypeRef(sym); ok && sourceType != fnType {
		if !sourceType.ReturnProvenanceKnown {
			if valueExpr, ok := a.immutableValueExprForSymbol(sym); ok && valueExpr != nil {
				a.ensureFunctionValueTypeSummaries(valueExpr, sourceType)
			}
		}
		if sourceType.ReturnProvenanceKnown {
			fnType.ReturnProvenance = cloneRegionRefState(sourceType.ReturnProvenance)
			fnType.ReturnProvenanceKnown = true
			return true
		}
	}
	if sourceType, ok := a.lookupCurrentFunctionValueType(sym); ok && sourceType != fnType && hasRegionProvenance(sourceType.ReturnProvenance) {
		fnType.ReturnProvenance = cloneRegionRefState(sourceType.ReturnProvenance)
		fnType.ReturnProvenanceKnown = true
		return true
	}
	if sourceType, ok := sym.Type.(*FuncType); ok && sourceType != fnType && hasRegionProvenance(sourceType.ReturnProvenance) {
		fnType.ReturnProvenance = cloneRegionRefState(sourceType.ReturnProvenance)
		fnType.ReturnProvenanceKnown = true
		return true
	}
	if sym.Kind != SymbolLocal || sym.Mutable {
		return false
	}
	decl, ok := sym.Node.(*ast.VarDeclStmt)
	if !ok || decl.Value == nil {
		return false
	}
	if a.returnProvenanceLocalInProgress == nil {
		a.returnProvenanceLocalInProgress = map[*Symbol]bool{}
	}
	if a.returnProvenanceLocalInProgress[sym] {
		return false
	}
	a.returnProvenanceLocalInProgress[sym] = true
	defer delete(a.returnProvenanceLocalInProgress, sym)
	a.inferFuncReturnProvenanceForExpr(decl.Value, fnType)
	return fnType.ReturnProvenanceKnown
}

func (a *Analyzer) inferFuncReturnBorrowedOwnerRefsForExpr(expr ast.Expr, fnType *FuncType) {
	if fnType == nil || fnType.ReturnBorrowedOwnerRefsKnown || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.inferFuncReturnBorrowedOwnerRefsForExpr(n.Inner, fnType)
	case *ast.FieldExpr:
		if fieldExpr, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok {
			a.inferFuncReturnBorrowedOwnerRefsForExpr(fieldExpr, fnType)
		}
	case *ast.Ident:
		if a.inferFuncReturnBorrowedOwnerRefsForLocalIdent(n, fnType) {
			return
		}
		if a.globalScope == nil {
			return
		}
		sym, _, ok := a.lookupVisibleGlobal(n.Name)
		if !ok {
			return
		}
		fnDecl, _ := sym.Node.(*ast.FuncDecl)
		if fnDecl == nil {
			return
		}
		a.inferFuncReturnBorrowedOwnerRefs(fnDecl, fnType)
	case *ast.SpecializeExpr:
		a.inferFuncReturnBorrowedOwnerRefsForExpr(n.Operand, fnType)
	}
}

func (a *Analyzer) inferFuncReturnBorrowedOwnerRefsForLocalIdent(ident *ast.Ident, fnType *FuncType) bool {
	if ident == nil || fnType == nil || a.currentScope == nil {
		return false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return false
	}
	if sourceType, ok := a.lookupCurrentFunctionValueType(sym); ok && sourceType != fnType && hasBorrowedOwnerRefSummary(sourceType.ReturnBorrowedOwnerRefs) {
		fnType.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(sourceType.ReturnBorrowedOwnerRefs)
		fnType.ReturnBorrowedOwnerRefsKnown = true
		return true
	}
	if sourceType, ok := sym.Type.(*FuncType); ok && sourceType != fnType && hasBorrowedOwnerRefSummary(sourceType.ReturnBorrowedOwnerRefs) {
		fnType.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(sourceType.ReturnBorrowedOwnerRefs)
		fnType.ReturnBorrowedOwnerRefsKnown = true
		return true
	}
	if sym.Kind != SymbolLocal || sym.Mutable {
		return false
	}
	decl, ok := sym.Node.(*ast.VarDeclStmt)
	if !ok || decl.Value == nil {
		return false
	}
	if a.returnBorrowedOwnerLocalProgress == nil {
		a.returnBorrowedOwnerLocalProgress = map[*Symbol]bool{}
	}
	if a.returnBorrowedOwnerLocalProgress[sym] {
		return false
	}
	a.returnBorrowedOwnerLocalProgress[sym] = true
	defer delete(a.returnBorrowedOwnerLocalProgress, sym)
	a.inferFuncReturnBorrowedOwnerRefsForExpr(decl.Value, fnType)
	return fnType.ReturnBorrowedOwnerRefsKnown
}
