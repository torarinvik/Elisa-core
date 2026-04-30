package semantic

import (
	"fmt"
	"llcontext/src/ast"
)

func (a *Analyzer) analyzeStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		var declType Type
		var valueType Type
		if n.Type != nil {
			declType = a.resolveType(n.Type)
		}
		if n.Value != nil {
			valueType = a.analyzeValueExpr(n.Value, declType)
			if declType == nil {
				declType = valueType
			} else if !AssignableTo(declType, valueType) {
				a.errorf(n.Pos(), "variable %q expects %s, got %s", n.Name, declType, valueType)
				a.reportShapeMismatchNotes(n.Pos(), declType, valueType)
			}
		} else if declType == nil {
			a.errorf(n.Pos(), "variable %q requires a type or initializer", n.Name)
			declType = invalidType
		}
		bindingType := declType
		if dstRef, ok := bindingType.(*RefType); ok {
			if srcRef, ok := valueType.(*RefType); ok && srcRef.Mutable && !dstRef.Mutable && AssignableTo(bindingType, valueType) {
				cloned := cloneRefType(dstRef)
				cloned.Mutable = true
				bindingType = cloned
			}
		}
		if specializedViewType, ok := concreteDArrayViewBindingType(bindingType, valueType); ok {
			bindingType = specializedViewType
		}
		if !n.Mutable {
			if specializedType, ok := a.specializeCallbackCarryingType(bindingType, valueType); ok {
				bindingType = specializedType
			}
		}
		sym := &Symbol{Name: n.Name, Kind: SymbolLocal, Type: bindingType, Node: n, Mutable: n.Mutable}
		a.defineLocal(sym, n.Pos())
		a.recordSpecializedValueTypeBinding(sym, valueType)
		a.recordValueBinding(sym, n.Value)
		a.markCreatedProtocolSymbol(sym, n.Value)
		a.recordBorrowedOwnerRefBinding(sym, n.Value)
		a.recordFunctionValueBinding(sym, n.Value)
		a.recordImmutableSymbolOptimizationFacts(sym, n.Value)
		a.recordRegionRefBinding(sym, n.Value)
		if from, fromType, ok := a.freezeMovedPackedStoreSource(n.Value); ok {
			a.remapPackedStoreDependencies(from, sym, PackedEnumStoreWithState(fromType, a.namedTypes["Frozen"]))
		}
		if n.Value != nil && AssignableTo(bindingType, valueType) {
			a.bindActivePackedStoreType(bindingType)
		}
		a.consumeAffineValueExpr(n.Value, bindingType, "move into local "+strconvQuote(n.Name))
	case *ast.LocalParamsStmt:
		a.analyzeLocalParamsStmt(n)
	case *ast.LetDestructureStmt:
		a.analyzeLetDestructureStmt(n)
	case *ast.TupleBindStmt:
		var expectedTuple Type
		targetTypes := make([]Type, len(n.Names))
		if !n.Declare {
			expectedFields := make([]TupleField, 0, len(n.Names))
			for i, binding := range n.Names {
				if binding.Name == "_" {
					expectedFields = append(expectedFields, TupleField{Name: binding.Name})
					continue
				}
				target := &ast.Ident{Position: binding.Position, Name: binding.Name}
				targetTypes[i] = a.assignmentTargetType(target)
				expectedFields = append(expectedFields, TupleField{Name: binding.Name, Type: targetTypes[i]})
			}
			expectedTuple = &TupleType{Fields: expectedFields}
		}
		valueType := a.analyzeValueExpr(n.Value, expectedTuple)
		fields, ok := a.resolvedStructFields(valueType)
		if !ok {
			a.errorf(n.Pos(), "tuple destructuring requires a tuple value, got %s", valueType)
			return
		}
		if _, isTuple := StripAggregateStateType(valueType).(*TupleType); !isTuple {
			a.errorf(n.Pos(), "tuple destructuring requires a tuple value, got %s", valueType)
			return
		}
		if len(n.Names) != len(fields) {
			a.errorf(n.Pos(), "tuple destructuring expects %d bindings, got %d", len(fields), len(n.Names))
		}
		limit := len(n.Names)
		if len(fields) < limit {
			limit = len(fields)
		}
		for i := 0; i < limit; i++ {
			binding := n.Names[i]
			fieldExpr := &ast.FieldExpr{Position: binding.Position, Object: n.Value, Field: fields[i].Name}
			a.recordAnalyzedExprType(fieldExpr, fields[i].Type)
			if binding.Name == "_" {
				if n.Declare {
					a.consumeAffineValueExpr(fieldExpr, fields[i].Type, "discard tuple element")
				}
				continue
			}
			if n.Declare {
				sym := &Symbol{Name: binding.Name, Kind: SymbolLocal, Type: fields[i].Type, Node: n, Mutable: false}
				a.defineLocal(sym, binding.Position)
				a.recordValueBinding(sym, fieldExpr)
				a.recordFunctionValueBinding(sym, fieldExpr)
				a.recordImmutableSymbolOptimizationFacts(sym, fieldExpr)
				a.recordBorrowedOwnerRefBinding(sym, fieldExpr)
				a.recordRegionRefBinding(sym, fieldExpr)
				continue
			}
			target := &ast.Ident{Position: binding.Position, Name: binding.Name}
			targetType := targetTypes[i]
			if targetType == nil {
				targetType = a.assignmentTargetType(target)
			}
			a.clearPackedVariantViewExpr(target)
			if !AssignableTo(targetType, fields[i].Type) {
				a.errorf(binding.Position, "cannot assign %s to %s", fields[i].Type, targetType)
				a.reportShapeMismatchNotes(binding.Position, targetType, fields[i].Type)
			}
			a.recordAssignmentRefinement(target, targetType, fields[i].Type)
			a.recordRegionRefAssignment(target, fieldExpr)
			a.recordSpecializedValueTypeTarget(target, fields[i].Type)
			a.recordNamedStateAssignmentTarget(target, fieldExpr, fields[i].Type)
			a.clearAffineValueTarget(target)
			a.trackAffineValueTarget(target, targetType)
			a.markCreatedProtocolTarget(target, fieldExpr, targetType)
			a.recordBorrowedOwnerRefTarget(target, targetType, fieldExpr)
			a.recordFunctionValueTarget(target, fieldExpr)
			if AssignableTo(targetType, fields[i].Type) {
				a.bindActivePackedStoreType(targetType)
			}
			a.consumeAffineValueExpr(fieldExpr, targetType, "assignment")
		}
	case *ast.MoveBindStmt:
		a.analyzeMoveBindStmt(n)
	case *ast.DeferStmt:
		a.analyzeDeferStmt(n)
	case *ast.ScopeStmt:
		a.analyzeScopeStmt(n)
	case *ast.RegionStmt:
		if n.Capacity != nil {
			capacityType := a.analyzeExpr(n.Capacity)
			if !IsNumericType(capacityType) {
				a.errorf(n.Capacity.Pos(), "region capacity must be numeric, got %s", capacityType)
			}
		}
		arenaType, ok := a.namedTypes["Arena"]
		if !ok {
			a.errorf(n.Pos(), "missing builtin Arena type for region lowering")
			arenaType = invalidType
		}
		sym := &Symbol{Name: n.Name, Kind: SymbolRegion, Type: arenaType, Node: n, Mutable: false}
		a.defineLocal(sym, n.Pos())
		if a.currentRegions != nil {
			a.currentRegions[sym] = regionState{}
		}
	case *ast.MarkStmt:
		a.analyzeMarkStmt(n)
	case *ast.CheckpointStmt:
		a.analyzeCheckpointStmt(n)
	case *ast.GroupedCheckpointStmt:
		a.analyzeGroupedCheckpointStmt(n)
	case *ast.RestoreStmt:
		a.analyzeRestoreStmt(n)
	case *ast.RestoreCheckpointStmt:
		a.analyzeRestoreCheckpointStmt(n)
	case *ast.ResetStmt:
		a.analyzeResetStmt(n)
	case *ast.DestroyStmt:
		sym, state := a.lookupRegionState(n.Name)
		if sym == nil {
			a.errorf(n.Pos(), "undefined region %q", n.Name)
			return
		}
		if state.Destroyed {
			a.errorf(n.Pos(), "region %q has already been destroyed", n.Name)
			return
		}
		before := state.Generation
		state.Destroyed = true
		a.currentRegions[sym] = state
		a.invalidateRegionRefs(sym, func(regionDependencyState) bool { return true }, fmt.Sprintf("destroy of region %q", n.Name))
		a.invalidateRegionMarks(sym, func(regionMarkState) bool { return true }, fmt.Sprintf("destroy of region %q", n.Name))
		a.recordRegionInvalidateTransform(n.Pos(), n.Name, "", "destroy region", before, -1)
	case *ast.AssignStmt:
		var targetType Type
		if n.Optional {
			targetType = a.optionalAssignmentTargetType(n.Target)
		} else {
			targetType = a.assignmentTargetType(n.Target)
		}
		a.clearPackedVariantViewExpr(n.Target)
		valueType := a.analyzeValueExpr(n.Value, targetType)
		if !AssignableTo(targetType, valueType) {
			a.errorf(n.Pos(), "cannot assign %s to %s", valueType, targetType)
			a.reportShapeMismatchNotes(n.Pos(), targetType, valueType)
		}
		if !n.Optional {
			a.recordAssignmentRefinement(n.Target, targetType, valueType)
			a.recordRegionRefAssignment(n.Target, n.Value)
		}
		if ident, ok := n.Target.(*ast.Ident); ok && a.currentScope != nil {
			if targetSym, ok := a.currentScope.Lookup(ident.Name); ok {
				if from, fromType, ok := a.freezeMovedPackedStoreSource(n.Value); ok {
					a.remapPackedStoreDependencies(from, targetSym, PackedEnumStoreWithState(fromType, a.namedTypes["Frozen"]))
				}
			}
		}
		a.recordSpecializedValueTypeTarget(n.Target, valueType)
		if !n.Optional {
			a.recordNamedStateAssignmentTarget(n.Target, n.Value, valueType)
			a.clearAffineValueTarget(n.Target)
			a.trackAffineValueTarget(n.Target, targetType)
			a.markCreatedProtocolTarget(n.Target, n.Value, targetType)
			a.recordBorrowedOwnerRefTarget(n.Target, targetType, n.Value)
			a.recordFunctionValueTarget(n.Target, n.Value)
		}
		if AssignableTo(targetType, valueType) {
			a.bindActivePackedStoreType(targetType)
		}
		a.consumeAffineValueExpr(n.Value, targetType, "assignment")
	case *ast.AugAssignStmt:
		targetType := a.assignmentTargetType(n.Target)
		valueType := a.analyzeExpr(n.Value)
		if !IsNumericType(targetType) || !IsNumericType(valueType) {
			a.errorf(n.Pos(), "augmented assignment requires numeric operands")
		}
		a.recordNamedStateAugAssignTarget(n.Target)
	case *ast.AsRefAssignStmt:
		targetType := a.asRefTargetType(n.Target, n.AsKind)
		a.clearPackedVariantViewExpr(n.Target)
		valueType := a.analyzeValueExpr(n.Value, targetType)
		if !AssignableTo(targetType, valueType) {
			a.errorf(n.Pos(), "cannot assign %s to %s", valueType, targetType)
			a.reportShapeMismatchNotes(n.Pos(), targetType, valueType)
		}
		a.recordAssignmentRefinement(n.Target, targetType, targetType)
		a.recordRegionRefAssignment(n.Target, n.Value)
		a.recordSpecializedValueTypeTarget(n.Target, valueType)
		a.recordNamedStateAssignmentTarget(n.Target, n.Value, valueType)
		a.clearAffineValueTarget(n.Target)
		a.trackAffineValueTarget(n.Target, targetType)
		a.markCreatedProtocolTarget(n.Target, n.Value, targetType)
		a.recordBorrowedOwnerRefTarget(n.Target, targetType, n.Value)
		a.recordFunctionValueTarget(n.Target, n.Value)
		if AssignableTo(targetType, valueType) {
			a.bindActivePackedStoreType(targetType)
		}
		a.consumeAffineValueExpr(n.Value, targetType, "assignment")
	case *ast.ReturnStmt:
		if n.Value == nil {
			if currentUnion, ok := a.currentReturn.(*ErrorUnionType); ok {
				if !SameType(currentUnion.Value, a.namedTypes["void"]) {
					a.errorf(n.Pos(), "return value required for %s", a.currentReturn)
				}
				a.validateCurrentFuncPoststates()
				return
			}
			if a.currentReturn != nil && !SameType(a.currentReturn, a.namedTypes["void"]) {
				a.errorf(n.Pos(), "return value required for %s", a.currentReturn)
			}
			a.validateCurrentFuncPoststates()
			return
		}
		valueType := a.analyzeValueExpr(n.Value, a.currentReturn)
		if a.currentReturn == nil {
			a.errorf(n.Pos(), "unexpected return value")
			return
		}
		if refState, ok := a.regionRefStateForExpr(n.Value); ok {
			if region, _, ok := firstLiveRegionDependency(refState); ok && region != nil {
				if _, isRef := valueType.(*RefType); isRef {
					a.errorf(n.Pos(), localRegionEscapeMessage("reference", region.Name))
				} else {
					a.errorf(n.Pos(), localRegionEscapeMessage("value", region.Name))
				}
			}
			if summary, ok := abstractParamOnlyRegionRefState(refState); ok {
				if merged, ok := mergeRegionRefStates(a.currentReturnProvenance, summary); ok {
					a.currentReturnProvenance = merged
				} else if !hasRegionProvenance(a.currentReturnProvenance) {
					a.currentReturnProvenance = cloneRegionRefState(summary)
				}
			}
		}
		if ownerState, ok := a.borrowedOwnerRefStateForExpr(n.Value); ok {
			if summary, ok := abstractParamOnlyBorrowedOwnerRefSummary(ownerState); ok {
				if merged, ok := mergeBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs, summary); ok {
					a.currentReturnBorrowedOwnerRefs = merged
				} else if !hasBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs) {
					a.currentReturnBorrowedOwnerRefs = summary
				}
			}
		}
		a.recordFreshReturnBindings(valueType)
		expectedReturn := a.matchReturnType(valueType)
		if !AssignableTo(expectedReturn, valueType) {
			a.errorf(n.Pos(), "return type expects %s, got %s", expectedReturn, valueType)
			a.reportShapeMismatchNotes(n.Pos(), expectedReturn, valueType)
		}
		a.validateCurrentFuncPoststatesForReturnValue(n.Value)
		a.consumeAffineValueExpr(n.Value, expectedReturn, "return")
	case *ast.IfStmt:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "if condition must be bool, got %s", condType)
		}
		mergedAffine := a.cloneAffineValueStates()
		mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
		functionValueBranches := make([]map[*Symbol]*FuncType, 0, len(n.Elifs)+2)
		specializedValueTypeBranches := make([]map[*Symbol]Type, 0, len(n.Elifs)+2)
		thenSnapshot := a.analyzeBlockWithConditionAffineClone(n.Then, a.currentScope, n.Cond, true)
		if !blockDefinitelyExits(n.Then) {
			mergedAffine = mergeAffineValueStates(mergedAffine, thenSnapshot.Affine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, thenSnapshot.BorrowedOwnerRefs)
			functionValueBranches = append(functionValueBranches, thenSnapshot.FunctionValues)
			specializedValueTypeBranches = append(specializedValueTypeBranches, thenSnapshot.SpecializedValueTypes)
		}
		for _, elif := range n.Elifs {
			elifType := a.analyzeExpr(elif.Cond)
			if !IsBoolType(elifType) {
				a.errorf(elif.Position, "elif condition must be bool, got %s", elifType)
			}
			elifSnapshot := a.analyzeBlockWithConditionAffineClone(elif.Body, a.currentScope, elif.Cond, true)
			if !blockDefinitelyExits(elif.Body) {
				mergedAffine = mergeAffineValueStates(mergedAffine, elifSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, elifSnapshot.BorrowedOwnerRefs)
				functionValueBranches = append(functionValueBranches, elifSnapshot.FunctionValues)
				specializedValueTypeBranches = append(specializedValueTypeBranches, elifSnapshot.SpecializedValueTypes)
			}
		}
		if len(n.Elifs) == 0 {
			elseSnapshot := a.analyzeBlockWithConditionAffineClone(n.Else, a.currentScope, n.Cond, false)
			if !blockDefinitelyExits(n.Else) {
				mergedAffine = mergeAffineValueStates(mergedAffine, elseSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, elseSnapshot.BorrowedOwnerRefs)
				functionValueBranches = append(functionValueBranches, elseSnapshot.FunctionValues)
				specializedValueTypeBranches = append(specializedValueTypeBranches, elseSnapshot.SpecializedValueTypes)
			}
		} else {
			elseSnapshot := a.analyzeBlockWithAffineClone(n.Else, NewScope(a.currentScope))
			if !blockDefinitelyExits(n.Else) {
				mergedAffine = mergeAffineValueStates(mergedAffine, elseSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, elseSnapshot.BorrowedOwnerRefs)
				functionValueBranches = append(functionValueBranches, elseSnapshot.FunctionValues)
				specializedValueTypeBranches = append(specializedValueTypeBranches, elseSnapshot.SpecializedValueTypes)
			}
		}
		if len(n.Else) == 0 {
			functionValueBranches = append(functionValueBranches, a.currentFunctionValues)
			specializedValueTypeBranches = append(specializedValueTypeBranches, a.currentSpecializedValueTypes)
		}
		a.currentAffineValues = mergedAffine
		a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
		if mergedFunctionValues, ok := a.intersectFunctionValueFlows(functionValueBranches...); ok {
			a.currentFunctionValues = mergedFunctionValues
		}
		if mergedSpecializedValueTypes, ok := a.intersectSpecializedValueTypeFlows(specializedValueTypeBranches...); ok {
			a.currentSpecializedValueTypes = mergedSpecializedValueTypes
		}
		a.applyPostIfFallthroughRefinement(n)
	case *ast.MatchStmt:
		a.analyzeMatchStmt(n)
	case *ast.ExpectPatternStmt:
		a.analyzeExpectPatternStmt(n)
	case *ast.InStoreStmt:
		a.analyzeInStoreStmt(n)
	case *ast.CanStmt:
		a.analyzeCanStmt(n)
	case *ast.WithStmt:
		a.analyzeWithStmt(n)
	case *ast.ArgsScopeStmt:
		a.analyzeArgsScopeStmt(n)

	case *ast.PoolStmt:
		a.analyzePoolStmt(n)
	case *ast.LockStmt:
		a.analyzeLockStmt(n)
	case *ast.WhileStmt:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "while condition must be bool, got %s", condType)
		}
		mergedAffine := a.cloneAffineValueStates()
		mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
		mergedFunctionValues := a.cloneFunctionValueBindings()
		mergedSpecializedValueTypes := a.cloneSpecializedValueTypeBindings()
		bodySnapshot := a.analyzeBlockWithConditionAffineClone(n.Body, a.currentScope, n.Cond, true)
		if !blockDefinitelyExits(n.Body) {
			mergedAffine = mergeAffineValueStates(mergedAffine, bodySnapshot.Affine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, bodySnapshot.BorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, bodySnapshot.FunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, bodySnapshot.SpecializedValueTypes)
		}
		a.currentAffineValues = mergedAffine
		a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
		a.currentFunctionValues = mergedFunctionValues
		a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	case *ast.ForStmt:
		a.analyzeForStmt(n)
	case *ast.IterForStmt:
		a.analyzeIterForStmt(n)
	case *ast.ParallelForStmt:
		a.analyzeParallelForStmt(n)
	case *ast.PassStmt:
		return
	case *ast.SignalStmt:
		refs := a.resolvePermissionRefs(n.Permissions, true)
		a.recordFunctionPermissionRefs(refs)
		return
	case *ast.PanicStmt:
		a.analyzeExpr(n.Message)
	case *ast.ExprStmt:
		if cond, ok := assertedCondition(n.Expr); ok {
			condType := a.analyzeCondExpr(cond)
			if !IsBoolType(condType) {
				a.errorf(n.Pos(), "assert condition must be bool, got %s", condType)
			}
			a.applyConditionRefinements(a.currentScope, cond, true)
			return
		}
		a.analyzeExpr(n.Expr)
	case *ast.StaticIfStmt:
		for _, stmt := range a.activeStmtBranch(n) {
			a.analyzeStmt(stmt)
		}
	case *ast.StaticErrorStmt:
		if msg, ok := a.evalConstStringExpr(n.Message); ok {
			a.errorf(n.Pos(), "static error: %s", msg)
		} else {
			a.errorf(n.Pos(), "static error triggered")
		}
	case *ast.DiscardStmt:
		valueType := a.analyzeExpr(n.Value)
		a.consumeAffineValueExpr(n.Value, valueType, "discard")
	}
}

