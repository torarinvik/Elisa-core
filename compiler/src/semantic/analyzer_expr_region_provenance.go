package semantic

import (
	"elisacore/src/ast"
	"strings"
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
		if state, ok := a.regionRefStateForBufferAddress(n.Operand); ok {
			return state, true
		}
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
		if n.AutoRegion {
			// new[auto] depends on the innermost active inferred region exactly like new[region]
			// depends on its named region — so the same return-escape / invalidation checks reject
			// letting the reference outlive that region.
			region := a.activeContainerRegionName()
			if region == "" {
				return regionRefState{}, false
			}
			sym, state := a.lookupRegionState(region)
			if sym == nil || state.Destroyed {
				return regionRefState{}, false
			}
			return regionRefStateFromDependency(sym, state.Generation), true
		}
		if n.Owner != nil {
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
	case *ast.LambdaExpr:
		// A closure that captures region-dependent values depends on those
		// regions: using the closure after the region is destroyed/reset is a
		// use-after-free. Carry the union of the captures' region provenance so
		// the existing invalidation + use-check (and return-escape check) apply
		// to the closure value itself.
		return a.regionRefStateForLambdaCaptures(n)
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
	case *ast.TupleExpr:
		// A tuple carries its elements' regions exactly like a struct literal carries its fields':
		// `t <- (true, xs[0:2])` out of a region block must not erase the view's region.
		tupleType, _ := a.exprTypes[n].(*TupleType)
		fieldStates := map[string]regionRefState{}
		unionStates := make([]regionRefState, 0, len(n.Elems))
		for i, elem := range n.Elems {
			elemState, ok := a.regionRefStateForExpr(elem)
			if !ok || !hasRegionProvenance(elemState) {
				continue
			}
			if tupleType != nil && i < len(tupleType.Fields) && tupleType.Fields[i].Name != "" {
				fieldStates[tupleType.Fields[i].Name] = elemState
			}
			unionStates = append(unionStates, elemState)
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
		// Dict-literal keys (`{k: v}`) can carry region provenance too; they are not
		// positionally index-addressable, so fold them into the union (and thus the
		// wildcard element bucket) rather than a numbered slot (deep audit #16).
		for _, keyExpr := range n.Keys {
			if state, ok := a.regionRefStateForExpr(keyExpr); ok && hasRegionProvenance(state) {
				elemStates = append(elemStates, state)
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
		if projected, ok := projectRegionFieldState(state, n.Field); ok {
			return projected, true
		}
		// The raw buffer pointer of a builtin view/container (`view.data`, `xs.items`)
		// points into the storage the whole value's provenance describes.
		if _, isRef := a.exprTypes[n].(*RefType); isRef && len(state.Fields) == 0 && hasRegionProvenance(state) {
			switch StripAggregateStateType(stripRefForBounds(a.exprTypes[n.Object])).(type) {
			case *SViewType, *ViewType, *CStrType, *DArrayType:
				return cloneRegionRefState(state), true
			}
		}
		return regionRefState{}, false
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return a.regionRefStateForRecoveredExpr(&ast.IndexExpr{Position: n.Position, Object: n.Object, Index: n.Index}, n.Fallback)
		}
		if viewType, ok := a.exprTypes[n.Object].(*ViewType); ok && viewType.SurfaceName == "packedtags" {
			return a.regionRefStateForExpr(n.Object)
		}
		resultType := a.exprTypes[n]
		if resultType == nil || !a.typeCanContainRegionRefs(resultType, map[string]bool{}) {
			return regionRefState{}, false
		}
		state, ok := a.regionRefStateForIndexElement(n)
		// An element that owns arena storage (`xs[0]` of a `darray[darray[u8]]`) copies only a
		// header; its backing may live anywhere that outlives the container — including the
		// container's own region, where nothing else records it. Bound it by the container's
		// region unless the container's element tracker already names every element's storage
		// (elementStorageState), which is exact and does not over-approximate.
		_, tracked := a.localContainerElementState(n)
		if !tracked && a.typeMayOwnArenaStorage(resultType, map[string]bool{}) {
			if owner, ownerOK := a.containerRegionDependency(a.exprTypes[n.Object]); ownerOK {
				if !ok {
					return owner, true
				}
				if merged, mergedOK := mergeRegionRefStates(state, owner); mergedOK {
					return merged, true
				}
			}
		}
		return state, ok
	case *ast.SliceExpr:
		if viewType, ok := a.exprTypes[n.Object].(*ViewType); ok && viewType.SurfaceName == "packedtags" {
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
	case *ast.BinaryExpr:
		// Pointer arithmetic (`p + n`, `n + p`, `p - n`) stays inside the pointee's buffer.
		if binaryExprRequiresUnsafePointerArithmetic(n.Op, a.exprTypes[n.Left], a.exprTypes[n.Right]) {
			if _, ok := a.exprTypes[n.Left].(*RefType); ok {
				return a.regionRefStateForExpr(n.Left)
			}
			return a.regionRefStateForExpr(n.Right)
		}
		return regionRefState{}, false
	case *ast.TryExpr:
		return a.regionRefStateForRecoveredExpr(n.Value, n.Fallback)
	case *ast.CatchExpr:
		return regionRefState{}, false
	case *ast.UnwrapElseExpr:
		return a.regionRefStateForRecoveredExpr(n.Value, n.Fallback)
	case *ast.GetExpr:
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
		// `old(expr)` in an `ensure` clause is a pseudo-call (the entry-time value of expr), not a real
		// callee — its provenance is that of the captured expression. Resolve it directly so we never
		// analyze the `old` identifier as a function (which has no binding → "undefined identifier old").
		if a.inEnsureContext && ast.IsOldCall(n) && len(n.Args) == 1 {
			return a.regionRefStateForExpr(n.Args[0])
		}
		if state, ok := a.regionRefStateForProofCarryingViewCall(n); ok {
			return state, true
		}
		if freezeStoreArg, ok := a.freezeStoreArg(n); ok {
			return a.regionRefStateForExpr(freezeStoreArg)
		}
		if _, ok := a.packedStoreConstructorCall(n); ok {
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
		if state, ok := a.regionRefStateForThreadHandleCall(n); ok {
			return state, true
		}
		fnType, _ := a.exprTypes[n.Func].(*FuncType)
		if fnType == nil && len(n.Args) == 1 && a.isTypeConstructorCall(n) {
			return a.regionRefStateForExpr(n.Args[0])
		}
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
				if state, ok := a.instantiateReturnProvenance(fnType.ReturnProvenance, n.Args); ok && hasRegionProvenance(state) {
					return state, true
				}
				// A generic callee's summary is computed over its template (`id[T](x: T) -> T`
				// carries nothing for a type parameter); fall through to the syntactic origins.
				if state, ok := a.regionRefStateForCallOrigins(n); ok {
					return state, true
				}
				return a.instantiateReturnProvenance(fnType.ReturnProvenance, n.Args)
			}
		}
		if state, ok := a.regionRefStateForCallOrigins(n); ok {
			return state, true
		}
		if state, ok := a.regionRefStateForThreadHandleCall(n); ok {
			return state, true
		}
		// A call may have a concrete region directly on its result type even when
		// it has no inferred return-provenance summary (notably built-in view
		// constructors such as darray.as_sview). Preserve that dependency when
		// the result is assigned into an existing, region-less binding.
		if state, ok := a.containerRegionDependency(a.exprTypes[n]); ok {
			return state, true
		}
		// `buf.as_sview()` / `buf.as_cstr()` borrow the receiver's buffer. A region-less
		// receiver (a parameter) stamps no region on the result type; carry the receiver's
		// own provenance instead.
		if builtin, ok := a.exprTypes[n.Func].(*FuncType); ok && builtin != nil && (builtin.Name == "darray.sview" || builtin.Name == "darray.cstr") {
			if field, ok := n.Func.(*ast.FieldExpr); ok && field != nil {
				if state, ok := a.regionRefStateForExpr(field.Object); ok && hasRegionProvenance(state) {
					return state, true
				}
				if state, ok := a.containerRegionDependency(a.exprTypes[field.Object]); ok {
					return state, true
				}
				return a.paramRootRegionDependency(field.Object)
			}
		}
		return regionRefState{}, false
	default:
		return regionRefState{}, false
	}
}

// regionRefStateForIndexElement is the provenance of the by-value element copy `xs[i]`.
func (a *Analyzer) regionRefStateForIndexElement(n *ast.IndexExpr) (regionRefState, bool) {
	if elemState, ok := a.localContainerElementState(n); ok {
		// An element copy of a tracked local container carries the provenance of the values
		// pushed into it, not the container's own region.
		if !hasRegionProvenance(elemState) {
			return regionRefState{}, false
		}
		return cloneRegionRefState(elemState), true
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
}

func (a *Analyzer) regionRefStateForLambdaCaptures(expr *ast.LambdaExpr) (regionRefState, bool) {
	if a == nil || expr == nil || a.currentScope == nil || a.lambdaInfo == nil {
		return regionRefState{}, false
	}
	info, ok := a.lambdaInfo[expr]
	if !ok || info == nil {
		return regionRefState{}, false
	}
	states := make([]regionRefState, 0, len(info.Captures))
	for _, name := range info.Captures {
		// Reuse the Ident path so alias roots / canonicalization are honored.
		state, ok := a.regionRefStateForExpr(&ast.Ident{Position: expr.Position, Name: name})
		if !ok || !hasRegionProvenance(state) {
			continue
		}
		states = append(states, state)
	}
	if len(states) == 0 {
		return regionRefState{}, false
	}
	return mergeRegionRefStates(states...)
}

func (a *Analyzer) regionRefStateForProofCarryingViewCall(call *ast.CallExpr) (regionRefState, bool) {
	if a == nil || call == nil || len(call.Args) == 0 {
		return regionRefState{}, false
	}
	helperName := callIdentName(call)
	if !a.isCompilerBuiltinHelperCall(call, helperName) {
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
	switch helperName {
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

// regionRefStateForBufferAddress gives `&xs[i]` (and `&xs[i].field`) the region of xs's element
// buffer. The element itself may be a scalar that carries no region, but its ADDRESS lives in the
// buffer, so the reference dies with the buffer's region: `firstp(xs: darray[u8]&) -> u8&` returning
// `&xs[0]` must hand its caller a borrow tied to the argument's region, not a region-less one.
func (a *Analyzer) regionRefStateForBufferAddress(operand ast.Expr) (regionRefState, bool) {
	for operand != nil {
		switch n := operand.(type) {
		case *ast.ParenExpr:
			operand = n.Inner
		case *ast.FieldExpr:
			// A field reached through a reference lives in the pointee, not in a buffer this
			// expression names; leave that to the ordinary provenance walk.
			if _, isRef := a.exprTypes[n.Object].(*RefType); isRef {
				return regionRefState{}, false
			}
			operand = n.Object
		case *ast.IndexExpr:
			if n.Fallback != nil {
				return regionRefState{}, false
			}
			objectType := a.exprTypes[n.Object]
			switch StripAggregateStateType(stripRefForBounds(objectType)).(type) {
			case *DArrayType, *ViewType, *SViewType, *CStrType, *DictType, *SetType:
				if state, ok := a.regionRefStateForExpr(n.Object); ok && hasRegionProvenance(state) {
					return summarizeRegionIndexStates(state)
				}
				if state, ok := a.containerRegionDependency(objectType); ok {
					return state, true
				}
				// A parameter's buffer belongs to the caller's argument. Its element type
				// may hold no references (darray[u8]), so the parameter has no abstract
				// region state; the address still borrows the argument's storage.
				return a.paramRootRegionDependency(n.Object)
			}
			operand = n.Object
		default:
			return regionRefState{}, false
		}
	}
	return regionRefState{}, false
}

// paramRootRegionDependency reports a parameter dependency when expr names a
// parameter or a by-value field path of one.
func (a *Analyzer) paramRootRegionDependency(expr ast.Expr) (regionRefState, bool) {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.FieldExpr:
			if _, isRef := a.exprTypes[n.Object].(*RefType); isRef {
				return regionRefState{}, false
			}
			expr = n.Object
		case *ast.Ident:
			if a.currentScope == nil {
				return regionRefState{}, false
			}
			sym, ok := a.currentScope.Lookup(n.Name)
			if !ok || sym == nil {
				return regionRefState{}, false
			}
			if sym.Kind == SymbolParam {
				return regionRefStateFromParamDependency(sym.ParamIndex), true
			}
			// A struct local's region-less buffers live in its home region.
			if sym.Kind == SymbolLocal && sym.HomeRegion != "" && structOwnsRegionlessBuffers(sym.Type) {
				region, state := a.lookupRegionState(sym.HomeRegion)
				if region == nil || state.Destroyed {
					return regionRefState{}, false
				}
				return regionRefStateFromDependency(region, state.Generation), true
			}
			return regionRefState{}, false
		default:
			return regionRefState{}, false
		}
	}
	return regionRefState{}, false
}

// isBufferOwningType reports container types whose elements live in a buffer that a
// borrowed view or element address can point into.
func isBufferOwningType(t Type) bool {
	switch StripAggregateStateType(stripRefForBounds(t)).(type) {
	case *DArrayType, *DictType, *SetType, *CStrType:
		return true
	}
	return false
}

// regionRefStateForCallOrigins is the provenance of a borrowed result traced through the
// callee's syntactic return origins (storageViewReturnOriginsFor): the merged provenance of
// every argument a return borrows from or copies. Only for generic callees, whose template
// summary cannot see that a `T` result carries a view.
func (a *Analyzer) regionRefStateForCallOrigins(call *ast.CallExpr) (regionRefState, bool) {
	resultType := a.exprTypes[call]
	if resultType == nil || !a.typeCarriesBorrowedStorage(resultType, map[Type]bool{}) {
		return regionRefState{}, false
	}
	decls, args, ok := a.storageViewOriginCallee(call)
	if !ok {
		return regionRefState{}, false
	}
	generic := false
	for _, decl := range decls {
		if decl != nil && (len(decl.TypeParams) > 0 || len(decl.GenericParams) > 0) {
			generic = true
		}
	}
	if !generic {
		return regionRefState{}, false
	}
	summary := a.storageViewReturnOriginsForAll(decls, 0)
	if !summary.Known {
		return regionRefState{}, false
	}
	var states []regionRefState
	for _, origin := range summary.Origins {
		if origin.Param < 0 || origin.Param >= len(args) || args[origin.Param] == nil {
			continue
		}
		if state, ok := a.regionRefStateForExpr(args[origin.Param]); ok && hasRegionProvenance(state) {
			states = append(states, state)
		}
	}
	if len(states) == 0 {
		return regionRefState{}, false
	}
	return mergeRegionRefStates(states...)
}

// regionRefStateForThreadHandleCall: a thread handle (`spawn1(fn(a) => v[0] + a, 0)`) owns the
// closure it runs, so it carries the provenance of every capture until joined. Returning the
// handle out of the frame that owns a captured view hands the thread freed memory.
func (a *Analyzer) regionRefStateForThreadHandleCall(call *ast.CallExpr) (regionRefState, bool) {
	resultType := a.exprTypes[call]
	if resultType == nil || !strings.HasPrefix(resultType.String(), "Thread[") {
		return regionRefState{}, false
	}
	var states []regionRefState
	for _, argument := range call.Args {
		if lambda, isLambda := stripOptimizationParens(argument).(*ast.LambdaExpr); isLambda && lambda != nil {
			if state, ok := a.regionRefStateForLambdaCaptures(lambda); ok && hasRegionProvenance(state) {
				states = append(states, state)
			}
		}
	}
	if len(states) == 0 {
		return regionRefState{}, false
	}
	return mergeRegionRefStates(states...)
}
