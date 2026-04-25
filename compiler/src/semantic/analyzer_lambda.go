package semantic

import "llcontext/src/ast"

type lambdaCaptureBinding struct {
	name    string
	typ     Type
	mutable bool
}

func unwrapLambdaExpr(expr ast.Expr) (*ast.LambdaExpr, bool) {
	switch n := expr.(type) {
	case *ast.LambdaExpr:
		return n, true
	case *ast.ParenExpr:
		return unwrapLambdaExpr(n.Inner)
	default:
		return nil, false
	}
}

func lambdaHasUntypedParams(expr *ast.LambdaExpr) bool {
	if expr == nil {
		return false
	}
	for _, param := range expr.Params {
		if param.Type == nil {
			return true
		}
	}
	return false
}

func lambdaFuncTypeSkeleton(name string, params []Type, paramNames []string, ret Type) *FuncType {
	return &FuncType{
		Name:                      name,
		Params:                    params,
		ExplicitParamCount:        len(params),
		ExplicitParamNames:        append([]string(nil), paramNames...),
		ExplicitParamDefaultExprs: make([]ast.Expr, len(params)),
		ExplicitParamHasDefault:   make([]bool, len(params)),
		Return:                    ret,
	}
}

func (a *Analyzer) analyzeDirectLambdaCallExpr(call *ast.CallExpr, lambda *ast.LambdaExpr) Type {
	if lambda == nil {
		return invalidType
	}
	paramNames := make([]string, 0, len(lambda.Params))
	for _, param := range lambda.Params {
		paramNames = append(paramNames, param.Name)
	}
	provisional := lambdaFuncTypeSkeleton("lambda", make([]Type, len(lambda.Params)), paramNames, invalidType)
	orderedArgs, ok := a.resolveFunctionCallArgs(call, provisional)
	if !ok {
		return invalidType
	}
	expectedParams := make([]Type, len(lambda.Params))
	for i := range orderedArgs {
		expectedParams[i] = a.analyzeValueExpr(orderedArgs[i], nil)
	}
	expected := lambdaFuncTypeSkeleton("lambda", expectedParams, paramNames, invalidType)
	ft, ok := a.analyzeLambdaExpr(lambda, expected).(*FuncType)
	if !ok {
		return invalidType
	}
	return a.analyzeResolvedCallExpr(call, ft, orderedArgs)
}