func concreteDArrayViewBindingType(declared Type, actual Type) (Type, bool) {
	declaredView, ok := declared.(*DArrayViewType)
	if !ok {
		return nil, false
	}
	actualView, ok := actual.(*DArrayViewType)
	if !ok {
		return nil, false
	}
	if !SameType(declaredView.Elem, actualView.Elem) {
		return nil, false
	}
	return actualView, true
}

func (a *Analyzer) analyzeMoveBindStmt(stmt *ast.MoveBindStmt) {
	if stmt == nil {
		return
	}
	valueType := a.analyzeExpr(stmt.Value)
	valueState, hasValueState := a.regionRefStateForExpr(stmt.Value)
	borrowedOwnerState, hasBorrowedOwnerState := a.borrowedOwnerRefStateForExpr(stmt.Value)
	switch p := stmt.Pattern.(type) {
	case *ast.MoveBindNamePattern:
		if p.Name != "_" {
			sym := &Symbol{Name: p.Name, Kind: SymbolLocal, Type: valueType, Node: p, Mutable: false}
			a.defineLocal(sym, p.Pos())
			a.recordValueBinding(sym, stmt.Value)
			a.recordFunctionValueBinding(sym, stmt.Value)
			a.recordImmutableSymbolOptimizationFacts(sym, stmt.Value)
			if hasBorrowedOwnerState {
				a.currentBorrowedOwnerRefs[sym] = borrowedOwnerState
			}
			if hasValueState {
				a.recordResolvedRegionRefBinding(sym, valueState)
			} else {
				a.recordRegionRefBinding(sym, stmt.Value)
			}
		}
	case *ast.MoveBindStructPattern:
		fields, ok := a.resolveMoveBindStructPattern(p, valueType)
		if !ok {
			return
		}
		for i, arg := range p.Args {
			if i >= len(fields) || arg.Name == "_" {
				continue
			}
			sym := &Symbol{Name: arg.Name, Kind: SymbolLocal, Type: fields[i].Type, Node: p, Mutable: false}
			a.defineLocal(sym, arg.Position)
			fieldExpr := &ast.FieldExpr{Position: arg.Position, Object: stmt.Value, Field: fields[i].Name}
			a.recordValueBinding(sym, fieldExpr)
			a.recordFunctionValueBinding(sym, fieldExpr)
			a.recordImmutableSymbolOptimizationFacts(sym, fieldExpr)
			if hasBorrowedOwnerState {
				if fieldState, ok := projectBorrowedOwnerRefFieldState(borrowedOwnerState, fields[i].Name); ok {
					a.currentBorrowedOwnerRefs[sym] = fieldState
				}
			}
			if !hasValueState {
				continue
			}
			if fieldState, ok := projectRegionFieldState(valueState, fields[i].Name); ok {
				a.recordResolvedRegionRefBinding(sym, fieldState)
			}
		}
	case *ast.MoveBindVariantPattern:
		payloads, enumType, packedStoreState, ok := a.resolveMoveBindVariantPattern(stmt, p, valueType)
		if !ok {
			return
		}
		_ = enumType
		a.bindResolvedMoveBindVariantFields(payloads, stmt.Value, p, p, valueState, hasValueState, borrowedOwnerState, hasBorrowedOwnerState, packedStoreState)
	default:
		a.errorf(stmt.Pos(), "unsupported move-as pattern %T", stmt.Pattern)
		return
	}
	a.consumeAffineValueExpr(stmt.Value, valueType, "move-as destructure")
}
