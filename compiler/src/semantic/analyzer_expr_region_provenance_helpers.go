package semantic

import (
	"elisacore/src/ast"
	"strings"
)

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
	t := a.exprType(expr)
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
	case *ast.CanExpr:
		a.inferFuncReturnProvenanceForExpr(n.Expr, fnType)
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
			sourceType = a.privateFuncType(nil, sourceType)
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
	if sourceType, ok := sym.Type.(*FuncType); ok && sourceType != fnType {
		sourceType = a.privateFuncType(nil, sourceType)
		if hasRegionProvenance(sourceType.ReturnProvenance) {
			fnType.ReturnProvenance = cloneRegionRefState(sourceType.ReturnProvenance)
			fnType.ReturnProvenanceKnown = true
			return true
		}
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
	case *ast.CanExpr:
		a.inferFuncReturnBorrowedOwnerRefsForExpr(n.Expr, fnType)
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
		case *ast.CanExpr:
			operand = n.Expr
		case *ast.ParenExpr:
			operand = n.Inner
		case *ast.FieldExpr:
			// A field reached through a reference lives in the pointee, not in a buffer this
			// expression names; leave that to the ordinary provenance walk.
			if _, isRef := a.exprType(n.Object).(*RefType); isRef {
				return regionRefState{}, false
			}
			operand = n.Object
		case *ast.IndexExpr:
			if n.Fallback != nil {
				return regionRefState{}, false
			}
			objectType := a.exprType(n.Object)
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
		case *ast.CanExpr:
			expr = n.Expr
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.FieldExpr:
			if _, isRef := a.exprType(n.Object).(*RefType); isRef {
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
	resultType := a.exprType(call)
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
	resultType := a.exprType(call)
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