func (a *Analyzer) analyzeLambdaExpr(expr *ast.LambdaExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	expectedFunc, _ := expected.(*FuncType)
	paramTypes := make([]Type, len(expr.Params))
	paramNames := make([]string, 0, len(expr.Params))
	for i, param := range expr.Params {
		paramNames = append(paramNames, param.Name)
		if param.Type != nil {
			paramTypes[i] = a.resolveType(param.Type)
			continue
		}
		if expectedFunc != nil && i < funcTypeExplicitParamCount(expectedFunc) && i < len(expectedFunc.Params) {
			paramTypes[i] = a.cloneTrackedValueType(expectedFunc.Params[i])
			continue
		}
		a.errorf(param.Position, "lambda parameter %q requires an explicit type or contextual function type", param.Name)
		paramTypes[i] = invalidType
	}
	if expectedFunc != nil && funcTypeExplicitParamCount(expectedFunc) != len(paramTypes) {
		a.errorf(expr.Pos(), "lambda expects %d parameters from context, got %d", funcTypeExplicitParamCount(expectedFunc), len(paramTypes))
	}

	returnType := Type(nil)
	if expr.ReturnType != nil {
		returnType = a.resolveType(expr.ReturnType)
	} else if expectedFunc != nil && expectedFunc.Return != nil && len(expr.Body) != 0 {
		returnType = a.cloneTrackedValueType(expectedFunc.Return)
	} else if len(expr.Body) != 0 {
		a.errorf(expr.Pos(), "block lambda requires an explicit return type or contextual function type")
		returnType = invalidType
	}

	fnType := lambdaFuncTypeSkeleton("lambda", paramTypes, paramNames, returnType)
	if expectedFunc != nil {
		fnType.DeclaredPermissionRefs = append([]ast.PermissionRef(nil), expectedFunc.DeclaredPermissionRefs...)
		fnType.DeclaredPermissions = append([]string(nil), expectedFunc.DeclaredPermissions...)
		fnType.PermissionRefs = append([]ast.PermissionRef(nil), expectedFunc.PermissionRefs...)
		fnType.Permissions = append([]string(nil), expectedFunc.Permissions...)
	}

	captures := a.collectLambdaCaptures(expr)
	if a.lambdaInfo == nil {
		a.lambdaInfo = map[*ast.LambdaExpr]*LambdaInfo{}
	}
	a.lambdaInfo[expr] = &LambdaInfo{Captures: captureNames(captures)}

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
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	savedFunctionPermissions := a.currentFunctionUsedPermissions
	savedFunctionPermissionRefs := a.currentFunctionUsedPermissionRefs
	savedReturnProvenance := a.currentReturnProvenance
	savedReturnBorrowedOwnerRefs := a.currentReturnBorrowedOwnerRefs
	savedConservativeCallWidenings := a.currentConservativeCallWidenings

	a.currentScope = NewScope(a.globalScope)
	a.currentReturn = returnType
	a.currentFuncDecl = &ast.FuncDecl{Position: expr.Position, Name: "lambda", Params: append([]ast.ParamDecl(nil), expr.Params...), ReturnType: expr.ReturnType, Body: append([]ast.Stmt(nil), expr.Body...)}
	a.currentFuncType = fnType
	a.returnFreshShapeStatus = freshReturnTracker(returnType)
	a.currentRegions = map[*Symbol]regionState{}
	a.currentRegionMarks = map[*Symbol]regionMarkState{}
	a.currentRegionRefs = map[*Symbol]regionRefState{}
	a.currentAffineValues = map[affineValueKey]affineValueState{}
	a.currentBorrowedOwnerRefs = map[*Symbol]borrowedOwnerRefState{}
	a.currentFunctionValues = map[*Symbol]*FuncType{}
	a.currentSpecializedValueTypes = map[*Symbol]Type{}
	a.currentValueBindings = map[*Symbol]ast.Expr{}
	a.currentPackedVariantViews = map[*Symbol]*PackedVariantViewType{}
	a.currentPackedStores = map[string]*PackedEnumStoreType{}
	a.currentPackedStoreResolutions = map[*Symbol]packedStoreResolution{}
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	a.currentReturnProvenance = regionRefState{}
	a.currentReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	a.currentConservativeCallWidenings = map[*Symbol][]conservativeCallWidening{}

	for _, capture := range captures {
		sym := &Symbol{Name: capture.name, Kind: SymbolLocal, Type: capture.typ, Node: expr, Mutable: capture.mutable}
		a.defineLocal(sym, expr.Position)
		a.recordValueBinding(sym, nil)
		if fnType, ok := capture.typ.(*FuncType); ok {
			a.currentFunctionValues[sym] = a.cloneFunctionValueType(fnType)
		}
		a.bindTrackedValueType(sym, capture.typ)
		a.bindActivePackedStoreType(capture.typ)
	}
	for i, param := range expr.Params {
		var paramType Type = invalidType
		if i < len(fnType.Params) {
			paramType = fnType.Params[i]
		}
		symType := paramType
		if ref, ok := paramType.(*RefType); ok && !ref.Mutable && a.paramIsMutable(param) {
			cloned := cloneRefType(ref)
			cloned.Mutable = true
			symType = cloned
		}
		sym := &Symbol{Name: param.Name, Kind: SymbolParam, Type: symType, Node: expr, ParamIndex: i, Mutable: a.paramIsMutable(param)}
		a.defineLocal(sym, param.Position)
		a.bindActivePackedStoreType(paramType)
		a.recordValueBinding(sym, nil)
		a.recordBorrowedOwnerRefParam(sym)
		if state, ok := a.abstractParamRegionRefState(paramType, i, map[string]bool{}); ok {
			a.recordResolvedRegionRefBinding(sym, state)
		}
	}

	if expr.BodyExpr != nil {
		expectedReturn := returnType
		if expectedReturn == nil || IsInvalidType(expectedReturn) {
			expectedReturn = nil
		}
		bodyType := a.analyzeValueExpr(expr.BodyExpr, expectedReturn)
		if returnType == nil || IsInvalidType(returnType) {
			returnType = bodyType
			fnType.Return = bodyType
			a.currentReturn = bodyType
			a.returnFreshShapeStatus = freshReturnTracker(bodyType)
		}
		a.recordLambdaReturnExpr(expr.BodyExpr, bodyType)
		if !AssignableTo(a.matchReturnType(bodyType), bodyType) {
			a.errorf(expr.BodyExpr.Pos(), "return type expects %s, got %s", a.matchReturnType(bodyType), bodyType)
		}
	} else {
		a.withLocalParamPackFrame(func() {
			for _, stmt := range expr.Body {
				a.analyzeStmt(stmt)
			}
		})
		if !isVoidType(fnType.Return) && !blockDefinitelyExits(expr.Body) {
			a.errorf(expr.Pos(), "lambda body may reach the end without returning %s", fnType.Return)
		}
	}

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
	fnType.FreshReturnShapeParams = inferredFreshReturnShapeParams(a.returnFreshShapeStatus)
	inferredRefs := canonicalizePermissionRefs(a.currentFunctionUsedPermissionRefs)
	inferredPermissions := permissionFamiliesFromRefs(inferredRefs)
	fnType.PermissionRefs = mergePermissionRefs(fnType.DeclaredPermissionRefs, inferredRefs)
	fnType.Permissions = mergePermissionFamilies(fnType.DeclaredPermissions, inferredPermissions)

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
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
	a.currentFunctionUsedPermissions = savedFunctionPermissions
	a.currentFunctionUsedPermissionRefs = savedFunctionPermissionRefs
	a.currentReturnProvenance = savedReturnProvenance
	a.currentReturnBorrowedOwnerRefs = savedReturnBorrowedOwnerRefs
	a.currentConservativeCallWidenings = savedConservativeCallWidenings

	a.recordAnalyzedExprType(expr, fnType)
	return fnType
}

func captureNames(captures []lambdaCaptureBinding) []string {
	names := make([]string, 0, len(captures))
	for _, capture := range captures {
		names = append(names, capture.name)
	}
	return names
}

func (a *Analyzer) collectLambdaCaptures(expr *ast.LambdaExpr) []lambdaCaptureBinding {
	if a == nil || expr == nil || a.currentScope == nil {
		return nil
	}
	collector := newDeferCaptureCollector(a, a.currentScope)
	for _, param := range expr.Params {
		if param.Name != "" {
			collector.rootLocals[param.Name] = true
		}
	}
	if expr.BodyExpr != nil {
		collector.collectExpr(expr.BodyExpr, cloneParallelForLocals(collector.rootLocals))
	} else {
		collector.collectStmts(expr.Body)
	}
	bindings := make([]lambdaCaptureBinding, 0, len(collector.captureOrder))
	for _, name := range collector.captureOrder {
		sym, ok := a.currentScope.Lookup(name)
		if !ok || sym == nil {
			continue
		}
		switch sym.Kind {
		case SymbolLocal, SymbolParam:
		default:
			if sym.Kind == SymbolRegion || sym.Kind == SymbolRegionMark {
				a.errorf(expr.Pos(), "lambda cannot capture %q", name)
			}
			continue
		}
		captureType := a.currentTrackedValueType(sym)
		if captureType == nil {
			captureType = sym.Type
		}
		if fnType, ok := a.lookupCurrentFunctionValueType(sym); ok {
			captureType = fnType
		}
		bindings = append(bindings, lambdaCaptureBinding{name: name, typ: captureType, mutable: sym.Mutable})
	}
	return bindings
}

func (a *Analyzer) recordLambdaReturnExpr(value ast.Expr, valueType Type) {
	if value == nil || a.currentReturn == nil {
		return
	}
	if refState, ok := a.regionRefStateForExpr(value); ok {
		if region, _, ok := firstLiveRegionDependency(refState); ok && region != nil {
			if _, isRef := valueType.(*RefType); isRef {
				a.errorf(value.Pos(), localRegionEscapeMessage("reference", region.Name))
			} else {
				a.errorf(value.Pos(), localRegionEscapeMessage("value", region.Name))
			}
		}
		if hasRegionProvenance(refState) {
			if merged, ok := mergeRegionRefStates(a.currentReturnProvenance, refState); ok {
				a.currentReturnProvenance = merged
			} else if !hasRegionProvenance(a.currentReturnProvenance) {
				a.currentReturnProvenance = cloneRegionRefState(refState)
			}
		}
	}
	if ownerState, ok := a.borrowedOwnerRefStateForExpr(value); ok {
		if summary, ok := abstractParamOnlyBorrowedOwnerRefSummary(ownerState); ok {
			if merged, ok := mergeBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs, summary); ok {
				a.currentReturnBorrowedOwnerRefs = merged
			} else if !hasBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs) {
				a.currentReturnBorrowedOwnerRefs = summary
			}
		}
	}
	a.recordFreshReturnBindings(valueType)
	a.consumeAffineValueExpr(value, a.matchReturnType(valueType), "return")
}
