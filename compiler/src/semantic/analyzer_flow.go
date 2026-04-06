package semantic

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"sort"
	"strconv"
	"strings"
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
				a.errorf(n.Pos(), "variable %q expects %s, got %s", n.Name, declType.String(), valueType.String())
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
			a.errorf(n.Pos(), "tuple destructuring requires a tuple value, got %s", valueType.String())
			return
		}
		if _, isTuple := StripAggregateStateType(valueType).(*TupleType); !isTuple {
			a.errorf(n.Pos(), "tuple destructuring requires a tuple value, got %s", valueType.String())
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
				a.errorf(binding.Position, "cannot assign %s to %s", fields[i].Type.String(), targetType.String())
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
	case *ast.OpenStmt:
		a.analyzeOpenStmt(n)
	case *ast.ViewStmt:
		a.analyzeViewStmt(n)
	case *ast.DeferStmt:
		a.analyzeDeferStmt(n)
	case *ast.RegionStmt:
		if n.Capacity != nil {
			capacityType := a.analyzeExpr(n.Capacity)
			if !IsNumericType(capacityType) {
				a.errorf(n.Capacity.Pos(), "region capacity must be numeric, got %s", capacityType.String())
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
	case *ast.RestoreStmt:
		a.analyzeRestoreStmt(n)
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
		state.Destroyed = true
		a.currentRegions[sym] = state
		a.invalidateRegionRefs(sym, func(regionDependencyState) bool { return true }, fmt.Sprintf("destroy of region %q", n.Name))
		a.invalidateRegionMarks(sym, func(regionMarkState) bool { return true }, fmt.Sprintf("destroy of region %q", n.Name))
	case *ast.AssignStmt:
		targetType := a.assignmentTargetType(n.Target)
		a.clearPackedVariantViewExpr(n.Target)
		valueType := a.analyzeValueExpr(n.Value, targetType)
		if !AssignableTo(targetType, valueType) {
			a.errorf(n.Pos(), "cannot assign %s to %s", valueType.String(), targetType.String())
			a.reportShapeMismatchNotes(n.Pos(), targetType, valueType)
		}
		a.recordAssignmentRefinement(n.Target, targetType, valueType)
		a.recordRegionRefAssignment(n.Target, n.Value)
		if ident, ok := n.Target.(*ast.Ident); ok && a.currentScope != nil {
			if targetSym, ok := a.currentScope.Lookup(ident.Name); ok {
				if from, fromType, ok := a.freezeMovedPackedStoreSource(n.Value); ok {
					a.remapPackedStoreDependencies(from, targetSym, PackedEnumStoreWithState(fromType, a.namedTypes["Frozen"]))
				}
			}
		}
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
			a.errorf(n.Pos(), "cannot assign %s to %s", valueType.String(), targetType.String())
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
					a.errorf(n.Pos(), "return value required for %s", a.currentReturn.String())
				}
				a.validateCurrentFuncPoststates()
				return
			}
			if a.currentReturn != nil && !SameType(a.currentReturn, a.namedTypes["void"]) {
				a.errorf(n.Pos(), "return value required for %s", a.currentReturn.String())
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
					a.errorf(n.Pos(), "cannot return reference allocated from local region %q", region.Name)
				} else {
					a.errorf(n.Pos(), "cannot return value depending on local region %q", region.Name)
				}
			}
			if summary, ok := abstractParamOnlyRegionRefState(refState); ok {
				if merged, ok := mergeRegionRefStates(a.currentReturnProvenance, summary); ok {
					a.currentReturnProvenance = merged
				} else if !hasRegionProvenance(a.currentReturnProvenance) {
					a.currentReturnProvenance = summary
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
			a.errorf(n.Pos(), "return type expects %s, got %s", expectedReturn.String(), valueType.String())
			a.reportShapeMismatchNotes(n.Pos(), expectedReturn, valueType)
		}
		a.validateCurrentFuncPoststatesForReturnValue(n.Value)
		a.consumeAffineValueExpr(n.Value, expectedReturn, "return")
	case *ast.IfStmt:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "if condition must be bool, got %s", condType.String())
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
				a.errorf(elif.Position, "elif condition must be bool, got %s", elifType.String())
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
	case *ast.InStoreStmt:
		a.analyzeInStoreStmt(n)
	case *ast.CanStmt:
		a.analyzeCanStmt(n)
	case *ast.WithStmt:
		a.analyzeWithStmt(n)

	case *ast.PoolStmt:
		a.analyzePoolStmt(n)
	case *ast.LockStmt:
		a.analyzeLockStmt(n)
	case *ast.WhileStmt:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "while condition must be bool, got %s", condType.String())
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
	case *ast.PanicStmt:
		a.analyzeExpr(n.Message)
	case *ast.ExprStmt:
		if cond, ok := assertedCondition(n.Expr); ok {
			condType := a.analyzeCondExpr(cond)
			if !IsBoolType(condType) {
				a.errorf(n.Pos(), "assert condition must be bool, got %s", condType.String())
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

func (a *Analyzer) analyzeOpenStmt(stmt *ast.OpenStmt) {
	if stmt == nil || stmt.Pattern == nil {
		return
	}
	valueType := a.analyzeExpr(stmt.Value)
	enumType, _, ok := resolveMatchableEnumType(valueType)
	if ok {
		if !enumType.Packed {
			a.errorf(stmt.Pos(), "open requires a packed enum value, got ordinary enum %q", enumType.Name)
			a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
			return
		}
		resolvedStmt := &ast.MoveBindStmt{Position: stmt.Position, Value: stmt.Value, Store: stmt.Store, Pattern: stmt.Pattern}
		payloads, _, packedStoreState, ok := a.resolveMoveBindVariantPattern(resolvedStmt, stmt.Pattern, valueType)
		valueState, hasValueState := a.regionRefStateForExpr(stmt.Value)
		borrowedOwnerState, hasBorrowedOwnerState := a.borrowedOwnerRefStateForExpr(stmt.Value)
		scope := NewScope(a.currentScope)
		savedScope := a.currentScope
		a.currentScope = scope
		a.bindResolvedMoveBindVariantFields(payloads, stmt.Value, stmt.Pattern, stmt.Pattern, valueState, hasValueState, borrowedOwnerState, hasBorrowedOwnerState, packedStoreState)
		a.currentScope = savedScope
		a.analyzeBlockWithRegionClone(stmt.Body, scope)
		if ok {
			a.recordAffineDestructureConsumption(stmt.Value, valueType, "open over affine enum")
		}
		return
	}
	treeType, _, ok := resolveMatchableTreeCategoryType(valueType)
	if ok {
		a.analyzeTreeOpenStmt(stmt, treeType)
		return
	}
	a.errorf(stmt.Pos(), "open requires a packed enum or tree-category value, got %s", valueType.String())
	a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
}

func (a *Analyzer) analyzeTreeOpenStmt(stmt *ast.OpenStmt, treeType *TreeCategoryType) {
	if stmt == nil || stmt.Pattern == nil || treeType == nil {
		return
	}
	a.validateTreeVariantBinderStore("open", treeType, stmt.Store)
	payloads, ok := a.resolveMoveBindTreeVariantPattern(stmt.Pattern, treeType)
	valueState, hasValueState := a.regionRefStateForExpr(stmt.Value)
	borrowedOwnerState, hasBorrowedOwnerState := a.borrowedOwnerRefStateForExpr(stmt.Value)
	scope := NewScope(a.currentScope)
	savedScope := a.currentScope
	a.currentScope = scope
	if ok {
		a.bindResolvedMoveBindVariantFields(payloads, stmt.Value, stmt.Pattern, stmt.Pattern, valueState, hasValueState, borrowedOwnerState, hasBorrowedOwnerState, nil)
	}
	a.currentScope = savedScope
	a.analyzeBlockWithRegionClone(stmt.Body, scope)
}

func (a *Analyzer) analyzeViewStmt(stmt *ast.ViewStmt) {
	if stmt == nil || stmt.Pattern == nil {
		return
	}
	valueType := a.analyzeExpr(stmt.Value)
	treeType, _, ok := resolveMatchableTreeCategoryType(valueType)
	if ok {
		a.analyzeTreeViewStmt(stmt, treeType)
		return
	}
	viewType, ok := a.resolveViewBindType(stmt, valueType)
	scope := NewScope(a.currentScope)
	if ok {
		savedScope := a.currentScope
		a.currentScope = scope
		a.bindMatchedPackedVariantView(stmt.Value, viewType)
		if stmt.Pattern.Name != "" && stmt.Pattern.Name != "_" {
			sym := &Symbol{Name: stmt.Pattern.Name, Kind: SymbolLocal, Type: viewType, Node: stmt.Pattern, Mutable: false}
			a.defineLocal(sym, stmt.Pattern.Pos())
			a.recordValueBinding(sym, stmt.Value)
			a.recordBorrowedOwnerRefBinding(sym, stmt.Value)
			a.recordFunctionValueBinding(sym, stmt.Value)
			a.recordImmutableSymbolOptimizationFacts(sym, stmt.Value)
			a.recordRegionRefBinding(sym, stmt.Value)
		}
		if len(stmt.Pattern.Args) != 0 {
			resolvedPattern := viewBindPatternAsMoveBindPattern(stmt.Pattern)
			resolvedStmt := &ast.MoveBindStmt{Position: stmt.Position, Value: stmt.Value, Store: stmt.Store, Pattern: resolvedPattern}
			payloads, _, packedStoreState, payloadsOK := a.resolveMoveBindVariantPattern(resolvedStmt, resolvedPattern, valueType)
			valueState, hasValueState := a.regionRefStateForExpr(stmt.Value)
			borrowedOwnerState, hasBorrowedOwnerState := a.borrowedOwnerRefStateForExpr(stmt.Value)
			if payloadsOK {
				a.bindResolvedMoveBindVariantFields(payloads, stmt.Value, resolvedPattern, stmt.Pattern, valueState, hasValueState, borrowedOwnerState, hasBorrowedOwnerState, packedStoreState)
			}
		}
		a.currentScope = savedScope
	}
	a.analyzeBlockWithRegionClone(stmt.Body, scope)
	if ok {
		a.recordAffineDestructureConsumption(stmt.Value, valueType, "view over affine enum")
	}
}

func (a *Analyzer) analyzeTreeViewStmt(stmt *ast.ViewStmt, treeType *TreeCategoryType) {
	if stmt == nil || stmt.Pattern == nil || treeType == nil {
		return
	}
	viewType, ok := a.resolveTreeViewBindType(stmt, treeType)
	scope := NewScope(a.currentScope)
	if ok {
		savedScope := a.currentScope
		a.currentScope = scope
		a.bindRefinedExprType(a.currentScope, stmt.Value, viewType)
		if stmt.Pattern.Name != "" && stmt.Pattern.Name != "_" {
			sym := &Symbol{Name: stmt.Pattern.Name, Kind: SymbolLocal, Type: viewType, Node: stmt.Pattern, Mutable: false}
			a.defineLocal(sym, stmt.Pattern.Pos())
			a.recordValueBinding(sym, stmt.Value)
			a.recordBorrowedOwnerRefBinding(sym, stmt.Value)
			a.recordFunctionValueBinding(sym, stmt.Value)
			a.recordImmutableSymbolOptimizationFacts(sym, stmt.Value)
			a.recordRegionRefBinding(sym, stmt.Value)
		}
		if len(stmt.Pattern.Args) != 0 {
			resolvedPattern := viewBindPatternAsMoveBindPattern(stmt.Pattern)
			payloads, payloadsOK := a.resolveMoveBindTreeVariantPattern(resolvedPattern, treeType)
			valueState, hasValueState := a.regionRefStateForExpr(stmt.Value)
			borrowedOwnerState, hasBorrowedOwnerState := a.borrowedOwnerRefStateForExpr(stmt.Value)
			if payloadsOK {
				a.bindResolvedMoveBindVariantFields(payloads, stmt.Value, resolvedPattern, stmt.Pattern, valueState, hasValueState, borrowedOwnerState, hasBorrowedOwnerState, nil)
			}
		}
		a.currentScope = savedScope
	}
	a.analyzeBlockWithRegionClone(stmt.Body, scope)
}

func viewBindPatternAsMoveBindPattern(pattern *ast.ViewBindPattern) *ast.MoveBindVariantPattern {
	if pattern == nil {
		return nil
	}
	return &ast.MoveBindVariantPattern{Position: pattern.Position, EnumName: pattern.EnumName, Variant: pattern.Variant, Args: append([]ast.MatchPatternArg(nil), pattern.Args...)}
}

func (a *Analyzer) validateTreeVariantBinderStore(keyword string, treeType *TreeCategoryType, storeExpr ast.Expr) {
	if treeType == nil || storeExpr == nil {
		return
	}
	a.errorf(storeExpr.Pos(), "tree %s over %q does not take an in-store clause", keyword, treeType.Name)
}

func (a *Analyzer) resolveTreeViewBindType(stmt *ast.ViewStmt, treeType *TreeCategoryType) (*TreeVariantViewType, bool) {
	if stmt == nil || stmt.Pattern == nil || treeType == nil {
		return nil, false
	}
	a.validateTreeVariantBinderStore("view", treeType, stmt.Store)
	if stmt.Pattern.EnumName != treeType.Name {
		a.errorf(stmt.Pattern.Pos(), "view pattern expects tree category %q, got %q", treeType.Name, stmt.Pattern.EnumName)
		return nil, false
	}
	variant, ok := treeType.Variant(stmt.Pattern.Variant)
	if !ok {
		a.errorf(stmt.Pattern.Pos(), "tree category %q has no variant %q", treeType.Name, stmt.Pattern.Variant)
		return nil, false
	}
	return treeType.VariantViewType(variant), true
}

func (a *Analyzer) resolveViewBindType(stmt *ast.ViewStmt, actual Type) (*PackedVariantViewType, bool) {
	if stmt == nil || stmt.Pattern == nil {
		return nil, false
	}
	enumType, _, ok := resolveMatchableEnumType(actual)
	if !ok {
		a.errorf(stmt.Pos(), "view requires a packed enum or tree-category value, got %s", actual.String())
		return nil, false
	}
	if !enumType.Packed {
		a.errorf(stmt.Pos(), "view requires a packed enum value, got ordinary enum %q", enumType.Name)
		return nil, false
	}
	a.validateMatchStore(stmt.Pos(), stmt.Value, actual, enumType, stmt.Store)
	if stmt.Pattern.EnumName != enumType.Name {
		a.errorf(stmt.Pattern.Pos(), "view pattern expects enum %q, got %q", enumType.Name, stmt.Pattern.EnumName)
		return nil, false
	}
	variant, ok := enumType.Variant(stmt.Pattern.Variant)
	if !ok {
		a.errorf(stmt.Pattern.Pos(), "enum %q has no variant %q", enumType.Name, stmt.Pattern.Variant)
		return nil, false
	}
	viewType := &PackedVariantViewType{Enum: enumType, Variant: variant}
	return viewType, true
}

func (a *Analyzer) analyzeDeferStmt(stmt *ast.DeferStmt) {
	if stmt == nil {
		return
	}
	if stmt.Mode == ast.DeferModeFunction && a.currentNonGlobalScopeDepth() != 1 {
		a.errorf(stmt.Pos(), "defer function is currently only supported in the outermost function scope")
	}
	a.validateDeferStmtBody(stmt.Body)
	savedReturnProvenance := a.currentReturnProvenance
	savedReturnBorrowedOwnerRefs := a.currentReturnBorrowedOwnerRefs
	a.analyzeBlockWithAffineClone(stmt.Body, NewScope(a.currentScope))
	a.currentReturnProvenance = savedReturnProvenance
	a.currentReturnBorrowedOwnerRefs = savedReturnBorrowedOwnerRefs
	collector := newDeferCaptureCollector(a, a.currentScope)
	collector.collectStmts(stmt.Body)
	if a.deferInfo == nil {
		a.deferInfo = map[*ast.DeferStmt]*DeferInfo{}
	}
	a.deferInfo[stmt] = &DeferInfo{Mode: stmt.Mode, Captures: append([]string(nil), collector.captureOrder...)}
}

func (a *Analyzer) currentNonGlobalScopeDepth() int {
	depth := 0
	for scope := a.currentScope; scope != nil && scope != a.globalScope; scope = scope.Parent {
		depth++
	}
	return depth
}

func (a *Analyzer) validateDeferStmtBody(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		a.validateDeferStmtBodyStmt(stmt)
	}
}

func (a *Analyzer) validateDeferStmtBodyStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.MoveBindStmt:
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Store)
	case *ast.OpenStmt:
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Store)
		a.validateDeferStmtBody(n.Body)
	case *ast.ViewStmt:
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Store)
		a.validateDeferStmtBody(n.Body)
	case *ast.DeferStmt:
		a.errorf(n.Pos(), "nested defer is not supported inside defer bodies")
		a.validateDeferStmtBody(n.Body)
	case *ast.AssignStmt:
		a.validateDeferStmtBodyExpr(n.Target)
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.AugAssignStmt:
		a.validateDeferStmtBodyExpr(n.Target)
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.AsRefAssignStmt:
		a.validateDeferStmtBodyExpr(n.Target)
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.ReturnStmt:
		a.errorf(n.Pos(), "defer body cannot return from the enclosing function")
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.IfStmt:
		a.validateDeferStmtBodyExpr(n.Cond)
		a.validateDeferStmtBody(n.Then)
		for _, elif := range n.Elifs {
			a.validateDeferStmtBodyExpr(elif.Cond)
			a.validateDeferStmtBody(elif.Body)
		}
		a.validateDeferStmtBody(n.Else)
	case *ast.MatchStmt:
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Store)
		for _, arm := range n.Arms {
			a.validateDeferStmtBody(arm.Body)
		}
	case *ast.InStoreStmt:
		a.validateDeferStmtBodyExpr(n.Store)
		a.validateDeferStmtBody(n.Body)
	case *ast.CanStmt:
		a.validateDeferStmtBody(n.Body)
	case *ast.PoolStmt:
		a.validateDeferStmtBodyExpr(n.Workers)
		a.validateDeferStmtBody(n.Body)
	case *ast.LockStmt:
		a.validateDeferStmtBodyExpr(n.Mutex)
		a.validateDeferStmtBody(n.Body)
	case *ast.WhileStmt:
		a.validateDeferStmtBodyExpr(n.Cond)
		a.validateDeferStmtBody(n.Body)
	case *ast.ForStmt:
		a.validateDeferStmtBodyExpr(n.Start)
		a.validateDeferStmtBodyExpr(n.End)
		a.validateDeferStmtBodyExpr(n.Step)
		a.validateDeferStmtBody(n.Body)
	case *ast.IterForStmt:
		a.validateDeferStmtBodyExpr(n.Source)
		a.validateDeferStmtBody(n.Body)
	case *ast.ParallelForStmt:
		a.validateDeferStmtBodyExpr(n.Source)
		a.validateDeferStmtBody(n.Body)
	case *ast.PanicStmt:
		a.validateDeferStmtBodyExpr(n.Message)
	case *ast.ExprStmt:
		a.validateDeferStmtBodyExpr(n.Expr)
	case *ast.StaticIfStmt:
		for _, active := range a.activeStmtBranch(n) {
			a.validateDeferStmtBodyStmt(active)
		}
	case *ast.StaticErrorStmt:
		a.validateDeferStmtBodyExpr(n.Message)
	case *ast.DiscardStmt:
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.RegionStmt:
		a.validateDeferStmtBodyExpr(n.Capacity)
	case *ast.PassStmt, *ast.MarkStmt, *ast.RestoreStmt, *ast.ResetStmt, *ast.DestroyStmt:
		return
	}
}

func (a *Analyzer) validateDeferStmtBodyExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		a.validateDeferStmtBodyExpr(n.Left)
		a.validateDeferStmtBodyExpr(n.Right)
	case *ast.UnaryExpr:
		a.validateDeferStmtBodyExpr(n.Operand)
	case *ast.CallExpr:
		a.validateDeferStmtBodyExpr(n.Func)
		for _, arg := range n.Args {
			a.validateDeferStmtBodyExpr(arg)
		}
	case *ast.FieldExpr:
		a.validateDeferStmtBodyExpr(n.Object)
	case *ast.IndexExpr:
		a.validateDeferStmtBodyExpr(n.Object)
		a.validateDeferStmtBodyExpr(n.Index)
	case *ast.SliceExpr:
		a.validateDeferStmtBodyExpr(n.Object)
		a.validateDeferStmtBodyExpr(n.Start)
		a.validateDeferStmtBodyExpr(n.End)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			a.validateDeferStmtBodyExpr(elem)
		}
	case *ast.CastExpr:
		a.validateDeferStmtBodyExpr(n.Operand)
	case *ast.TernaryExpr:
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Cond)
		a.validateDeferStmtBodyExpr(n.Alt)
	case *ast.AddrOfExpr:
		a.validateDeferStmtBodyExpr(n.Operand)
	case *ast.MoveExpr:
		a.validateDeferStmtBodyExpr(n.Operand)
	case *ast.SpecializeExpr:
		a.validateDeferStmtBodyExpr(n.Operand)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			a.validateDeferStmtBodyExpr(arg)
		}
	case *ast.ParenExpr:
		a.validateDeferStmtBodyExpr(n.Inner)
	case *ast.RaiseExpr:
		a.errorf(n.Pos(), "defer body cannot raise from the enclosing function")
		a.validateDeferStmtBodyExpr(n.Error)
	case *ast.TryExpr:
		if n.Fallback == nil {
			a.errorf(n.Pos(), "defer body cannot use try propagation without an else fallback")
		}
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Fallback)
	case *ast.UnwrapElseExpr:
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Fallback)
	case *ast.AllocExpr:
		a.validateDeferStmtBodyExpr(n.Owner)
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.CanExpr:
		a.validateDeferStmtBodyExpr(n.Expr)
	case *ast.MatchExpr:
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Store)
		for _, arm := range n.Arms {
			a.validateDeferStmtBody(arm.Body)
		}
	case *ast.VisitExpr:
		a.validateDeferStmtBodyExpr(n.Value)
		for _, arm := range n.Arms {
			a.validateDeferStmtBody(arm.Body)
		}
	case *ast.FoldExpr:
		a.validateDeferStmtBodyExpr(n.Value)
		for _, arm := range n.Arms {
			a.validateDeferStmtBody(arm.Body)
		}
	}
}

func (a *Analyzer) clonePackedVariantViewBindings() map[*Symbol]*PackedVariantViewType {
	if a.currentPackedVariantViews == nil {
		return nil
	}
	cloned := make(map[*Symbol]*PackedVariantViewType, len(a.currentPackedVariantViews))
	for sym, viewType := range a.currentPackedVariantViews {
		cloned[sym] = viewType
	}
	return cloned
}

func unwrapPackedVariantViewExpr(expr ast.Expr) ast.Expr {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return unwrapPackedVariantViewExpr(n.Inner)
	case *ast.CastExpr:
		return unwrapPackedVariantViewExpr(n.Operand)
	case *ast.CanExpr:
		return unwrapPackedVariantViewExpr(n.Expr)
	default:
		return expr
	}
}

func directValueBindingAliasRoot(scope *Scope, expr ast.Expr) *Symbol {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return directValueBindingAliasRoot(scope, n.Inner)
	case *ast.Ident:
		if scope == nil {
			return nil
		}
		sym, ok := scope.Lookup(n.Name)
		if !ok || sym == nil {
			return nil
		}
		if root := symbolAliasRoot(sym); root != nil {
			return root
		}
		return sym
	default:
		return nil
	}
}

func (a *Analyzer) aliasRootRefinementKey(scope *Scope, expr ast.Expr) (string, bool) {
	if scope == nil {
		return "", false
	}
	ident, ok := unwrapPackedVariantViewExpr(expr).(*ast.Ident)
	if !ok || ident == nil {
		return "", false
	}
	sym, ok := scope.Lookup(ident.Name)
	if !ok || sym == nil {
		return "", false
	}
	root := symbolAliasRoot(sym)
	if root == nil || root == sym || root.Name == "" {
		return "", false
	}
	return root.Name, true
}

func (a *Analyzer) bindRefinedExprType(scope *Scope, expr ast.Expr, refined Type) {
	if scope == nil || refined == nil {
		return
	}
	key, ok := exprRefinementKey(unwrapPackedVariantViewExpr(expr))
	if aliasKey, aliasOK := a.aliasRootRefinementKey(scope, expr); aliasOK {
		key = aliasKey
		ok = true
	}
	if !ok {
		return
	}
	scope.Refinements[key] = refined
}

func (a *Analyzer) bindMatchedPackedVariantView(expr ast.Expr, viewType *PackedVariantViewType) {
	if viewType == nil || a.currentScope == nil {
		return
	}
	a.bindRefinedExprType(a.currentScope, expr, viewType)
	ident, ok := unwrapPackedVariantViewExpr(expr).(*ast.Ident)
	if !ok {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return
	}
	if a.currentPackedVariantViews == nil {
		a.currentPackedVariantViews = map[*Symbol]*PackedVariantViewType{}
	}
	a.currentPackedVariantViews[sym] = viewType
}

func (a *Analyzer) lookupRefinedPackedVariantView(expr ast.Expr) (*PackedVariantViewType, bool) {
	if a.currentPackedVariantViews == nil || a.currentScope == nil {
		return nil, false
	}
	ident, ok := unwrapPackedVariantViewExpr(expr).(*ast.Ident)
	if !ok {
		return nil, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if ok && sym != nil {
		if viewType, ok := a.currentPackedVariantViews[sym]; ok && viewType != nil {
			return viewType, true
		}
	}
	for candidate, viewType := range a.currentPackedVariantViews {
		if candidate != nil && candidate.Name == ident.Name && viewType != nil {
			return viewType, true
		}
	}
	return nil, false
}

func (a *Analyzer) clearPackedVariantViewExpr(expr ast.Expr) {
	if a.currentScope == nil {
		return
	}
	if key, ok := exprRefinementKey(unwrapPackedVariantViewExpr(expr)); ok {
		for scope := a.currentScope; scope != nil; scope = scope.Parent {
			if refined, exists := scope.Refinements[key]; exists {
				if _, isPackedView := refined.(*PackedVariantViewType); isPackedView {
					delete(scope.Refinements, key)
				}
				break
			}
		}
	}
	if a.currentPackedVariantViews == nil {
		return
	}
	ident, ok := unwrapPackedVariantViewExpr(expr).(*ast.Ident)
	if !ok {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return
	}
	delete(a.currentPackedVariantViews, sym)
}

type moveBindResolvedField struct {
	Name    string
	Type    Type
	Mutable bool
}

type moveBindResolvedVariantField struct {
	Path     []string
	Type     Type
	BindName string
	Position lexer.Pos
}

func (a *Analyzer) resolvedStructFields(actual Type) ([]moveBindResolvedField, bool) {
	var (
		base     *StructType
		bindings map[string]Type
	)
	actual = StripAggregateStateType(actual)
	switch tt := actual.(type) {
	case *StructType:
		base = tt
	case *TreeBlockType:
		if tt == nil {
			return nil, false
		}
		fields := make([]moveBindResolvedField, 0, len(tt.Fields))
		for _, field := range tt.Decl.Fields {
			resolved, ok := tt.Fields[field.Name]
			if !ok {
				continue
			}
			fields = append(fields, moveBindResolvedField{Name: field.Name, Type: resolved.Type, Mutable: resolved.Mutable})
		}
		return fields, true
	case *TreeStructType:
		if tt == nil {
			return nil, false
		}
		fields := make([]moveBindResolvedField, 0, len(tt.Fields))
		for _, field := range tt.Decl.Fields {
			resolved, ok := tt.Fields[field.Name]
			if !ok {
				continue
			}
			fields = append(fields, moveBindResolvedField{Name: field.Name, Type: resolved.Type, Mutable: resolved.Mutable})
		}
		return fields, true
	case *TupleType:
		if tt == nil {
			return nil, false
		}
		fields := make([]moveBindResolvedField, 0, len(tt.Fields))
		for _, field := range tt.Fields {
			fields = append(fields, moveBindResolvedField{Name: field.Name, Type: field.Type, Mutable: false})
		}
		return fields, true
	case *GenericInstanceType:
		structBase, ok := tt.Base.(*StructType)
		if !ok {
			return nil, false
		}
		base = structBase
		bindings = genericBindingsForStructInstance(base, tt.Args)
	default:
		return nil, false
	}
	if base == nil {
		return nil, false
	}
	if base.Decl == nil {
		return nil, false
	}
	fields := make([]moveBindResolvedField, 0, len(base.Decl.Fields))
	for i := 0; i < len(base.Decl.Fields); i++ {
		fieldDecl := base.Decl.Fields[i]
		field, ok := base.Fields[fieldDecl.Name]
		if !ok {
			continue
		}
		fieldType := field.Type
		if len(bindings) != 0 {
			fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
		}
		fields = append(fields, moveBindResolvedField{Name: fieldDecl.Name, Type: fieldType, Mutable: field.Mutable})
	}
	return fields, true
}

func (a *Analyzer) resolveMoveBindStructPattern(pattern *ast.MoveBindStructPattern, actual Type) ([]moveBindResolvedField, bool) {
	if pattern == nil {
		return nil, false
	}
	actual = StripAggregateStateType(actual)
	fields, ok := a.resolvedStructFields(actual)
	if !ok {
		a.errorf(pattern.Pos(), "move-as pattern %q requires a concrete struct value, got %s", pattern.TypeName, actual.String())
		return nil, false
	}
	switch tt := actual.(type) {
	case *StructType:
		if tt.Name != pattern.TypeName {
			a.errorf(pattern.Pos(), "move-as pattern expects struct %q, got %q", pattern.TypeName, tt.Name)
			return nil, false
		}
		if tt.Decl == nil {
			a.errorf(pattern.Pos(), "move-as destructuring is not supported for builtin struct %q", tt.Name)
			return nil, false
		}
	case *GenericInstanceType:
		base, _ := tt.Base.(*StructType)
		if base == nil || base.Name != pattern.TypeName {
			got := actual.String()
			if base != nil {
				got = base.Name
			}
			a.errorf(pattern.Pos(), "move-as pattern expects struct %q, got %q", pattern.TypeName, got)
			return nil, false
		}
		if base.Decl == nil {
			a.errorf(pattern.Pos(), "move-as destructuring is not supported for builtin struct %q", base.Name)
			return nil, false
		}
	case *TupleType:
		a.errorf(pattern.Pos(), "move-as pattern %q requires a concrete struct value, got %s", pattern.TypeName, actual.String())
		return nil, false
	}
	if len(pattern.Args) != len(fields) {
		a.errorf(pattern.Pos(), "move-as pattern %q expects %d bindings, got %d", pattern.TypeName, len(fields), len(pattern.Args))
	}
	limit := len(pattern.Args)
	if len(fields) < limit {
		limit = len(fields)
	}
	return fields[:limit], true
}

func (a *Analyzer) resolveMatchStructPattern(pattern *ast.MatchStructPattern, actual Type) ([]moveBindResolvedField, []*ast.MatchPatternArg, bool) {
	if pattern == nil {
		return nil, nil, false
	}
	actual = StripAggregateStateType(actual)
	fields, ok := a.resolvedStructFields(actual)
	if !ok {
		a.errorf(pattern.Pos(), "struct pattern %q requires a concrete struct value, got %s", pattern.TypeName, actual.String())
		return nil, nil, false
	}
	switch tt := actual.(type) {
	case *StructType:
		if tt.Name != pattern.TypeName {
			a.errorf(pattern.Pos(), "struct pattern expects struct %q, got %q", pattern.TypeName, tt.Name)
			return nil, nil, false
		}
		if tt.Decl == nil {
			a.errorf(pattern.Pos(), "struct pattern destructuring is not supported for builtin struct %q", tt.Name)
			return nil, nil, false
		}
	case *GenericInstanceType:
		base, _ := tt.Base.(*StructType)
		if base == nil || base.Name != pattern.TypeName {
			got := actual.String()
			if base != nil {
				got = base.Name
			}
			a.errorf(pattern.Pos(), "struct pattern expects struct %q, got %q", pattern.TypeName, got)
			return nil, nil, false
		}
		if base.Decl == nil {
			a.errorf(pattern.Pos(), "struct pattern destructuring is not supported for builtin struct %q", base.Name)
			return nil, nil, false
		}
	case *TreeBlockType:
		if tt.Name != pattern.TypeName {
			a.errorf(pattern.Pos(), "struct pattern expects struct %q, got %q", pattern.TypeName, tt.Name)
			return nil, nil, false
		}
	case *TreeStructType:
		if tt.Name != pattern.TypeName {
			a.errorf(pattern.Pos(), "struct pattern expects struct %q, got %q", pattern.TypeName, tt.Name)
			return nil, nil, false
		}
	case *TupleType:
		a.errorf(pattern.Pos(), "struct pattern %q requires a concrete struct value, got %s", pattern.TypeName, actual.String())
		return nil, nil, false
	}
	ordered := make([]*ast.MatchPatternArg, len(fields))
	fieldIndexes := make(map[string]int, len(fields))
	for i := range fields {
		fieldIndexes[fields[i].Name] = i
	}
	seen := map[int]lexer.Pos{}
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		if arg.Name == "" {
			a.errorf(arg.Position, "struct pattern fields must use named field matches")
			continue
		}
		index, ok := fieldIndexes[arg.Name]
		if !ok {
			a.errorf(arg.Position, "struct %q has no field %q", pattern.TypeName, arg.Name)
			continue
		}
		if prev, exists := seen[index]; exists {
			a.errorf(arg.Position, "struct %q field %q is matched more than once (first at %s:%d:%d)", pattern.TypeName, arg.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[index] = arg.Position
		ordered[index] = arg
	}
	pattern.ResolvedArgs = ordered
	return fields, ordered, true
}

func moveBindVariantAsMatchPattern(pattern *ast.MoveBindVariantPattern) *ast.MatchVariantPattern {
	if pattern == nil {
		return nil
	}
	return &ast.MatchVariantPattern{Position: pattern.Position, EnumName: pattern.EnumName, Variant: pattern.Variant, Args: append([]ast.MatchPatternArg(nil), pattern.Args...)}
}

func moveBindVariantFieldKey(variant *EnumVariant, index int) string {
	if variant == nil {
		return ""
	}
	if label := variant.PayloadLabel(index); label != "" {
		return label
	}
	return fmt.Sprintf("#%d", index)
}

func (a *Analyzer) resolveMoveBindVariantPattern(stmt *ast.MoveBindStmt, pattern *ast.MoveBindVariantPattern, actual Type) ([]moveBindResolvedVariantField, *EnumType, *regionRefState, bool) {
	if pattern == nil {
		return nil, nil, nil, false
	}
	enumType, _, ok := resolveMatchableEnumType(actual)
	if !ok {
		a.errorf(pattern.Pos(), "move-as variant pattern %q.%q requires an enum value, got %s", pattern.EnumName, pattern.Variant, actual.String())
		return nil, nil, nil, false
	}
	if enumType.Name != pattern.EnumName {
		a.errorf(pattern.Pos(), "move-as pattern expects enum %q, got %q", pattern.EnumName, enumType.Name)
		return nil, nil, nil, false
	}
	var storeState *regionRefState
	if enumType.Packed {
		a.validateMoveBindStore(pattern.Pos(), stmt.Value, actual, enumType, stmt.Store)
		if stmt.Store != nil {
			if state, ok := a.regionRefStateForExpr(stmt.Store); ok {
				cloned := cloneRegionRefState(state)
				storeState = &cloned
			}
		}
	} else if stmt.Store != nil {
		a.errorf(stmt.Store.Pos(), "ordinary enum move-as over %q does not take an in-store clause", enumType.Name)
		return nil, nil, nil, false
	}
	variant, ok := enumType.Variant(pattern.Variant)
	if !ok {
		a.errorf(pattern.Pos(), "enum %q has no variant %q", enumType.Name, pattern.Variant)
		return nil, nil, nil, false
	}
	orderedArgs := a.resolveMatchPatternArgs(moveBindVariantAsMatchPattern(pattern), variant, enumType.Name+"."+variant.Name, false)
	fields := make([]moveBindResolvedVariantField, 0, len(orderedArgs))
	for i, arg := range orderedArgs {
		if arg == nil {
			continue
		}
		fields = a.collectMoveBindVariantBindings(arg.Pattern, variant.Payload[i], []string{moveBindVariantFieldKey(variant, i)}, fields)
	}
	return fields, enumType, storeState, true
}

func (a *Analyzer) resolveMoveBindTreeVariantPattern(pattern *ast.MoveBindVariantPattern, treeType *TreeCategoryType) ([]moveBindResolvedVariantField, bool) {
	if pattern == nil || treeType == nil {
		return nil, false
	}
	if treeType.Name != pattern.EnumName {
		a.errorf(pattern.Pos(), "move-as pattern expects tree category %q, got %q", pattern.EnumName, treeType.Name)
		return nil, false
	}
	variant, ok := treeType.Variant(pattern.Variant)
	if !ok {
		a.errorf(pattern.Pos(), "tree category %q has no variant %q", treeType.Name, pattern.Variant)
		return nil, false
	}
	orderedArgs := a.resolveMatchPatternArgs(moveBindVariantAsMatchPattern(pattern), variant, treeType.Name+"."+variant.Name, false)
	fields := make([]moveBindResolvedVariantField, 0, len(orderedArgs))
	for i, arg := range orderedArgs {
		if arg == nil {
			continue
		}
		fields = a.collectMoveBindVariantBindings(arg.Pattern, variant.Payload[i], []string{moveBindVariantFieldKey(variant, i)}, fields)
	}
	return fields, true
}

func (a *Analyzer) collectMoveBindVariantBindings(pattern ast.MatchPattern, expected Type, path []string, fields []moveBindResolvedVariantField) []moveBindResolvedVariantField {
	if pattern == nil {
		return fields
	}
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return fields
	case *ast.MatchBindPattern:
		return append(fields, moveBindResolvedVariantField{Path: append([]string(nil), path...), Type: expected, BindName: p.Name, Position: p.Pos()})
	case *ast.MatchStringLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), &ast.StringLit{Position: p.Position, Value: p.Value}, expected, "move-as nested pattern")
		return fields
	case *ast.MatchLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), p.Value, expected, "move-as nested pattern")
		return fields
	case *ast.MatchStructPattern:
		resolvedFields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return fields
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			childPath := append(append([]string(nil), path...), resolvedFields[i].Name)
			fields = a.collectMoveBindVariantBindings(arg.Pattern, resolvedFields[i].Type, childPath, fields)
		}
		return fields
	case *ast.MatchVariantPattern:
		enumType, _, enumOK := resolveMatchableEnumType(expected)
		if enumOK && enumType != nil {
			if p.EnumName != enumType.Name {
				a.errorf(p.Pos(), "nested move-as pattern expects enum %q, got %q", enumType.Name, p.EnumName)
				return fields
			}
			variant, ok := enumType.Variant(p.Variant)
			if !ok {
				a.errorf(p.Pos(), "enum %q has no variant %q", enumType.Name, p.Variant)
				return fields
			}
			orderedArgs := a.resolveMatchPatternArgs(p, variant, enumType.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				childPath := append(append([]string(nil), path...), moveBindVariantFieldKey(variant, i))
				fields = a.collectMoveBindVariantBindings(arg.Pattern, variant.Payload[i], childPath, fields)
			}
			return fields
		}
		treeType, _, treeOK := resolveMatchableTreeCategoryType(expected)
		if !treeOK || treeType == nil {
			a.errorf(p.Pos(), "nested move-as pattern %q requires an enum or tree-category payload, got %s", p.EnumName+"."+p.Variant, expected.String())
			return fields
		}
		if p.EnumName != treeType.Name {
			a.errorf(p.Pos(), "nested move-as pattern expects tree category %q, got %q", treeType.Name, p.EnumName)
			return fields
		}
		variant, ok := treeType.Variant(p.Variant)
		if !ok {
			a.errorf(p.Pos(), "tree category %q has no variant %q", treeType.Name, p.Variant)
			return fields
		}
		orderedArgs := a.resolveMatchPatternArgs(p, variant, treeType.Name+"."+variant.Name, true)
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			childPath := append(append([]string(nil), path...), moveBindVariantFieldKey(variant, i))
			fields = a.collectMoveBindVariantBindings(arg.Pattern, variant.Payload[i], childPath, fields)
		}
		return fields
	default:
		a.errorf(pattern.Pos(), "unsupported move-as nested pattern %T", pattern)
		return fields
	}
}

func (a *Analyzer) resolveVariantPayloadValueExpr(value ast.Expr, typeName string, variantName string, key string) (ast.Expr, bool) {
	if value == nil || typeName == "" || variantName == "" || key == "" {
		return nil, false
	}
	switch n := value.(type) {
	case *ast.ParenExpr:
		return a.resolveVariantPayloadValueExpr(n.Inner, typeName, variantName, key)
	case *ast.CastExpr:
		return a.resolveVariantPayloadValueExpr(n.Operand, typeName, variantName, key)
	case *ast.MoveExpr:
		return a.resolveVariantPayloadValueExpr(n.Operand, typeName, variantName, key)
	case *ast.CanExpr:
		return a.resolveVariantPayloadValueExpr(n.Expr, typeName, variantName, key)
	case *ast.AllocExpr:
		return a.resolveVariantPayloadValueExpr(n.Value, typeName, variantName, key)
	case *ast.FieldExpr:
		resolved, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field)
		if !ok {
			return nil, false
		}
		return a.resolveVariantPayloadValueExpr(resolved, typeName, variantName, key)
	case *ast.Ident:
		if a.currentScope == nil {
			return nil, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym.Kind != SymbolLocal || sym.Mutable {
			return nil, false
		}
		decl, ok := sym.Node.(*ast.VarDeclStmt)
		if !ok || decl.Value == nil {
			return nil, false
		}
		return a.resolveVariantPayloadValueExpr(decl.Value, typeName, variantName, key)
	case *ast.CallExpr:
		enumType, variant, ok := a.enumConstructorCall(n)
		if ok && enumType != nil && variant != nil {
			if enumType.Name != typeName || variant.Name != variantName {
				return nil, false
			}
			var orderedArgs []ast.Expr
			if enumType.Packed {
				var commonArgs map[string]ast.Expr
				orderedArgs, commonArgs, ok = a.resolvePackedEnumConstructorArgs(n, enumType, variant)
				_ = commonArgs
			} else {
				orderedArgs, ok = a.resolveEnumConstructorArgs(n, enumType, variant)
			}
			if !ok {
				return nil, false
			}
			for i, arg := range orderedArgs {
				if moveBindVariantFieldKey(variant, i) == key {
					return arg, true
				}
			}
			return nil, false
		}
		treeType, variant, ok := a.treeConstructorCall(n)
		if !ok || treeType == nil || variant == nil {
			return nil, false
		}
		if treeType.Name != typeName || variant.Name != variantName {
			return nil, false
		}
		orderedArgs, _, ok := a.resolveTreeConstructorArgs(n, treeType, variant)
		if !ok {
			return nil, false
		}
		for i, arg := range orderedArgs {
			if moveBindVariantFieldKey(variant, i) == key {
				return arg, true
			}
		}
		return nil, false
	default:
		return nil, false
	}
}

func (a *Analyzer) resolveMoveBindVariantPayloadValueExpr(value ast.Expr, pattern *ast.MoveBindVariantPattern, key string) (ast.Expr, bool) {
	if pattern == nil {
		return nil, false
	}
	return a.resolveVariantPayloadValueExpr(value, pattern.EnumName, pattern.Variant, key)
}

func (a *Analyzer) resolveMatchVariantPayloadValueExprPath(value ast.Expr, pattern *ast.MatchVariantPattern, path []string) (ast.Expr, bool) {
	if pattern == nil || len(path) == 0 {
		return nil, false
	}
	current, ok := a.resolveMatchVariantPayloadValueExpr(value, pattern, path[0])
	if !ok {
		return nil, false
	}
	if len(path) == 1 {
		return current, true
	}
	base, _, ok := a.lookupVisibleType(pattern.EnumName)
	if !ok {
		return nil, false
	}
	switch variantBase := base.(type) {
	case *EnumType:
		if variantBase == nil {
			return nil, false
		}
		variant, ok := variantBase.Variant(pattern.Variant)
		if !ok || variant == nil {
			return nil, false
		}
		orderedArgs := a.resolveMatchPatternArgs(pattern, variant, variantBase.Name+"."+variant.Name, true)
		for i, arg := range orderedArgs {
			if arg == nil || moveBindVariantFieldKey(variant, i) != path[0] {
				continue
			}
			nested, ok := arg.Pattern.(*ast.MatchVariantPattern)
			if !ok {
				return nil, false
			}
			return a.resolveMatchVariantPayloadValueExprPath(current, nested, path[1:])
		}
	case *TreeCategoryType:
		if variantBase == nil {
			return nil, false
		}
		variant, ok := variantBase.Variant(pattern.Variant)
		if !ok || variant == nil {
			return nil, false
		}
		orderedArgs := a.resolveMatchPatternArgs(pattern, variant, variantBase.Name+"."+variant.Name, true)
		for i, arg := range orderedArgs {
			if arg == nil || moveBindVariantFieldKey(variant, i) != path[0] {
				continue
			}
			nested, ok := arg.Pattern.(*ast.MatchVariantPattern)
			if !ok {
				return nil, false
			}
			return a.resolveMatchVariantPayloadValueExprPath(current, nested, path[1:])
		}
	}
	return nil, false
}

func (a *Analyzer) resolveMoveBindVariantPayloadValueExprPath(value ast.Expr, pattern *ast.MoveBindVariantPattern, path []string) (ast.Expr, bool) {
	if pattern == nil {
		return nil, false
	}
	return a.resolveMatchVariantPayloadValueExprPath(value, moveBindVariantAsMatchPattern(pattern), path)
}

func projectRegionFieldPathState(state regionRefState, path []string) (regionRefState, bool) {
	current := cloneRegionRefState(state)
	for _, field := range path {
		next, ok := projectRegionFieldState(current, field)
		if !ok {
			return regionRefState{}, false
		}
		current = next
	}
	return current, true
}

func projectBorrowedOwnerRefFieldPathState(state borrowedOwnerRefState, path []string) (borrowedOwnerRefState, bool) {
	current := cloneBorrowedOwnerRefState(state)
	for _, field := range path {
		next, ok := projectBorrowedOwnerRefFieldState(current, field)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		current = next
	}
	return current, true
}

func (a *Analyzer) bindResolvedMoveBindVariantFields(payloads []moveBindResolvedVariantField, value ast.Expr, pattern *ast.MoveBindVariantPattern, node ast.Node, valueState regionRefState, hasValueState bool, borrowedOwnerState borrowedOwnerRefState, hasBorrowedOwnerState bool, packedStoreState *regionRefState) {
	if a == nil || pattern == nil {
		return
	}
	for _, payload := range payloads {
		if payload.BindName == "" || payload.BindName == "_" {
			continue
		}
		sym := &Symbol{Name: payload.BindName, Kind: SymbolLocal, Type: payload.Type, Node: node, Mutable: false}
		a.defineLocal(sym, payload.Position)
		if valueExpr, ok := a.resolveMoveBindVariantPayloadValueExprPath(value, pattern, payload.Path); ok {
			a.recordValueBinding(sym, valueExpr)
			a.recordFunctionValueBinding(sym, valueExpr)
			a.recordImmutableSymbolOptimizationFacts(sym, valueExpr)
		}
		if hasBorrowedOwnerState {
			if fieldState, ok := projectBorrowedOwnerRefFieldPathState(borrowedOwnerState, payload.Path); ok {
				a.currentBorrowedOwnerRefs[sym] = fieldState
			}
		}
		if !hasValueState && packedStoreState == nil {
			continue
		}
		if hasValueState {
			if fieldState, ok := projectRegionFieldPathState(valueState, payload.Path); ok {
				a.recordResolvedRegionRefBinding(sym, fieldState)
				continue
			}
			if a.typeCanContainRegionRefs(payload.Type, map[string]bool{}) {
				a.recordResolvedRegionRefBinding(sym, valueState)
				continue
			}
		}
		if packedStoreState != nil && a.typeCanContainRegionRefs(payload.Type, map[string]bool{}) {
			a.recordResolvedRegionRefBinding(sym, *packedStoreState)
		}
	}
}

func (a *Analyzer) resolveMatchVariantPayloadValueExpr(value ast.Expr, pattern *ast.MatchVariantPattern, key string) (ast.Expr, bool) {
	if pattern == nil {
		return nil, false
	}
	return a.resolveVariantPayloadValueExpr(value, pattern.EnumName, pattern.Variant, key)
}

func (a *Analyzer) canInferPackedStoreFromValue(valueExpr ast.Expr, actual Type, enumType *EnumType) bool {
	if enumType == nil || !enumType.Packed || actual == nil {
		return false
	}
	actual = StripAggregateStateType(actual)
	if viewType, ok := actual.(*PackedVariantViewType); ok && viewType != nil && viewType.Enum == enumType {
		return true
	}
	if _, _, ok := a.resolvePackedNodeStoreRoot(valueExpr, enumType); ok {
		return true
	}
	if a.valueHasExactPackedStoreIndexRoot(valueExpr, enumType) {
		return true
	}
	return false
}

func (a *Analyzer) valueHasExactPackedStoreIndexRoot(expr ast.Expr, enumType *EnumType) bool {
	if a == nil || expr == nil || enumType == nil || !enumType.Packed {
		return false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.valueHasExactPackedStoreIndexRoot(n.Inner, enumType)
	case *ast.CastExpr:
		return a.valueHasExactPackedStoreIndexRoot(n.Operand, enumType)
	case *ast.MoveExpr:
		return a.valueHasExactPackedStoreIndexRoot(n.Operand, enumType)
	case *ast.CanExpr:
		return a.valueHasExactPackedStoreIndexRoot(n.Expr, enumType)
	case *ast.Ident:
		if a.currentScope == nil {
			return false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil {
			return false
		}
		if a.currentValueBindings != nil {
			if valueExpr, ok := a.currentValueBindings[sym]; ok && valueExpr != nil {
				return a.valueHasExactPackedStoreIndexRoot(valueExpr, enumType)
			}
			if root := symbolAliasRoot(sym); root != nil && root != sym {
				if valueExpr, ok := a.currentValueBindings[root]; ok && valueExpr != nil {
					return a.valueHasExactPackedStoreIndexRoot(valueExpr, enumType)
				}
			}
		}
		return false
	case *ast.FieldExpr:
		if resolved, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok && resolved != nil {
			return a.valueHasExactPackedStoreIndexRoot(resolved, enumType)
		}
		return false
	case *ast.IndexExpr:
		return a.exprIsExactPackedStoreRoot(n.Object, enumType)
	default:
		return false
	}
}

func (a *Analyzer) exprIsExactPackedStoreRoot(expr ast.Expr, enumType *EnumType) bool {
	if a == nil || expr == nil || enumType == nil || !enumType.Packed {
		return false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.exprIsExactPackedStoreRoot(n.Inner, enumType)
	case *ast.CastExpr:
		return a.exprIsExactPackedStoreRoot(n.Operand, enumType)
	case *ast.MoveExpr:
		return a.exprIsExactPackedStoreRoot(n.Operand, enumType)
	case *ast.FieldExpr:
		if resolved, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok && resolved != nil {
			return a.exprIsExactPackedStoreRoot(resolved, enumType)
		}
	}
	actual := a.exprTypes[expr]
	if actual == nil {
		actual = a.analyzeExpr(expr)
	}
	storeType, ok := StripAggregateStateType(actual).(*PackedEnumStoreType)
	return ok && storeType != nil && storeType.Enum == enumType
}

func (a *Analyzer) validateMoveBindStore(pos lexer.Pos, valueExpr ast.Expr, actual Type, enumType *EnumType, storeExpr ast.Expr) {
	if enumType == nil {
		return
	}
	if !enumType.Packed {
		if storeExpr != nil {
			a.errorf(storeExpr.Pos(), "ordinary enum move-as over %q does not take an in-store clause", enumType.Name)
		}
		return
	}
	if storeExpr == nil {
		if a.canInferPackedStoreFromValue(valueExpr, actual, enumType) {
			return
		}
		if _, ok := a.lookupPackedStore(enumType); ok {
			return
		}
		a.errorf(pos, "packed enum move-as over %q requires an in %s clause", enumType.Name, packedEnumStoreTypeName(enumType.Name))
		return
	}
	storeType := a.analyzeExpr(storeExpr)
	packedStore, ok := storeType.(*PackedEnumStoreType)
	if !ok {
		a.errorf(storeExpr.Pos(), "packed enum move-as over %q requires store type %q, got %s", enumType.Name, packedEnumStoreTypeName(enumType.Name), storeType.String())
		return
	}
	if packedStore.Enum != enumType {
		a.errorf(storeExpr.Pos(), "packed enum move-as over %q requires store type %q, got %s", enumType.Name, packedEnumStoreTypeName(enumType.Name), storeType.String())
	}
}

func (a *Analyzer) analyzeCanStmt(stmt *ast.CanStmt) {
	refs := a.resolvePermissionRefs(stmt.Permissions, true)
	a.recordFunctionPermissionRefs(refs)
	a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
}

func (a *Analyzer) analyzePoolStmt(stmt *ast.PoolStmt) {
	poolCall := &ast.CallExpr{
		Position: stmt.Position,
		Func:     &ast.Ident{Position: stmt.Position, Name: "pool_new"},
		Args:     []ast.Expr{stmt.Workers},
	}
	poolType := a.analyzeExpr(poolCall)
	savedScope := a.currentScope
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedRegionRefs := a.currentRegionRefs
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	savedPools := a.currentPoolScopes
	a.currentScope = NewScope(savedScope)
	a.currentRegions = a.cloneRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentRegionRefs = a.cloneRegionRefStates()
	a.currentPackedVariantViews = a.clonePackedVariantViewBindings()
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	a.currentPoolScopes = append(append([]poolScopeState(nil), savedPools...), poolScopeState{Name: stmt.Name})
	a.defineLocal(&Symbol{Name: stmt.Name, Kind: SymbolLocal, Type: poolType, Node: stmt, Mutable: false}, stmt.Pos())
	for _, inner := range stmt.Body {
		a.analyzeStmt(inner)
	}
	a.currentScope = savedScope
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
	a.currentPoolScopes = savedPools
}

func (a *Analyzer) analyzeForStmt(stmt *ast.ForStmt) {
	startType := a.analyzeExpr(stmt.Start)
	endType := a.analyzeExpr(stmt.End)
	loopType := CommonNumericType(startType, endType)
	if !IsIntegralType(loopType) {
		a.errorf(stmt.Pos(), "for loop range requires integral bounds, got %s and %s", startType.String(), endType.String())
		loopType = invalidType
	}
	if stmt.Step != nil {
		stepType := a.analyzeExpr(stmt.Step)
		loopType = CommonNumericType(loopType, stepType)
		if !IsIntegralType(stepType) || !IsIntegralType(loopType) {
			a.errorf(stmt.Step.Pos(), "for loop range step must be integral, got %s", stepType.String())
			loopType = invalidType
		}
		if value, ok := a.evalConstExpr(stmt.Step); ok && value.Kind == ConstInt && value.Int == 0 {
			a.errorf(stmt.Step.Pos(), "for loop range step cannot be zero")
		}
	}
	if stmt.Op != lexer.TOKEN_RANGE && stmt.Op != lexer.TOKEN_RANGE_LT && stmt.Op != lexer.TOKEN_RANGE_GT {
		a.errorf(stmt.Pos(), "for loop uses unsupported range operator %s", lexer.TokenName(stmt.Op))
	}

	loopScope := NewScope(a.currentScope)
	loopSym := &Symbol{Name: stmt.Name, Kind: SymbolLocal, Type: loopType, Node: stmt, Mutable: false}
	a.defineLocalInScope(loopScope, loopSym, stmt.Pos())

	mergedAffine := a.cloneAffineValueStates()
	mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
	mergedFunctionValues := a.cloneFunctionValueBindings()
	mergedSpecializedValueTypes := a.cloneSpecializedValueTypeBindings()
	bodySnapshot := a.analyzeBlockWithAffineClone(stmt.Body, loopScope)
	if !blockDefinitelyExits(stmt.Body) {
		mergedAffine = mergeAffineValueStates(mergedAffine, bodySnapshot.Affine)
		mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, bodySnapshot.BorrowedOwnerRefs)
		mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, bodySnapshot.FunctionValues)
		mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, bodySnapshot.SpecializedValueTypes)
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
}

type iterLoopSourceInfo struct {
	ItemType        Type
	AllowRef        bool
	AllowMutableRef bool
	ItemFacts       OptimizationFacts
	HasItemFacts    bool
}

func (a *Analyzer) inferIterLoopItemOptimizationFacts(sourceExpr ast.Expr, sourceType Type) (OptimizationFacts, bool) {
	if a == nil || sourceExpr == nil || sourceType == nil {
		return OptimizationFacts{}, false
	}
	switch tt := sourceType.(type) {
	case *GenericInstanceType:
		if _, ok := ChunksExactViewItemType(tt); ok {
			return a.inferChunksExactItemOptimizationFacts(&ast.IndexExpr{
				Position: sourceExpr.Pos(),
				Object:   sourceExpr,
				Index:    &ast.Ident{Position: sourceExpr.Pos(), Name: "__iter_index"},
			})
		}
	}
	return OptimizationFacts{}, false
}

func (a *Analyzer) resolveIterLoopSourceInfo(sourceExpr ast.Expr, sourceType Type) (iterLoopSourceInfo, bool) {
	if sourceType == nil {
		return iterLoopSourceInfo{}, false
	}
	facts, hasFacts := a.exprFacts[sourceExpr]
	readOnly := hasFacts && facts.ReadOnly
	switch tt := sourceType.(type) {
	case *ArrayType:
		if isStringArrayType(tt) {
			return iterLoopSourceInfo{ItemType: a.namedTypes["char"]}, true
		}
		return iterLoopSourceInfo{ItemType: tt.Elem, AllowRef: true, AllowMutableRef: true}, true
	case *DArrayType:
		return iterLoopSourceInfo{ItemType: tt.Elem, AllowRef: true, AllowMutableRef: !readOnly}, true
	case *ViewType:
		return iterLoopSourceInfo{ItemType: tt.Elem, AllowRef: true, AllowMutableRef: !readOnly}, true
	case *DArrayViewType:
		if tt.SurfaceName != "" && tt.SurfaceName != "dview" {
			return iterLoopSourceInfo{}, false
		}
		return iterLoopSourceInfo{ItemType: tt.Elem, AllowRef: true, AllowMutableRef: !readOnly}, true
	case *DStrType:
		return iterLoopSourceInfo{ItemType: a.namedTypes["char"]}, true
	case *SViewType:
		return iterLoopSourceInfo{ItemType: a.namedTypes["char"]}, true
	case *GenericInstanceType:
		if itemType, ok := EnumerateViewItemType(tt); ok {
			return iterLoopSourceInfo{ItemType: itemType}, true
		}
		if itemType, ok := TreeChildrenItemType(tt); ok {
			return iterLoopSourceInfo{ItemType: itemType}, true
		}
		if itemType, ok := ChunksExactViewItemType(tt); ok {
			info := iterLoopSourceInfo{ItemType: itemType}
			if itemFacts, ok := a.inferIterLoopItemOptimizationFacts(sourceExpr, sourceType); ok {
				info.ItemFacts = itemFacts
				info.HasItemFacts = true
			}
			return info, true
		}
		return iterLoopSourceInfo{}, false
	case *StructType:
		if isRuntimeStringViewType(tt) {
			return iterLoopSourceInfo{ItemType: a.namedTypes["char"]}, true
		}
		return iterLoopSourceInfo{}, false
	case *RefType:
		if tt.State != RefStateNonNull {
			a.errorf(sourceExpr.Pos(), "iterable for loop requires a proven non-null reference source, got %s", sourceType.String())
			return iterLoopSourceInfo{}, false
		}
		info, ok := a.resolveIterLoopSourceInfo(sourceExpr, tt.Elem)
		if !ok {
			return iterLoopSourceInfo{}, false
		}
		if !tt.Mutable {
			info.AllowMutableRef = false
		}
		return info, true
	default:
		return iterLoopSourceInfo{}, false
	}
}

func iterLoopRefType(itemType Type, mutable bool) Type {
	return &RefType{Elem: itemType, Mutable: mutable, State: RefStateNonNull, Storage: RefStorageAny}
}

func (a *Analyzer) bindIterLoopPattern(scope *Scope, pattern ast.MoveBindPattern, mode ast.IterBindMode, itemType Type, itemFacts OptimizationFacts, hasItemFacts bool) bool {
	if scope == nil || pattern == nil {
		return false
	}
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() {
		a.currentScope = savedScope
	}()
	bindingTypeFor := func(pos lexer.Pos, fieldName string, fieldType Type, fieldMutable bool) Type {
		if mode == ast.IterBindValue {
			return fieldType
		}
		if mode == ast.IterBindMutableRef && !fieldMutable {
			a.errorf(pos, "for mutable ref destructuring requires mutable field %q", fieldName)
			return invalidType
		}
		return iterLoopRefType(fieldType, mode == ast.IterBindMutableRef)
	}
	switch p := pattern.(type) {
	case *ast.MoveBindNamePattern:
		if p.Name == "_" {
			return true
		}
		sym := &Symbol{Name: p.Name, Kind: SymbolLocal, Type: bindingTypeFor(p.Pos(), p.Name, itemType, true), Node: p, Mutable: false}
		a.defineLocal(sym, p.Pos())
		if mode == ast.IterBindValue && hasItemFacts && a.symbolFacts != nil {
			a.symbolFacts[sym] = itemFacts
		}
		return true
	case *ast.MoveBindStructPattern:
		fields, ok := a.resolveMoveBindStructPattern(p, itemType)
		if !ok {
			return false
		}
		for i, arg := range p.Args {
			if i >= len(fields) || arg.Name == "_" {
				continue
			}
			sym := &Symbol{Name: arg.Name, Kind: SymbolLocal, Type: bindingTypeFor(arg.Position, fields[i].Name, fields[i].Type, fields[i].Mutable), Node: p, Mutable: false}
			a.defineLocal(sym, arg.Position)
		}
		return true
	case *ast.MoveBindTuplePattern:
		tupleType, ok := StripAggregateStateType(itemType).(*TupleType)
		if !ok || tupleType == nil {
			a.errorf(p.Pos(), "iterable for tuple pattern requires a tuple item, got %s", itemType.String())
			return false
		}
		if len(p.Args) != len(tupleType.Fields) {
			a.errorf(p.Pos(), "iterable tuple pattern expects %d bindings, got %d", len(tupleType.Fields), len(p.Args))
		}
		limit := len(p.Args)
		if len(tupleType.Fields) < limit {
			limit = len(tupleType.Fields)
		}
		for i := 0; i < limit; i++ {
			arg := p.Args[i]
			if arg.Name == "_" {
				continue
			}
			fieldName := tupleType.Fields[i].Name
			if fieldName == "" {
				fieldName = fmt.Sprintf("_%d", i)
			}
			sym := &Symbol{Name: arg.Name, Kind: SymbolLocal, Type: bindingTypeFor(arg.Position, fieldName, tupleType.Fields[i].Type, false), Node: p, Mutable: false}
			a.defineLocal(sym, arg.Position)
		}
		return true
	case *ast.MoveBindVariantPattern:
		a.errorf(p.Pos(), "iterable for loop pattern must be irrefutable; variant patterns are not supported here")
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported iterable for pattern %T", pattern)
		return false
	}
}

func (a *Analyzer) analyzeIterForStmt(stmt *ast.IterForStmt) {
	sourceType := a.analyzeExpr(stmt.Source)
	info, ok := a.resolveIterLoopSourceInfo(stmt.Source, sourceType)
	if !ok {
		a.errorf(stmt.Source.Pos(), "iterable for loop currently requires an array, dynamic array, view, string-like iterable, ChunksExactView, enumerate(source), or children(node), got %s", sourceType.String())
		info.ItemType = invalidType
	}
	if stmt.Mode == ast.IterBindValue && a.containsAffineHandleValues(info.ItemType, map[string]bool{}) {
		a.errorf(stmt.Pos(), "for value iteration does not support affine element type %s; use ref or mutable ref", info.ItemType.String())
	}
	if stmt.Mode != ast.IterBindValue && a.containsAffineHandleValues(info.ItemType, map[string]bool{}) && !isBorrowableAffineOwnerType(info.ItemType) {
		a.errorf(stmt.Pos(), "references to values containing affine handles are not supported; got %s&", info.ItemType.String())
	}
	switch stmt.Mode {
	case ast.IterBindRef:
		if !info.AllowRef {
			a.errorf(stmt.Pos(), "for ref requires an addressable array-like iterable, got %s", sourceType.String())
		}
	case ast.IterBindMutableRef:
		if !info.AllowMutableRef {
			a.errorf(stmt.Pos(), "for mutable ref requires a writable addressable array-like iterable, got %s", sourceType.String())
		}
	}

	loopScope := NewScope(a.currentScope)
	a.bindIterLoopPattern(loopScope, stmt.Pattern, stmt.Mode, info.ItemType, info.ItemFacts, info.HasItemFacts)

	mergedAffine := a.cloneAffineValueStates()
	mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
	mergedFunctionValues := a.cloneFunctionValueBindings()
	mergedSpecializedValueTypes := a.cloneSpecializedValueTypeBindings()
	bodySnapshot := a.analyzeBlockWithAffineClone(stmt.Body, loopScope)
	if !blockDefinitelyExits(stmt.Body) {
		mergedAffine = mergeAffineValueStates(mergedAffine, bodySnapshot.Affine)
		mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, bodySnapshot.BorrowedOwnerRefs)
		mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, bodySnapshot.FunctionValues)
		mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, bodySnapshot.SpecializedValueTypes)
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
}

func (a *Analyzer) analyzeParallelForStmt(stmt *ast.ParallelForStmt) {
	sourceType := a.analyzeExpr(stmt.Source)
	itemType, ok := parallelForItemType(sourceType)
	if !ok {
		a.errorf(stmt.Source.Pos(), "parallel for requires a frozen packed store or readonly dense view, got %s", sourceType.String())
		itemType = invalidType
	}
	if ok {
		if storeType, isStore := sourceType.(*PackedEnumStoreType); isStore {
			if !IsFrozenPackedEnumStoreType(storeType) {
				a.errorf(stmt.Source.Pos(), "parallel for requires a frozen packed store or readonly dense view, got %s", sourceType.String())
			}
		} else {
			facts, hasFacts := a.exprFacts[stmt.Source]
			if !hasFacts || !facts.ReadOnly || !facts.Contiguous || !facts.UnitStride || !facts.HasExactExtent() {
				a.errorf(stmt.Source.Pos(), "parallel for requires a readonly contiguous exact-extent view, got %s", sourceType.String())
			}
		}
	}
	if len(a.currentPoolScopes) == 0 {
		a.errorf(stmt.Pos(), "parallel for requires an enclosing pool scope")
	}
	a.validateThreadTransferArg("parallel for", stmt.Source, sourceType)

	loopScope := NewScope(a.currentScope)
	loopSym := &Symbol{Name: stmt.Name, Kind: SymbolLocal, Type: itemType, Node: stmt, Mutable: false}
	savedScope := a.currentScope
	a.currentScope = loopScope
	a.defineLocal(loopSym, stmt.Pos())
	if stmt.IndexName != "" {
		indexSym := &Symbol{Name: stmt.IndexName, Kind: SymbolLocal, Type: a.namedTypes["usize"], Node: stmt, Mutable: false}
		a.defineLocal(indexSym, stmt.Pos())
	}
	a.currentScope = savedScope
	a.analyzeBlockWithAffineClone(stmt.Body, loopScope)

	rootLocals := []string{stmt.Name}
	if stmt.IndexName != "" {
		rootLocals = append(rootLocals, stmt.IndexName)
	}
	captureCollector := newParallelForCaptureCollector(a, a.currentScope, rootLocals...)
	captureCollector.collectStmts(stmt.Body)
	for _, name := range captureCollector.captureOrder {
		sym, ok := a.currentScope.Lookup(name)
		if !ok || !parallelForCapturableSymbolKind(sym.Kind) {
			continue
		}
		if !a.parallelForCaptureTypeAllowed(sym.Type, map[string]bool{}) {
			a.errorf(stmt.Pos(), "parallel for capture %q has unsupported shared type %s", name, sym.Type.String())
			continue
		}
		if bindingExpr, ok := a.currentValueBindings[sym]; ok && bindingExpr != nil {
			a.validateThreadTransferArg("parallel for", bindingExpr, sym.Type)
		} else {
			a.validateThreadTransferArg("parallel for", &ast.Ident{Position: stmt.Position, Name: name}, sym.Type)
		}
	}
	for _, msg := range captureCollector.errors {
		a.errorf(stmt.Pos(), "%s", msg)
	}
	if a.parallelForInfo == nil {
		a.parallelForInfo = map[*ast.ParallelForStmt]*ParallelForInfo{}
	}
	a.parallelForInfo[stmt] = &ParallelForInfo{
		SourceType: sourceType,
		ItemType:   itemType,
		Captures:   append([]string(nil), captureCollector.captureOrder...),
	}
}

func parallelForItemType(t Type) (Type, bool) {
	switch tt := t.(type) {
	case *PackedEnumStoreType:
		if IsFrozenPackedEnumStoreType(tt) {
			return tt.Enum, true
		}
	case *DArrayViewType:
		return tt.Elem, true
	case *GenericInstanceType:
		if itemType, ok := ChunksExactViewItemType(tt); ok {
			return itemType, true
		}
	case *SViewType, *DStrType:
		return builtinCharType(), true
	}
	return nil, false
}

func parallelForCapturableSymbolKind(kind SymbolKind) bool {
	switch kind {
	case SymbolParam, SymbolLocal, SymbolRegion, SymbolRegionMark:
		return true
	default:
		return false
	}
}

func (a *Analyzer) parallelForCaptureTypeAllowed(t Type, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	key := fmt.Sprintf("%T:%s", t, t.String())
	if seen[key] {
		return true
	}
	seen[key] = true
	switch tt := t.(type) {
	case *BuiltinType, *NullType, *NeverType, *TypeParamType, *FuncType, *ErrorSetType:
		return true
	case *ConstEnumType:
		return a.parallelForCaptureTypeAllowed(tt.Storage, seen)
	case *OptionalType:
		return a.parallelForCaptureTypeAllowed(tt.Value, seen)
	case *ErrorUnionType:
		return a.parallelForCaptureTypeAllowed(tt.Value, seen)
	case *ArrayType:
		return a.parallelForCaptureTypeAllowed(tt.Elem, seen)
	case *EnumType:
		if tt.Packed {
			return true
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if !a.parallelForCaptureTypeAllowed(payload, seen) {
					return false
				}
			}
		}
		for _, field := range tt.Common {
			if !a.parallelForCaptureTypeAllowed(field.Type, seen) {
				return false
			}
		}
		return true
	case *StructType:
		for _, field := range tt.Fields {
			if !a.parallelForCaptureTypeAllowed(field.Type, seen) {
				return false
			}
		}
		return true
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		if !ok {
			return false
		}
		bindings := genericBindingsForStructInstance(base, tt.Args)
		for _, field := range base.Fields {
			fieldType := field.Type
			if len(bindings) != 0 {
				fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
			}
			if !a.parallelForCaptureTypeAllowed(fieldType, seen) {
				return false
			}
		}
		return true
	case *PackedEnumStoreType:
		return IsFrozenPackedEnumStoreType(tt)
	case *DArrayViewType:
		return tt.SurfaceName == "packedview" && a.parallelForCaptureTypeAllowed(tt.Elem, seen)
	default:
		return false
	}
}

type parallelForCaptureCollector struct {
	analyzer     *Analyzer
	outerScope   *Scope
	rootLocals   map[string]bool
	captureOrder []string
	captureSeen  map[string]bool
	errors       []string
}

func newParallelForCaptureCollector(analyzer *Analyzer, outerScope *Scope, loopNames ...string) *parallelForCaptureCollector {
	rootLocals := map[string]bool{}
	for _, name := range loopNames {
		if name != "" {
			rootLocals[name] = true
		}
	}
	return &parallelForCaptureCollector{
		analyzer:    analyzer,
		outerScope:  outerScope,
		rootLocals:  rootLocals,
		captureSeen: map[string]bool{},
	}
}

func cloneParallelForLocals(locals map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(locals))
	for name, ok := range locals {
		cloned[name] = ok
	}
	return cloned
}

func (c *parallelForCaptureCollector) addError(msg string) {
	for _, existing := range c.errors {
		if existing == msg {
			return
		}
	}
	c.errors = append(c.errors, msg)
}

func (c *parallelForCaptureCollector) noteCapture(name string) {
	if c.captureSeen[name] {
		return
	}
	c.captureSeen[name] = true
	c.captureOrder = append(c.captureOrder, name)
}

func (c *parallelForCaptureCollector) collectStmts(stmts []ast.Stmt) {
	locals := cloneParallelForLocals(c.rootLocals)
	for _, stmt := range stmts {
		c.collectStmt(stmt, locals)
	}
}

func (c *parallelForCaptureCollector) collectStmt(stmt ast.Stmt, locals map[string]bool) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		if n.Value != nil {
			c.collectExpr(n.Value, locals)
		}
		locals[n.Name] = true
	case *ast.MoveBindStmt:
		c.collectExpr(n.Value, locals)
		if n.Store != nil {
			c.collectExpr(n.Store, locals)
		}
		for _, name := range parallelForMoveBindNames(n.Pattern) {
			locals[name] = true
		}
	case *ast.OpenStmt:
		c.collectExpr(n.Value, locals)
		if n.Store != nil {
			c.collectExpr(n.Store, locals)
		}
		inner := cloneParallelForLocals(locals)
		for _, name := range parallelForMatchPatternNames(n.Pattern.Args) {
			inner[name] = true
		}
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, inner)
		}
	case *ast.ViewStmt:
		c.collectExpr(n.Value, locals)
		if n.Store != nil {
			c.collectExpr(n.Store, locals)
		}
		inner := cloneParallelForLocals(locals)
		if n.Pattern != nil && n.Pattern.Name != "" {
			inner[n.Pattern.Name] = true
		}
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, inner)
		}
	case *ast.DeferStmt:
		bodyLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.AssignStmt:
		c.collectAssignmentTarget(n.Target, locals)
		c.collectExpr(n.Value, locals)
	case *ast.AugAssignStmt:
		c.collectAssignmentTarget(n.Target, locals)
		c.collectExpr(n.Value, locals)
	case *ast.AsRefAssignStmt:
		c.collectAssignmentTarget(n.Target, locals)
		c.collectExpr(n.Value, locals)
	case *ast.ReturnStmt:
		c.addError("parallel for body cannot return from the enclosing function")
		if n.Value != nil {
			c.collectExpr(n.Value, locals)
		}
	case *ast.IfStmt:
		c.collectExpr(n.Cond, locals)
		thenLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Then {
			c.collectStmt(innerStmt, thenLocals)
		}
		for _, elif := range n.Elifs {
			c.collectExpr(elif.Cond, locals)
			elifLocals := cloneParallelForLocals(locals)
			for _, innerStmt := range elif.Body {
				c.collectStmt(innerStmt, elifLocals)
			}
		}
		elseLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Else {
			c.collectStmt(innerStmt, elseLocals)
		}
	case *ast.WhileStmt:
		c.collectExpr(n.Cond, locals)
		bodyLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.ParallelForStmt:
		c.addError("parallel for body cannot nest another parallel for")
	case *ast.MatchStmt:
		c.collectExpr(n.Value, locals)
		if n.Store != nil {
			c.collectExpr(n.Store, locals)
		}
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			for _, name := range parallelForMatchArmPatternNames(arm.Pattern) {
				armLocals[name] = true
			}
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
			}
		}
	case *ast.InStoreStmt:
		c.collectExpr(n.Store, locals)
		bodyLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.CanStmt:
		bodyLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.PoolStmt:
		c.collectExpr(n.Workers, locals)
		bodyLocals := cloneParallelForLocals(locals)
		bodyLocals[n.Name] = true
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.LockStmt:
		c.collectExpr(n.Mutex, locals)
		bodyLocals := cloneParallelForLocals(locals)
		bodyLocals[n.GuardName] = true
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.PanicStmt:
		if n.Message != nil {
			c.collectExpr(n.Message, locals)
		}
	case *ast.ExprStmt:
		c.collectExpr(n.Expr, locals)
	case *ast.StaticIfStmt:
		for _, active := range c.analyzer.activeStmtBranch(n) {
			c.collectStmt(active, cloneParallelForLocals(locals))
		}
	case *ast.StaticErrorStmt:
		c.collectExpr(n.Message, locals)
	case *ast.DiscardStmt:
		c.collectExpr(n.Value, locals)
	case *ast.RegionStmt:
		if n.Capacity != nil {
			c.collectExpr(n.Capacity, locals)
		}
		locals[n.Name] = true
	case *ast.DestroyStmt:
		if !locals[n.Name] {
			c.addError(fmt.Sprintf("parallel for body cannot destroy outer region %q", n.Name))
		}
	case *ast.MarkStmt:
		if !locals[n.RegionName] {
			c.addError(fmt.Sprintf("parallel for body cannot mark outer region %q", n.RegionName))
		}
		locals[n.Name] = true
	case *ast.RestoreStmt:
		if !locals[n.RegionName] {
			c.addError(fmt.Sprintf("parallel for body cannot restore outer region %q", n.RegionName))
		}
		if !locals[n.MarkName] {
			c.addError(fmt.Sprintf("parallel for body cannot restore from outer checkpoint %q", n.MarkName))
		}
	case *ast.ResetStmt:
		if !locals[n.Name] {
			c.addError(fmt.Sprintf("parallel for body cannot reset outer region %q", n.Name))
		}
	}
}

type deferCaptureCollector struct {
	analyzer     *Analyzer
	outerScope   *Scope
	rootLocals   map[string]bool
	captureOrder []string
	captureSeen  map[string]bool
}

func newDeferCaptureCollector(analyzer *Analyzer, outerScope *Scope) *deferCaptureCollector {
	return &deferCaptureCollector{
		analyzer:    analyzer,
		outerScope:  outerScope,
		rootLocals:  map[string]bool{},
		captureSeen: map[string]bool{},
	}
}

func (c *deferCaptureCollector) noteCapture(name string) {
	if c.captureSeen[name] {
		return
	}
	c.captureSeen[name] = true
	c.captureOrder = append(c.captureOrder, name)
}

func (c *deferCaptureCollector) collectStmts(stmts []ast.Stmt) {
	locals := cloneParallelForLocals(c.rootLocals)
	for _, stmt := range stmts {
		c.collectStmt(stmt, locals)
	}
}

func (c *deferCaptureCollector) collectStmt(stmt ast.Stmt, locals map[string]bool) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		if n.Value != nil {
			c.collectExpr(n.Value, locals)
		}
		locals[n.Name] = true
	case *ast.MoveBindStmt:
		c.collectExpr(n.Value, locals)
		if n.Store != nil {
			c.collectExpr(n.Store, locals)
		}
		for _, name := range parallelForMoveBindNames(n.Pattern) {
			locals[name] = true
		}
	case *ast.OpenStmt:
		c.collectExpr(n.Value, locals)
		if n.Store != nil {
			c.collectExpr(n.Store, locals)
		}
		inner := cloneParallelForLocals(locals)
		for _, name := range parallelForMatchPatternNames(n.Pattern.Args) {
			inner[name] = true
		}
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, inner)
		}
	case *ast.ViewStmt:
		c.collectExpr(n.Value, locals)
		if n.Store != nil {
			c.collectExpr(n.Store, locals)
		}
		inner := cloneParallelForLocals(locals)
		if n.Pattern != nil && n.Pattern.Name != "" {
			inner[n.Pattern.Name] = true
		}
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, inner)
		}
	case *ast.DeferStmt:
		bodyLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.AssignStmt:
		c.collectExpr(n.Target, locals)
		c.collectExpr(n.Value, locals)
	case *ast.AugAssignStmt:
		c.collectExpr(n.Target, locals)
		c.collectExpr(n.Value, locals)
	case *ast.AsRefAssignStmt:
		c.collectExpr(n.Target, locals)
		c.collectExpr(n.Value, locals)
	case *ast.ReturnStmt:
		c.collectExpr(n.Value, locals)
	case *ast.IfStmt:
		c.collectExpr(n.Cond, locals)
		thenLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Then {
			c.collectStmt(innerStmt, thenLocals)
		}
		for _, elif := range n.Elifs {
			c.collectExpr(elif.Cond, locals)
			elifLocals := cloneParallelForLocals(locals)
			for _, innerStmt := range elif.Body {
				c.collectStmt(innerStmt, elifLocals)
			}
		}
		elseLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Else {
			c.collectStmt(innerStmt, elseLocals)
		}
	case *ast.WhileStmt:
		c.collectExpr(n.Cond, locals)
		bodyLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.ForStmt:
		c.collectExpr(n.Start, locals)
		c.collectExpr(n.End, locals)
		c.collectExpr(n.Step, locals)
		bodyLocals := cloneParallelForLocals(locals)
		bodyLocals[n.Name] = true
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.IterForStmt:
		c.collectExpr(n.Source, locals)
		bodyLocals := cloneParallelForLocals(locals)
		for _, name := range parallelForMoveBindNames(n.Pattern) {
			bodyLocals[name] = true
		}
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.ParallelForStmt:
		c.collectExpr(n.Source, locals)
		bodyLocals := cloneParallelForLocals(locals)
		bodyLocals[n.Name] = true
		if n.IndexName != "" {
			bodyLocals[n.IndexName] = true
		}
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.MatchStmt:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Store, locals)
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			for _, name := range parallelForMatchArmPatternNames(arm.Pattern) {
				armLocals[name] = true
			}
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
			}
		}
	case *ast.InStoreStmt:
		c.collectExpr(n.Store, locals)
		bodyLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.CanStmt:
		bodyLocals := cloneParallelForLocals(locals)
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.PoolStmt:
		c.collectExpr(n.Workers, locals)
		bodyLocals := cloneParallelForLocals(locals)
		bodyLocals[n.Name] = true
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.LockStmt:
		c.collectExpr(n.Mutex, locals)
		bodyLocals := cloneParallelForLocals(locals)
		bodyLocals[n.GuardName] = true
		for _, innerStmt := range n.Body {
			c.collectStmt(innerStmt, bodyLocals)
		}
	case *ast.PanicStmt:
		c.collectExpr(n.Message, locals)
	case *ast.ExprStmt:
		c.collectExpr(n.Expr, locals)
	case *ast.StaticIfStmt:
		for _, active := range c.analyzer.activeStmtBranch(n) {
			c.collectStmt(active, cloneParallelForLocals(locals))
		}
	case *ast.StaticErrorStmt:
		c.collectExpr(n.Message, locals)
	case *ast.DiscardStmt:
		c.collectExpr(n.Value, locals)
	case *ast.RegionStmt:
		c.collectExpr(n.Capacity, locals)
		locals[n.Name] = true
	case *ast.MarkStmt:
		locals[n.Name] = true
	case *ast.RestoreStmt, *ast.ResetStmt, *ast.DestroyStmt, *ast.PassStmt:
		return
	}
}

func (c *deferCaptureCollector) collectExpr(expr ast.Expr, locals map[string]bool) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.Ident:
		if locals[n.Name] {
			return
		}
		sym, ok := c.outerScope.Lookup(n.Name)
		if ok && parallelForCapturableSymbolKind(sym.Kind) {
			c.noteCapture(n.Name)
		}
	case *ast.BinaryExpr:
		c.collectExpr(n.Left, locals)
		c.collectExpr(n.Right, locals)
	case *ast.UnaryExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.CallExpr:
		c.collectExpr(n.Func, locals)
		for _, arg := range n.Args {
			c.collectExpr(arg, locals)
		}
	case *ast.FieldExpr:
		c.collectExpr(n.Object, locals)
	case *ast.IndexExpr:
		c.collectExpr(n.Object, locals)
		c.collectExpr(n.Index, locals)
	case *ast.SliceExpr:
		c.collectExpr(n.Object, locals)
		c.collectExpr(n.Start, locals)
		c.collectExpr(n.End, locals)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			c.collectExpr(elem, locals)
		}
	case *ast.CastExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.TernaryExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Cond, locals)
		c.collectExpr(n.Alt, locals)
	case *ast.AddrOfExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.MoveExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.SpecializeExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			c.collectExpr(arg, locals)
		}
	case *ast.ParenExpr:
		c.collectExpr(n.Inner, locals)
	case *ast.RaiseExpr:
		c.collectExpr(n.Error, locals)
	case *ast.TryExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Fallback, locals)
	case *ast.UnwrapElseExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Fallback, locals)
	case *ast.AllocExpr:
		c.collectExpr(n.Owner, locals)
		c.collectExpr(n.Value, locals)
	case *ast.CanExpr:
		c.collectExpr(n.Expr, locals)
	case *ast.MatchExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Store, locals)
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			for _, name := range parallelForMatchArmPatternNames(arm.Pattern) {
				armLocals[name] = true
			}
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
			}
		}
	case *ast.VisitExpr:
		c.collectExpr(n.Value, locals)
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			appendVisitArmLocals(armLocals, arm)
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
			}
		}
	case *ast.FoldExpr:
		c.collectExpr(n.Value, locals)
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			appendVisitArmLocals(armLocals, arm)
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
			}
		}
	}
}

func appendVisitArmLocals(locals map[string]bool, arm ast.VisitArm) {
	if locals == nil {
		return
	}
	if arm.BindName != "" {
		locals[arm.BindName] = true
	}
	if arm.ChildResultsName != "" {
		locals[arm.ChildResultsName] = true
	}
	for _, binding := range arm.ChildBindings {
		if binding.BindName != "" {
			locals[binding.BindName] = true
		}
	}
}

func (c *parallelForCaptureCollector) collectAssignmentTarget(expr ast.Expr, locals map[string]bool) {
	c.collectExpr(expr, locals)
	if root, ok := parallelForAssignmentRoot(expr); ok && !locals[root] {
		c.addError(fmt.Sprintf("parallel for body cannot mutate outer binding %q", root))
	}
}

func (c *parallelForCaptureCollector) collectExpr(expr ast.Expr, locals map[string]bool) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.Ident:
		if locals[n.Name] {
			return
		}
		sym, ok := c.outerScope.Lookup(n.Name)
		if ok && parallelForCapturableSymbolKind(sym.Kind) {
			c.noteCapture(n.Name)
		}
	case *ast.BinaryExpr:
		c.collectExpr(n.Left, locals)
		c.collectExpr(n.Right, locals)
	case *ast.UnaryExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.CallExpr:
		c.collectExpr(n.Func, locals)
		for _, arg := range n.Args {
			c.collectExpr(arg, locals)
		}
	case *ast.FieldExpr:
		c.collectExpr(n.Object, locals)
	case *ast.IndexExpr:
		c.collectExpr(n.Object, locals)
		c.collectExpr(n.Index, locals)
	case *ast.SliceExpr:
		c.collectExpr(n.Object, locals)
		c.collectExpr(n.Start, locals)
		c.collectExpr(n.End, locals)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			c.collectExpr(elem, locals)
		}
	case *ast.CastExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.TernaryExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Cond, locals)
		c.collectExpr(n.Alt, locals)
	case *ast.AddrOfExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.MoveExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.SpecializeExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			c.collectExpr(arg, locals)
		}
	case *ast.ParenExpr:
		c.collectExpr(n.Inner, locals)
	case *ast.RaiseExpr:
		c.collectExpr(n.Error, locals)
	case *ast.TryExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Fallback, locals)
	case *ast.UnwrapElseExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Fallback, locals)
	case *ast.AllocExpr:
		c.collectExpr(n.Owner, locals)
		c.collectExpr(n.Value, locals)
	case *ast.CanExpr:
		c.collectExpr(n.Expr, locals)
	case *ast.MatchExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Store, locals)
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			for _, name := range parallelForMatchArmPatternNames(arm.Pattern) {
				armLocals[name] = true
			}
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
			}
		}
	}
}

func parallelForMatchPatternNames(args []ast.MatchPatternArg) []string {
	var out []string
	for _, arg := range args {
		out = append(out, parallelForMatchArmPatternNames(arg.Pattern)...)
	}
	return out
}

func parallelForMatchArmPatternNames(pattern ast.MatchPattern) []string {
	switch p := pattern.(type) {
	case *ast.MatchBindPattern:
		return []string{p.Name}
	case *ast.MatchStructPattern:
		return parallelForMatchPatternNames(p.Args)
	case *ast.MatchVariantPattern:
		return parallelForMatchPatternNames(p.Args)
	default:
		return nil
	}
}

func parallelForMoveBindNames(pattern ast.MoveBindPattern) []string {
	switch p := pattern.(type) {
	case *ast.MoveBindNamePattern:
		return []string{p.Name}
	case *ast.MoveBindStructPattern:
		var out []string
		for _, arg := range p.Args {
			if arg.Name != "" {
				out = append(out, arg.Name)
			}
		}
		return out
	case *ast.MoveBindTuplePattern:
		var out []string
		for _, arg := range p.Args {
			if arg.Name != "" {
				out = append(out, arg.Name)
			}
		}
		return out
	case *ast.MoveBindVariantPattern:
		return parallelForMatchPatternNames(p.Args)
	default:
		return nil
	}
}

func parallelForAssignmentRoot(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.FieldExpr:
		return parallelForAssignmentRoot(n.Object)
	case *ast.IndexExpr:
		return parallelForAssignmentRoot(n.Object)
	case *ast.ParenExpr:
		return parallelForAssignmentRoot(n.Inner)
	case *ast.CastExpr:
		return parallelForAssignmentRoot(n.Operand)
	default:
		return "", false
	}
}

func (a *Analyzer) analyzeLockStmt(stmt *ast.LockStmt) {
	lockCall := &ast.CallExpr{
		Position: stmt.Position,
		Func:     &ast.Ident{Position: stmt.Position, Name: "mutex_lock"},
		Args: []ast.Expr{&ast.CastExpr{
			Position: stmt.Mutex.Pos(),
			Operand: &ast.AddrOfExpr{
				Position: stmt.Mutex.Pos(),
				Operand:  stmt.Mutex,
			},
			Target: &ast.RefType{
				Position: stmt.Mutex.Pos(),
				Elem:     &ast.NamedType{Position: stmt.Mutex.Pos(), Name: "Mutex"},
				State:    ast.RefStateNonNull,
				Storage:  ast.RefStorageAny,
				Explicit: true,
			},
		}},
	}
	guardType := a.analyzeExpr(lockCall)
	savedScope := a.currentScope
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedRegionRefs := a.currentRegionRefs
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	guardSym := &Symbol{Name: stmt.GuardName, Kind: SymbolLocal, Type: guardType, Node: stmt, Mutable: true}
	a.currentScope = NewScope(savedScope)
	a.currentRegions = a.cloneRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentRegionRefs = a.cloneRegionRefStates()
	a.currentPackedVariantViews = a.clonePackedVariantViewBindings()
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	a.defineLocal(guardSym, stmt.Pos())
	for _, inner := range stmt.Body {
		a.analyzeStmt(inner)
	}
	if a.currentAffineValues != nil {
		for key := range a.currentAffineValues {
			if key.Root == guardSym {
				delete(a.currentAffineValues, key)
			}
		}
	}
	a.currentScope = savedScope
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
}

func (a *Analyzer) analyzeInStoreStmt(stmt *ast.InStoreStmt) {
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	savedPackedVariantViews := a.currentPackedVariantViews
	savedTreeAllocOwner := a.currentTreeAllocOwner
	if owner, _, ok := a.classifyTreeAllocOwnerExpr(stmt.Store); ok {
		a.currentTreeAllocOwner = owner
		a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
		a.currentTreeAllocOwner = savedTreeAllocOwner
		a.currentPackedVariantViews = savedPackedVariantViews
		a.currentPackedStores = savedPackedStores
		a.currentPackedStoreResolutions = savedPackedStoreResolutions
		return
	}
	storeType := a.exprTypes[stmt.Store]
	if storeType == nil {
		storeType = a.analyzeExpr(stmt.Store)
	}
	if treeStore, ok := storeType.(*TreeStoreType); ok {
		a.currentTreeAllocOwner = treeAllocOwnerBinding{Kind: treeAllocOwnerStore, StoreFamily: treeStore.Family}
		a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
		a.currentTreeAllocOwner = savedTreeAllocOwner
		a.currentPackedVariantViews = savedPackedVariantViews
		a.currentPackedStores = savedPackedStores
		a.currentPackedStoreResolutions = savedPackedStoreResolutions
		return
	}
	packedStore, ok := storeType.(*PackedEnumStoreType)
	if !ok {
		a.errorf(stmt.Store.Pos(), "in-block requires a tree store, packed enum store, perm, an Arena value, or an Arena reference, got %s", storeType.String())
		a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
		a.currentTreeAllocOwner = savedTreeAllocOwner
		a.currentPackedVariantViews = savedPackedVariantViews
		a.currentPackedStores = savedPackedStores
		a.currentPackedStoreResolutions = savedPackedStoreResolutions
		return
	}
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	if a.currentPackedStores == nil {
		a.currentPackedStores = map[string]*PackedEnumStoreType{}
	}
	a.currentPackedStores[packedStore.Enum.Name] = packedStore
	a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
	a.currentTreeAllocOwner = savedTreeAllocOwner
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
}

func (a *Analyzer) analyzeMarkStmt(stmt *ast.MarkStmt) {
	regionSym, state := a.lookupRegionState(stmt.RegionName)
	if regionSym == nil {
		a.errorf(stmt.Pos(), "undefined region %q", stmt.RegionName)
		return
	}
	if state.Destroyed {
		a.errorf(stmt.Pos(), "region %q has already been destroyed", stmt.RegionName)
		return
	}
	markType, ok := a.namedTypes["ArenaMark"]
	if !ok {
		a.errorf(stmt.Pos(), "missing builtin ArenaMark type for region checkpoints")
		markType = invalidType
	}
	markSym := &Symbol{Name: stmt.Name, Kind: SymbolRegionMark, Type: markType, Node: stmt, Mutable: false}
	a.defineLocal(markSym, stmt.Pos())
	state.Generation++
	a.currentRegions[regionSym] = state
	if a.currentRegionMarks == nil {
		a.currentRegionMarks = map[*Symbol]regionMarkState{}
	}
	a.currentRegionMarks[markSym] = regionMarkState{Region: regionSym, Generation: state.Generation, Valid: true}
}

func (a *Analyzer) analyzeRestoreStmt(stmt *ast.RestoreStmt) {
	regionSym, state := a.lookupRegionState(stmt.RegionName)
	if regionSym == nil {
		a.errorf(stmt.Pos(), "undefined region %q", stmt.RegionName)
		return
	}
	if state.Destroyed {
		a.errorf(stmt.Pos(), "region %q has already been destroyed", stmt.RegionName)
		return
	}
	markSym, markState := a.lookupRegionMark(stmt.MarkName)
	if markSym == nil {
		a.errorf(stmt.Pos(), "undefined checkpoint %q", stmt.MarkName)
		return
	}
	if markState.Region != regionSym {
		a.errorf(stmt.Pos(), "checkpoint %q belongs to region %q, not %q", stmt.MarkName, markState.Region.Name, stmt.RegionName)
		return
	}
	if !markState.Valid {
		a.errorf(stmt.Pos(), "checkpoint %q is invalid after %s", stmt.MarkName, markState.InvalidatedBy)
		return
	}
	reason := fmt.Sprintf("restore of region %q from checkpoint %q", stmt.RegionName, stmt.MarkName)
	a.invalidateRegionRefs(regionSym, func(dep regionDependencyState) bool {
		return dep.Generation >= markState.Generation
	}, reason)
	a.invalidateRegionMarks(regionSym, func(other regionMarkState) bool {
		return other.Generation > markState.Generation
	}, reason)
	state.Generation = markState.Generation
	a.currentRegions[regionSym] = state
	if saved, ok := a.currentRegionMarks[markSym]; ok {
		saved.Valid = true
		saved.InvalidatedBy = ""
		a.currentRegionMarks[markSym] = saved
	}
}

func (a *Analyzer) analyzeResetStmt(stmt *ast.ResetStmt) {
	regionSym, state := a.lookupRegionState(stmt.Name)
	if regionSym == nil {
		a.errorf(stmt.Pos(), "undefined region %q", stmt.Name)
		return
	}
	if state.Destroyed {
		a.errorf(stmt.Pos(), "region %q has already been destroyed", stmt.Name)
		return
	}
	reason := fmt.Sprintf("reset of region %q", stmt.Name)
	a.invalidateRegionRefs(regionSym, func(regionDependencyState) bool { return true }, reason)
	a.invalidateRegionMarks(regionSym, func(regionMarkState) bool { return true }, reason)
	state.Generation = 0
	a.currentRegions[regionSym] = state
}

func (a *Analyzer) analyzeMatchStmt(stmt *ast.MatchStmt) {
	valueType := a.analyzeExpr(stmt.Value)
	enumType, _, ok := resolveMatchableEnumType(valueType)
	if ok {
		a.analyzeEnumMatchStmt(stmt, valueType, enumType)
		return
	}
	treeType, _, ok := resolveMatchableTreeCategoryType(valueType)
	if ok {
		a.analyzeTreeMatchStmt(stmt, treeType)
		return
	}
	if isStringMatchableType(valueType) {
		a.analyzeStringMatchStmt(stmt, valueType)
		return
	}
	if _, ok := a.resolvedStructFields(valueType); ok {
		a.analyzeStructMatchStmt(stmt, valueType)
		return
	}
	a.errorf(stmt.Pos(), "match requires an enum, tree-category, or string value, got %s", valueType.String())
	for _, arm := range stmt.Arms {
		a.analyzeBlockWithRegionClone(arm.Body, NewScope(a.currentScope))
	}
}

func (a *Analyzer) analyzeEnumMatchStmt(stmt *ast.MatchStmt, valueType Type, enumType *EnumType) {
	a.validateMatchStore(stmt.Pos(), stmt.Value, valueType, enumType, stmt.Store)
	baselineCloned := false
	var baselineAffine map[affineValueKey]affineValueState
	var baselineBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var baselineFunctionValues map[*Symbol]*FuncType
	var baselineSpecializedValueTypes map[*Symbol]Type
	cloneBaseline := func() {
		if baselineCloned {
			return
		}
		baselineAffine = a.cloneAffineValueStates()
		baselineBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
		baselineFunctionValues = a.cloneFunctionValueBindings()
		baselineSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
		baselineCloned = true
	}
	var mergedAffine map[affineValueKey]affineValueState
	var mergedBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var mergedFunctionValues map[*Symbol]*FuncType
	var mergedSpecializedValueTypes map[*Symbol]Type
	hasFallthrough := false
	priorPatterns := make([]ast.MatchPattern, 0, len(stmt.Arms))
	covered := map[string]bool{}
	hasWildcard := false
	for i, arm := range stmt.Arms {
		if a.matchPatternShadowedByPrevious(arm.Pattern, enumType, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		if a.analyzeTopLevelMatchPattern(arm.Pattern, enumType, stmt.Value, scope, i, len(stmt.Arms), covered) {
			hasWildcard = true
		}
		a.bindPackedVariantViewAliasForBody(arm.Pattern, enumType, stmt.Value, arm.Body, scope)
		armSnapshot := a.analyzeBlockWithAffineClone(arm.Body, scope)
		if !blockDefinitelyExits(arm.Body) {
			if !hasFallthrough {
				mergedAffine = armSnapshot.Affine
				mergedBorrowedOwnerRefs = armSnapshot.BorrowedOwnerRefs
				mergedFunctionValues = armSnapshot.FunctionValues
				mergedSpecializedValueTypes = armSnapshot.SpecializedValueTypes
				hasFallthrough = true
			} else {
				mergedAffine = mergeAffineValueStates(mergedAffine, armSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, armSnapshot.BorrowedOwnerRefs)
				mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, armSnapshot.FunctionValues)
				mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, armSnapshot.SpecializedValueTypes)
			}
		}
		priorPatterns = append(priorPatterns, arm.Pattern)
	}
	if !a.matchCoversAllVariants(enumType, covered, hasWildcard) {
		cloneBaseline()
		if !hasFallthrough {
			mergedAffine = baselineAffine
			mergedBorrowedOwnerRefs = baselineBorrowedOwnerRefs
			mergedFunctionValues = baselineFunctionValues
			mergedSpecializedValueTypes = baselineSpecializedValueTypes
		} else {
			mergedAffine = mergeAffineValueStates(mergedAffine, baselineAffine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, baselineBorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, baselineFunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, baselineSpecializedValueTypes)
		}
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	a.recordAffineDestructureConsumption(stmt.Value, valueType, "match over affine enum")
}

func (a *Analyzer) analyzeTreeMatchStmt(stmt *ast.MatchStmt, treeType *TreeCategoryType) {
	a.validateTreeMatchStore(treeType, stmt.Store)
	baselineCloned := false
	var baselineAffine map[affineValueKey]affineValueState
	var baselineBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var baselineFunctionValues map[*Symbol]*FuncType
	var baselineSpecializedValueTypes map[*Symbol]Type
	cloneBaseline := func() {
		if baselineCloned {
			return
		}
		baselineAffine = a.cloneAffineValueStates()
		baselineBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
		baselineFunctionValues = a.cloneFunctionValueBindings()
		baselineSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
		baselineCloned = true
	}
	var mergedAffine map[affineValueKey]affineValueState
	var mergedBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var mergedFunctionValues map[*Symbol]*FuncType
	var mergedSpecializedValueTypes map[*Symbol]Type
	hasFallthrough := false
	priorPatterns := make([]ast.MatchPattern, 0, len(stmt.Arms))
	covered := map[string]bool{}
	hasWildcard := false
	for i, arm := range stmt.Arms {
		if a.matchPatternShadowedByPrevious(arm.Pattern, treeType, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		if a.analyzeTopLevelTreeMatchPattern(arm.Pattern, treeType, stmt.Value, scope, i, len(stmt.Arms), covered) {
			hasWildcard = true
		}
		armSnapshot := a.analyzeBlockWithAffineClone(arm.Body, scope)
		if !blockDefinitelyExits(arm.Body) {
			if !hasFallthrough {
				mergedAffine = armSnapshot.Affine
				mergedBorrowedOwnerRefs = armSnapshot.BorrowedOwnerRefs
				mergedFunctionValues = armSnapshot.FunctionValues
				mergedSpecializedValueTypes = armSnapshot.SpecializedValueTypes
				hasFallthrough = true
			} else {
				mergedAffine = mergeAffineValueStates(mergedAffine, armSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, armSnapshot.BorrowedOwnerRefs)
				mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, armSnapshot.FunctionValues)
				mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, armSnapshot.SpecializedValueTypes)
			}
		}
		priorPatterns = append(priorPatterns, arm.Pattern)
	}
	if !a.matchCoversAllVariants(treeType, covered, hasWildcard) {
		cloneBaseline()
		if !hasFallthrough {
			mergedAffine = baselineAffine
			mergedBorrowedOwnerRefs = baselineBorrowedOwnerRefs
			mergedFunctionValues = baselineFunctionValues
			mergedSpecializedValueTypes = baselineSpecializedValueTypes
		} else {
			mergedAffine = mergeAffineValueStates(mergedAffine, baselineAffine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, baselineBorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, baselineFunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, baselineSpecializedValueTypes)
		}
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
}

func (a *Analyzer) analyzeMatchExpr(expr *ast.MatchExpr) Type {
	valueType := a.analyzeExpr(expr.Value)
	enumType, _, ok := resolveMatchableEnumType(valueType)
	if ok {
		return a.analyzeEnumMatchExpr(expr, valueType, enumType)
	}
	treeType, _, ok := resolveMatchableTreeCategoryType(valueType)
	if ok {
		return a.analyzeTreeMatchExpr(expr, treeType)
	}
	if isStringMatchableType(valueType) {
		return a.analyzeStringMatchExpr(expr, valueType)
	}
	if _, ok := a.resolvedStructFields(valueType); ok {
		return a.analyzeStructMatchExpr(expr, valueType)
	}
	a.errorf(expr.Pos(), "match requires an enum, tree-category, or string value, got %s", valueType.String())
	for _, arm := range expr.Arms {
		a.analyzeMatchExprArmBody(arm.Body, NewScope(a.currentScope))
	}
	return invalidType

}

type treeVisitRootKind int

const (
	treeVisitRootKindCategory treeVisitRootKind = iota
	treeVisitRootKindExact
	treeVisitRootKindFamily
)

type treeVisitRootInfo struct {
	Kind     treeVisitRootKind
	Family   *TreeType
	Category *TreeCategoryType
	Exact    Type
}

type treeVisitArmInfo struct {
	Arm      ast.VisitArm
	Key      string
	BindType Type
	Category *TreeCategoryType
	Variant  *EnumVariant
	Exact    Type
}

func resolveTreeVisitSourceType(actual Type) (Type, *TreeType, bool) {
	actual = StripAggregateStateType(actual)
	switch tt := actual.(type) {
	case *TreeCategoryType:
		if tt == nil || tt.Family == nil {
			return nil, nil, false
		}
		return tt, tt.Family, true
	case *TreeVariantViewType:
		if tt == nil || tt.Category == nil || tt.Category.Family == nil {
			return nil, nil, false
		}
		return tt, tt.Category.Family, true
	case *TreeBlockType:
		if tt == nil || tt.Family == nil {
			return nil, nil, false
		}
		return tt, tt.Family, true
	case *TreeStructType:
		if tt == nil || tt.Family == nil {
			return nil, nil, false
		}
		return tt, tt.Family, true
	case *TreeNodeType:
		if tt == nil || tt.Family == nil {
			return nil, nil, false
		}
		return tt, tt.Family, true
	default:
		return nil, nil, false
	}
}

func visitDomainKeys(root treeVisitRootInfo) []string {
	switch root.Kind {
	case treeVisitRootKindCategory:
		if root.Category == nil {
			return nil
		}
		keys := make([]string, 0, len(root.Category.Variants))
		for _, variant := range root.Category.Variants {
			keys = append(keys, root.Category.Name+"."+variant.Name)
		}
		return keys
	case treeVisitRootKindExact:
		if root.Exact == nil {
			return nil
		}
		return []string{root.Exact.String()}
	case treeVisitRootKindFamily:
		if root.Family == nil {
			return nil
		}
		keys := make([]string, 0)
		memberNames := make([]string, 0, len(root.Family.MemberTypes))
		for name := range root.Family.MemberTypes {
			if name == "Node" || name == "Store" {
				continue
			}
			memberNames = append(memberNames, name)
		}
		sort.Strings(memberNames)
		for _, name := range memberNames {
			member := root.Family.MemberTypes[name]
			if category, ok := member.(*TreeCategoryType); ok {
				for _, variant := range category.Variants {
					keys = append(keys, category.Name+"."+variant.Name)
				}
			} else {
				keys = append(keys, member.String())
			}
		}
		return keys
	default:
		return nil
	}
}

func (a *Analyzer) reportNonExhaustiveVisit(pos lexer.Pos, root treeVisitRootInfo, covered map[string]bool, hasWildcard bool) {
	if hasWildcard {
		return
	}
	missing := make([]string, 0)
	for _, key := range visitDomainKeys(root) {
		if !covered[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	label := "visit"
	switch root.Kind {
	case treeVisitRootKindCategory:
		if root.Category != nil {
			label = root.Category.String()
		}
	case treeVisitRootKindExact:
		if root.Exact != nil {
			label = root.Exact.String()
		}
	case treeVisitRootKindFamily:
		if root.Family != nil && root.Family.NodeType != nil {
			label = root.Family.NodeType.String()
		}
	}
	a.errorf(pos, "non-exhaustive visit over %s; missing %s", label, strings.Join(missing, ", "))
}

func (a *Analyzer) resolveVisitRootInfo(valueType Type, rootExpr ast.TypeExpr, pos lexer.Pos) (treeVisitRootInfo, bool) {
	sourceMember, sourceFamily, ok := resolveTreeVisitSourceType(valueType)
	if !ok || sourceFamily == nil {
		a.errorf(pos, "visit/fold expects a tree node source, got %s", valueType.String())
		return treeVisitRootInfo{}, false
	}
	if rootExpr == nil {
		if category, _, ok := resolveMatchableTreeCategoryType(valueType); ok {
			return treeVisitRootInfo{Kind: treeVisitRootKindCategory, Family: category.Family, Category: category}, true
		}
		switch tt := sourceMember.(type) {
		case *TreeBlockType:
			return treeVisitRootInfo{Kind: treeVisitRootKindExact, Family: tt.Family, Exact: tt}, true
		case *TreeStructType:
			return treeVisitRootInfo{Kind: treeVisitRootKindExact, Family: tt.Family, Exact: tt}, true
		default:
			a.errorf(pos, "visit/fold requires an explicit `as Family.Node` root for %s", valueType.String())
			return treeVisitRootInfo{}, false
		}
	}
	rootType := a.resolveType(rootExpr)
	switch tt := StripAggregateStateType(rootType).(type) {
	case *TreeNodeType:
		if tt == nil || tt.Family == nil {
			break
		}
		if sourceFamily != tt.Family {
			a.errorf(rootExpr.Pos(), "visit/fold root %s does not match source family %s", tt.String(), sourceFamily.Name)
			return treeVisitRootInfo{}, false
		}
		return treeVisitRootInfo{Kind: treeVisitRootKindFamily, Family: tt.Family}, true
	case *TreeCategoryType:
		category, _, matchable := resolveMatchableTreeCategoryType(valueType)
		if !matchable || category != tt {
			a.errorf(rootExpr.Pos(), "visit/fold root %s requires a %s source, got %s", tt.String(), tt.String(), valueType.String())
			return treeVisitRootInfo{}, false
		}
		return treeVisitRootInfo{Kind: treeVisitRootKindCategory, Family: tt.Family, Category: tt}, true
	case *TreeBlockType:
		if !SameType(sourceMember, tt) {
			a.errorf(rootExpr.Pos(), "visit/fold root %s requires a %s source, got %s", tt.String(), tt.String(), valueType.String())
			return treeVisitRootInfo{}, false
		}
		return treeVisitRootInfo{Kind: treeVisitRootKindExact, Family: tt.Family, Exact: tt}, true
	case *TreeStructType:
		if !SameType(sourceMember, tt) {
			a.errorf(rootExpr.Pos(), "visit/fold root %s requires a %s source, got %s", tt.String(), tt.String(), valueType.String())
			return treeVisitRootInfo{}, false
		}
		return treeVisitRootInfo{Kind: treeVisitRootKindExact, Family: tt.Family, Exact: tt}, true
	}
	a.errorf(rootExpr.Pos(), "visit/fold root expects a tree category, tree member, or Family.Node type, got %s", rootType.String())
	return treeVisitRootInfo{}, false
}

func (a *Analyzer) resolveVisitArmInfo(root treeVisitRootInfo, arm ast.VisitArm) (treeVisitArmInfo, bool) {
	if arm.Wildcard {
		return treeVisitArmInfo{Arm: arm}, true
	}
	namedTarget := &ast.NamedType{Position: arm.Position, Name: arm.TargetName}
	if category, variant, ok := a.treeVariantTargetFromNamedType(namedTarget); ok {
		switch root.Kind {
		case treeVisitRootKindCategory:
			if category != root.Category {
				a.errorf(arm.Position, "visit arm %q is outside the %s visit domain", arm.TargetName, root.Category.String())
				return treeVisitArmInfo{}, false
			}
		case treeVisitRootKindFamily:
			if category == nil || category.Family != root.Family {
				a.errorf(arm.Position, "visit arm %q is outside the %s visit domain", arm.TargetName, root.Family.NodeType.String())
				return treeVisitArmInfo{}, false
			}
		default:
			a.errorf(arm.Position, "visit arm %q cannot appear when visiting exact member %s", arm.TargetName, root.Exact.String())
			return treeVisitArmInfo{}, false
		}
		viewType := category.VariantViewType(variant)
		return treeVisitArmInfo{Arm: arm, Key: viewType.String(), BindType: viewType, Category: category, Variant: variant}, true
	}
	resolved, _, ok := a.lookupVisibleType(arm.TargetName)
	if !ok {
		a.errorf(arm.Position, "unknown visit arm target %q", arm.TargetName)
		return treeVisitArmInfo{}, false
	}
	switch tt := resolved.(type) {
	case *TreeBlockType:
		switch root.Kind {
		case treeVisitRootKindFamily:
			if tt.Family != root.Family {
				a.errorf(arm.Position, "visit arm %q is outside the %s visit domain", arm.TargetName, root.Family.NodeType.String())
				return treeVisitArmInfo{}, false
			}
		case treeVisitRootKindExact:
			if !SameType(tt, root.Exact) {
				a.errorf(arm.Position, "visit arm %q is outside the %s visit domain", arm.TargetName, root.Exact.String())
				return treeVisitArmInfo{}, false
			}
		default:
			a.errorf(arm.Position, "visit arm %q cannot appear in a %s visit", arm.TargetName, root.Category.String())
			return treeVisitArmInfo{}, false
		}
		return treeVisitArmInfo{Arm: arm, Key: tt.String(), BindType: tt, Exact: tt}, true
	case *TreeStructType:
		switch root.Kind {
		case treeVisitRootKindFamily:
			if tt.Family != root.Family {
				a.errorf(arm.Position, "visit arm %q is outside the %s visit domain", arm.TargetName, root.Family.NodeType.String())
				return treeVisitArmInfo{}, false
			}
		case treeVisitRootKindExact:
			if !SameType(tt, root.Exact) {
				a.errorf(arm.Position, "visit arm %q is outside the %s visit domain", arm.TargetName, root.Exact.String())
				return treeVisitArmInfo{}, false
			}
		default:
			a.errorf(arm.Position, "visit arm %q cannot appear in a %s visit", arm.TargetName, root.Category.String())
			return treeVisitArmInfo{}, false
		}
		return treeVisitArmInfo{Arm: arm, Key: tt.String(), BindType: tt, Exact: tt}, true
	case *TreeCategoryType:
		a.errorf(arm.Position, "visit arm %q must name a concrete tree variant or exact tree member", arm.TargetName)
		return treeVisitArmInfo{}, false
	default:
		a.errorf(arm.Position, "visit arm %q is not a tree visit target", arm.TargetName)
		return treeVisitArmInfo{}, false
	}
}

func (a *Analyzer) analyzeVisitArmBody(armInfo treeVisitArmInfo, resultType Type, scope *Scope, forFold bool) (Type, affineFlowSnapshot, bool) {
	if armInfo.Arm.BindName != "" && armInfo.BindType != nil {
		a.defineLocalInScope(scope, &Symbol{Name: armInfo.Arm.BindName, Kind: SymbolLocal, Type: armInfo.BindType, Mutable: false}, armInfo.Arm.Position)
	}
	if forFold && armInfo.Arm.ChildResultsName != "" && resultType != nil {
		childViewType := &DArrayViewType{Elem: resultType, SurfaceName: "dview"}
		a.defineLocalInScope(scope, &Symbol{Name: armInfo.Arm.ChildResultsName, Kind: SymbolLocal, Type: childViewType, Mutable: false}, armInfo.Arm.Position)
	}
	if len(armInfo.Arm.ChildBindings) != 0 {
		if !forFold {
			a.errorf(armInfo.Arm.Position, "visit arm %q cannot bind fold child results", armInfo.Arm.TargetName)
		} else {
			bindingTypes := treeFoldArmChildBindingTypes(armInfo.BindType, resultType)
			seenFields := map[string]bool{}
			for _, binding := range armInfo.Arm.ChildBindings {
				if binding.FieldName == "" || binding.BindName == "" {
					continue
				}
				if seenFields[binding.FieldName] {
					a.errorf(binding.Position, "fold arm %q binds child result %q more than once", armInfo.Arm.TargetName, binding.FieldName)
					continue
				}
				seenFields[binding.FieldName] = true
				bindingType, ok := bindingTypes[binding.FieldName]
				if !ok {
					a.errorf(binding.Position, "fold arm %q has no structural child result named %q", armInfo.Arm.TargetName, binding.FieldName)
					continue
				}
				a.defineLocalInScope(scope, &Symbol{Name: binding.BindName, Kind: SymbolLocal, Type: bindingType, Mutable: false}, binding.Position)
			}
		}
	}
	bodyScope := scope
	guardFallthrough := affineFlowSnapshot{}
	hasGuard := armInfo.Arm.Guard != nil
	if hasGuard {
		guardType, guardSnapshot := a.analyzeExprInAffineScope(armInfo.Arm.Guard, scope)
		if !IsBoolType(guardType) {
			a.errorf(armInfo.Arm.Guard.Pos(), "visit arm guard must be bool, got %s", guardType.String())
		}
		guardFallthrough = guardSnapshot
		bodyScope = a.refinedScopeForCondition(scope, armInfo.Arm.Guard, true)
	}
	bodyType, bodySnapshot := a.analyzeMatchExprArmBodyWithAffineSnapshot(armInfo.Arm.Body, bodyScope)
	canFallthrough := hasGuard || !blockDefinitelyExits(armInfo.Arm.Body)
	if hasGuard {
		if blockDefinitelyExits(armInfo.Arm.Body) {
			return bodyType, guardFallthrough, true
		}
		bodySnapshot.Affine = mergeAffineValueStates(bodySnapshot.Affine, guardFallthrough.Affine)
		bodySnapshot.BorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(bodySnapshot.BorrowedOwnerRefs, guardFallthrough.BorrowedOwnerRefs)
		bodySnapshot.FunctionValues = a.mergeFunctionValueBindings(bodySnapshot.FunctionValues, guardFallthrough.FunctionValues)
		bodySnapshot.SpecializedValueTypes = a.mergeSpecializedValueTypeBindings(bodySnapshot.SpecializedValueTypes, guardFallthrough.SpecializedValueTypes)
	}
	return bodyType, bodySnapshot, canFallthrough
}

func treeFoldArmChildBindingTypes(bindType Type, resultType Type) map[string]Type {
	if bindType == nil || resultType == nil {
		return nil
	}
	childViewType := &DArrayViewType{Elem: resultType, SurfaceName: "dview"}
	out := make(map[string]Type)
	for _, binding := range TreeStructuralChildBindings(bindType) {
		if binding.Name == "" {
			continue
		}
		switch binding.Relation {
		case ast.EnumPayloadRelationChild:
			if _, optional := UnwrapOptionalType(binding.Type); optional {
				out[binding.Name] = OptionalTreeFoldChildBindingType(resultType)
			} else {
				out[binding.Name] = resultType
			}
		case ast.EnumPayloadRelationChildren:
			if _, optional := UnwrapOptionalType(binding.Type); optional {
				out[binding.Name] = &OptionalType{Value: childViewType}
			} else {
				out[binding.Name] = childViewType
			}
		}
	}
	return out
}

func (a *Analyzer) analyzeVisitExpr(expr *ast.VisitExpr) Type {
	valueType := a.analyzeExpr(expr.Value)
	root, ok := a.resolveVisitRootInfo(valueType, expr.Root, expr.Pos())
	if !ok {
		for _, arm := range expr.Arms {
			a.analyzeMatchExprArmBody(arm.Body, NewScope(a.currentScope))
		}
		return invalidType
	}
	covered := map[string]bool{}
	priorKeys := map[string]bool{}
	hasWildcard := false
	resultType := Type(nil)
	baselineCloned := false
	var baselineAffine map[affineValueKey]affineValueState
	var baselineBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var baselineFunctionValues map[*Symbol]*FuncType
	var baselineSpecializedValueTypes map[*Symbol]Type
	cloneBaseline := func() {
		if baselineCloned {
			return
		}
		baselineAffine = a.cloneAffineValueStates()
		baselineBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
		baselineFunctionValues = a.cloneFunctionValueBindings()
		baselineSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
		baselineCloned = true
	}
	var mergedAffine map[affineValueKey]affineValueState
	var mergedBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var mergedFunctionValues map[*Symbol]*FuncType
	var mergedSpecializedValueTypes map[*Symbol]Type
	hasFallthrough := false
	for _, arm := range expr.Arms {
		armInfo, armOK := a.resolveVisitArmInfo(root, arm)
		guarded := arm.Guard != nil
		if armInfo.Arm.Wildcard {
			if hasWildcard {
				a.errorf(arm.Position, "visit wildcard arm is unreachable because an earlier wildcard already matches")
			}
			if !guarded {
				hasWildcard = true
			}
		} else if armOK {
			if hasWildcard {
				a.errorf(arm.Position, "visit arm %q is unreachable because an earlier wildcard already matches", arm.TargetName)
			}
			if priorKeys[armInfo.Key] {
				a.errorf(arm.Position, "visit arm %q is unreachable because an earlier arm already matches it", arm.TargetName)
			}
			if !guarded {
				priorKeys[armInfo.Key] = true
				covered[armInfo.Key] = true
			}
		}
		scope := NewScope(a.currentScope)
		armType, armSnapshot, armCanFallthrough := a.analyzeVisitArmBody(armInfo, nil, scope, false)
		if armCanFallthrough {
			if !hasFallthrough {
				mergedAffine = armSnapshot.Affine
				mergedBorrowedOwnerRefs = armSnapshot.BorrowedOwnerRefs
				mergedFunctionValues = armSnapshot.FunctionValues
				mergedSpecializedValueTypes = armSnapshot.SpecializedValueTypes
				hasFallthrough = true
			} else {
				mergedAffine = mergeAffineValueStates(mergedAffine, armSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, armSnapshot.BorrowedOwnerRefs)
				mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, armSnapshot.FunctionValues)
				mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, armSnapshot.SpecializedValueTypes)
			}
		}
		if resultType == nil {
			resultType = armType
			continue
		}
		merged := MergeTypes(resultType, armType)
		if IsInvalidType(merged) {
			a.errorf(arm.Position, "visit expression arms are incompatible: %s and %s", resultType.String(), armType.String())
			resultType = invalidType
			continue
		}
		resultType = merged
	}
	if !hasWildcard {
		cloneBaseline()
		if !hasFallthrough {
			mergedAffine = baselineAffine
			mergedBorrowedOwnerRefs = baselineBorrowedOwnerRefs
			mergedFunctionValues = baselineFunctionValues
			mergedSpecializedValueTypes = baselineSpecializedValueTypes
		} else if len(visitDomainKeys(root)) != len(covered) {
			mergedAffine = mergeAffineValueStates(mergedAffine, baselineAffine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, baselineBorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, baselineFunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, baselineSpecializedValueTypes)
		}
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	a.reportNonExhaustiveVisit(expr.Pos(), root, covered, hasWildcard)
	if resultType == nil {
		return neverType
	}
	return resultType
}

func treeVisitRootBindType(root treeVisitRootInfo) Type {
	switch root.Kind {
	case treeVisitRootKindCategory:
		return root.Category
	case treeVisitRootKindExact:
		return root.Exact
	case treeVisitRootKindFamily:
		if root.Family != nil {
			return root.Family.NodeType
		}
	}
	return nil
}

func treeExactStructuralChildTypes(exact Type) []Type {
	family, ok := TreeFamilyForMemberType(exact)
	if !ok || family == nil {
		return nil
	}
	var decls []ast.FieldDecl
	switch tt := StripAggregateStateType(exact).(type) {
	case *TreeBlockType:
		decls = TreeBlockFieldDeclsWithCommon(tt)
	case *TreeStructType:
		decls = TreeStructFieldDeclsWithCommon(tt)
	default:
		return nil
	}
	out := make([]Type, 0, len(decls))
	for _, fieldDecl := range decls {
		field, ok := TreeExactFieldInfo(exact, fieldDecl.Name)
		if !ok {
			continue
		}
		relation := TreeFieldStructuralRelation(family, field.Type)
		if itemType, ok := TreeStructuralChildItemType(field.Type, relation); ok && itemType != nil {
			out = append(out, itemType)
		}
	}
	return out
}

func treeVisitRootStructuralChildTypes(root treeVisitRootInfo) []Type {
	switch root.Kind {
	case treeVisitRootKindCategory:
		if root.Category == nil {
			return nil
		}
		out := make([]Type, 0)
		for _, variant := range root.Category.Variants {
			for i, payloadType := range variant.Payload {
				relation := variant.PayloadRelation(i)
				if itemType, ok := TreeStructuralChildItemType(payloadType, relation); ok && itemType != nil {
					out = append(out, itemType)
				}
			}
		}
		return out
	case treeVisitRootKindExact:
		return treeExactStructuralChildTypes(root.Exact)
	case treeVisitRootKindFamily:
		if root.Family == nil {
			return nil
		}
		out := make([]Type, 0)
		for _, member := range TreeFamilyExactMembersInTagOrder(root.Family) {
			switch tt := member.(type) {
			case *TreeVariantViewType:
				if tt == nil || tt.Variant == nil {
					continue
				}
				for i, payloadType := range tt.Variant.Payload {
					relation := tt.Variant.PayloadRelation(i)
					if itemType, ok := TreeStructuralChildItemType(payloadType, relation); ok && itemType != nil {
						out = append(out, itemType)
					}
				}
			default:
				out = append(out, treeExactStructuralChildTypes(member)...)
			}
		}
		return out
	default:
		return nil
	}
}

func (a *Analyzer) validateFoldRecursionRoot(pos lexer.Pos, root treeVisitRootInfo) {
	if a == nil || root.Kind == treeVisitRootKindFamily {
		return
	}
	rootType := treeVisitRootBindType(root)
	if rootType == nil {
		return
	}
	for _, childType := range treeVisitRootStructuralChildTypes(root) {
		if childType == nil {
			continue
		}
		if !AssignableTo(rootType, childType) {
			familyLabel := "<tree>"
			if root.Family != nil && root.Family.NodeType != nil {
				familyLabel = root.Family.NodeType.String()
			}
			a.errorf(pos, "fold over %s requires an explicit `as %s` root because structural children include %s", rootType.String(), familyLabel, childType.String())
			return
		}
	}
}

func (a *Analyzer) recordFoldExprInfo(expr *ast.FoldExpr) {
	if a == nil || expr == nil || a.currentScope == nil {
		return
	}
	collector := newDeferCaptureCollector(a, a.currentScope)
	for _, arm := range expr.Arms {
		armLocals := map[string]bool{}
		appendVisitArmLocals(armLocals, arm)
		collector.collectExpr(arm.Guard, cloneParallelForLocals(armLocals))
		for _, stmt := range arm.Body {
			collector.collectStmt(stmt, cloneParallelForLocals(armLocals))
		}
	}
	a.foldInfo[expr] = &FoldInfo{Captures: append([]string(nil), collector.captureOrder...)}
}

func (a *Analyzer) analyzeFoldExpr(expr *ast.FoldExpr) Type {
	valueType := a.analyzeExpr(expr.Value)
	root, ok := a.resolveVisitRootInfo(valueType, expr.Root, expr.Pos())
	if !ok {
		for _, arm := range expr.Arms {
			a.analyzeMatchExprArmBody(arm.Body, NewScope(a.currentScope))
		}
		return invalidType
	}
	a.validateFoldRecursionRoot(expr.Pos(), root)
	a.recordFoldExprInfo(expr)
	resultType := a.resolveType(expr.ResultType)
	covered := map[string]bool{}
	priorKeys := map[string]bool{}
	hasWildcard := false
	baselineCloned := false
	var baselineAffine map[affineValueKey]affineValueState
	var baselineBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var baselineFunctionValues map[*Symbol]*FuncType
	var baselineSpecializedValueTypes map[*Symbol]Type
	cloneBaseline := func() {
		if baselineCloned {
			return
		}
		baselineAffine = a.cloneAffineValueStates()
		baselineBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
		baselineFunctionValues = a.cloneFunctionValueBindings()
		baselineSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
		baselineCloned = true
	}
	var mergedAffine map[affineValueKey]affineValueState
	var mergedBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var mergedFunctionValues map[*Symbol]*FuncType
	var mergedSpecializedValueTypes map[*Symbol]Type
	hasFallthrough := false
	for _, arm := range expr.Arms {
		armInfo, armOK := a.resolveVisitArmInfo(root, arm)
		guarded := arm.Guard != nil
		if armInfo.Arm.Wildcard {
			if hasWildcard {
				a.errorf(arm.Position, "fold wildcard arm is unreachable because an earlier wildcard already matches")
			}
			if !guarded {
				hasWildcard = true
			}
		} else if armOK {
			if hasWildcard {
				a.errorf(arm.Position, "fold arm %q is unreachable because an earlier wildcard already matches", arm.TargetName)
			}
			if priorKeys[armInfo.Key] {
				a.errorf(arm.Position, "fold arm %q is unreachable because an earlier arm already matches it", arm.TargetName)
			}
			if !guarded {
				priorKeys[armInfo.Key] = true
				covered[armInfo.Key] = true
			}
		}
		scope := NewScope(a.currentScope)
		armType, armSnapshot, armCanFallthrough := a.analyzeVisitArmBody(armInfo, resultType, scope, true)
		if !IsNeverType(armType) && !AssignableTo(resultType, armType) {
			a.errorf(arm.Position, "fold arm %q expects %s, got %s", arm.TargetName, resultType.String(), armType.String())
			a.reportShapeMismatchNotes(arm.Position, resultType, armType)
		}
		if armCanFallthrough {
			if !hasFallthrough {
				mergedAffine = armSnapshot.Affine
				mergedBorrowedOwnerRefs = armSnapshot.BorrowedOwnerRefs
				mergedFunctionValues = armSnapshot.FunctionValues
				mergedSpecializedValueTypes = armSnapshot.SpecializedValueTypes
				hasFallthrough = true
			} else {
				mergedAffine = mergeAffineValueStates(mergedAffine, armSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, armSnapshot.BorrowedOwnerRefs)
				mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, armSnapshot.FunctionValues)
				mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, armSnapshot.SpecializedValueTypes)
			}
		}
	}
	if !hasWildcard {
		cloneBaseline()
		if !hasFallthrough {
			mergedAffine = baselineAffine
			mergedBorrowedOwnerRefs = baselineBorrowedOwnerRefs
			mergedFunctionValues = baselineFunctionValues
			mergedSpecializedValueTypes = baselineSpecializedValueTypes
		} else if len(visitDomainKeys(root)) != len(covered) {
			mergedAffine = mergeAffineValueStates(mergedAffine, baselineAffine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, baselineBorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, baselineFunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, baselineSpecializedValueTypes)
		}
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	a.reportNonExhaustiveVisit(expr.Pos(), root, covered, hasWildcard)
	return resultType
}

func (a *Analyzer) analyzeEnumMatchExpr(expr *ast.MatchExpr, valueType Type, enumType *EnumType) Type {
	a.validateMatchStore(expr.Pos(), expr.Value, valueType, enumType, expr.Store)
	covered := map[string]bool{}
	hasWildcard := false
	resultType := Type(nil)
	baselineCloned := false
	var baselineAffine map[affineValueKey]affineValueState
	var baselineBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var baselineFunctionValues map[*Symbol]*FuncType
	var baselineSpecializedValueTypes map[*Symbol]Type
	cloneBaseline := func() {
		if baselineCloned {
			return
		}
		baselineAffine = a.cloneAffineValueStates()
		baselineBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
		baselineFunctionValues = a.cloneFunctionValueBindings()
		baselineSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
		baselineCloned = true
	}
	var mergedAffine map[affineValueKey]affineValueState
	var mergedBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var mergedFunctionValues map[*Symbol]*FuncType
	var mergedSpecializedValueTypes map[*Symbol]Type
	hasFallthrough := false
	priorPatterns := make([]ast.MatchPattern, 0, len(expr.Arms))
	for i, arm := range expr.Arms {
		if a.matchPatternShadowedByPrevious(arm.Pattern, enumType, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		if a.analyzeTopLevelMatchPattern(arm.Pattern, enumType, expr.Value, scope, i, len(expr.Arms), covered) {
			hasWildcard = true
		}
		a.bindPackedVariantViewAliasForBody(arm.Pattern, enumType, expr.Value, arm.Body, scope)
		armType, armSnapshot := a.analyzeMatchExprArmBodyWithAffineSnapshot(arm.Body, scope)
		if !blockDefinitelyExits(arm.Body) {
			if !hasFallthrough {
				mergedAffine = armSnapshot.Affine
				mergedBorrowedOwnerRefs = armSnapshot.BorrowedOwnerRefs
				mergedFunctionValues = armSnapshot.FunctionValues
				mergedSpecializedValueTypes = armSnapshot.SpecializedValueTypes
				hasFallthrough = true
			} else {
				mergedAffine = mergeAffineValueStates(mergedAffine, armSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, armSnapshot.BorrowedOwnerRefs)
				mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, armSnapshot.FunctionValues)
				mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, armSnapshot.SpecializedValueTypes)
			}
		}
		if resultType == nil {
			resultType = armType
			priorPatterns = append(priorPatterns, arm.Pattern)
			continue
		}
		merged := MergeTypes(resultType, armType)
		if IsInvalidType(merged) {
			a.errorf(arm.Position, "match expression arms are incompatible: %s and %s", resultType.String(), armType.String())
			resultType = invalidType
			priorPatterns = append(priorPatterns, arm.Pattern)
			continue
		}
		resultType = merged
		priorPatterns = append(priorPatterns, arm.Pattern)
	}
	if !a.matchCoversAllVariants(enumType, covered, hasWildcard) {
		cloneBaseline()
		if !hasFallthrough {
			mergedAffine = baselineAffine
			mergedBorrowedOwnerRefs = baselineBorrowedOwnerRefs
			mergedFunctionValues = baselineFunctionValues
			mergedSpecializedValueTypes = baselineSpecializedValueTypes
		} else {
			mergedAffine = mergeAffineValueStates(mergedAffine, baselineAffine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, baselineBorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, baselineFunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, baselineSpecializedValueTypes)
		}
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	a.recordAffineDestructureConsumption(expr.Value, valueType, "match over affine enum")
	a.reportNonExhaustiveMatch(expr.Pos(), enumType, covered, hasWildcard)
	if resultType == nil {
		return neverType
	}
	return resultType
}

func (a *Analyzer) analyzeTreeMatchExpr(expr *ast.MatchExpr, treeType *TreeCategoryType) Type {
	a.validateTreeMatchStore(treeType, expr.Store)
	covered := map[string]bool{}
	hasWildcard := false
	resultType := Type(nil)
	baselineCloned := false
	var baselineAffine map[affineValueKey]affineValueState
	var baselineBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var baselineFunctionValues map[*Symbol]*FuncType
	var baselineSpecializedValueTypes map[*Symbol]Type
	cloneBaseline := func() {
		if baselineCloned {
			return
		}
		baselineAffine = a.cloneAffineValueStates()
		baselineBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
		baselineFunctionValues = a.cloneFunctionValueBindings()
		baselineSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
		baselineCloned = true
	}
	var mergedAffine map[affineValueKey]affineValueState
	var mergedBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var mergedFunctionValues map[*Symbol]*FuncType
	var mergedSpecializedValueTypes map[*Symbol]Type
	hasFallthrough := false
	priorPatterns := make([]ast.MatchPattern, 0, len(expr.Arms))
	for i, arm := range expr.Arms {
		if a.matchPatternShadowedByPrevious(arm.Pattern, treeType, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		if a.analyzeTopLevelTreeMatchPattern(arm.Pattern, treeType, expr.Value, scope, i, len(expr.Arms), covered) {
			hasWildcard = true
		}
		armType, armSnapshot := a.analyzeMatchExprArmBodyWithAffineSnapshot(arm.Body, scope)
		if !blockDefinitelyExits(arm.Body) {
			if !hasFallthrough {
				mergedAffine = armSnapshot.Affine
				mergedBorrowedOwnerRefs = armSnapshot.BorrowedOwnerRefs
				mergedFunctionValues = armSnapshot.FunctionValues
				mergedSpecializedValueTypes = armSnapshot.SpecializedValueTypes
				hasFallthrough = true
			} else {
				mergedAffine = mergeAffineValueStates(mergedAffine, armSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, armSnapshot.BorrowedOwnerRefs)
				mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, armSnapshot.FunctionValues)
				mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, armSnapshot.SpecializedValueTypes)
			}
		}
		if resultType == nil {
			resultType = armType
			priorPatterns = append(priorPatterns, arm.Pattern)
			continue
		}
		merged := MergeTypes(resultType, armType)
		if IsInvalidType(merged) {
			a.errorf(arm.Position, "match expression arms are incompatible: %s and %s", resultType.String(), armType.String())
			resultType = invalidType
			priorPatterns = append(priorPatterns, arm.Pattern)
			continue
		}
		resultType = merged
		priorPatterns = append(priorPatterns, arm.Pattern)
	}
	if !a.matchCoversAllVariants(treeType, covered, hasWildcard) {
		cloneBaseline()
		if !hasFallthrough {
			mergedAffine = baselineAffine
			mergedBorrowedOwnerRefs = baselineBorrowedOwnerRefs
			mergedFunctionValues = baselineFunctionValues
			mergedSpecializedValueTypes = baselineSpecializedValueTypes
		} else {
			mergedAffine = mergeAffineValueStates(mergedAffine, baselineAffine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, baselineBorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, baselineFunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, baselineSpecializedValueTypes)
		}
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	a.reportNonExhaustiveMatch(expr.Pos(), treeType, covered, hasWildcard)
	if resultType == nil {
		return neverType
	}
	return resultType
}

func (a *Analyzer) analyzeStringMatchStmt(stmt *ast.MatchStmt, valueType Type) {
	if stmt.Store != nil {
		a.errorf(stmt.Store.Pos(), "string match does not take an in-store clause")
	}
	baselineCloned := false
	var baselineAffine map[affineValueKey]affineValueState
	var baselineBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var baselineFunctionValues map[*Symbol]*FuncType
	var baselineSpecializedValueTypes map[*Symbol]Type
	cloneBaseline := func() {
		if baselineCloned {
			return
		}
		baselineAffine = a.cloneAffineValueStates()
		baselineBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
		baselineFunctionValues = a.cloneFunctionValueBindings()
		baselineSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
		baselineCloned = true
	}
	var mergedAffine map[affineValueKey]affineValueState
	var mergedBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var mergedFunctionValues map[*Symbol]*FuncType
	var mergedSpecializedValueTypes map[*Symbol]Type
	hasFallthrough := false
	priorPatterns := make([]ast.MatchPattern, 0, len(stmt.Arms))
	hasWildcard := false
	for i, arm := range stmt.Arms {
		if a.stringMatchPatternShadowedByPrevious(arm.Pattern, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		if a.analyzeTopLevelStringMatchPattern(arm.Pattern, valueType, scope, i, len(stmt.Arms)) {
			hasWildcard = true
		}
		armSnapshot := a.analyzeBlockWithAffineClone(arm.Body, scope)
		if !blockDefinitelyExits(arm.Body) {
			if !hasFallthrough {
				mergedAffine = armSnapshot.Affine
				mergedBorrowedOwnerRefs = armSnapshot.BorrowedOwnerRefs
				mergedFunctionValues = armSnapshot.FunctionValues
				mergedSpecializedValueTypes = armSnapshot.SpecializedValueTypes
				hasFallthrough = true
			} else {
				mergedAffine = mergeAffineValueStates(mergedAffine, armSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, armSnapshot.BorrowedOwnerRefs)
				mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, armSnapshot.FunctionValues)
				mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, armSnapshot.SpecializedValueTypes)
			}
		}
		priorPatterns = append(priorPatterns, arm.Pattern)
	}
	if !hasWildcard {
		cloneBaseline()
		if !hasFallthrough {
			mergedAffine = baselineAffine
			mergedBorrowedOwnerRefs = baselineBorrowedOwnerRefs
			mergedFunctionValues = baselineFunctionValues
			mergedSpecializedValueTypes = baselineSpecializedValueTypes
		} else {
			mergedAffine = mergeAffineValueStates(mergedAffine, baselineAffine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, baselineBorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, baselineFunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, baselineSpecializedValueTypes)
		}
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
}

func (a *Analyzer) analyzeStringMatchExpr(expr *ast.MatchExpr, valueType Type) Type {
	if expr.Store != nil {
		a.errorf(expr.Store.Pos(), "string match does not take an in-store clause")
	}
	resultType := Type(nil)
	baselineCloned := false
	var baselineAffine map[affineValueKey]affineValueState
	var baselineBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var baselineFunctionValues map[*Symbol]*FuncType
	var baselineSpecializedValueTypes map[*Symbol]Type
	cloneBaseline := func() {
		if baselineCloned {
			return
		}
		baselineAffine = a.cloneAffineValueStates()
		baselineBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
		baselineFunctionValues = a.cloneFunctionValueBindings()
		baselineSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
		baselineCloned = true
	}
	var mergedAffine map[affineValueKey]affineValueState
	var mergedBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var mergedFunctionValues map[*Symbol]*FuncType
	var mergedSpecializedValueTypes map[*Symbol]Type
	hasFallthrough := false
	priorPatterns := make([]ast.MatchPattern, 0, len(expr.Arms))
	hasWildcard := false
	for i, arm := range expr.Arms {
		if a.stringMatchPatternShadowedByPrevious(arm.Pattern, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		if a.analyzeTopLevelStringMatchPattern(arm.Pattern, valueType, scope, i, len(expr.Arms)) {
			hasWildcard = true
		}
		armType, armSnapshot := a.analyzeMatchExprArmBodyWithAffineSnapshot(arm.Body, scope)
		if !blockDefinitelyExits(arm.Body) {
			if !hasFallthrough {
				mergedAffine = armSnapshot.Affine
				mergedBorrowedOwnerRefs = armSnapshot.BorrowedOwnerRefs
				mergedFunctionValues = armSnapshot.FunctionValues
				mergedSpecializedValueTypes = armSnapshot.SpecializedValueTypes
				hasFallthrough = true
			} else {
				mergedAffine = mergeAffineValueStates(mergedAffine, armSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, armSnapshot.BorrowedOwnerRefs)
				mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, armSnapshot.FunctionValues)
				mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, armSnapshot.SpecializedValueTypes)
			}
		}
		if resultType == nil {
			resultType = armType
			priorPatterns = append(priorPatterns, arm.Pattern)
			continue
		}
		merged := MergeTypes(resultType, armType)
		if IsInvalidType(merged) {
			a.errorf(arm.Position, "match expression arms are incompatible: %s and %s", resultType.String(), armType.String())
			resultType = invalidType
			priorPatterns = append(priorPatterns, arm.Pattern)
			continue
		}
		resultType = merged
		priorPatterns = append(priorPatterns, arm.Pattern)
	}
	if !hasWildcard {
		cloneBaseline()
		if !hasFallthrough {
			mergedAffine = baselineAffine
			mergedBorrowedOwnerRefs = baselineBorrowedOwnerRefs
			mergedFunctionValues = baselineFunctionValues
			mergedSpecializedValueTypes = baselineSpecializedValueTypes
		} else {
			mergedAffine = mergeAffineValueStates(mergedAffine, baselineAffine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, baselineBorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, baselineFunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, baselineSpecializedValueTypes)
		}
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	a.reportNonExhaustiveStringMatchExpr(expr.Pos(), hasWildcard)
	if resultType == nil {
		return neverType
	}
	return resultType
}

func (a *Analyzer) analyzeStructMatchStmt(stmt *ast.MatchStmt, valueType Type) {
	if stmt.Store != nil {
		a.errorf(stmt.Store.Pos(), "struct match does not take an in-store clause")
	}
	baselineCloned := false
	var baselineAffine map[affineValueKey]affineValueState
	var baselineBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var baselineFunctionValues map[*Symbol]*FuncType
	var baselineSpecializedValueTypes map[*Symbol]Type
	cloneBaseline := func() {
		if baselineCloned {
			return
		}
		baselineAffine = a.cloneAffineValueStates()
		baselineBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
		baselineFunctionValues = a.cloneFunctionValueBindings()
		baselineSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
		baselineCloned = true
	}
	var mergedAffine map[affineValueKey]affineValueState
	var mergedBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var mergedFunctionValues map[*Symbol]*FuncType
	var mergedSpecializedValueTypes map[*Symbol]Type
	hasFallthrough := false
	priorPatterns := make([]ast.MatchPattern, 0, len(stmt.Arms))
	hasWildcard := false
	for i, arm := range stmt.Arms {
		if a.matchPatternShadowedByPrevious(arm.Pattern, valueType, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		if a.analyzeTopLevelStructMatchPattern(arm.Pattern, valueType, stmt.Value, scope, i, len(stmt.Arms)) {
			hasWildcard = true
		}
		armSnapshot := a.analyzeBlockWithAffineClone(arm.Body, scope)
		if !blockDefinitelyExits(arm.Body) {
			if !hasFallthrough {
				mergedAffine = armSnapshot.Affine
				mergedBorrowedOwnerRefs = armSnapshot.BorrowedOwnerRefs
				mergedFunctionValues = armSnapshot.FunctionValues
				mergedSpecializedValueTypes = armSnapshot.SpecializedValueTypes
				hasFallthrough = true
			} else {
				mergedAffine = mergeAffineValueStates(mergedAffine, armSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, armSnapshot.BorrowedOwnerRefs)
				mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, armSnapshot.FunctionValues)
				mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, armSnapshot.SpecializedValueTypes)
			}
		}
		priorPatterns = append(priorPatterns, arm.Pattern)
	}
	if !hasWildcard {
		cloneBaseline()
		if !hasFallthrough {
			mergedAffine = baselineAffine
			mergedBorrowedOwnerRefs = baselineBorrowedOwnerRefs
			mergedFunctionValues = baselineFunctionValues
			mergedSpecializedValueTypes = baselineSpecializedValueTypes
		} else {
			mergedAffine = mergeAffineValueStates(mergedAffine, baselineAffine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, baselineBorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, baselineFunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, baselineSpecializedValueTypes)
		}
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
}

func (a *Analyzer) analyzeStructMatchExpr(expr *ast.MatchExpr, valueType Type) Type {
	if expr.Store != nil {
		a.errorf(expr.Store.Pos(), "struct match does not take an in-store clause")
	}
	resultType := Type(nil)
	baselineCloned := false
	var baselineAffine map[affineValueKey]affineValueState
	var baselineBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var baselineFunctionValues map[*Symbol]*FuncType
	var baselineSpecializedValueTypes map[*Symbol]Type
	cloneBaseline := func() {
		if baselineCloned {
			return
		}
		baselineAffine = a.cloneAffineValueStates()
		baselineBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
		baselineFunctionValues = a.cloneFunctionValueBindings()
		baselineSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
		baselineCloned = true
	}
	var mergedAffine map[affineValueKey]affineValueState
	var mergedBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var mergedFunctionValues map[*Symbol]*FuncType
	var mergedSpecializedValueTypes map[*Symbol]Type
	hasFallthrough := false
	priorPatterns := make([]ast.MatchPattern, 0, len(expr.Arms))
	hasWildcard := false
	for i, arm := range expr.Arms {
		if a.matchPatternShadowedByPrevious(arm.Pattern, valueType, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		if a.analyzeTopLevelStructMatchPattern(arm.Pattern, valueType, expr.Value, scope, i, len(expr.Arms)) {
			hasWildcard = true
		}
		armType, armSnapshot := a.analyzeMatchExprArmBodyWithAffineSnapshot(arm.Body, scope)
		if !blockDefinitelyExits(arm.Body) {
			if !hasFallthrough {
				mergedAffine = armSnapshot.Affine
				mergedBorrowedOwnerRefs = armSnapshot.BorrowedOwnerRefs
				mergedFunctionValues = armSnapshot.FunctionValues
				mergedSpecializedValueTypes = armSnapshot.SpecializedValueTypes
				hasFallthrough = true
			} else {
				mergedAffine = mergeAffineValueStates(mergedAffine, armSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, armSnapshot.BorrowedOwnerRefs)
				mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, armSnapshot.FunctionValues)
				mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, armSnapshot.SpecializedValueTypes)
			}
		}
		if resultType == nil {
			resultType = armType
			priorPatterns = append(priorPatterns, arm.Pattern)
			continue
		}
		merged := MergeTypes(resultType, armType)
		if IsInvalidType(merged) {
			a.errorf(arm.Position, "match expression arms are incompatible: %s and %s", resultType.String(), armType.String())
			resultType = invalidType
			priorPatterns = append(priorPatterns, arm.Pattern)
			continue
		}
		resultType = merged
		priorPatterns = append(priorPatterns, arm.Pattern)
	}
	if !hasWildcard {
		cloneBaseline()
		if !hasFallthrough {
			mergedAffine = baselineAffine
			mergedBorrowedOwnerRefs = baselineBorrowedOwnerRefs
			mergedFunctionValues = baselineFunctionValues
			mergedSpecializedValueTypes = baselineSpecializedValueTypes
		} else {
			mergedAffine = mergeAffineValueStates(mergedAffine, baselineAffine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, baselineBorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, baselineFunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, baselineSpecializedValueTypes)
		}
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	a.reportNonExhaustiveStructMatchExpr(expr.Pos(), hasWildcard)
	if resultType == nil {
		return neverType
	}
	return resultType
}

func resolveMatchableEnumType(actual Type) (*EnumType, *PackedVariantViewType, bool) {
	switch tt := actual.(type) {
	case *EnumType:
		if tt == nil {
			return nil, nil, false
		}
		return tt, nil, true
	case *PackedVariantViewType:
		if tt == nil || tt.Enum == nil {
			return nil, nil, false
		}
		return tt.Enum, tt, true
	default:
		return nil, nil, false
	}
}

func isStringMatchableType(actual Type) bool {
	if actual == nil {
		return false
	}
	literalType := &RefType{Elem: &BuiltinType{Name: "u8"}, State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}
	return runtimeStringComparable(actual, literalType)
}

func (a *Analyzer) validateMatchStore(pos lexer.Pos, valueExpr ast.Expr, actual Type, enumType *EnumType, storeExpr ast.Expr) {
	if enumType == nil {
		return
	}
	if !enumType.Packed {
		if storeExpr != nil {
			a.errorf(storeExpr.Pos(), "ordinary enum match over %q does not take an in-store clause", enumType.Name)
		}
		return
	}
	if storeExpr == nil {
		if a.canInferPackedStoreFromValue(valueExpr, actual, enumType) {
			return
		}
		if _, ok := a.lookupPackedStore(enumType); ok {
			return
		}
		a.errorf(pos, "packed enum match over %q requires an in %s clause", enumType.Name, packedEnumStoreTypeName(enumType.Name))
		return
	}
	storeType := a.analyzeExpr(storeExpr)
	packedStore, ok := storeType.(*PackedEnumStoreType)
	if !ok {
		a.errorf(storeExpr.Pos(), "packed enum match over %q requires store type %q, got %s", enumType.Name, packedEnumStoreTypeName(enumType.Name), storeType.String())
		return
	}
	if packedStore.Enum != enumType {
		a.errorf(storeExpr.Pos(), "packed enum match over %q requires store type %q, got %s", enumType.Name, packedEnumStoreTypeName(enumType.Name), storeType.String())
	}
}

func (a *Analyzer) validateTreeMatchStore(treeType *TreeCategoryType, storeExpr ast.Expr) {
	if treeType == nil || storeExpr == nil {
		return
	}
	a.errorf(storeExpr.Pos(), "tree match over %q does not take an in-store clause", treeType.Name)
}

func (a *Analyzer) matchPatternShadowedByPrevious(pattern ast.MatchPattern, expected Type, prior []ast.MatchPattern) bool {
	for _, prev := range prior {
		if a.matchPatternCovers(prev, pattern, expected) {
			return true
		}
	}
	return false
}

func (a *Analyzer) stringMatchPatternShadowedByPrevious(pattern ast.MatchPattern, prior []ast.MatchPattern) bool {
	for _, prev := range prior {
		switch p := prev.(type) {
		case *ast.MatchWildcardPattern:
			return true
		case *ast.MatchStringLiteralPattern:
			curr, ok := pattern.(*ast.MatchStringLiteralPattern)
			if ok && curr.Value == p.Value {
				return true
			}
		case *ast.MatchLiteralPattern:
			curr, ok := pattern.(*ast.MatchLiteralPattern)
			if ok && a.matchLiteralPatternEquals(p.Value, curr.Value) {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) matchPatternCovers(prev ast.MatchPattern, current ast.MatchPattern, expected Type) bool {
	switch p := prev.(type) {
	case *ast.MatchWildcardPattern:
		return true
	case *ast.MatchBindPattern:
		return true
	case *ast.MatchStringLiteralPattern:
		switch curr := current.(type) {
		case *ast.MatchStringLiteralPattern:
			return curr.Value == p.Value
		case *ast.MatchLiteralPattern:
			return a.matchLiteralPatternEquals(&ast.StringLit{Position: p.Position, Value: p.Value}, curr.Value)
		default:
			return false
		}
	case *ast.MatchLiteralPattern:
		switch curr := current.(type) {
		case *ast.MatchLiteralPattern:
			return a.matchLiteralPatternEquals(p.Value, curr.Value)
		case *ast.MatchStringLiteralPattern:
			return a.matchLiteralPatternEquals(p.Value, &ast.StringLit{Position: curr.Position, Value: curr.Value})
		default:
			return false
		}
	case *ast.MatchVariantPattern:
		currVariant, ok := current.(*ast.MatchVariantPattern)
		if !ok {
			return false
		}
		switch variantBase := expected.(type) {
		case *EnumType:
			if variantBase == nil || p.EnumName != variantBase.Name || currVariant.EnumName != variantBase.Name || p.Variant != currVariant.Variant {
				return false
			}
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				return false
			}
			prevArgs, ok := orderedMatchPatternArgs(p, variant)
			if !ok {
				return false
			}
			currArgs, ok := orderedMatchPatternArgs(currVariant, variant)
			if !ok {
				return false
			}
			for i := range prevArgs {
				if prevArgs[i] == nil || currArgs[i] == nil {
					return false
				}
				if !a.matchPatternCovers(prevArgs[i].Pattern, currArgs[i].Pattern, variant.Payload[i]) {
					return false
				}
			}
			return true
		case *TreeCategoryType:
			if variantBase == nil || p.EnumName != variantBase.Name || currVariant.EnumName != variantBase.Name || p.Variant != currVariant.Variant {
				return false
			}
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				return false
			}
			prevArgs, ok := orderedMatchPatternArgs(p, variant)
			if !ok {
				return false
			}
			currArgs, ok := orderedMatchPatternArgs(currVariant, variant)
			if !ok {
				return false
			}
			for i := range prevArgs {
				if prevArgs[i] == nil || currArgs[i] == nil {
					return false
				}
				if !a.matchPatternCovers(prevArgs[i].Pattern, currArgs[i].Pattern, variant.Payload[i]) {
					return false
				}
			}
			return true
		default:
			return false
		}
	case *ast.MatchStructPattern:
		currStruct, ok := current.(*ast.MatchStructPattern)
		if !ok {
			return false
		}
		fields, ok := a.resolvedStructFields(expected)
		if !ok {
			return false
		}
		if !structPatternMatchesType(p, expected) || !structPatternMatchesType(currStruct, expected) {
			return false
		}
		prevArgs, ok := orderedStructMatchPatternArgs(p, fields)
		if !ok {
			return false
		}
		currArgs, ok := orderedStructMatchPatternArgs(currStruct, fields)
		if !ok {
			return false
		}
		for i := range prevArgs {
			if prevArgs[i] == nil {
				continue
			}
			if currArgs[i] == nil {
				return false
			}
			if !a.matchPatternCovers(prevArgs[i].Pattern, currArgs[i].Pattern, fields[i].Type) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func structPatternMatchesType(pattern *ast.MatchStructPattern, expected Type) bool {
	if pattern == nil {
		return false
	}
	switch tt := StripAggregateStateType(expected).(type) {
	case *StructType:
		return tt != nil && tt.Name == pattern.TypeName && tt.Decl != nil
	case *GenericInstanceType:
		base, _ := tt.Base.(*StructType)
		return base != nil && base.Name == pattern.TypeName && base.Decl != nil
	case *TreeBlockType:
		return tt != nil && tt.Name == pattern.TypeName
	case *TreeStructType:
		return tt != nil && tt.Name == pattern.TypeName
	default:
		return false
	}
}

func orderedStructMatchPatternArgs(pattern *ast.MatchStructPattern, fields []moveBindResolvedField) ([]*ast.MatchPatternArg, bool) {
	if pattern == nil {
		return nil, false
	}
	ordered := make([]*ast.MatchPatternArg, len(fields))
	if len(pattern.Args) == 0 {
		return ordered, true
	}
	fieldIndexes := make(map[string]int, len(fields))
	for i := range fields {
		fieldIndexes[fields[i].Name] = i
	}
	seen := make([]bool, len(fields))
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		if arg.Name == "" {
			return nil, false
		}
		index, ok := fieldIndexes[arg.Name]
		if !ok || seen[index] {
			return nil, false
		}
		seen[index] = true
		ordered[index] = arg
	}
	return ordered, true
}

func (a *Analyzer) matchLiteralPatternEquals(left ast.Expr, right ast.Expr) bool {
	if a == nil || left == nil || right == nil {
		return false
	}
	leftValue, leftOK := a.evalConstExpr(left)
	rightValue, rightOK := a.evalConstExpr(right)
	if leftOK && rightOK {
		equal, ok := a.evalConstEquality(leftValue, rightValue, true)
		return ok && equal.Kind == ConstBool && equal.Bool
	}
	_, leftNull := left.(*ast.NullLit)
	_, rightNull := right.(*ast.NullLit)
	return leftNull && rightNull
}

func orderedMatchPatternArgs(pattern *ast.MatchVariantPattern, variant *EnumVariant) ([]*ast.MatchPatternArg, bool) {
	ordered := make([]*ast.MatchPatternArg, len(variant.Payload))
	if len(pattern.Args) == 0 {
		return ordered, true
	}
	namedCount := 0
	for i := range pattern.Args {
		if pattern.Args[i].Name != "" {
			namedCount++
		}
	}
	if namedCount != 0 && namedCount != len(pattern.Args) {
		return nil, false
	}
	if namedCount == 0 {
		if len(pattern.Args) != len(variant.Payload) {
			return nil, false
		}
		for i := range pattern.Args {
			ordered[i] = &pattern.Args[i]
		}
		return ordered, true
	}
	if !variant.HasNamedPayloads() || len(pattern.Args) != len(variant.Payload) {
		return nil, false
	}
	seen := map[int]bool{}
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		index, ok := variant.PayloadIndex(arg.Name)
		if !ok || seen[index] {
			return nil, false
		}
		seen[index] = true
		ordered[index] = arg
	}
	for i := range ordered {
		if ordered[i] == nil {
			return nil, false
		}
	}
	return ordered, true
}
func matchPatternSummary(pattern ast.MatchPattern) string {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return "_"
	case *ast.MatchBindPattern:
		return p.Name
	case *ast.MatchStringLiteralPattern:
		return p.Value
	case *ast.MatchLiteralPattern:
		return matchLiteralPatternSummary(p.Value)
	case *ast.MatchStructPattern:
		parts := make([]string, 0, len(p.Args))
		for _, arg := range p.Args {
			part := matchPatternSummary(arg.Pattern)
			if arg.Name != "" {
				part = arg.Name + ": " + part
			}
			parts = append(parts, part)
		}
		if len(parts) == 0 {
			return p.TypeName + "()"
		}
		return p.TypeName + "(" + strings.Join(parts, ", ") + ")"
	case *ast.MatchVariantPattern:
		if len(p.Args) == 0 {
			return p.EnumName + "." + p.Variant
		}
		parts := make([]string, 0, len(p.Args))
		for _, arg := range p.Args {
			part := matchPatternSummary(arg.Pattern)
			if arg.Name != "" {
				part = arg.Name + ": " + part
			}
			parts = append(parts, part)
		}
		return p.EnumName + "." + p.Variant + "(" + strings.Join(parts, ", ") + ")"
	default:
		return "<pattern>"
	}
}

func matchLiteralPatternSummary(expr ast.Expr) string {
	if expr == nil {
		return "<literal>"
	}
	switch n := expr.(type) {
	case *ast.IntLit:
		if n.Suffix != "" {
			return n.Value + n.Suffix
		}
		return n.Value
	case *ast.FloatLit:
		if n.Suffix != "" {
			return n.Value + n.Suffix
		}
		return n.Value
	case *ast.StringLit:
		return strconv.Quote(n.Value)
	case *ast.CharLit:
		return n.Value
	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	case *ast.NullLit:
		return "null"
	case *ast.UnaryExpr:
		return lexer.TokenName(n.Op) + matchLiteralPatternSummary(n.Operand)
	case *ast.ParenExpr:
		return "(" + matchLiteralPatternSummary(n.Inner) + ")"
	default:
		return "<literal>"
	}
}

func (a *Analyzer) analyzeMatchExprArmBody(body []ast.Stmt, scope *Scope) Type {
	savedScope := a.currentScope
	savedRegions := a.currentRegions
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	a.currentScope = scope
	a.currentRegions = a.cloneRegionStates()
	a.currentPackedVariantViews = a.clonePackedVariantViewBindings()
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	defer func() {
		a.currentScope = savedScope
		a.currentRegions = savedRegions
		a.currentPackedVariantViews = savedPackedVariantViews
		a.currentPackedStores = savedPackedStores
		a.currentPackedStoreResolutions = savedPackedStoreResolutions
	}()
	if len(body) == 0 {
		return invalidType
	}
	for i, stmt := range body {
		isLast := i == len(body)-1
		if !isLast {
			a.analyzeStmt(stmt)
			if stmtDefinitelyExits(stmt) {
				return neverType
			}
			continue
		}
		if exprStmt, ok := stmt.(*ast.ExprStmt); ok {
			return a.analyzeExpr(exprStmt.Expr)
		}
		a.analyzeStmt(stmt)
		if stmtDefinitelyExits(stmt) {
			return neverType
		}
		a.errorf(stmt.Pos(), "match expression arm must end with an expression")
		return invalidType
	}
	return invalidType
}

func (a *Analyzer) analyzeTopLevelMatchPattern(pattern ast.MatchPattern, enumType *EnumType, valueExpr ast.Expr, scope *Scope, index int, armCount int, covered map[string]bool) bool {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		if index != armCount-1 {
			a.errorf(p.Pos(), "wildcard match arm must be the final arm")
		}
		return true
	case *ast.MatchVariantPattern:
		if p.EnumName != enumType.Name {
			a.errorf(p.Pos(), "match arm expects enum %q, got %q", enumType.Name, p.EnumName)
			return false
		}
		variant, ok := enumType.Variant(p.Variant)
		if !ok {
			a.errorf(p.Pos(), "enum %q has no variant %q", enumType.Name, p.Variant)
			return false
		}
		qualified := enumType.Name + "." + variant.Name
		if covered != nil {
			covered[variant.Name] = true
		}
		if enumType.Packed {
			a.bindMatchedPackedVariantView(valueExpr, &PackedVariantViewType{Enum: enumType, Variant: variant})
		}
		orderedArgs := a.resolveMatchPatternArgs(p, variant, qualified, false)
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			payloadExpr, _ := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
			a.analyzeNestedMatchPattern(arg.Pattern, variant.Payload[i], payloadExpr, scope)
		}
		return false
	case *ast.MatchBindPattern:
		a.errorf(p.Pos(), "top-level match arm must use %q variants or _", enumType.Name)
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported match pattern %T", pattern)
		return false
	}
}

func (a *Analyzer) analyzeTopLevelTreeMatchPattern(pattern ast.MatchPattern, treeType *TreeCategoryType, valueExpr ast.Expr, scope *Scope, index int, armCount int, covered map[string]bool) bool {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		if index != armCount-1 {
			a.errorf(p.Pos(), "wildcard match arm must be the final arm")
		}
		return true
	case *ast.MatchVariantPattern:
		if p.EnumName != treeType.Name {
			a.errorf(p.Pos(), "match arm expects tree category %q, got %q", treeType.Name, p.EnumName)
			return false
		}
		variant, ok := treeType.Variant(p.Variant)
		if !ok {
			a.errorf(p.Pos(), "tree category %q has no variant %q", treeType.Name, p.Variant)
			return false
		}
		qualified := treeType.Name + "." + variant.Name
		if covered != nil {
			covered[variant.Name] = true
		}
		a.bindRefinedExprType(scope, valueExpr, treeType.VariantViewType(variant))
		orderedArgs := a.resolveMatchPatternArgs(p, variant, qualified, false)
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			payloadExpr, _ := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
			a.analyzeNestedMatchPattern(arg.Pattern, variant.Payload[i], payloadExpr, scope)
		}
		return false
	case *ast.MatchBindPattern:
		a.errorf(p.Pos(), "top-level match arm must use %q variants or _", treeType.Name)
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported match pattern %T", pattern)
		return false
	}
}

func (a *Analyzer) analyzeTopLevelStringMatchPattern(pattern ast.MatchPattern, valueType Type, scope *Scope, index int, armCount int) bool {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		if index != armCount-1 {
			a.errorf(p.Pos(), "wildcard match arm must be the final arm")
		}
		return true
	case *ast.MatchStringLiteralPattern:
		if !isStringMatchableType(valueType) {
			a.errorf(p.Pos(), "match arm expects a string value, got %s", valueType.String())
		}
		return false
	case *ast.MatchBindPattern:
		a.errorf(p.Pos(), "top-level string match arm must use a string literal or _")
		return false
	case *ast.MatchVariantPattern:
		a.errorf(p.Pos(), "top-level string match arm must use a string literal or _")
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported match pattern %T", pattern)
		return false
	}
}

func (a *Analyzer) analyzeTopLevelStructMatchPattern(pattern ast.MatchPattern, valueType Type, valueExpr ast.Expr, scope *Scope, index int, armCount int) bool {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		if index != armCount-1 {
			a.errorf(p.Pos(), "wildcard match arm must be the final arm")
		}
		return true
	case *ast.MatchStructPattern:
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, valueType)
		if !ok {
			return false
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			fieldExpr := &ast.FieldExpr{Position: arg.Position, Object: valueExpr, Field: fields[i].Name}
			a.analyzeNestedMatchPattern(arg.Pattern, fields[i].Type, fieldExpr, scope)
		}
		return false
	case *ast.MatchBindPattern:
		a.errorf(p.Pos(), "top-level struct match arm must use Struct(...), or _")
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported top-level struct match pattern %T", pattern)
		return false
	}
}

func (a *Analyzer) reportNonExhaustiveStructMatchExpr(pos lexer.Pos, hasWildcard bool) {
	if hasWildcard {
		return
	}
	a.errorf(pos, "non-exhaustive struct match expression; add a final _ arm")
}

func (a *Analyzer) bindPackedVariantViewAliasForBody(pattern ast.MatchPattern, enumType *EnumType, valueExpr ast.Expr, body []ast.Stmt, scope *Scope) {
	if a == nil || scope == nil || enumType == nil || !enumType.Packed || !matchBodyReferencesVariantFields(body, valueExpr) {
		return
	}
	variantPattern, ok := pattern.(*ast.MatchVariantPattern)
	if !ok || variantPattern == nil || variantPattern.EnumName != enumType.Name {
		return
	}
	variant, ok := enumType.Variant(variantPattern.Variant)
	if !ok || variant == nil {
		return
	}
	ident, ok := unwrapPackedVariantViewExpr(valueExpr).(*ast.Ident)
	if !ok || ident == nil {
		return
	}
	if _, exists := scope.Symbols[ident.Name]; exists {
		return
	}
	var aliasOf *Symbol
	if scope.Parent != nil {
		aliasOf, _ = scope.Parent.Lookup(ident.Name)
	}
	viewType := &PackedVariantViewType{Enum: enumType, Variant: variant}
	sym := &Symbol{Name: ident.Name, Kind: SymbolLocal, Type: viewType, Node: variantPattern, AliasOf: aliasOf, Mutable: false}
	a.defineLocalInScope(scope, sym, variantPattern.Pos())
	if a.currentPackedVariantViews != nil {
		a.currentPackedVariantViews[sym] = viewType
	}
}

func matchBodyReferencesVariantFields(stmts []ast.Stmt, valueExpr ast.Expr) bool {
	ident, ok := unwrapPackedVariantViewExpr(valueExpr).(*ast.Ident)
	if !ok || ident == nil || ident.Name == "" {
		return false
	}
	for _, stmt := range stmts {
		if stmtReferencesVariantFields(stmt, ident.Name) {
			return true
		}
	}
	return false
}

func stmtReferencesVariantFields(stmt ast.Stmt, name string) bool {
	if stmt == nil || name == "" {
		return false
	}
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		return exprReferencesVariantFields(n.Value, name)
	case *ast.MoveBindStmt:
		return exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Store, name)
	case *ast.OpenStmt:
		if exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Store, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.ViewStmt:
		if exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Store, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.AssignStmt:
		return exprReferencesVariantFields(n.Target, name) || exprReferencesVariantFields(n.Value, name)
	case *ast.AugAssignStmt:
		return exprReferencesVariantFields(n.Target, name) || exprReferencesVariantFields(n.Value, name)
	case *ast.AsRefAssignStmt:
		return exprReferencesVariantFields(n.Target, name) || exprReferencesVariantFields(n.Value, name)
	case *ast.ReturnStmt:
		return exprReferencesVariantFields(n.Value, name)
	case *ast.IfStmt:
		if exprReferencesVariantFields(n.Cond, name) {
			return true
		}
		for _, inner := range n.Then {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
		for _, elif := range n.Elifs {
			if exprReferencesVariantFields(elif.Cond, name) {
				return true
			}
			for _, inner := range elif.Body {
				if stmtReferencesVariantFields(inner, name) {
					return true
				}
			}
		}
		for _, inner := range n.Else {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.MatchStmt:
		if exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Store, name) {
			return true
		}
		for _, arm := range n.Arms {
			for _, inner := range arm.Body {
				if stmtReferencesVariantFields(inner, name) {
					return true
				}
			}
		}
	case *ast.InStoreStmt:
		if exprReferencesVariantFields(n.Store, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.CanStmt:
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.PoolStmt:
		if exprReferencesVariantFields(n.Workers, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.LockStmt:
		if exprReferencesVariantFields(n.Mutex, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.WhileStmt:
		if exprReferencesVariantFields(n.Cond, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.ForStmt:
		if exprReferencesVariantFields(n.Start, name) || exprReferencesVariantFields(n.End, name) || exprReferencesVariantFields(n.Step, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.IterForStmt:
		if exprReferencesVariantFields(n.Source, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.ParallelForStmt:
		if exprReferencesVariantFields(n.Source, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.PanicStmt:
		return exprReferencesVariantFields(n.Message, name)
	case *ast.ExprStmt:
		return exprReferencesVariantFields(n.Expr, name)
	case *ast.StaticIfStmt:
		for _, inner := range n.Then {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
		for _, elif := range n.Elifs {
			for _, inner := range elif.Body {
				if stmtReferencesVariantFields(inner, name) {
					return true
				}
			}
		}
		for _, inner := range n.Else {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.StaticErrorStmt:
		return exprReferencesVariantFields(n.Message, name)
	case *ast.DiscardStmt:
		return exprReferencesVariantFields(n.Value, name)
	}
	return false
}

func exprReferencesVariantFields(expr ast.Expr, name string) bool {
	if expr == nil || name == "" {
		return false
	}
	switch n := expr.(type) {
	case *ast.Ident:
		return false
	case *ast.FieldExpr:
		if ident, ok := unwrapPackedVariantViewExpr(n.Object).(*ast.Ident); ok && ident != nil && ident.Name == name {
			return true
		}
		return exprReferencesVariantFields(n.Object, name)
	case *ast.BinaryExpr:
		return exprReferencesVariantFields(n.Left, name) || exprReferencesVariantFields(n.Right, name)
	case *ast.UnaryExpr:
		return exprReferencesVariantFields(n.Operand, name)
	case *ast.CallExpr:
		if exprReferencesVariantFields(n.Func, name) {
			return true
		}
		for _, arg := range n.Args {
			if exprReferencesVariantFields(arg, name) {
				return true
			}
		}
	case *ast.IndexExpr:
		return exprReferencesVariantFields(n.Object, name) || exprReferencesVariantFields(n.Index, name)
	case *ast.SliceExpr:
		return exprReferencesVariantFields(n.Object, name) || exprReferencesVariantFields(n.Start, name) || exprReferencesVariantFields(n.End, name)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			if exprReferencesVariantFields(elem, name) {
				return true
			}
		}
	case *ast.CastExpr:
		return exprReferencesVariantFields(n.Operand, name)
	case *ast.TernaryExpr:
		return exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Cond, name) || exprReferencesVariantFields(n.Alt, name)
	case *ast.AddrOfExpr:
		return exprReferencesVariantFields(n.Operand, name)
	case *ast.MoveExpr:
		return exprReferencesVariantFields(n.Operand, name)
	case *ast.SpecializeExpr:
		return exprReferencesVariantFields(n.Operand, name)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			if exprReferencesVariantFields(arg, name) {
				return true
			}
		}
	case *ast.ParenExpr:
		return exprReferencesVariantFields(n.Inner, name)
	case *ast.RaiseExpr:
		return exprReferencesVariantFields(n.Error, name)
	case *ast.TryExpr:
		return exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Fallback, name)
	case *ast.UnwrapElseExpr:
		return exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Fallback, name)
	case *ast.AllocExpr:
		return exprReferencesVariantFields(n.Owner, name) || exprReferencesVariantFields(n.Value, name)
	case *ast.CanExpr:
		return exprReferencesVariantFields(n.Expr, name)
	case *ast.MatchExpr:
		if exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Store, name) {
			return true
		}
		for _, arm := range n.Arms {
			for _, inner := range arm.Body {
				if stmtReferencesVariantFields(inner, name) {
					return true
				}
			}
		}
	case *ast.VisitExpr:
		if exprReferencesVariantFields(n.Value, name) {
			return true
		}
		for _, arm := range n.Arms {
			for _, inner := range arm.Body {
				if stmtReferencesVariantFields(inner, name) {
					return true
				}
			}
		}
	case *ast.FoldExpr:
		if exprReferencesVariantFields(n.Value, name) {
			return true
		}
		for _, arm := range n.Arms {
			for _, inner := range arm.Body {
				if stmtReferencesVariantFields(inner, name) {
					return true
				}
			}
		}
	}
	return false
}

func (a *Analyzer) analyzeNestedMatchPattern(pattern ast.MatchPattern, expected Type, valueExpr ast.Expr, scope *Scope) {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return
	case *ast.MatchBindPattern:
		sym := &Symbol{Name: p.Name, Kind: SymbolLocal, Type: expected, Node: p, Mutable: false}
		a.defineLocal(sym, p.Pos())
		a.recordValueBinding(sym, valueExpr)
		a.recordBorrowedOwnerRefBinding(sym, valueExpr)
		a.recordFunctionValueBinding(sym, valueExpr)
		a.recordImmutableSymbolOptimizationFacts(sym, valueExpr)
		a.recordRegionRefBinding(sym, valueExpr)
	case *ast.MatchVariantPattern:
		switch variantBase := expected.(type) {
		case *EnumType:
			if p.EnumName != variantBase.Name {
				a.errorf(p.Pos(), "nested match pattern expects enum %q, got %q", variantBase.Name, p.EnumName)
				return
			}
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				a.errorf(p.Pos(), "enum %q has no variant %q", variantBase.Name, p.Variant)
				return
			}
			orderedArgs := a.resolveMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				payloadExpr, _ := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
				a.analyzeNestedMatchPattern(arg.Pattern, variant.Payload[i], payloadExpr, scope)
			}
		case *TreeCategoryType:
			if p.EnumName != variantBase.Name {
				a.errorf(p.Pos(), "nested match pattern expects tree category %q, got %q", variantBase.Name, p.EnumName)
				return
			}
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				a.errorf(p.Pos(), "tree category %q has no variant %q", variantBase.Name, p.Variant)
				return
			}
			orderedArgs := a.resolveMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				payloadExpr, _ := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
				a.analyzeNestedMatchPattern(arg.Pattern, variant.Payload[i], payloadExpr, scope)
			}
		default:
			a.errorf(p.Pos(), "nested variant pattern %q requires an enum or tree-category payload, got %s", p.EnumName+"."+p.Variant, expected.String())
		}
	case *ast.MatchStringLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), &ast.StringLit{Position: p.Position, Value: p.Value}, expected, "nested literal match pattern")
	case *ast.MatchLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), p.Value, expected, "nested literal match pattern")
	case *ast.MatchStructPattern:
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			fieldExpr := &ast.FieldExpr{Position: arg.Position, Object: valueExpr, Field: fields[i].Name}
			a.analyzeNestedMatchPattern(arg.Pattern, fields[i].Type, fieldExpr, scope)
		}
	default:
		a.errorf(pattern.Pos(), "unsupported nested match pattern %T", pattern)
	}
}

func (a *Analyzer) analyzeLiteralMatchPatternExpr(pos lexer.Pos, literalExpr ast.Expr, expected Type, context string) {
	if literalExpr == nil || expected == nil {
		return
	}
	actual := a.analyzeValueExpr(literalExpr, expected)
	if runtimeStringComparable(expected, actual) {
		return
	}
	if IsNumericType(expected) && IsNumericType(actual) {
		return
	}
	if IsBoolType(expected) && IsBoolType(actual) {
		return
	}
	if (IsNullType(actual) && isRefLike(expected)) || (IsNullType(expected) && isRefLike(actual)) {
		return
	}
	if AssignableTo(expected, actual) || AssignableTo(actual, expected) || refsComparableIgnoringMutability(expected, actual) {
		return
	}
	a.errorf(pos, "%s cannot compare %s against %s", context, actual.String(), expected.String())
}

func (a *Analyzer) resolveMatchPatternArgs(pattern *ast.MatchVariantPattern, variant *EnumVariant, qualified string, nested bool) []*ast.MatchPatternArg {
	ordered := make([]*ast.MatchPatternArg, len(variant.Payload))
	if len(pattern.Args) == 0 {
		pattern.ResolvedArgs = ordered
		return ordered
	}
	namedCount := 0
	for i := range pattern.Args {
		if pattern.Args[i].Name != "" {
			namedCount++
		}
	}
	if namedCount != 0 && namedCount != len(pattern.Args) {
		a.errorf(pattern.Pos(), "%s cannot mix positional and named payload patterns", matchPatternContext(qualified, nested))
	}
	if namedCount == 0 {
		if len(pattern.Args) != len(variant.Payload) {
			a.errorf(pattern.Pos(), "%s expects %d payload patterns, got %d", matchPatternContext(qualified, nested), len(variant.Payload), len(pattern.Args))
		}
		limit := len(pattern.Args)
		if len(ordered) < limit {
			limit = len(ordered)
		}
		for i := 0; i < limit; i++ {
			ordered[i] = &pattern.Args[i]
		}
		pattern.ResolvedArgs = ordered
		return ordered
	}
	if !variant.HasNamedPayloads() {
		a.errorf(pattern.Pos(), "%s does not declare named payload fields", matchPatternContext(qualified, nested))
		pattern.ResolvedArgs = ordered
		return ordered
	}
	seen := map[int]lexer.Pos{}
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		index, ok := variant.PayloadIndex(arg.Name)
		if !ok {
			a.errorf(arg.Position, "%s has no payload field %q", matchPatternContext(qualified, nested), arg.Name)
			continue
		}
		if prev, exists := seen[index]; exists {
			a.errorf(arg.Position, "%s payload field %q is matched more than once (first at %s:%d:%d)", matchPatternContext(qualified, nested), arg.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[index] = arg.Position
		ordered[index] = arg
	}
	missing := make([]string, 0)
	for i := range ordered {
		if ordered[i] == nil {
			missing = append(missing, variant.PayloadLabel(i))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		a.errorf(pattern.Pos(), "%s is missing named payload patterns for: %s", matchPatternContext(qualified, nested), strings.Join(missing, ", "))
	}
	pattern.ResolvedArgs = ordered
	return ordered
}

func matchPatternContext(qualified string, nested bool) string {
	if nested {
		return "nested match arm " + strconvQuote(qualified)
	}
	return "match arm " + strconvQuote(qualified)
}

func (a *Analyzer) matchCoversAllVariants(variantBase Type, covered map[string]bool, hasWildcard bool) bool {
	if hasWildcard {
		return true
	}
	switch tt := variantBase.(type) {
	case *EnumType:
		if tt == nil {
			return false
		}
		for _, variant := range tt.Variants {
			if !covered[variant.Name] {
				return false
			}
		}
		return true
	case *TreeCategoryType:
		if tt == nil {
			return false
		}
		for _, variant := range tt.Variants {
			if !covered[variant.Name] {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func strconvQuote(s string) string {
	return "\"" + s + "\""
}

func (a *Analyzer) reportNonExhaustiveMatch(pos lexer.Pos, variantBase Type, covered map[string]bool, hasWildcard bool) {
	if hasWildcard {
		return
	}
	switch tt := variantBase.(type) {
	case *EnumType:
		if tt == nil {
			return
		}
		missing := make([]string, 0)
		for _, variant := range tt.Variants {
			if !covered[variant.Name] {
				missing = append(missing, tt.Name+"."+variant.Name)
			}
		}
		if len(missing) == 0 {
			return
		}
		a.errorf(pos, "non-exhaustive match over %q; missing variants: %s", tt.Name, strings.Join(missing, ", "))
	case *TreeCategoryType:
		if tt == nil {
			return
		}
		missing := make([]string, 0)
		for _, variant := range tt.Variants {
			if !covered[variant.Name] {
				missing = append(missing, tt.Name+"."+variant.Name)
			}
		}
		if len(missing) == 0 {
			return
		}
		a.errorf(pos, "non-exhaustive match over %q; missing variants: %s", tt.Name, strings.Join(missing, ", "))
	}
}

func (a *Analyzer) reportNonExhaustiveStringMatchExpr(pos lexer.Pos, hasWildcard bool) {
	if hasWildcard {
		return
	}
	a.errorf(pos, "non-exhaustive string match expression; add a final _ arm")
}

func (a *Analyzer) analyzeBlock(stmts []ast.Stmt) {
	saved := a.currentScope
	a.currentScope = NewScope(saved)
	for _, stmt := range stmts {
		a.analyzeStmt(stmt)
	}
	a.currentScope = saved
}

func (a *Analyzer) analyzeBlockInScope(stmts []ast.Stmt, scope *Scope) {
	saved := a.currentScope
	a.currentScope = scope
	for _, stmt := range stmts {
		a.analyzeStmt(stmt)
	}
	a.currentScope = saved
}

func (a *Analyzer) analyzeBlockWithRegionClone(stmts []ast.Stmt, scope *Scope) {
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedRegionRefs := a.currentRegionRefs
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	a.currentRegions = a.cloneRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentRegionRefs = a.cloneRegionRefStates()
	a.currentPackedVariantViews = a.clonePackedVariantViewBindings()
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	a.analyzeBlockInScope(stmts, scope)
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
}

type affineFlowSnapshot struct {
	Affine                map[affineValueKey]affineValueState
	BorrowedOwnerRefs     map[*Symbol]borrowedOwnerRefState
	FunctionValues        map[*Symbol]*FuncType
	SpecializedValueTypes map[*Symbol]Type
	ValueBindings         map[*Symbol]ast.Expr
}

type borrowedOwnerRefSummaryTarget struct {
	ParamIndex int
	Path       []borrowReturnAnnotationStep
}

type borrowedOwnerRefSummary struct {
	HasDirect bool
	Direct    borrowedOwnerRefSummaryTarget
	Fields    map[string]borrowedOwnerRefSummary
}

func (a *Analyzer) analyzeBlockWithAffineClone(stmts []ast.Stmt, scope *Scope) affineFlowSnapshot {
	return a.analyzeBlockWithAffineClonePrepared(stmts, scope, nil)
}

func (a *Analyzer) analyzeBlockWithConditionAffineClone(stmts []ast.Stmt, parent *Scope, cond ast.Expr, truthy bool) affineFlowSnapshot {
	scope := a.refinedScopeForCondition(parent, cond, truthy)
	return a.analyzeBlockWithAffineClonePrepared(stmts, scope, func() {
		a.applyConditionRefinementsInternal(scope, cond, truthy, true)
	})
}

func unwrapDirectStructIsCondition(expr ast.Expr) (*ast.BinaryExpr, ast.Expr, *ast.MatchStructPattern, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return unwrapDirectStructIsCondition(n.Inner)
	case *ast.BinaryExpr:
		if n.Op != lexer.TOKEN_IS {
			return nil, nil, nil, false
		}
		testExpr, ok := n.Right.(*ast.StructTestExpr)
		if !ok || testExpr == nil || testExpr.Pattern == nil {
			return nil, nil, nil, false
		}
		return n, n.Left, testExpr.Pattern, true
	default:
		return nil, nil, nil, false
	}
}

func (a *Analyzer) collectConditionStructPatternBindingTypes(pattern ast.MatchPattern, expected Type, out map[string]Type) {
	if a == nil || pattern == nil || expected == nil || out == nil {
		return
	}
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern:
		return
	case *ast.MatchBindPattern:
		if p.Name == "" || p.Name == "_" {
			return
		}
		if prev, ok := out[p.Name]; ok {
			if !SameType(prev, expected) {
				a.errorf(p.Pos(), "condition binding %q has inconsistent types %s and %s", p.Name, prev.String(), expected.String())
			}
			return
		}
		out[p.Name] = expected
	case *ast.MatchStructPattern:
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			a.collectConditionStructPatternBindingTypes(arg.Pattern, fields[i].Type, out)
		}
	case *ast.MatchVariantPattern:
		switch variantBase := expected.(type) {
		case *EnumType:
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				return
			}
			orderedArgs := a.resolveMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				a.collectConditionStructPatternBindingTypes(arg.Pattern, variant.Payload[i], out)
			}
		case *TreeCategoryType:
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				return
			}
			orderedArgs := a.resolveMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				a.collectConditionStructPatternBindingTypes(arg.Pattern, variant.Payload[i], out)
			}
		}
	}
}

func (a *Analyzer) collectGuaranteedTruthyConditionBindingTypes(expr ast.Expr) map[string]Type {
	if a == nil || expr == nil {
		return nil
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.collectGuaranteedTruthyConditionBindingTypes(n.Inner)
	case *ast.UnaryExpr:
		return nil
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			left := a.collectGuaranteedTruthyConditionBindingTypes(n.Left)
			right := a.collectGuaranteedTruthyConditionBindingTypes(n.Right)
			if len(left) == 0 {
				return right
			}
			if len(right) == 0 {
				return left
			}
			out := make(map[string]Type, len(left)+len(right))
			for name, typ := range left {
				out[name] = typ
			}
			for name, typ := range right {
				if prev, ok := out[name]; ok && !SameType(prev, typ) {
					a.errorf(n.Pos(), "condition binding %q has inconsistent types %s and %s", name, prev.String(), typ.String())
					continue
				}
				out[name] = typ
			}
			return out
		case lexer.TOKEN_OR:
			left := a.collectGuaranteedTruthyConditionBindingTypes(n.Left)
			right := a.collectGuaranteedTruthyConditionBindingTypes(n.Right)
			if len(left) == 0 || len(right) == 0 {
				return nil
			}
			out := map[string]Type{}
			for name, leftType := range left {
				rightType, ok := right[name]
				if !ok || !SameType(leftType, rightType) {
					continue
				}
				out[name] = leftType
			}
			if len(out) == 0 {
				return nil
			}
			return out
		}
	}
	_, valueExpr, pattern, ok := unwrapDirectStructIsCondition(expr)
	if !ok || valueExpr == nil || pattern == nil {
		return nil
	}
	valueType := a.exprTypes[valueExpr]
	if valueType == nil {
		valueType = a.analyzeExpr(valueExpr)
	}
	out := map[string]Type{}
	a.collectConditionStructPatternBindingTypes(pattern, valueType, out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a *Analyzer) collectPossibleTruthyConditionBindingTypes(expr ast.Expr) map[string]Type {
	if a == nil || expr == nil {
		return nil
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.collectPossibleTruthyConditionBindingTypes(n.Inner)
	case *ast.UnaryExpr:
		return nil
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR:
			left := a.collectPossibleTruthyConditionBindingTypes(n.Left)
			right := a.collectPossibleTruthyConditionBindingTypes(n.Right)
			if len(left) == 0 {
				return right
			}
			if len(right) == 0 {
				return left
			}
			out := make(map[string]Type, len(left)+len(right))
			for name, typ := range left {
				out[name] = typ
			}
			for name, typ := range right {
				if _, ok := out[name]; !ok {
					out[name] = typ
				}
			}
			return out
		}
	}
	_, valueExpr, pattern, ok := unwrapDirectStructIsCondition(expr)
	if !ok || valueExpr == nil || pattern == nil {
		return nil
	}
	valueType := a.exprTypes[valueExpr]
	if valueType == nil {
		valueType = a.analyzeExpr(valueExpr)
	}
	out := map[string]Type{}
	a.collectConditionStructPatternBindingTypes(pattern, valueType, out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a *Analyzer) recordConditionalBindingHints(scope *Scope, expr ast.Expr, truthy bool) {
	if a == nil || scope == nil || !truthy || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.recordConditionalBindingHints(scope, n.Inner, truthy)
		return
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			a.recordConditionalBindingHints(scope, n.Operand, !truthy)
		}
		return
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			a.recordConditionalBindingHints(scope, n.Left, true)
			a.recordConditionalBindingHints(scope, n.Right, true)
			return
		case lexer.TOKEN_OR:
			leftPossible := a.collectPossibleTruthyConditionBindingTypes(n.Left)
			rightPossible := a.collectPossibleTruthyConditionBindingTypes(n.Right)
			guaranteed := a.collectGuaranteedTruthyConditionBindingTypes(n)
			allNames := map[string]bool{}
			for name := range leftPossible {
				allNames[name] = true
			}
			for name := range rightPossible {
				allNames[name] = true
			}
			for name := range allNames {
				if _, ok := guaranteed[name]; ok {
					continue
				}
				leftType, leftOK := leftPossible[name]
				rightType, rightOK := rightPossible[name]
				hint := ""
				switch {
				case leftOK && rightOK && !SameType(leftType, rightType):
					hint = fmt.Sprintf("identifier %q is not available here because truthy `or` branches do not agree on that binding: left branch binds it as %s, while right branch binds it as %s; use different bind names or restructure the condition", name, leftType.String(), rightType.String())
				case leftOK && !rightOK:
					hint = fmt.Sprintf("identifier %q is not available here because truthy `or` branches do not agree on that binding: left branch binds it as %s, while right branch does not bind it", name, leftType.String())
				case !leftOK && rightOK:
					hint = fmt.Sprintf("identifier %q is not available here because truthy `or` branches do not agree on that binding: left branch does not bind it, while right branch binds it as %s", name, rightType.String())
				case leftOK || rightOK:
					hint = fmt.Sprintf("identifier %q is not available here because truthy `or` condition bindings are only introduced when every successful branch binds that name", name)
				}
				if hint != "" {
					scope.ConditionalBindingHints[name] = hint
				}
			}
			a.recordConditionalBindingHints(scope, n.Left, true)
			a.recordConditionalBindingHints(scope, n.Right, true)
			return
		}
	}
}

func (a *Analyzer) bindConditionPatternLocals(scope *Scope, expr ast.Expr, truthy bool) {
	if a == nil || scope == nil || !truthy {
		return
	}
	a.recordConditionalBindingHints(scope, expr, truthy)
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.bindConditionPatternLocals(scope, n.Inner, truthy)
		return
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			a.bindConditionPatternLocals(scope, n.Operand, !truthy)
		}
		return
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			a.bindConditionPatternLocals(scope, n.Left, true)
			a.bindConditionPatternLocals(scope, n.Right, true)
			return
		case lexer.TOKEN_OR:
			for name, typ := range a.collectGuaranteedTruthyConditionBindingTypes(n) {
				sym := &Symbol{Name: name, Kind: SymbolLocal, Type: typ, Node: n, Mutable: false}
				a.defineLocalInScope(scope, sym, n.Pos())
			}
			return
		}
	}
	_, valueExpr, pattern, ok := unwrapDirectStructIsCondition(expr)
	if !ok || valueExpr == nil || pattern == nil {
		return
	}
	valueType := a.exprTypes[valueExpr]
	if valueType == nil {
		valueType = a.analyzeExprInScope(valueExpr, scope)
	}
	a.bindConditionStructPatternLocals(scope, pattern, valueType, valueExpr)
}

func (a *Analyzer) bindConditionStructPatternLocals(scope *Scope, pattern ast.MatchPattern, expected Type, valueExpr ast.Expr) {
	if a == nil || scope == nil || pattern == nil || expected == nil || valueExpr == nil {
		return
	}
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern:
		return
	case *ast.MatchBindPattern:
		if p.Name == "_" {
			return
		}
		sym := &Symbol{Name: p.Name, Kind: SymbolLocal, Type: expected, Node: p, Mutable: false}
		a.defineLocalInScope(scope, sym, p.Pos())
		a.recordValueBinding(sym, valueExpr)
		a.recordBorrowedOwnerRefBinding(sym, valueExpr)
		a.recordFunctionValueBinding(sym, valueExpr)
		a.recordImmutableSymbolOptimizationFacts(sym, valueExpr)
		a.recordRegionRefBinding(sym, valueExpr)
	case *ast.MatchStructPattern:
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			fieldExpr := &ast.FieldExpr{Position: arg.Position, Object: valueExpr, Field: fields[i].Name}
			a.bindConditionStructPatternLocals(scope, arg.Pattern, fields[i].Type, fieldExpr)
		}
	case *ast.MatchVariantPattern:
		switch variantBase := expected.(type) {
		case *EnumType:
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				return
			}
			orderedArgs := a.resolveMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				payloadExpr, ok := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
				if !ok || payloadExpr == nil {
					continue
				}
				a.bindConditionStructPatternLocals(scope, arg.Pattern, variant.Payload[i], payloadExpr)
			}
		case *TreeCategoryType:
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				return
			}
			orderedArgs := a.resolveMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				payloadExpr, ok := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
				if !ok || payloadExpr == nil {
					continue
				}
				a.bindConditionStructPatternLocals(scope, arg.Pattern, variant.Payload[i], payloadExpr)
			}
		}
	}
}

func (a *Analyzer) analyzeBlockWithAffineClonePrepared(stmts []ast.Stmt, scope *Scope, prepare func()) affineFlowSnapshot {
	savedAffine := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	savedPackedVariantViews := a.currentPackedVariantViews
	a.currentAffineValues = a.cloneAffineValueStates()
	a.currentBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
	a.currentFunctionValues = a.cloneFunctionValueBindings()
	a.currentSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
	a.currentValueBindings = a.cloneValueBindings()
	if prepare != nil {
		prepare()
	}
	a.analyzeBlockWithRegionClone(stmts, scope)
	snapshot := affineFlowSnapshot{Affine: a.cloneAffineValueStates(), BorrowedOwnerRefs: a.cloneBorrowedOwnerRefBindings(), FunctionValues: a.cloneFunctionValueBindings(), SpecializedValueTypes: a.cloneSpecializedValueTypeBindings(), ValueBindings: a.cloneValueBindings()}
	a.currentAffineValues = savedAffine
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	a.currentPackedVariantViews = savedPackedVariantViews
	return snapshot
}

func (a *Analyzer) cloneRegionStates() map[*Symbol]regionState {
	if a.currentRegions == nil {
		return nil
	}
	cloned := make(map[*Symbol]regionState, len(a.currentRegions))
	for sym, state := range a.currentRegions {
		cloned[sym] = state
	}
	return cloned
}

func (a *Analyzer) cloneRegionMarkStates() map[*Symbol]regionMarkState {
	if a.currentRegionMarks == nil {
		return nil
	}
	cloned := make(map[*Symbol]regionMarkState, len(a.currentRegionMarks))
	for sym, state := range a.currentRegionMarks {
		cloned[sym] = state
	}
	return cloned
}

func (a *Analyzer) cloneRegionRefStates() map[*Symbol]regionRefState {
	if a.currentRegionRefs == nil {
		return nil
	}
	cloned := make(map[*Symbol]regionRefState, len(a.currentRegionRefs))
	for sym, state := range a.currentRegionRefs {
		cloned[sym] = cloneRegionRefStateSharedFields(state)
	}
	return cloned
}

func cloneRegionDependencyStates(src map[*Symbol]regionDependencyState) map[*Symbol]regionDependencyState {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[*Symbol]regionDependencyState, len(src))
	for region, dep := range src {
		cloned[region] = dep
	}
	return cloned
}

func clonePackedStoreDependencyStates(src map[*Symbol]packedStoreDependencyState) map[*Symbol]packedStoreDependencyState {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[*Symbol]packedStoreDependencyState, len(src))
	for store, dep := range src {
		cloned[store] = dep
	}
	return cloned
}

func cloneRegionParamDeps(src IntBitSet) IntBitSet {
	return src.Clone()
}

func hasRegionParamDependencies(state regionRefState) bool {
	return state.HasDirectParamDep || !state.ParamDeps.IsEmpty()
}

func regionRefStateHasParamDep(state regionRefState, index int) bool {
	if state.HasDirectParamDep && state.DirectParamDep == index {
		return true
	}
	return state.ParamDeps.Contains(index)
}

func regionRefStateParamDepCount(state regionRefState) int {
	if !state.HasDirectParamDep {
		return state.ParamDeps.Count()
	}
	if !state.ParamDeps.Contains(state.DirectParamDep) {
		return state.ParamDeps.Count() + 1
	}
	return state.ParamDeps.Count()
}

func forEachRegionParamDep(state regionRefState, fn func(int)) {
	if fn == nil {
		return
	}
	if state.HasDirectParamDep {
		fn(state.DirectParamDep)
	}
	state.ParamDeps.ForEach(func(index int) {
		if state.HasDirectParamDep && index == state.DirectParamDep {
			return
		}
		fn(index)
	})
}

func appendRegionParamDep(state *regionRefState, index int) {
	if state == nil || index < 0 {
		return
	}
	if state.HasDirectParamDep {
		if state.DirectParamDep == index {
			return
		}
		state.ParamDeps.Add(index)
		return
	}
	if !state.ParamDeps.IsEmpty() {
		state.ParamDeps.Add(index)
		return
	}
	state.DirectParamDep = index
	state.HasDirectParamDep = true
}

func cloneRegionRefFields(src map[string]regionRefState) map[string]regionRefState {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]regionRefState, len(src))
	for name, fieldState := range src {
		cloned[name] = fieldState
	}
	return cloned
}

func cloneRegionRefState(state regionRefState) regionRefState {
	return regionRefState{
		Deps:                    cloneRegionDependencyStates(state.Deps),
		StoreDeps:               clonePackedStoreDependencyStates(state.StoreDeps),
		DirectParamDep:          state.DirectParamDep,
		HasDirectParamDep:       state.HasDirectParamDep,
		ParamDeps:               cloneRegionParamDeps(state.ParamDeps),
		Fields:                  cloneRegionRefFields(state.Fields),
		PackedStoreSummary:      state.PackedStoreSummary,
		PackedStoreSummaryKnown: state.PackedStoreSummaryKnown,
	}
}

func cloneRegionRefStateSharedFields(state regionRefState) regionRefState {
	return regionRefState{
		Deps:                    state.Deps,
		StoreDeps:               state.StoreDeps,
		DirectParamDep:          state.DirectParamDep,
		HasDirectParamDep:       state.HasDirectParamDep,
		ParamDeps:               state.ParamDeps,
		Fields:                  state.Fields,
		PackedStoreSummary:      state.PackedStoreSummary,
		PackedStoreSummaryKnown: state.PackedStoreSummaryKnown,
	}
}

func cloneRegionRefStateShallowFields(state regionRefState) regionRefState {
	return withPackedStoreProvenanceSummary(regionRefState{
		Deps:              state.Deps,
		StoreDeps:         state.StoreDeps,
		DirectParamDep:    state.DirectParamDep,
		HasDirectParamDep: state.HasDirectParamDep,
		ParamDeps:         state.ParamDeps,
	})
}

func withPackedStoreProvenanceSummary(state regionRefState) regionRefState {
	if state.PackedStoreSummaryKnown {
		return state
	}
	state.PackedStoreSummary = summarizePackedStoreProvenance(state)
	state.PackedStoreSummaryKnown = true
	return state
}

func hasRegionDependencies(state regionRefState) bool {
	return len(state.Deps) != 0 || len(state.StoreDeps) != 0
}

func hasRegionProvenance(state regionRefState) bool {
	return hasRegionDependencies(state) || hasRegionParamDependencies(state) || len(state.Fields) != 0
}

func regionRefStateFromDependency(region *Symbol, generation int) regionRefState {
	if region == nil {
		return regionRefState{}
	}
	return withPackedStoreProvenanceSummary(regionRefState{
		Deps: map[*Symbol]regionDependencyState{
			region: {
				Generation: generation,
				Valid:      true,
			},
		},
	})
}

func regionRefStateFromPackedStoreDependency(store *Symbol, storeType *PackedEnumStoreType) regionRefState {
	if store == nil || storeType == nil {
		return regionRefState{}
	}
	return withPackedStoreProvenanceSummary(regionRefState{
		StoreDeps: map[*Symbol]packedStoreDependencyState{
			store: {Type: storeType},
		},
	})
}

func regionRefStateFromParamDependency(index int) regionRefState {
	if index < 0 {
		return regionRefState{}
	}
	return withPackedStoreProvenanceSummary(regionRefState{
		DirectParamDep:    index,
		HasDirectParamDep: true,
	})
}

func mergeRegionRefStates(states ...regionRefState) (regionRefState, bool) {
	merged := regionRefState{PackedStoreSummaryKnown: true}
	for _, state := range states {
		if !hasRegionProvenance(state) {
			continue
		}
		mergePackedStoreProvenanceInto(&merged.PackedStoreSummary, summarizePackedStoreProvenance(state))
		if len(state.Deps) != 0 {
			if merged.Deps == nil {
				merged.Deps = map[*Symbol]regionDependencyState{}
			}
			for region, dep := range state.Deps {
				existing, ok := merged.Deps[region]
				if !ok {
					merged.Deps[region] = dep
					continue
				}
				if !existing.Valid {
					if !dep.Valid && dep.Generation > existing.Generation {
						merged.Deps[region] = dep
					}
					continue
				}
				if !dep.Valid {
					merged.Deps[region] = dep
					continue
				}
				if dep.Generation > existing.Generation {
					merged.Deps[region] = dep
				}
			}
		}
		if len(state.StoreDeps) != 0 {
			if merged.StoreDeps == nil {
				merged.StoreDeps = map[*Symbol]packedStoreDependencyState{}
			}
			for store, dep := range state.StoreDeps {
				merged.StoreDeps[store] = dep
			}
		}
		forEachRegionParamDep(state, func(index int) {
			appendRegionParamDep(&merged, index)
		})
		if len(state.Fields) != 0 {
			if merged.Fields == nil {
				merged.Fields = map[string]regionRefState{}
			}
			for name, fieldState := range state.Fields {
				if existing, ok := merged.Fields[name]; ok {
					if next, ok := mergeFlatRegionRefStates(existing, fieldState); ok {
						merged.Fields[name] = next
					} else {
						delete(merged.Fields, name)
					}
				} else {
					merged.Fields[name] = cloneRegionRefStateSharedFields(fieldState)
				}
			}
		}
	}
	if !hasRegionProvenance(merged) {
		return regionRefState{}, false
	}
	return merged, true
}

func mergeFlatRegionRefStates(left regionRefState, right regionRefState) (regionRefState, bool) {
	if len(left.Fields) != 0 || len(right.Fields) != 0 {
		return mergeRegionRefStates(left, right)
	}
	if !hasRegionProvenance(left) {
		if !hasRegionProvenance(right) {
			return regionRefState{}, false
		}
		return cloneRegionRefStateSharedFields(right), true
	}
	if !hasRegionProvenance(right) {
		return cloneRegionRefStateSharedFields(left), true
	}

	merged := regionRefState{PackedStoreSummaryKnown: true}
	mergePackedStoreProvenanceInto(&merged.PackedStoreSummary, summarizePackedStoreProvenance(left))
	mergePackedStoreProvenanceInto(&merged.PackedStoreSummary, summarizePackedStoreProvenance(right))

	if len(left.Deps) != 0 || len(right.Deps) != 0 {
		merged.Deps = map[*Symbol]regionDependencyState{}
		for region, dep := range left.Deps {
			merged.Deps[region] = dep
		}
		for region, dep := range right.Deps {
			existing, ok := merged.Deps[region]
			if !ok {
				merged.Deps[region] = dep
				continue
			}
			if !existing.Valid {
				if !dep.Valid && dep.Generation > existing.Generation {
					merged.Deps[region] = dep
				}
				continue
			}
			if !dep.Valid {
				merged.Deps[region] = dep
				continue
			}
			if dep.Generation > existing.Generation {
				merged.Deps[region] = dep
			}
		}
	}
	if len(left.StoreDeps) != 0 || len(right.StoreDeps) != 0 {
		merged.StoreDeps = map[*Symbol]packedStoreDependencyState{}
		for store, dep := range left.StoreDeps {
			merged.StoreDeps[store] = dep
		}
		for store, dep := range right.StoreDeps {
			merged.StoreDeps[store] = dep
		}
	}
	forEachRegionParamDep(left, func(index int) {
		appendRegionParamDep(&merged, index)
	})
	forEachRegionParamDep(right, func(index int) {
		appendRegionParamDep(&merged, index)
	})
	return merged, true
}

func mergeRegionRefStatesWithExplicitFields(states []regionRefState, fieldStates map[string]regionRefState) (regionRefState, bool) {
	merged := regionRefState{PackedStoreSummaryKnown: true}
	found := false
	for _, state := range states {
		if !hasRegionProvenance(state) {
			continue
		}
		found = true
		mergePackedStoreProvenanceInto(&merged.PackedStoreSummary, summarizePackedStoreProvenance(state))
		if len(state.Deps) != 0 {
			if merged.Deps == nil {
				merged.Deps = map[*Symbol]regionDependencyState{}
			}
			for region, dep := range state.Deps {
				existing, ok := merged.Deps[region]
				if !ok {
					merged.Deps[region] = dep
					continue
				}
				if !existing.Valid {
					if !dep.Valid && dep.Generation > existing.Generation {
						merged.Deps[region] = dep
					}
					continue
				}
				if !dep.Valid {
					merged.Deps[region] = dep
					continue
				}
				if dep.Generation > existing.Generation {
					merged.Deps[region] = dep
				}
			}
		}
		if len(state.StoreDeps) != 0 {
			if merged.StoreDeps == nil {
				merged.StoreDeps = map[*Symbol]packedStoreDependencyState{}
			}
			for store, dep := range state.StoreDeps {
				merged.StoreDeps[store] = dep
			}
		}
		forEachRegionParamDep(state, func(index int) {
			appendRegionParamDep(&merged, index)
		})
	}
	if len(fieldStates) != 0 {
		merged.Fields = fieldStates
		found = true
		merged.PackedStoreSummaryKnown = false
		return withPackedStoreProvenanceSummary(merged), true
	}
	if !found || !hasRegionProvenance(merged) {
		return regionRefState{}, false
	}
	return merged, true
}

func regionIndexFieldKey(index int64) string {
	return fmt.Sprintf("[%d]", index)
}

func regionAnyIndexFieldKey() string {
	return "[*]"
}

func isRegionIndexFieldKey(name string) bool {
	return strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]")
}

func projectRegionFieldState(state regionRefState, field string) (regionRefState, bool) {
	if len(state.Fields) == 0 {
		return regionRefState{}, false
	}
	fieldState, ok := state.Fields[field]
	if !ok || !hasRegionProvenance(fieldState) {
		return regionRefState{}, false
	}
	return cloneRegionRefState(fieldState), true
}

func projectRegionIndexState(state regionRefState, index ast.Expr, evalConst func(ast.Expr) (ConstValue, bool)) (regionRefState, bool) {
	if len(state.Fields) == 0 {
		return regionRefState{}, false
	}
	if evalConst != nil {
		if value, ok := evalConst(index); ok && value.Kind == ConstInt {
			if fieldState, ok := projectRegionIndexKeyState(state, regionIndexFieldKey(value.Int)); ok {
				return fieldState, true
			}
		}
	}
	return projectRegionIndexKeyState(state, regionAnyIndexFieldKey())
}

func projectRegionIndexKeyState(state regionRefState, key string) (regionRefState, bool) {
	if len(state.Fields) == 0 {
		return regionRefState{}, false
	}
	fieldState, ok := state.Fields[key]
	if !ok || !hasRegionProvenance(fieldState) {
		return regionRefState{}, false
	}
	return cloneRegionRefState(fieldState), true
}

func summarizeRegionIndexStates(state regionRefState) (regionRefState, bool) {
	if !hasRegionProvenance(state) {
		return regionRefState{}, false
	}
	summary := cloneRegionRefStateShallowFields(state)
	if len(state.Fields) == 0 {
		return summary, true
	}
	indexStates := make([]regionRefState, 0, len(state.Fields))
	for name, fieldState := range state.Fields {
		if !isRegionIndexFieldKey(name) {
			continue
		}
		if !hasRegionProvenance(fieldState) {
			continue
		}
		indexStates = append(indexStates, fieldState)
	}
	if len(indexStates) == 0 {
		summary.Fields = nil
		return withPackedStoreProvenanceSummary(summary), true
	}
	merged, ok := mergeRegionRefStates(indexStates...)
	if !ok {
		summary.Fields = nil
		return withPackedStoreProvenanceSummary(summary), true
	}
	summary.Fields = map[string]regionRefState{
		regionAnyIndexFieldKey(): merged,
	}
	summary.PackedStoreSummaryKnown = false
	return withPackedStoreProvenanceSummary(summary), true
}

func firstInvalidRegionDependency(state regionRefState) (*Symbol, regionDependencyState, bool) {
	for region, dep := range state.Deps {
		if !dep.Valid {
			return region, dep, true
		}
	}
	return nil, regionDependencyState{}, false
}

func abstractParamOnlyRegionRefState(state regionRefState) (regionRefState, bool) {
	if !hasRegionProvenance(state) {
		return regionRefState{}, false
	}
	out := regionRefState{}
	out.DirectParamDep = state.DirectParamDep
	out.HasDirectParamDep = state.HasDirectParamDep
	out.ParamDeps = cloneRegionParamDeps(state.ParamDeps)
	if len(state.Fields) != 0 {
		for name, fieldState := range state.Fields {
			filtered, ok := abstractParamOnlyRegionRefState(fieldState)
			if !ok {
				continue
			}
			if out.Fields == nil {
				out.Fields = map[string]regionRefState{}
			}
			out.Fields[name] = filtered
		}
	}
	if !hasRegionProvenance(out) {
		return regionRefState{}, false
	}
	return withPackedStoreProvenanceSummary(out), true
}

func (a *Analyzer) instantiateReturnProvenance(state regionRefState, args []ast.Expr) (regionRefState, bool) {
	if !hasRegionProvenance(state) {
		return regionRefState{}, false
	}
	argStates := make([]regionRefState, 0, regionRefStateParamDepCount(state))
	forEachRegionParamDep(state, func(index int) {
		if index < 0 || index >= len(args) {
			return
		}
		argState, ok := a.regionRefStateForExpr(args[index])
		if !ok {
			return
		}
		argStates = append(argStates, argState)
	})
	fieldStates := map[string]regionRefState{}
	if len(state.Fields) != 0 {
		for name, fieldState := range state.Fields {
			instField, ok := a.instantiateReturnProvenance(fieldState, args)
			if !ok {
				continue
			}
			fieldStates[name] = instField
		}
	}
	if len(fieldStates) != 0 {
		return mergeRegionRefStatesWithExplicitFields(argStates, fieldStates)
	}
	instantiated := regionRefState{}
	if mergedArgs, ok := mergeRegionRefStates(argStates...); ok {
		instantiated = mergedArgs
	}
	if !hasRegionProvenance(instantiated) {
		return regionRefState{}, false
	}
	instantiated.PackedStoreSummaryKnown = false
	return withPackedStoreProvenanceSummary(instantiated), true
}

func firstLiveRegionDependency(state regionRefState) (*Symbol, regionDependencyState, bool) {
	for region, dep := range state.Deps {
		if dep.Valid {
			return region, dep, true
		}
	}
	return nil, regionDependencyState{}, false
}

func invalidateRegionDependencyInState(state regionRefState, region *Symbol, predicate func(regionDependencyState) bool, reason string) (regionRefState, bool) {
	changed := false
	depsCloned := false
	fieldsCloned := false
	if region != nil {
		if dep, ok := state.Deps[region]; ok && dep.Valid {
			if predicate == nil || predicate(dep) {
				if !depsCloned {
					state.Deps = cloneRegionDependencyStates(state.Deps)
					depsCloned = true
				}
				dep.Valid = false
				dep.InvalidatedBy = reason
				state.Deps[region] = dep
				changed = true
			}
		}
	}
	if len(state.Fields) != 0 {
		for name, fieldState := range state.Fields {
			nextField, fieldChanged := invalidateRegionDependencyInState(fieldState, region, predicate, reason)
			if !fieldChanged {
				continue
			}
			if !fieldsCloned {
				state.Fields = cloneRegionRefFields(state.Fields)
				fieldsCloned = true
			}
			state.Fields[name] = nextField
			changed = true
		}
	}
	if changed {
		state.PackedStoreSummaryKnown = false
		state = withPackedStoreProvenanceSummary(state)
	}
	return state, changed
}

func firstNonShareablePackedStoreDependency(state regionRefState) (*Symbol, packedStoreDependencyState, bool) {
	for store, dep := range state.StoreDeps {
		if dep.Type == nil || !IsFrozenPackedEnumStoreType(dep.Type) {
			return store, dep, true
		}
	}
	for _, fieldState := range state.Fields {
		if store, dep, ok := firstNonShareablePackedStoreDependency(fieldState); ok {
			return store, dep, true
		}
	}
	return nil, packedStoreDependencyState{}, false
}

func hasPackedStoreDependencies(state regionRefState) bool {
	if len(state.StoreDeps) != 0 {
		return true
	}
	for _, fieldState := range state.Fields {
		if hasPackedStoreDependencies(fieldState) {
			return true
		}
	}
	return false
}

func (a *Analyzer) cloneAffineValueStates() map[affineValueKey]affineValueState {
	if a.currentAffineValues == nil {
		return nil
	}
	cloned := make(map[affineValueKey]affineValueState, len(a.currentAffineValues))
	for key, state := range a.currentAffineValues {
		cloned[key] = state
	}
	return cloned
}

func cloneBorrowedOwnerRefState(state borrowedOwnerRefState) borrowedOwnerRefState {
	cloned := borrowedOwnerRefState{HasDirect: state.HasDirect, Direct: state.Direct}
	if len(state.Fields) != 0 {
		cloned.Fields = make(map[string]borrowedOwnerRefState, len(state.Fields))
		for key, child := range state.Fields {
			cloned.Fields[key] = cloneBorrowedOwnerRefState(child)
		}
	}
	return cloned
}

func cloneBorrowedOwnerRefStateShallowFields(state borrowedOwnerRefState) borrowedOwnerRefState {
	return borrowedOwnerRefState{HasDirect: state.HasDirect, Direct: state.Direct}
}

func cloneBorrowedOwnerRefSummaryTarget(target borrowedOwnerRefSummaryTarget) borrowedOwnerRefSummaryTarget {
	cloned := borrowedOwnerRefSummaryTarget{ParamIndex: target.ParamIndex}
	if len(target.Path) != 0 {
		cloned.Path = append([]borrowReturnAnnotationStep(nil), target.Path...)
	}
	return cloned
}

func cloneBorrowedOwnerRefSummary(summary borrowedOwnerRefSummary) borrowedOwnerRefSummary {
	cloned := borrowedOwnerRefSummary{HasDirect: summary.HasDirect}
	if summary.HasDirect {
		cloned.Direct = cloneBorrowedOwnerRefSummaryTarget(summary.Direct)
	}
	if len(summary.Fields) != 0 {
		cloned.Fields = make(map[string]borrowedOwnerRefSummary, len(summary.Fields))
		for key, child := range summary.Fields {
			cloned.Fields[key] = cloneBorrowedOwnerRefSummary(child)
		}
	}
	return cloned
}

func borrowedOwnerRefSummaryTargetEqual(left borrowedOwnerRefSummaryTarget, right borrowedOwnerRefSummaryTarget) bool {
	if left.ParamIndex != right.ParamIndex || len(left.Path) != len(right.Path) {
		return false
	}
	for i := range left.Path {
		leftStep := left.Path[i]
		rightStep := right.Path[i]
		if leftStep.Field != rightStep.Field || leftStep.Wildcard != rightStep.Wildcard {
			return false
		}
		if (leftStep.Index == nil) != (rightStep.Index == nil) {
			return false
		}
		if leftStep.Index != nil && rightStep.Index != nil && *leftStep.Index != *rightStep.Index {
			return false
		}
	}
	return true
}

func hasBorrowedOwnerRefSummary(summary borrowedOwnerRefSummary) bool {
	return summary.HasDirect || len(summary.Fields) != 0
}

func mergeBorrowedOwnerRefSummary(dst borrowedOwnerRefSummary, src borrowedOwnerRefSummary) (borrowedOwnerRefSummary, bool) {
	merged := borrowedOwnerRefSummary{}
	if dst.HasDirect && src.HasDirect && borrowedOwnerRefSummaryTargetEqual(dst.Direct, src.Direct) {
		merged.HasDirect = true
		merged.Direct = cloneBorrowedOwnerRefSummaryTarget(dst.Direct)
	}
	if len(dst.Fields) != 0 && len(src.Fields) != 0 {
		for key, child := range dst.Fields {
			srcChild, ok := src.Fields[key]
			if !ok {
				continue
			}
			mergedChild, ok := mergeBorrowedOwnerRefSummary(child, srcChild)
			if !ok {
				continue
			}
			if merged.Fields == nil {
				merged.Fields = map[string]borrowedOwnerRefSummary{}
			}
			merged.Fields[key] = mergedChild
		}
	}
	if !hasBorrowedOwnerRefSummary(merged) {
		return borrowedOwnerRefSummary{}, false
	}
	return merged, true
}

func assignBorrowedOwnerRefSummaryAtPath(dst borrowedOwnerRefSummary, steps []borrowReturnAnnotationStep, value borrowedOwnerRefSummary) borrowedOwnerRefSummary {
	if len(steps) == 0 {
		if merged, ok := mergeBorrowedOwnerRefSummary(dst, value); ok {
			return merged
		}
		return cloneBorrowedOwnerRefSummary(value)
	}
	if dst.Fields == nil {
		dst.Fields = map[string]borrowedOwnerRefSummary{}
	}
	key := regionFieldKeyForBorrowStep(steps[0])
	child := dst.Fields[key]
	dst.Fields[key] = assignBorrowedOwnerRefSummaryAtPath(child, steps[1:], value)
	return dst
}

func parseAffineValueKeyPath(path string) ([]borrowReturnAnnotationStep, bool) {
	if path == "" {
		return nil, true
	}
	parts := strings.Split(path, ".")
	steps := make([]borrowReturnAnnotationStep, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, false
		}
		if strings.HasPrefix(part, "[") && strings.HasSuffix(part, "]") {
			token := part[1 : len(part)-1]
			if token == "*" {
				steps = append(steps, borrowReturnAnnotationStep{Wildcard: true})
				continue
			}
			value, err := strconv.ParseInt(token, 10, 64)
			if err != nil {
				return nil, false
			}
			valueCopy := value
			steps = append(steps, borrowReturnAnnotationStep{Index: &valueCopy})
			continue
		}
		steps = append(steps, borrowReturnAnnotationStep{Field: part})
	}
	return steps, true
}

func borrowedOwnerRefSummaryFromAffineValueKey(key affineValueKey) (borrowedOwnerRefSummaryTarget, bool) {
	if key.Root == nil || key.Root.Kind != SymbolParam {
		return borrowedOwnerRefSummaryTarget{}, false
	}
	steps, ok := parseAffineValueKeyPath(key.Path)
	if !ok {
		return borrowedOwnerRefSummaryTarget{}, false
	}
	return borrowedOwnerRefSummaryTarget{ParamIndex: key.Root.ParamIndex, Path: steps}, true
}

func abstractParamOnlyBorrowedOwnerRefSummary(state borrowedOwnerRefState) (borrowedOwnerRefSummary, bool) {
	if !hasBorrowedOwnerRefState(state) {
		return borrowedOwnerRefSummary{}, false
	}
	out := borrowedOwnerRefSummary{}
	if state.HasDirect {
		target, ok := borrowedOwnerRefSummaryFromAffineValueKey(state.Direct)
		if ok {
			out.HasDirect = true
			out.Direct = target
		}
	}
	if len(state.Fields) != 0 {
		for key, child := range state.Fields {
			filtered, ok := abstractParamOnlyBorrowedOwnerRefSummary(child)
			if !ok {
				continue
			}
			if out.Fields == nil {
				out.Fields = map[string]borrowedOwnerRefSummary{}
			}
			out.Fields[key] = filtered
		}
	}
	if !hasBorrowedOwnerRefSummary(out) {
		return borrowedOwnerRefSummary{}, false
	}
	return out, true
}

func projectBorrowedOwnerRefStateAtSteps(state borrowedOwnerRefState, steps []borrowReturnAnnotationStep) (borrowedOwnerRefState, bool) {
	current := state
	for _, step := range steps {
		var ok bool
		switch {
		case step.Field != "":
			current, ok = projectBorrowedOwnerRefFieldState(current, step.Field)
		case step.Wildcard:
			current, ok = projectBorrowedOwnerRefIndexKeyState(current, regionAnyIndexFieldKey())
		case step.Index != nil:
			current, ok = projectBorrowedOwnerRefIndexKeyState(current, regionIndexFieldKey(*step.Index))
		default:
			return borrowedOwnerRefState{}, false
		}
		if !ok {
			return borrowedOwnerRefState{}, false
		}
	}
	return current, true
}

func summarizeBorrowedOwnerRefIndexStates(state borrowedOwnerRefState) (borrowedOwnerRefState, bool) {
	if !hasBorrowedOwnerRefState(state) {
		return borrowedOwnerRefState{}, false
	}
	summary := cloneBorrowedOwnerRefStateShallowFields(state)
	if len(state.Fields) == 0 {
		return summary, true
	}
	indexStates := make([]borrowedOwnerRefState, 0, len(state.Fields))
	for name, fieldState := range state.Fields {
		if !isRegionIndexFieldKey(name) {
			continue
		}
		if !hasBorrowedOwnerRefState(fieldState) {
			continue
		}
		indexStates = append(indexStates, fieldState)
	}
	if len(indexStates) == 0 {
		summary.Fields = nil
		return summary, true
	}
	merged := cloneBorrowedOwnerRefState(indexStates[0])
	for i := 1; i < len(indexStates); i++ {
		next, ok := mergeBorrowedOwnerRefState(merged, indexStates[i])
		if !ok {
			summary.Fields = nil
			return summary, true
		}
		merged = next
	}
	if !hasBorrowedOwnerRefState(merged) {
		summary.Fields = nil
		return summary, true
	}
	summary.Fields = map[string]borrowedOwnerRefState{
		regionAnyIndexFieldKey(): merged,
	}
	return summary, true
}

func (a *Analyzer) instantiateBorrowedOwnerRefSummary(summary borrowedOwnerRefSummary, args []ast.Expr) (borrowedOwnerRefState, bool) {
	if !hasBorrowedOwnerRefSummary(summary) {
		return borrowedOwnerRefState{}, false
	}
	instantiated := borrowedOwnerRefState{}
	if summary.HasDirect {
		index := summary.Direct.ParamIndex
		if index >= 0 && index < len(args) {
			if argState, ok := a.borrowedOwnerRefStateForExpr(args[index]); ok {
				if len(summary.Direct.Path) == 0 {
					instantiated = cloneBorrowedOwnerRefState(argState)
				} else if projected, ok := projectBorrowedOwnerRefStateAtSteps(argState, summary.Direct.Path); ok {
					instantiated = cloneBorrowedOwnerRefState(projected)
				}
			}
		}
	}
	if len(summary.Fields) != 0 {
		for key, child := range summary.Fields {
			instChild, ok := a.instantiateBorrowedOwnerRefSummary(child, args)
			if !ok {
				continue
			}
			if instantiated.Fields == nil {
				instantiated.Fields = map[string]borrowedOwnerRefState{}
			}
			instantiated.Fields[key] = instChild
		}
	}
	if !hasBorrowedOwnerRefState(instantiated) {
		return borrowedOwnerRefState{}, false
	}
	return instantiated, true
}

func hasBorrowedOwnerRefState(state borrowedOwnerRefState) bool {
	return state.HasDirect || len(state.Fields) != 0
}

func mergeBorrowedOwnerRefState(dst borrowedOwnerRefState, src borrowedOwnerRefState) (borrowedOwnerRefState, bool) {
	merged := borrowedOwnerRefState{}
	if dst.HasDirect && src.HasDirect && dst.Direct == src.Direct {
		merged.HasDirect = true
		merged.Direct = dst.Direct
	}
	if len(dst.Fields) != 0 && len(src.Fields) != 0 {
		for key, child := range dst.Fields {
			srcChild, ok := src.Fields[key]
			if !ok {
				continue
			}
			mergedChild, ok := mergeBorrowedOwnerRefState(child, srcChild)
			if !ok {
				continue
			}
			if merged.Fields == nil {
				merged.Fields = map[string]borrowedOwnerRefState{}
			}
			merged.Fields[key] = mergedChild
		}
	}
	if !hasBorrowedOwnerRefState(merged) {
		return borrowedOwnerRefState{}, false
	}
	return merged, true
}

func assignBorrowedOwnerRefStateAtPath(dst borrowedOwnerRefState, steps []borrowReturnAnnotationStep, value borrowedOwnerRefState) borrowedOwnerRefState {
	if len(steps) == 0 {
		return cloneBorrowedOwnerRefState(value)
	}
	if dst.Fields == nil {
		dst.Fields = map[string]borrowedOwnerRefState{}
	}
	key := regionFieldKeyForBorrowStep(steps[0])
	child := dst.Fields[key]
	dst.Fields[key] = assignBorrowedOwnerRefStateAtPath(child, steps[1:], value)
	return dst
}

func clearBorrowedOwnerRefStateAtPath(dst borrowedOwnerRefState, steps []borrowReturnAnnotationStep) borrowedOwnerRefState {
	if len(steps) == 0 {
		return borrowedOwnerRefState{}
	}
	if len(dst.Fields) == 0 {
		return dst
	}
	key := regionFieldKeyForBorrowStep(steps[0])
	child, ok := dst.Fields[key]
	if !ok {
		return dst
	}
	child = clearBorrowedOwnerRefStateAtPath(child, steps[1:])
	if !hasBorrowedOwnerRefState(child) {
		delete(dst.Fields, key)
	} else {
		dst.Fields[key] = child
	}
	if len(dst.Fields) == 0 {
		dst.Fields = nil
	}
	return dst
}

func (a *Analyzer) cloneBorrowedOwnerRefBindings() map[*Symbol]borrowedOwnerRefState {
	if a.currentBorrowedOwnerRefs == nil {
		return nil
	}
	cloned := make(map[*Symbol]borrowedOwnerRefState, len(a.currentBorrowedOwnerRefs))
	for sym, state := range a.currentBorrowedOwnerRefs {
		cloned[sym] = cloneBorrowedOwnerRefState(state)
	}
	return cloned
}

func (a *Analyzer) cloneFunctionValueType(fn *FuncType) *FuncType {
	if fn == nil {
		return nil
	}
	cloned, _ := a.substituteType(fn, nil, nil, nil, nil).(*FuncType)
	return cloned
}

func (a *Analyzer) cloneFunctionValueBindings() map[*Symbol]*FuncType {
	if a.currentFunctionValues == nil {
		return nil
	}
	cloned := make(map[*Symbol]*FuncType, len(a.currentFunctionValues))
	for sym, fn := range a.currentFunctionValues {
		cloned[sym] = a.cloneFunctionValueType(fn)
	}
	return cloned
}

func (a *Analyzer) cloneTrackedValueType(t Type) Type {
	return a.cloneTrackedValueTypeWithSeen(t, map[Type]Type{})
}

func (a *Analyzer) cloneTrackedValueTypeWithSeen(t Type, seen map[Type]Type) Type {
	if t == nil {
		return nil
	}
	if cloned, ok := seen[t]; ok {
		return cloned
	}
	switch tt := t.(type) {
	case *StructType:
		cloned := *tt
		cloned.Fields = map[string]Field{}
		clonedPtr := &cloned
		seen[t] = clonedPtr
		for name, field := range tt.Fields {
			field.Type = a.cloneTrackedValueTypeWithSeen(field.Type, seen)
			cloned.Fields[name] = field
		}
		return clonedPtr
	case *GenericInstanceType:
		cloned := *tt
		cloned.Args = make([]Type, len(tt.Args))
		seen[t] = &cloned
		for i, arg := range tt.Args {
			cloned.Args[i] = a.cloneTrackedValueTypeWithSeen(arg, seen)
		}
		cloned.Base = a.cloneTrackedValueTypeWithSeen(tt.Base, seen)
		return &cloned
	case *RefType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Elem = a.cloneTrackedValueTypeWithSeen(tt.Elem, seen)
		return &cloned
	case *ArrayType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Elem = a.cloneTrackedValueTypeWithSeen(tt.Elem, seen)
		return &cloned
	case *DArrayType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Elem = a.cloneTrackedValueTypeWithSeen(tt.Elem, seen)
		return &cloned
	case *OptionalType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Value = a.cloneTrackedValueTypeWithSeen(tt.Value, seen)
		return &cloned
	case *ViewType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Elem = a.cloneTrackedValueTypeWithSeen(tt.Elem, seen)
		return &cloned
	case *DArrayViewType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Elem = a.cloneTrackedValueTypeWithSeen(tt.Elem, seen)
		return &cloned
	case *DictType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Key = a.cloneTrackedValueTypeWithSeen(tt.Key, seen)
		cloned.Value = a.cloneTrackedValueTypeWithSeen(tt.Value, seen)
		return &cloned
	case *FuncType:
		cloned, _ := a.substituteType(tt, nil, nil, nil, nil).(*FuncType)
		if cloned == nil {
			return nil
		}
		seen[t] = cloned
		return cloned
	case *ErrorUnionType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Value = a.cloneTrackedValueTypeWithSeen(tt.Value, seen)
		return &cloned
	case *PackedEnumStoreType:
		cloned := *tt
		seen[t] = &cloned
		cloned.State = a.cloneTrackedValueTypeWithSeen(tt.State, seen)
		return &cloned
	case *DStrType:
		cloned := *tt
		seen[t] = &cloned
		return &cloned
	case *SViewType:
		cloned := *tt
		seen[t] = &cloned
		return &cloned
	case *BuiltinType, *TypeParamType, *NeverType, *NullType, *InvalidType, *ErrorSetType, *EnumType, *OpaqueType:
		seen[t] = t
		return t
	default:
		cloned := a.substituteType(t, nil, nil, nil, nil)
		seen[t] = cloned
		return cloned
	}
}

func (a *Analyzer) cloneTrackedTypesByRoot(src map[*Symbol]Type) map[*Symbol]Type {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[*Symbol]Type, len(src))
	for sym, tracked := range src {
		cloned[sym] = a.cloneTrackedValueType(tracked)
	}
	return cloned
}

func trackedNamedStateStructBase(t Type) (*StructType, bool) {
	switch tt := t.(type) {
	case *AggregateStateType:
		return trackedNamedStateStructBase(tt.Base)
	default:
		return namedStateStructBase(t)
	}
}

func trackedNamedStateCurrentArg(t Type) (Type, bool) {
	switch tt := t.(type) {
	case *AggregateStateType:
		return trackedNamedStateCurrentArg(tt.Base)
	default:
		return namedStateCurrentArg(t)
	}
}

func replaceTrackedNamedStateArg(template Type, state Type) Type {
	switch tt := template.(type) {
	case *AggregateStateType:
		inner := replaceTrackedNamedStateArg(tt.Base, state)
		if inner == nil {
			return nil
		}
		return cloneAggregateStateWithBase(inner, aggregateStateStates(tt))
	default:
		base, ok := namedStateStructBase(template)
		if !ok || base == nil {
			return nil
		}
		return instantiateNamedStateStructLiteralType(base, template, state)
	}
}

func applyNamedStateFromActualType(template Type, actual Type) (Type, bool) {
	templateBase, ok := trackedNamedStateStructBase(template)
	if !ok || templateBase == nil {
		return nil, false
	}
	actualBase, ok := trackedNamedStateStructBase(actual)
	if !ok || actualBase == nil || actualBase.Name != templateBase.Name {
		return nil, false
	}
	state, ok := trackedNamedStateCurrentArg(actual)
	if !ok || state == nil {
		return nil, false
	}
	replaced := replaceTrackedNamedStateArg(template, state)
	if replaced == nil {
		return nil, false
	}
	return replaced, true
}

func (a *Analyzer) mergeTrackedNamedStateValueTypes(dst Type, src Type) (Type, bool) {
	if dst == nil || src == nil {
		return nil, false
	}
	switch dt := dst.(type) {
	case *AggregateStateType:
		st, ok := src.(*AggregateStateType)
		if !ok || !sameAggregateStateLists(aggregateStateStates(dt), aggregateStateStates(st)) {
			return nil, false
		}
		mergedBase, ok := a.mergeTrackedNamedStateValueTypes(dt.Base, st.Base)
		if !ok {
			mergedBase, ok = a.mergeSpecializedValueTypes(dt.Base, st.Base)
			if !ok {
				return nil, false
			}
		}
		return cloneAggregateStateWithBase(mergedBase, aggregateStateStates(dt)), true
	case *StructStateCaseType, *StructStateSetType:
		merged := mergeNamedStateTypes(dst, src, nil)
		if IsInvalidType(merged) {
			return nil, false
		}
		return merged, true
	case *GenericInstanceType:
		st, ok := src.(*GenericInstanceType)
		if !ok || dt.Name != st.Name || len(dt.Args) != len(st.Args) {
			return nil, false
		}
		base, ok := dt.Base.(*StructType)
		if !ok || base == nil {
			return nil, false
		}
		stateIndex := namedStateArgIndex(base)
		if stateIndex < 0 {
			return nil, false
		}
		mergedBase, ok := a.mergeSpecializedValueTypes(dt.Base, st.Base)
		if !ok {
			return nil, false
		}
		args := make([]Type, len(dt.Args))
		for i := range dt.Args {
			if i == stateIndex {
				mergedArg := mergeNamedStateTypes(dt.Args[i], st.Args[i], base.NamedStateCases)
				if IsInvalidType(mergedArg) {
					return nil, false
				}
				args[i] = mergedArg
				continue
			}
			if !SameType(dt.Args[i], st.Args[i]) {
				return nil, false
			}
			args[i] = a.cloneTrackedValueType(dt.Args[i])
		}
		cloned := *dt
		cloned.Base = mergedBase
		cloned.Args = args
		return &cloned, true
	default:
		return nil, false
	}
}

func (a *Analyzer) cloneSpecializedValueTypeBindings() map[*Symbol]Type {
	if a.currentSpecializedValueTypes == nil {
		return nil
	}
	cloned := make(map[*Symbol]Type, len(a.currentSpecializedValueTypes))
	for sym, typ := range a.currentSpecializedValueTypes {
		cloned[sym] = a.cloneTrackedValueType(typ)
	}
	return cloned
}

func (a *Analyzer) cloneValueBindings() map[*Symbol]ast.Expr {
	if a.currentValueBindings == nil {
		return nil
	}
	cloned := make(map[*Symbol]ast.Expr, len(a.currentValueBindings))
	for sym, expr := range a.currentValueBindings {
		cloned[sym] = expr
	}
	return cloned
}

func mergeAffineValueStates(dst map[affineValueKey]affineValueState, src map[affineValueKey]affineValueState) map[affineValueKey]affineValueState {
	if dst == nil && src == nil {
		return nil
	}
	if dst == nil {
		dst = map[affineValueKey]affineValueState{}
	}
	for key, state := range src {
		existing, ok := dst[key]
		if !ok {
			dst[key] = state
			continue
		}
		if existing.ConsumedBy == "" && state.ConsumedBy != "" {
			existing.ConsumedBy = state.ConsumedBy
		}
		if existing.LiveProtocolType == nil && state.LiveProtocolType != nil {
			existing.LiveProtocolType = state.LiveProtocolType
		}
		if existing.LiveProtocolDescription == "" && state.LiveProtocolDescription != "" {
			existing.LiveProtocolDescription = state.LiveProtocolDescription
		}
		dst[key] = existing
	}
	return dst
}

func mergeBorrowedOwnerRefBindings(dst map[*Symbol]borrowedOwnerRefState, src map[*Symbol]borrowedOwnerRefState) map[*Symbol]borrowedOwnerRefState {
	if dst == nil || src == nil {
		return nil
	}
	merged := make(map[*Symbol]borrowedOwnerRefState, len(dst))
	for sym, state := range dst {
		srcState, ok := src[sym]
		if !ok {
			continue
		}
		mergedState, ok := mergeBorrowedOwnerRefState(state, srcState)
		if ok {
			merged[sym] = mergedState
		}
	}
	return merged
}

func intersectRegionRefSummary(dst regionRefState, src regionRefState) (regionRefState, bool) {
	merged := regionRefState{}
	if hasRegionParamDependencies(dst) && hasRegionParamDependencies(src) {
		forEachRegionParamDep(dst, func(index int) {
			if !regionRefStateHasParamDep(src, index) {
				return
			}
			appendRegionParamDep(&merged, index)
		})
	}
	if len(dst.Fields) != 0 && len(src.Fields) != 0 {
		for key, child := range dst.Fields {
			srcChild, ok := src.Fields[key]
			if !ok {
				continue
			}
			mergedChild, ok := intersectRegionRefSummary(child, srcChild)
			if !ok {
				continue
			}
			if merged.Fields == nil {
				merged.Fields = map[string]regionRefState{}
			}
			merged.Fields[key] = mergedChild
		}
	}
	if !hasRegionProvenance(merged) {
		return regionRefState{}, false
	}
	return merged, true
}

func (a *Analyzer) mergeFunctionValueTypes(dst *FuncType, src *FuncType) (*FuncType, bool) {
	if dst == nil || src == nil || !SameType(dst, src) {
		return nil, false
	}
	merged := a.cloneFunctionValueType(dst)
	if merged == nil {
		return nil, false
	}
	merged.ReturnProvenance = regionRefState{}
	merged.ReturnProvenanceKnown = dst.ReturnProvenanceKnown && src.ReturnProvenanceKnown
	if merged.ReturnProvenanceKnown {
		if state, ok := intersectRegionRefSummary(dst.ReturnProvenance, src.ReturnProvenance); ok {
			merged.ReturnProvenance = state
		}
	}
	merged.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	merged.ReturnBorrowedOwnerRefsKnown = dst.ReturnBorrowedOwnerRefsKnown && src.ReturnBorrowedOwnerRefsKnown
	if merged.ReturnBorrowedOwnerRefsKnown {
		if summary, ok := mergeBorrowedOwnerRefSummary(dst.ReturnBorrowedOwnerRefs, src.ReturnBorrowedOwnerRefs); ok {
			merged.ReturnBorrowedOwnerRefs = summary
		}
	}
	merged.SinkParams = nil
	merged.SinkParamsKnown = dst.SinkParamsKnown && src.SinkParamsKnown && len(dst.SinkParams) == len(src.SinkParams)
	if merged.SinkParamsKnown {
		merged.SinkParams = make([]bool, len(dst.SinkParams))
		for i := range dst.SinkParams {
			merged.SinkParams[i] = dst.SinkParams[i] && src.SinkParams[i]
		}
	}
	merged.ReturnIsolation = ReturnIsolationSummary{}
	merged.ReturnIsolationKnown = dst.ReturnIsolationKnown && src.ReturnIsolationKnown && returnIsolationSummariesEqual(dst.ReturnIsolation, src.ReturnIsolation)
	if merged.ReturnIsolationKnown {
		merged.ReturnIsolation = dst.ReturnIsolation
	}
	return merged, true
}

func (a *Analyzer) mergeFunctionValueBindings(dst map[*Symbol]*FuncType, src map[*Symbol]*FuncType) map[*Symbol]*FuncType {
	if dst == nil || src == nil {
		return nil
	}
	merged := make(map[*Symbol]*FuncType, len(dst))
	for sym, fn := range dst {
		srcFn, ok := src[sym]
		if !ok {
			continue
		}
		mergedFn, ok := a.mergeFunctionValueTypes(fn, srcFn)
		if ok {
			merged[sym] = mergedFn
		}
	}
	return merged
}

type specializedTypeMergeKey struct {
	dst Type
	src Type
}

func (a *Analyzer) mergeSpecializedValueTypes(dst Type, src Type) (Type, bool) {
	return a.mergeSpecializedValueTypesWithSeen(dst, src, map[specializedTypeMergeKey]bool{})
}

func (a *Analyzer) mergeSpecializedValueTypesWithSeen(dst Type, src Type, active map[specializedTypeMergeKey]bool) (Type, bool) {
	if merged, ok := a.mergeTrackedNamedStateValueTypes(dst, src); ok {
		return merged, true
	}
	if dst == nil || src == nil || !SameType(dst, src) {
		return nil, false
	}
	key := specializedTypeMergeKey{dst: dst, src: src}
	if active[key] {
		return a.cloneTrackedValueType(dst), true
	}
	active[key] = true
	defer delete(active, key)
	if dstFunc, ok := dst.(*FuncType); ok {
		srcFunc, ok := src.(*FuncType)
		if !ok {
			return nil, false
		}
		return a.mergeFunctionValueTypes(dstFunc, srcFunc)
	}
	switch tt := dst.(type) {
	case *StructType:
		srcStruct, ok := src.(*StructType)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(tt.Fields)
		for name, field := range tt.Fields {
			srcField, ok := srcStruct.Fields[name]
			if !ok {
				continue
			}
			mergedFieldType, ok := a.mergeSpecializedValueTypesWithSeen(field.Type, srcField.Type, active)
			if !ok {
				continue
			}
			field.Type = mergedFieldType
			fields[name] = field
		}
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		srcInstance, ok := src.(*GenericInstanceType)
		if !ok {
			return nil, false
		}
		mergedBase, ok := a.mergeSpecializedValueTypesWithSeen(tt.Base, srcInstance.Base, active)
		if !ok {
			return nil, false
		}
		args := make([]Type, 0, len(tt.Args))
		for _, arg := range tt.Args {
			args = append(args, a.cloneTrackedValueType(arg))
		}
		cloned := *tt
		cloned.Args = args
		cloned.Base = mergedBase
		return &cloned, true
	case *RefType:
		srcRef, ok := src.(*RefType)
		if !ok {
			return nil, false
		}
		mergedElem, ok := a.mergeSpecializedValueTypesWithSeen(tt.Elem, srcRef.Elem, active)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = mergedElem
		return &cloned, true
	case *ArrayType:
		srcArray, ok := src.(*ArrayType)
		if !ok {
			return nil, false
		}
		mergedElem, ok := a.mergeSpecializedValueTypesWithSeen(tt.Elem, srcArray.Elem, active)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = mergedElem
		return &cloned, true
	case *DArrayType:
		srcArray, ok := src.(*DArrayType)
		if !ok {
			return nil, false
		}
		mergedElem, ok := a.mergeSpecializedValueTypesWithSeen(tt.Elem, srcArray.Elem, active)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = mergedElem
		return &cloned, true
	case *OptionalType:
		srcOpt, ok := src.(*OptionalType)
		if !ok {
			return nil, false
		}
		mergedValue, ok := a.mergeSpecializedValueTypesWithSeen(tt.Value, srcOpt.Value, active)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Value = mergedValue
		return &cloned, true
	case *ViewType:
		srcView, ok := src.(*ViewType)
		if !ok {
			return nil, false
		}
		mergedElem, ok := a.mergeSpecializedValueTypesWithSeen(tt.Elem, srcView.Elem, active)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = mergedElem
		return &cloned, true
	case *DArrayViewType:
		srcView, ok := src.(*DArrayViewType)
		if !ok {
			return nil, false
		}
		mergedElem, ok := a.mergeSpecializedValueTypesWithSeen(tt.Elem, srcView.Elem, active)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = mergedElem
		return &cloned, true
	case *DictType:
		srcDict, ok := src.(*DictType)
		if !ok {
			return nil, false
		}
		mergedKey, ok := a.mergeSpecializedValueTypesWithSeen(tt.Key, srcDict.Key, active)
		if !ok {
			return nil, false
		}
		mergedValue, ok := a.mergeSpecializedValueTypesWithSeen(tt.Value, srcDict.Value, active)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Key = mergedKey
		cloned.Value = mergedValue
		return &cloned, true
	default:
		return a.cloneTrackedValueType(dst), true
	}
}

func (a *Analyzer) mergeSpecializedValueTypeBindings(dst map[*Symbol]Type, src map[*Symbol]Type) map[*Symbol]Type {
	if dst == nil || src == nil {
		return nil
	}
	merged := make(map[*Symbol]Type, len(dst))
	for sym, typ := range dst {
		srcType, ok := src[sym]
		if !ok {
			continue
		}
		mergedType, ok := a.mergeSpecializedValueTypes(typ, srcType)
		if !ok {
			continue
		}
		if normalized, ok := a.specializeCallbackCarryingType(sym.Type, mergedType); ok {
			merged[sym] = normalized
			continue
		}
		merged[sym] = a.cloneTrackedValueType(mergedType)
	}
	return merged
}

func (a *Analyzer) cloneSpecializedValueTypeMap(src map[*Symbol]Type) map[*Symbol]Type {
	if src == nil {
		return nil
	}
	cloned := make(map[*Symbol]Type, len(src))
	for sym, typ := range src {
		cloned[sym] = a.cloneTrackedValueType(typ)
	}
	return cloned
}

func (a *Analyzer) intersectSpecializedValueTypeFlows(flows ...map[*Symbol]Type) (map[*Symbol]Type, bool) {
	var merged map[*Symbol]Type
	mergedAny := false
	for _, flow := range flows {
		if !mergedAny {
			merged = a.cloneSpecializedValueTypeMap(flow)
			mergedAny = true
			continue
		}
		merged = a.mergeSpecializedValueTypeBindings(merged, flow)
	}
	if !mergedAny {
		return nil, false
	}
	return merged, true
}

func (a *Analyzer) cloneFunctionValueMap(src map[*Symbol]*FuncType) map[*Symbol]*FuncType {
	if src == nil {
		return nil
	}
	cloned := make(map[*Symbol]*FuncType, len(src))
	for sym, fn := range src {
		cloned[sym] = a.cloneFunctionValueType(fn)
	}
	return cloned
}

func (a *Analyzer) intersectFunctionValueFlows(flows ...map[*Symbol]*FuncType) (map[*Symbol]*FuncType, bool) {
	var merged map[*Symbol]*FuncType
	mergedAny := false
	for _, flow := range flows {
		if !mergedAny {
			merged = a.cloneFunctionValueMap(flow)
			mergedAny = true
			continue
		}
		merged = a.mergeFunctionValueBindings(merged, flow)
	}
	if !mergedAny {
		return nil, false
	}
	return merged, true
}

func (a *Analyzer) trackAffineValueSymbol(sym *Symbol) {
	if sym == nil || !a.containsAffineHandleValues(sym.Type, map[string]bool{}) {
		return
	}
	a.registerLiveProtocolValuePaths(affineValueKey{Root: sym}, sym.Type)
}

func borrowableOwnerRefElemType(t Type) (Type, bool) {
	ref, ok := t.(*RefType)
	if !ok || !isBorrowableAffineOwnerType(ref.Elem) {
		return nil, false
	}
	return ref.Elem, true
}

func (a *Analyzer) containsBorrowedOwnerRefValues(t Type, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	if _, ok := borrowableOwnerRefElemType(t); ok {
		return true
	}
	key := t.String()
	if seen[key] {
		return false
	}
	seen[key] = true
	switch tt := t.(type) {
	case *ArrayType:
		return a.containsBorrowedOwnerRefValues(tt.Elem, seen)
	case *DArrayType:
		return a.containsBorrowedOwnerRefValues(tt.Elem, seen)
	case *OptionalType:
		return a.containsBorrowedOwnerRefValues(tt.Value, seen)
	case *ViewType:
		return a.containsBorrowedOwnerRefValues(tt.Elem, seen)
	case *DArrayViewType:
		return a.containsBorrowedOwnerRefValues(tt.Elem, seen)
	case *DictType:
		return a.containsBorrowedOwnerRefValues(tt.Key, seen) || a.containsBorrowedOwnerRefValues(tt.Value, seen)
	case *PackedVariantViewType:
		for _, field := range tt.Enum.Common {
			if a.containsBorrowedOwnerRefValues(field.Type, seen) {
				return true
			}
		}
		for _, payloadType := range tt.Variant.Payload {
			if a.containsBorrowedOwnerRefValues(payloadType, seen) {
				return true
			}
		}
		return false
	case *EnumType:
		for _, field := range tt.Common {
			if a.containsBorrowedOwnerRefValues(field.Type, seen) {
				return true
			}
		}
		for _, variant := range tt.Variants {
			for _, payloadType := range variant.Payload {
				if a.containsBorrowedOwnerRefValues(payloadType, seen) {
					return true
				}
			}
		}
		return false
	case *StructType:
		for _, field := range tt.Fields {
			if a.containsBorrowedOwnerRefValues(field.Type, seen) {
				return true
			}
		}
		return false
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				if a.containsBorrowedOwnerRefValues(fieldType, seen) {
					return true
				}
			}
			return false
		}
		for _, arg := range tt.Args {
			if a.containsBorrowedOwnerRefValues(arg, seen) {
				return true
			}
		}
		return a.containsBorrowedOwnerRefValues(tt.Base, seen)
	default:
		return false
	}
}

func (a *Analyzer) recordBorrowedOwnerRefParam(sym *Symbol) {
	if a.currentBorrowedOwnerRefs == nil || sym == nil {
		return
	}
	state, ok := a.abstractParamBorrowedOwnerRefState(sym.Type, affineValueKey{Root: sym}, map[string]bool{})
	if !ok {
		delete(a.currentBorrowedOwnerRefs, sym)
		return
	}
	a.currentBorrowedOwnerRefs[sym] = state
}

func (a *Analyzer) recordBorrowedOwnerRefBinding(sym *Symbol, value ast.Expr) {
	if a.currentBorrowedOwnerRefs == nil || sym == nil {
		return
	}
	if !a.containsBorrowedOwnerRefValues(sym.Type, map[string]bool{}) {
		delete(a.currentBorrowedOwnerRefs, sym)
		return
	}
	if state, ok := a.borrowedOwnerRefStateForExpr(value); ok {
		a.currentBorrowedOwnerRefs[sym] = state
		return
	}
	if _, ok := borrowableOwnerRefElemType(sym.Type); ok {
		a.currentBorrowedOwnerRefs[sym] = borrowedOwnerRefState{HasDirect: true, Direct: affineValueKey{Root: sym}}
		return
	}
	delete(a.currentBorrowedOwnerRefs, sym)
}

func (a *Analyzer) recordBorrowedOwnerRefTarget(target ast.Expr, expected Type, value ast.Expr) {
	if a.currentBorrowedOwnerRefs == nil {
		return
	}
	root, steps, ok := a.lookupBorrowedOwnerRefTargetPath(target)
	if !ok || root == nil {
		return
	}
	if len(steps) == 0 {
		a.recordBorrowedOwnerRefBinding(root, value)
		return
	}
	if !a.containsBorrowedOwnerRefValues(root.Type, map[string]bool{}) {
		delete(a.currentBorrowedOwnerRefs, root)
		return
	}
	current := a.currentBorrowedOwnerRefs[root]
	if a.containsBorrowedOwnerRefValues(expected, map[string]bool{}) {
		if state, ok := a.borrowedOwnerRefStateForExpr(value); ok {
			current = assignBorrowedOwnerRefStateAtPath(current, steps, state)
		} else {
			current = clearBorrowedOwnerRefStateAtPath(current, steps)
		}
	} else {
		current = clearBorrowedOwnerRefStateAtPath(current, steps)
	}
	if hasBorrowedOwnerRefState(current) {
		a.currentBorrowedOwnerRefs[root] = current
	} else {
		delete(a.currentBorrowedOwnerRefs, root)
	}
}

func (a *Analyzer) recordValueBinding(sym *Symbol, value ast.Expr) {
	if a.currentValueBindings == nil || sym == nil {
		return
	}
	if sym.Kind != SymbolLocal && sym.Kind != SymbolParam {
		return
	}
	if sym.Mutable || value == nil {
		sym.AliasOf = nil
		delete(a.currentValueBindings, sym)
		return
	}
	sym.AliasOf = nil
	if aliasRoot := directValueBindingAliasRoot(a.currentScope, value); aliasRoot != nil && aliasRoot != sym {
		sym.AliasOf = aliasRoot
	}
	a.currentValueBindings[sym] = value
}

func (a *Analyzer) recordSpecializedValueTypeBinding(sym *Symbol, valueType Type) {
	if a.currentSpecializedValueTypes == nil || sym == nil {
		return
	}
	if sym.Kind != SymbolLocal && sym.Kind != SymbolParam {
		return
	}
	trackedType := sym.Type
	trackedAny := false
	if specializedType, ok := a.specializeCallbackCarryingType(trackedType, valueType); ok {
		trackedType = specializedType
		trackedAny = true
	}
	if namedStateType, ok := applyNamedStateFromActualType(trackedType, valueType); ok {
		trackedType = namedStateType
		trackedAny = true
	}
	if trackedAny {
		a.currentSpecializedValueTypes[sym] = a.cloneTrackedValueType(trackedType)
		return
	}
	delete(a.currentSpecializedValueTypes, sym)
}

func (a *Analyzer) currentTrackedValueType(sym *Symbol) Type {
	if sym == nil {
		return nil
	}
	current := sym.Type
	if specializedType, ok := a.lookupCurrentSpecializedValueType(sym); ok {
		current = specializedType
	}
	if a.currentScope != nil {
		ident := &ast.Ident{Name: sym.Name}
		if refinedType, ok := a.lookupRefinedExprType(ident); ok {
			if specializedType, ok := a.specializeCallbackCarryingType(refinedType, current); ok {
				current = specializedType
			} else {
				current = refinedType
			}
		}
	}
	return current
}

func (a *Analyzer) bindTrackedValueType(sym *Symbol, tracked Type) {
	if a.currentSpecializedValueTypes == nil || sym == nil || tracked == nil {
		return
	}
	a.currentSpecializedValueTypes[sym] = a.cloneTrackedValueType(tracked)
	if a.currentScope != nil && sym.Name != "" {
		a.currentScope.Refinements[sym.Name] = tracked
	}
}

func cloneBorrowReturnAnnotationSteps(steps []borrowReturnAnnotationStep) []borrowReturnAnnotationStep {
	if len(steps) == 0 {
		return nil
	}
	cloned := make([]borrowReturnAnnotationStep, len(steps))
	for i, step := range steps {
		cloned[i] = cloneBorrowReturnAnnotationStep(step)
	}
	return cloned
}

func joinBorrowReturnAnnotationSteps(prefix []borrowReturnAnnotationStep, suffix []borrowReturnAnnotationStep) []borrowReturnAnnotationStep {
	if len(prefix) == 0 {
		return cloneBorrowReturnAnnotationSteps(suffix)
	}
	if len(suffix) == 0 {
		return cloneBorrowReturnAnnotationSteps(prefix)
	}
	joined := make([]borrowReturnAnnotationStep, 0, len(prefix)+len(suffix))
	joined = append(joined, cloneBorrowReturnAnnotationSteps(prefix)...)
	joined = append(joined, cloneBorrowReturnAnnotationSteps(suffix)...)
	return joined
}

func borrowReturnAnnotationPathEqual(left []borrowReturnAnnotationStep, right []borrowReturnAnnotationStep) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !borrowReturnAnnotationStepsEqual(left[i], right[i]) {
			return false
		}
	}
	return true
}

func poststatePathsOverlap(left []borrowReturnAnnotationStep, right []borrowReturnAnnotationStep) bool {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		l := left[i]
		r := right[i]
		switch {
		case l.Field != "" || r.Field != "":
			if l.Field == "" || r.Field == "" || l.Field != r.Field {
				return false
			}
		case l.Wildcard || r.Wildcard:
			continue
		case l.Index != nil || r.Index != nil:
			if l.Index == nil || r.Index == nil || *l.Index != *r.Index {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (a *Analyzer) noteConservativeCallWidening(root *Symbol, steps []borrowReturnAnnotationStep) {
	if a == nil || a.currentConservativeCallWidenings == nil || root == nil {
		return
	}
	cloned := cloneBorrowReturnAnnotationSteps(steps)
	for _, existing := range a.currentConservativeCallWidenings[root] {
		if borrowReturnAnnotationPathEqual(existing, cloned) {
			return
		}
	}
	a.currentConservativeCallWidenings[root] = append(a.currentConservativeCallWidenings[root], cloned)
}

func namedStateTargetDisplayName(root *Symbol, steps []borrowReturnAnnotationStep) string {
	if root == nil || root.Name == "" {
		return "<value>"
	}
	var b strings.Builder
	b.WriteString(root.Name)
	for _, step := range steps {
		switch {
		case step.Field != "":
			b.WriteString(".")
			b.WriteString(step.Field)
		case step.Index != nil:
			b.WriteString("[")
			b.WriteString(strconv.FormatInt(*step.Index, 10))
			b.WriteString("]")
		case step.Wildcard:
			b.WriteString("[*]")
		}
	}
	return b.String()
}

func namedStateTargetPathExpr(pos lexer.Pos, root *Symbol, steps []borrowReturnAnnotationStep) (ast.Expr, bool) {
	if root == nil || root.Name == "" {
		return nil, false
	}
	var expr ast.Expr = &ast.Ident{Position: pos, Name: root.Name}
	for _, step := range steps {
		switch {
		case step.Field != "":
			expr = &ast.FieldExpr{Position: pos, Object: expr, Field: step.Field}
		case step.Index != nil:
			expr = &ast.IndexExpr{Position: pos, Object: expr, Index: &ast.IntLit{Position: pos, Value: strconv.FormatInt(*step.Index, 10)}}
		default:
			return nil, false
		}
	}
	return expr, true
}

func (a *Analyzer) namedStateMutationTargetPath(expr ast.Expr) (*Symbol, []borrowReturnAnnotationStep, bool) {
	if expr == nil {
		return nil, nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.namedStateMutationTargetPath(n.Inner)
	case *ast.CastExpr:
		return a.namedStateMutationTargetPath(n.Operand)
	case *ast.MoveExpr:
		return a.namedStateMutationTargetPath(n.Operand)
	case *ast.CanExpr:
		return a.namedStateMutationTargetPath(n.Expr)
	case *ast.AddrOfExpr:
		return a.namedStateMutationTargetPath(n.Operand)
	case *ast.Ident:
		if a.currentScope == nil {
			return nil, nil, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil {
			return nil, nil, false
		}
		if _, isRef := sym.Type.(*RefType); isRef && a.currentValueBindings != nil {
			if valueExpr, ok := a.currentValueBindings[sym]; ok && valueExpr != nil {
				if root, steps, ok := a.namedStateMutationTargetPath(valueExpr); ok {
					return root, steps, true
				}
			}
			if root := symbolAliasRoot(sym); root != nil && root != sym {
				if valueExpr, ok := a.currentValueBindings[root]; ok && valueExpr != nil {
					if resolvedRoot, steps, ok := a.namedStateMutationTargetPath(valueExpr); ok {
						return resolvedRoot, steps, true
					}
				}
			}
		}
		return sym, nil, true
	case *ast.FieldExpr:
		root, steps, ok := a.namedStateMutationTargetPath(n.Object)
		if !ok || root == nil {
			return nil, nil, false
		}
		return root, append(steps, borrowReturnAnnotationStep{Field: n.Field}), true
	case *ast.IndexExpr:
		root, steps, ok := a.namedStateMutationTargetPath(n.Object)
		if !ok || root == nil {
			return nil, nil, false
		}
		step, ok := a.resolveProjectedFieldIndexStep(n.Index)
		if !ok {
			step = borrowReturnAnnotationStep{Wildcard: true}
		}
		return root, append(steps, step), true
	default:
		return nil, nil, false
	}
}

func namedStateAssignmentFieldPrefix(steps []borrowReturnAnnotationStep) []string {
	fields := make([]string, 0, len(steps))
	for _, step := range steps {
		if step.Field == "" {
			break
		}
		fields = append(fields, step.Field)
	}
	return fields
}

func namedStatePathsOverlap(left []string, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func namedStateDerivedExprDependsOnPath(expr ast.Expr, fields []string) bool {
	if len(fields) == 0 || expr == nil {
		return false
	}
	if path, ok := derivedStateSelfFieldPath(expr); ok {
		return namedStatePathsOverlap(path, fields)
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return namedStateDerivedExprDependsOnPath(n.Inner, fields)
	case *ast.UnaryExpr:
		return namedStateDerivedExprDependsOnPath(n.Operand, fields)
	case *ast.BinaryExpr:
		return namedStateDerivedExprDependsOnPath(n.Left, fields) || namedStateDerivedExprDependsOnPath(n.Right, fields)
	default:
		return false
	}
}

func namedStateAssignmentAffectsDerivedState(base *StructType, steps []borrowReturnAnnotationStep) bool {
	if base == nil || len(base.DerivedStates) == 0 {
		return false
	}
	if len(steps) == 0 {
		return true
	}
	fields := namedStateAssignmentFieldPrefix(steps)
	if len(fields) == 0 {
		return true
	}
	for _, state := range base.DerivedStates {
		if namedStateDerivedExprDependsOnPath(state.Condition, fields) {
			return true
		}
	}
	return false
}

func (a *Analyzer) inferDirectFieldAssignedNamedState(pos lexer.Pos, root *Symbol, structSteps []borrowReturnAnnotationStep, base *StructType, fieldName string, value ast.Expr) (Type, bool) {
	if a == nil || root == nil || base == nil || base.Decl == nil || len(base.NamedStateCases) == 0 {
		return nil, false
	}
	rootExpr, ok := namedStateTargetPathExpr(pos, root, structSteps)
	if !ok || rootExpr == nil {
		return nil, false
	}
	targetName := namedStateTargetDisplayName(root, structSteps)
	fieldValues := make(map[string]ast.Expr, len(base.Decl.Fields))
	for _, fieldDecl := range base.Decl.Fields {
		if fieldDecl.Name == fieldName {
			fieldValues[fieldDecl.Name] = value
			continue
		}
		fieldValues[fieldDecl.Name] = &ast.FieldExpr{Position: pos, Object: rootExpr, Field: fieldDecl.Name}
	}
	trueStates := make([]string, 0, len(base.NamedStateCases))
	for _, stateName := range base.NamedStateCases {
		proven, holds := a.evaluateDerivedStateForFields(base, stateName, fieldValues)
		if !proven {
			return nil, false
		}
		if holds {
			trueStates = append(trueStates, stateName)
		}
	}
	switch len(trueStates) {
	case 1:
		return newNamedStateType(base.Name, base.NamedStateCases, trueStates), true
	case 0:
		a.errorf(pos, "assignment to %q leaves %q in no derived state", fieldName, targetName)
		return fullNamedStateType(base), true
	default:
		a.errorf(pos, "assignment to %q leaves %q satisfying multiple derived states: %s", fieldName, targetName, strings.Join(trueStates, ", "))
		return fullNamedStateType(base), true
	}
}

func (a *Analyzer) widenNamedStatesDeep(t Type) (Type, bool) {
	return a.widenNamedStatesDeepWithSeen(t, map[Type]bool{})
}

func (a *Analyzer) widenNamedStatesDeepWithSeen(t Type, seen map[Type]bool) (Type, bool) {
	if t == nil {
		return nil, false
	}
	if seen[t] {
		return nil, false
	}
	seen[t] = true
	defer delete(seen, t)
	switch tt := t.(type) {
	case *AggregateStateType:
		nextBase, ok := a.widenNamedStatesDeepWithSeen(tt.Base, seen)
		if !ok {
			return nil, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *RefType:
		nextElem, ok := a.widenNamedStatesDeepWithSeen(tt.Elem, seen)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *StructType:
		if base, ok := namedStateStructBase(tt); ok && base != nil {
			if widened := replaceTrackedNamedStateArg(tt, fullNamedStateType(base)); widened != nil && !SameType(widened, t) {
				if next, ok := a.widenNamedStatesDeepWithSeen(widened, seen); ok {
					return next, true
				}
				return widened, true
			}
		}
		changed := false
		fields := cloneStructFields(tt.Fields)
		for name, field := range tt.Fields {
			nextField, ok := a.widenNamedStatesDeepWithSeen(field.Type, seen)
			if !ok {
				continue
			}
			field.Type = nextField
			fields[name] = field
			changed = true
		}
		if !changed {
			return nil, false
		}
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		current := tt
		changed := false
		if base, ok := current.Base.(*StructType); ok && base != nil && len(base.NamedStateCases) != 0 {
			fullState := fullNamedStateType(base)
			if currentState, ok := namedStateCurrentArg(current); ok && currentState != nil && !sameNamedStateType(currentState, fullState) {
				idx := namedStateArgIndex(base)
				if idx >= 0 && idx < len(current.Args) {
					args := append([]Type(nil), current.Args...)
					args[idx] = fullState
					cloned := *current
					cloned.Args = args
					current = &cloned
					changed = true
				}
			}
		}
		baseStruct, ok := current.Base.(*StructType)
		if !ok || baseStruct == nil {
			if changed {
				return current, true
			}
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		for name, field := range baseStruct.Fields {
			currentFieldType, ok := a.lookupResolvedFieldType(current, name)
			if !ok {
				currentFieldType = field.Type
			}
			nextField, ok := a.widenNamedStatesDeepWithSeen(currentFieldType, seen)
			if !ok {
				continue
			}
			field.Type = nextField
			fields[name] = field
			changed = true
		}
		if !changed {
			return nil, false
		}
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *current
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return nil, false
	}
}

func (a *Analyzer) widenNamedStatesDeepAtPath(current Type, steps []borrowReturnAnnotationStep) (Type, bool) {
	if current == nil {
		return nil, false
	}
	if len(steps) == 0 {
		return a.widenNamedStatesDeep(current)
	}
	switch tt := current.(type) {
	case *AggregateStateType:
		nextBase, ok := a.widenNamedStatesDeepAtPath(tt.Base, steps)
		if !ok {
			return nil, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *RefType:
		nextElem, ok := a.widenNamedStatesDeepAtPath(tt.Elem, steps)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	}
	step := steps[0]
	if step.Field == "" {
		return a.widenNamedStatesDeep(current)
	}
	switch tt := current.(type) {
	case *StructType:
		field, ok := tt.Fields[step.Field]
		if !ok {
			return nil, false
		}
		nextField, ok := a.widenNamedStatesDeepAtPath(field.Type, steps[1:])
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(tt.Fields)
		field.Type = nextField
		fields[step.Field] = field
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok || baseStruct == nil {
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(tt, step.Field)
		if !ok {
			return nil, false
		}
		nextField, ok := a.widenNamedStatesDeepAtPath(currentFieldType, steps[1:])
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		field, ok := fields[step.Field]
		if !ok {
			return nil, false
		}
		field.Type = nextField
		fields[step.Field] = field
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return a.widenNamedStatesDeep(current)
	}
}

func (a *Analyzer) projectTrackedValueTypeAtPath(current Type, steps []borrowReturnAnnotationStep) (Type, bool) {
	if current == nil {
		return nil, false
	}
	if len(steps) == 0 {
		return a.cloneTrackedValueType(current), true
	}
	switch tt := current.(type) {
	case *AggregateStateType:
		return a.projectTrackedValueTypeAtPath(tt.Base, steps)
	case *RefType:
		return a.projectTrackedValueTypeAtPath(tt.Elem, steps)
	}
	step := steps[0]
	if step.Field != "" {
		fieldType, ok := a.lookupResolvedFieldType(current, step.Field)
		if !ok {
			return nil, false
		}
		return a.projectTrackedValueTypeAtPath(fieldType, steps[1:])
	}
	if step.Wildcard || step.Index != nil {
		switch tt := current.(type) {
		case *ArrayType:
			return a.projectTrackedValueTypeAtPath(tt.Elem, steps[1:])
		case *DArrayType:
			return a.projectTrackedValueTypeAtPath(tt.Elem, steps[1:])
		case *ViewType:
			return a.projectTrackedValueTypeAtPath(tt.Elem, steps[1:])
		case *DArrayViewType:
			return a.projectTrackedValueTypeAtPath(tt.Elem, steps[1:])
		}
	}
	return nil, false
}

func (a *Analyzer) replaceTrackedValueTypeAtPath(current Type, steps []borrowReturnAnnotationStep, replacement Type) (Type, bool) {
	if current == nil || replacement == nil {
		return nil, false
	}
	if len(steps) == 0 {
		return a.cloneTrackedValueType(replacement), true
	}
	switch tt := current.(type) {
	case *AggregateStateType:
		nextBase, ok := a.replaceTrackedValueTypeAtPath(tt.Base, steps, replacement)
		if !ok {
			return nil, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *RefType:
		nextElem, ok := a.replaceTrackedValueTypeAtPath(tt.Elem, steps, replacement)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *ArrayType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.replaceTrackedValueTypeAtPath(tt.Elem, steps[1:], replacement)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *DArrayType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.replaceTrackedValueTypeAtPath(tt.Elem, steps[1:], replacement)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *ViewType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.replaceTrackedValueTypeAtPath(tt.Elem, steps[1:], replacement)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *DArrayViewType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.replaceTrackedValueTypeAtPath(tt.Elem, steps[1:], replacement)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	}
	step := steps[0]
	if step.Field == "" {
		return nil, false
	}
	switch tt := current.(type) {
	case *StructType:
		field, ok := tt.Fields[step.Field]
		if !ok {
			return nil, false
		}
		nextFieldType, ok := a.replaceTrackedValueTypeAtPath(field.Type, steps[1:], replacement)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(tt.Fields)
		field.Type = nextFieldType
		fields[step.Field] = field
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok || baseStruct == nil {
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(tt, step.Field)
		if !ok {
			return nil, false
		}
		nextFieldType, ok := a.replaceTrackedValueTypeAtPath(currentFieldType, steps[1:], replacement)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		field, ok := fields[step.Field]
		if !ok {
			return nil, false
		}
		field.Type = nextFieldType
		fields[step.Field] = field
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return nil, false
	}
}

func (a *Analyzer) applyNamedStatePoststateAtPath(current Type, steps []borrowReturnAnnotationStep, stateCases []string) (Type, bool) {
	if current == nil {
		return nil, false
	}
	switch tt := current.(type) {
	case *AggregateStateType:
		nextBase, ok := a.applyNamedStatePoststateAtPath(tt.Base, steps, stateCases)
		if !ok {
			return nil, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *RefType:
		nextElem, ok := a.applyNamedStatePoststateAtPath(tt.Elem, steps, stateCases)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	}
	if len(steps) == 0 {
		base, ok := namedStateStructBase(current)
		if !ok || base == nil {
			return nil, false
		}
		desired := newNamedStateType(base.Name, base.NamedStateCases, stateCases)
		if desired == nil {
			return nil, false
		}
		replaced := replaceTrackedNamedStateArg(current, desired)
		if replaced == nil {
			return nil, false
		}
		return replaced, true
	}
	step := steps[0]
	if step.Field == "" {
		return nil, false
	}
	switch tt := current.(type) {
	case *StructType:
		field, ok := tt.Fields[step.Field]
		if !ok {
			return nil, false
		}
		nextField, ok := a.applyNamedStatePoststateAtPath(field.Type, steps[1:], stateCases)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(tt.Fields)
		field.Type = nextField
		fields[step.Field] = field
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok || baseStruct == nil {
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(tt, step.Field)
		if !ok {
			return nil, false
		}
		nextField, ok := a.applyNamedStatePoststateAtPath(currentFieldType, steps[1:], stateCases)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		field, ok := fields[step.Field]
		if !ok {
			return nil, false
		}
		field.Type = nextField
		fields[step.Field] = field
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return nil, false
	}
}

func (a *Analyzer) applyRefStatePoststateAtPath(current Type, steps []borrowReturnAnnotationStep, desired RefState) (Type, bool) {
	if current == nil {
		return nil, false
	}
	switch tt := current.(type) {
	case *AggregateStateType:
		nextBase, ok := a.applyRefStatePoststateAtPath(tt.Base, steps, desired)
		if !ok {
			return nil, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *RefType:
		if len(steps) == 0 {
			return cloneRefTypeWithState(tt, desired), true
		}
		nextElem, ok := a.applyRefStatePoststateAtPath(tt.Elem, steps, desired)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *ArrayType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.applyRefStatePoststateAtPath(tt.Elem, steps[1:], desired)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *DArrayType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.applyRefStatePoststateAtPath(tt.Elem, steps[1:], desired)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *ViewType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.applyRefStatePoststateAtPath(tt.Elem, steps[1:], desired)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *DArrayViewType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.applyRefStatePoststateAtPath(tt.Elem, steps[1:], desired)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	}
	if len(steps) == 0 {
		return nil, false
	}
	step := steps[0]
	if step.Field == "" {
		return nil, false
	}
	switch tt := current.(type) {
	case *StructType:
		field, ok := tt.Fields[step.Field]
		if !ok {
			return nil, false
		}
		nextField, ok := a.applyRefStatePoststateAtPath(field.Type, steps[1:], desired)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(tt.Fields)
		field.Type = nextField
		fields[step.Field] = field
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok || baseStruct == nil {
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(tt, step.Field)
		if !ok {
			return nil, false
		}
		nextField, ok := a.applyRefStatePoststateAtPath(currentFieldType, steps[1:], desired)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		field, ok := fields[step.Field]
		if !ok {
			return nil, false
		}
		field.Type = nextField
		fields[step.Field] = field
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return nil, false
	}
}

func (a *Analyzer) applyFuncPoststateAtPath(original Type, current Type, steps []borrowReturnAnnotationStep, poststate FuncPoststate) (Type, bool) {
	if current == nil {
		return nil, false
	}
	switch poststate.Kind {
	case FuncPoststateKindPreserve:
		replacement, ok := a.projectTrackedValueTypeAtPath(original, steps)
		if !ok {
			return nil, false
		}
		return a.replaceTrackedValueTypeAtPath(current, steps, replacement)
	case FuncPoststateKindNamedState:
		return a.applyNamedStatePoststateAtPath(current, steps, poststate.StateCases)
	case FuncPoststateKindRefState:
		return a.applyRefStatePoststateAtPath(current, steps, poststate.RefState)
	default:
		return nil, false
	}
}

func splitFuncPoststatesByCondition(poststates []FuncPoststate) (always []FuncPoststate, whenTrue []FuncPoststate, whenFalse []FuncPoststate) {
	for _, poststate := range poststates {
		switch poststate.Condition.Kind {
		case FuncPoststateConditionReturnBool:
			if poststate.Condition.ReturnBool {
				whenTrue = append(whenTrue, poststate)
			} else {
				whenFalse = append(whenFalse, poststate)
			}
		default:
			always = append(always, poststate)
		}
	}
	return always, whenTrue, whenFalse
}

func (a *Analyzer) applyFuncPoststateListAtArgPath(original Type, current Type, argSteps []borrowReturnAnnotationStep, poststates []FuncPoststate) (Type, bool) {
	if current == nil || len(poststates) == 0 {
		return current, false
	}
	updated := current
	changed := false
	for _, poststate := range poststates {
		fullPath := joinBorrowReturnAnnotationSteps(argSteps, poststate.Path)
		next, ok := a.applyFuncPoststateAtPath(original, updated, fullPath, poststate)
		if !ok || next == nil {
			continue
		}
		updated = next
		changed = true
	}
	return updated, changed
}

type trackedValueMergePair struct {
	Left  Type
	Right Type
}

func (a *Analyzer) mergeTrackedValueTypes(left Type, right Type) (Type, bool) {
	return a.mergeTrackedValueTypesWithSeen(left, right, map[trackedValueMergePair]Type{})
}

func (a *Analyzer) mergeTrackedValueTypesWithSeen(left Type, right Type, seen map[trackedValueMergePair]Type) (Type, bool) {
	if left == nil || right == nil {
		return nil, false
	}
	if SameType(left, right) {
		return a.cloneTrackedValueType(left), true
	}
	if merged := MergeTypes(left, right); !IsInvalidType(merged) {
		return merged, true
	}
	pair := trackedValueMergePair{Left: left, Right: right}
	if merged, ok := seen[pair]; ok {
		return merged, true
	}
	switch lt := left.(type) {
	case *AggregateStateType:
		rt, ok := right.(*AggregateStateType)
		if !ok {
			return nil, false
		}
		states, ok := mergeAggregateStateLists(aggregateStateStates(lt), aggregateStateStates(rt))
		if !ok {
			return nil, false
		}
		base, ok := a.mergeTrackedValueTypesWithSeen(lt.Base, rt.Base, seen)
		if !ok || base == nil {
			return nil, false
		}
		merged := cloneAggregateStateWithBase(base, states)
		seen[pair] = merged
		return merged, true
	case *RefType:
		rt, ok := right.(*RefType)
		if !ok || lt.StateParam != rt.StateParam || lt.StorageParam != rt.StorageParam {
			return nil, false
		}
		storage, explicit, okStorage := mergeRefStorages(lt.Storage, rt.Storage, lt.ExplicitStorage, rt.ExplicitStorage)
		region, okRegion := mergeRefRegions(lt.Region, rt.Region)
		state, okState := mergeRefStates(lt.State, rt.State)
		if !okStorage || !okRegion || !okState {
			return nil, false
		}
		merged := &RefType{Mutable: lt.Mutable && rt.Mutable, State: state, StateParam: lt.StateParam, Storage: storage, StorageParam: lt.StorageParam, Region: region, ExplicitStorage: explicit}
		seen[pair] = merged
		elem, ok := a.mergeTrackedValueTypesWithSeen(lt.Elem, rt.Elem, seen)
		if !ok || elem == nil {
			return nil, false
		}
		merged.Elem = elem
		return merged, true
	case *ArrayType:
		rt, ok := right.(*ArrayType)
		if !ok || lt.SurfaceName != rt.SurfaceName || !arraySizesEqual(lt, rt) {
			return nil, false
		}
		elem, ok := a.mergeTrackedValueTypesWithSeen(lt.Elem, rt.Elem, seen)
		if !ok || elem == nil {
			return nil, false
		}
		merged := *lt
		merged.Elem = elem
		seen[pair] = &merged
		return &merged, true
	case *DArrayType:
		rt, ok := right.(*DArrayType)
		if !ok || lt.SurfaceName != rt.SurfaceName || !SameShape(lt.Shape, rt.Shape) {
			return nil, false
		}
		elem, ok := a.mergeTrackedValueTypesWithSeen(lt.Elem, rt.Elem, seen)
		if !ok || elem == nil {
			return nil, false
		}
		merged := *lt
		merged.Elem = elem
		seen[pair] = &merged
		return &merged, true
	case *ViewType:
		rt, ok := right.(*ViewType)
		if !ok {
			return nil, false
		}
		elem, ok := a.mergeTrackedValueTypesWithSeen(lt.Elem, rt.Elem, seen)
		if !ok || elem == nil {
			return nil, false
		}
		merged := *lt
		if !viewBoundsEqual(lt, rt) {
			merged.Begin = ""
			merged.End = ""
		}
		merged.Elem = elem
		seen[pair] = &merged
		return &merged, true
	case *DArrayViewType:
		rt, ok := right.(*DArrayViewType)
		if !ok || lt.SurfaceName != rt.SurfaceName {
			return nil, false
		}
		elem, ok := a.mergeTrackedValueTypesWithSeen(lt.Elem, rt.Elem, seen)
		if !ok || elem == nil {
			return nil, false
		}
		merged := *lt
		if lt.Begin != rt.Begin || lt.End != rt.End {
			merged.Begin = ""
			merged.End = ""
		}
		merged.Elem = elem
		seen[pair] = &merged
		return &merged, true
	case *OptionalType:
		rt, ok := right.(*OptionalType)
		if !ok {
			return nil, false
		}
		value, ok := a.mergeTrackedValueTypesWithSeen(lt.Value, rt.Value, seen)
		if !ok || value == nil {
			return nil, false
		}
		merged := &OptionalType{Value: value}
		seen[pair] = merged
		return merged, true
	case *DictType:
		rt, ok := right.(*DictType)
		if !ok || lt.SurfaceName != rt.SurfaceName {
			return nil, false
		}
		key, ok := a.mergeTrackedValueTypesWithSeen(lt.Key, rt.Key, seen)
		if !ok || key == nil {
			return nil, false
		}
		value, ok := a.mergeTrackedValueTypesWithSeen(lt.Value, rt.Value, seen)
		if !ok || value == nil {
			return nil, false
		}
		merged := &DictType{Key: key, Value: value, SurfaceName: lt.SurfaceName}
		seen[pair] = merged
		return merged, true
	case *StructType:
		rt, ok := right.(*StructType)
		if !ok || lt.Name != rt.Name {
			return nil, false
		}
		merged := *lt
		merged.Fields = cloneStructFields(lt.Fields)
		mergedPtr := &merged
		seen[pair] = mergedPtr
		for name, leftField := range lt.Fields {
			rightField, ok := rt.Fields[name]
			if !ok {
				return nil, false
			}
			if SameType(leftField.Type, rightField.Type) {
				continue
			}
			fieldType, ok := a.mergeTrackedValueTypesWithSeen(leftField.Type, rightField.Type, seen)
			if !ok || fieldType == nil {
				return nil, false
			}
			leftField.Type = fieldType
			mergedPtr.Fields[name] = leftField
		}
		return mergedPtr, true
	case *GenericInstanceType:
		rt, ok := right.(*GenericInstanceType)
		if !ok || lt.Name != rt.Name || len(lt.Args) != len(rt.Args) {
			return nil, false
		}
		merged := *lt
		merged.Args = append([]Type(nil), lt.Args...)
		mergedPtr := &merged
		seen[pair] = mergedPtr
		for i := range lt.Args {
			if SameType(lt.Args[i], rt.Args[i]) {
				continue
			}
			argType, ok := a.mergeTrackedValueTypesWithSeen(lt.Args[i], rt.Args[i], seen)
			if !ok || argType == nil {
				return nil, false
			}
			mergedPtr.Args[i] = argType
		}
		if SameType(lt.Base, rt.Base) {
			mergedPtr.Base = a.cloneTrackedValueType(lt.Base)
			return mergedPtr, true
		}
		base, ok := a.mergeTrackedValueTypesWithSeen(lt.Base, rt.Base, seen)
		if !ok || base == nil {
			return nil, false
		}
		mergedPtr.Base = base
		return mergedPtr, true
	case *PackedEnumStoreType:
		rt, ok := right.(*PackedEnumStoreType)
		if !ok || lt.Name != rt.Name || lt.Enum != rt.Enum {
			return nil, false
		}
		if lt.State == nil && rt.State == nil {
			merged := *lt
			seen[pair] = &merged
			return &merged, true
		}
		state, ok := a.mergeTrackedValueTypesWithSeen(lt.State, rt.State, seen)
		if !ok || state == nil {
			return nil, false
		}
		merged := *lt
		merged.State = state
		seen[pair] = &merged
		return &merged, true
	case *DStrType:
		rt, ok := right.(*DStrType)
		if !ok || lt.SurfaceName != rt.SurfaceName {
			return nil, false
		}
		merged := *lt
		if !SameShape(lt.Shape, rt.Shape) {
			merged.Shape = &WildcardShape{}
		}
		seen[pair] = &merged
		return &merged, true
	case *SViewType:
		rt, ok := right.(*SViewType)
		if !ok {
			return nil, false
		}
		merged := *lt
		if lt.Begin != rt.Begin || lt.End != rt.End {
			merged.Begin = ""
			merged.End = ""
		}
		seen[pair] = &merged
		return &merged, true
	default:
		return nil, false
	}
}

func (a *Analyzer) computeCallArgPoststateTrackedType(original Type, argSteps []borrowReturnAnnotationStep, poststates []FuncPoststate, outcomeKnown bool, outcomeValue bool) (Type, bool) {
	if original == nil {
		return nil, false
	}
	if len(poststates) == 0 {
		widened, ok := a.widenNamedStatesDeepAtPath(original, argSteps)
		if !ok || widened == nil || SameType(widened, original) {
			return nil, false
		}
		return widened, true
	}
	baseline := original
	baselineChanged := false
	if widened, ok := a.widenNamedStatesDeepAtPath(baseline, argSteps); ok && widened != nil {
		baseline = widened
		baselineChanged = true
	}
	updated := baseline
	always, whenTrue, whenFalse := splitFuncPoststatesByCondition(poststates)
	changed := baselineChanged
	if next, applied := a.applyFuncPoststateListAtArgPath(original, updated, argSteps, always); applied && next != nil {
		updated = next
		changed = true
	}
	if len(whenTrue) == 0 && len(whenFalse) == 0 {
		if !changed {
			return nil, false
		}
		return updated, true
	}
	if outcomeKnown {
		branch := updated
		matching := whenFalse
		if outcomeValue {
			matching = whenTrue
		}
		if next, applied := a.applyFuncPoststateListAtArgPath(original, branch, argSteps, matching); applied && next != nil {
			branch = next
			changed = true
		}
		if !changed {
			return nil, false
		}
		return branch, true
	}
	trueBranch := updated
	falseBranch := updated
	branchChanged := false
	if next, applied := a.applyFuncPoststateListAtArgPath(original, trueBranch, argSteps, whenTrue); applied && next != nil {
		trueBranch = next
		branchChanged = true
	}
	if next, applied := a.applyFuncPoststateListAtArgPath(original, falseBranch, argSteps, whenFalse); applied && next != nil {
		falseBranch = next
		branchChanged = true
	}
	joined, ok := a.mergeTrackedValueTypes(trueBranch, falseBranch)
	if !ok || joined == nil || IsInvalidType(joined) {
		joined = updated
	}
	if !changed && !branchChanged {
		return nil, false
	}
	return joined, true
}

func (a *Analyzer) rememberConditionalCallPoststates(call *ast.CallExpr, fnType *FuncType, originalByRoot map[*Symbol]Type) {
	if a == nil || call == nil || fnType == nil || !funcPoststatesHaveConditional(fnType.Poststates) {
		return
	}
	if a.conditionalCallPoststateOriginals == nil {
		a.conditionalCallPoststateOriginals = map[*ast.CallExpr]map[*Symbol]Type{}
	}
	a.conditionalCallPoststateOriginals[call] = a.cloneTrackedTypesByRoot(originalByRoot)
}

func (a *Analyzer) updateNamedStateTypeAtPath(root *Symbol, current Type, structSteps []borrowReturnAnnotationStep, steps []borrowReturnAnnotationStep, pos lexer.Pos, value ast.Expr, valueType Type, unknown bool) (Type, bool) {
	if current == nil {
		return nil, false
	}
	switch tt := current.(type) {
	case *AggregateStateType:
		nextBase, ok := a.updateNamedStateTypeAtPath(root, tt.Base, structSteps, steps, pos, value, valueType, unknown)
		if !ok {
			return nil, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *RefType:
		nextElem, ok := a.updateNamedStateTypeAtPath(root, tt.Elem, structSteps, steps, pos, value, valueType, unknown)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	}
	updatedCurrent := current
	changed := false
	if base, ok := trackedNamedStateStructBase(updatedCurrent); ok && base != nil {
		if len(steps) == 0 {
			if unknown {
				if widened := replaceTrackedNamedStateArg(updatedCurrent, fullNamedStateType(base)); widened != nil {
					return widened, !SameType(widened, current)
				}
			} else if trackedType, ok := applyNamedStateFromActualType(updatedCurrent, valueType); ok {
				return trackedType, !SameType(trackedType, current)
			}
		} else if namedStateAssignmentAffectsDerivedState(base, steps) {
			state := fullNamedStateType(base)
			if !unknown && len(steps) == 1 && steps[0].Field != "" {
				if inferredState, ok := a.inferDirectFieldAssignedNamedState(pos, root, structSteps, base, steps[0].Field, value); ok {
					state = inferredState
				}
			}
			if replaced := replaceTrackedNamedStateArg(updatedCurrent, state); replaced != nil {
				updatedCurrent = replaced
				changed = !SameType(replaced, current)
			}
		}
	}
	if len(steps) == 0 {
		if changed {
			return updatedCurrent, true
		}
		return nil, false
	}
	step := steps[0]
	if step.Field == "" {
		if widened, ok := a.widenNamedStatesDeep(updatedCurrent); ok {
			return widened, true
		}
		if changed {
			return updatedCurrent, true
		}
		return nil, false
	}
	nextStructSteps := append(cloneBorrowReturnAnnotationSteps(structSteps), cloneBorrowReturnAnnotationStep(step))
	switch currentType := updatedCurrent.(type) {
	case *StructType:
		field, ok := currentType.Fields[step.Field]
		if !ok {
			if changed {
				return updatedCurrent, true
			}
			return nil, false
		}
		nextFieldType, ok := a.updateNamedStateTypeAtPath(root, field.Type, nextStructSteps, steps[1:], pos, value, valueType, unknown)
		if !ok {
			if changed {
				return updatedCurrent, true
			}
			return nil, false
		}
		fields := cloneStructFields(currentType.Fields)
		field.Type = nextFieldType
		fields[step.Field] = field
		return cloneStructTypeWithFields(currentType, fields), true
	case *GenericInstanceType:
		baseStruct, ok := currentType.Base.(*StructType)
		if !ok || baseStruct == nil {
			if changed {
				return updatedCurrent, true
			}
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(currentType, step.Field)
		if !ok {
			if changed {
				return updatedCurrent, true
			}
			return nil, false
		}
		nextFieldType, ok := a.updateNamedStateTypeAtPath(root, currentFieldType, nextStructSteps, steps[1:], pos, value, valueType, unknown)
		if !ok {
			if changed {
				return updatedCurrent, true
			}
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		field, ok := fields[step.Field]
		if !ok {
			if changed {
				return updatedCurrent, true
			}
			return nil, false
		}
		field.Type = nextFieldType
		fields[step.Field] = field
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *currentType
		cloned.Base = clonedBase
		return &cloned, true
	default:
		if changed {
			return updatedCurrent, true
		}
		return nil, false
	}
}

func (a *Analyzer) recordNamedStateAssignmentTarget(target ast.Expr, value ast.Expr, valueType Type) {
	if a.currentSpecializedValueTypes == nil || target == nil {
		return
	}
	root, steps, ok := a.namedStateMutationTargetPath(target)
	if !ok || root == nil {
		return
	}
	current := a.currentTrackedValueType(root)
	updatedType, ok := a.updateNamedStateTypeAtPath(root, current, nil, steps, target.Pos(), value, valueType, false)
	if !ok || updatedType == nil {
		return
	}
	a.bindTrackedValueType(root, updatedType)
}

func (a *Analyzer) recordNamedStateAugAssignTarget(target ast.Expr) {
	if a.currentSpecializedValueTypes == nil || target == nil {
		return
	}
	root, steps, ok := a.namedStateMutationTargetPath(target)
	if !ok || root == nil {
		return
	}
	current := a.currentTrackedValueType(root)
	updatedType, ok := a.updateNamedStateTypeAtPath(root, current, nil, steps, target.Pos(), nil, nil, true)
	if !ok || updatedType == nil {
		return
	}
	a.bindTrackedValueType(root, updatedType)
}

func (a *Analyzer) recordNamedStateCallArgMutation(arg ast.Expr, paramType Type) {
	if a.currentSpecializedValueTypes == nil || arg == nil || paramType == nil {
		return
	}
	refType, ok := paramType.(*RefType)
	if !ok || refType == nil {
		return
	}
	root, steps, ok := a.namedStateMutationTargetPath(arg)
	if !ok || root == nil {
		return
	}
	a.noteConservativeCallWidening(root, steps)
	current := a.currentTrackedValueType(root)
	updatedType, ok := a.widenNamedStatesDeepAtPath(current, steps)
	if !ok || updatedType == nil {
		return
	}
	a.bindTrackedValueType(root, updatedType)
}

func funcPoststatesForParam(poststates []FuncPoststate, paramIndex int) []FuncPoststate {
	if len(poststates) == 0 {
		return nil
	}
	filtered := make([]FuncPoststate, 0, 1)
	for _, poststate := range poststates {
		if poststate.ParamIndex == paramIndex {
			filtered = append(filtered, poststate)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func (a *Analyzer) recordCallArgPoststates(arg ast.Expr, paramType Type, poststates []FuncPoststate, originalByRoot map[*Symbol]Type) {
	if len(poststates) == 0 {
		a.recordNamedStateCallArgMutation(arg, paramType)
		return
	}
	if a.currentSpecializedValueTypes == nil || arg == nil || paramType == nil {
		return
	}
	if _, ok := paramType.(*RefType); !ok {
		return
	}
	root, argSteps, ok := a.namedStateMutationTargetPath(arg)
	if !ok || root == nil {
		return
	}
	original := originalByRoot[root]
	if original == nil {
		original = a.currentTrackedValueType(root)
		originalByRoot[root] = original
	}
	updated, changed := a.computeCallArgPoststateTrackedType(original, argSteps, poststates, false, false)
	if changed && updated != nil {
		a.bindTrackedValueType(root, updated)
	}
}

func (a *Analyzer) recordFunctionValueBinding(sym *Symbol, value ast.Expr) {
	if a.currentFunctionValues == nil || sym == nil {
		return
	}
	if sym.Kind != SymbolLocal && sym.Kind != SymbolParam {
		return
	}
	fnType, ok := a.functionValueTypeForExpr(value)
	if !ok {
		delete(a.currentFunctionValues, sym)
		return
	}
	a.currentFunctionValues[sym] = fnType
}

func (a *Analyzer) updateSpecializedValueTypeAtPath(declared Type, current Type, steps []borrowReturnAnnotationStep, actual Type) (Type, bool) {
	if declared == nil {
		return nil, false
	}
	if current == nil {
		current = declared
	}
	if len(steps) == 0 {
		if refined := assignedRefinementType(declared, actual); refined != nil {
			return refined, true
		}
		if tracked, ok := applyNamedStateFromActualType(declared, actual); ok {
			return tracked, true
		}
		if specialized, ok := a.specializeCallbackCarryingType(declared, actual); ok {
			return specialized, true
		}
		return a.cloneTrackedValueType(declared), true
	}
	step := steps[0]
	if step.Field == "" {
		return nil, false
	}
	switch declaredType := declared.(type) {
	case *RefType:
		currentType, _ := current.(*RefType)
		if currentType == nil {
			currentType = declaredType
		}
		nextElem, ok := a.updateSpecializedValueTypeAtPath(declaredType.Elem, currentType.Elem, steps, actual)
		if !ok {
			return nil, false
		}
		cloned := *currentType
		cloned.Elem = nextElem
		return &cloned, true
	case *StructType:
		currentType, _ := current.(*StructType)
		if currentType == nil {
			currentType = declaredType
		}
		declaredFieldType, ok := a.lookupResolvedFieldType(declaredType, step.Field)
		if !ok {
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(currentType, step.Field)
		if !ok {
			currentFieldType = declaredFieldType
		}
		nextFieldType, ok := a.updateSpecializedValueTypeAtPath(declaredFieldType, currentFieldType, steps[1:], actual)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(currentType.Fields)
		field, ok := fields[step.Field]
		if !ok {
			return nil, false
		}
		field.Type = nextFieldType
		fields[step.Field] = field
		return cloneStructTypeWithFields(currentType, fields), true
	case *GenericInstanceType:
		currentType, _ := current.(*GenericInstanceType)
		if currentType == nil {
			currentType = declaredType
		}
		declaredFieldType, ok := a.lookupResolvedFieldType(declaredType, step.Field)
		if !ok {
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(currentType, step.Field)
		if !ok {
			currentFieldType = declaredFieldType
		}
		nextFieldType, ok := a.updateSpecializedValueTypeAtPath(declaredFieldType, currentFieldType, steps[1:], actual)
		if !ok {
			return nil, false
		}
		baseStruct, ok := currentType.Base.(*StructType)
		if !ok || baseStruct == nil {
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		field, ok := fields[step.Field]
		if !ok {
			return nil, false
		}
		field.Type = nextFieldType
		fields[step.Field] = field
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *currentType
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return nil, false
	}
}

func (a *Analyzer) recordSpecializedValueTypeTarget(target ast.Expr, valueType Type) {
	if a.currentSpecializedValueTypes == nil {
		return
	}
	root, steps, ok := a.lookupBorrowedOwnerRefTargetPath(target)
	if !ok || root == nil {
		return
	}
	if len(steps) == 0 {
		a.recordSpecializedValueTypeBinding(root, valueType)
		return
	}
	current := root.Type
	if currentType, ok := a.lookupCurrentSpecializedValueType(root); ok {
		current = currentType
	}
	updatedType, ok := a.updateSpecializedValueTypeAtPath(root.Type, current, steps, valueType)
	if !ok {
		delete(a.currentSpecializedValueTypes, root)
		return
	}
	if specializedType, ok := a.specializeCallbackCarryingType(root.Type, updatedType); ok {
		a.currentSpecializedValueTypes[root] = a.cloneTrackedValueType(specializedType)
		return
	}
	a.currentSpecializedValueTypes[root] = a.cloneTrackedValueType(updatedType)
}

func (a *Analyzer) recordFunctionValueTarget(target ast.Expr, value ast.Expr) {
	ident, ok := target.(*ast.Ident)
	if !ok || a.currentScope == nil {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return
	}
	a.recordFunctionValueBinding(sym, value)
}

func (a *Analyzer) functionValueTypeForExpr(expr ast.Expr) (*FuncType, bool) {
	if expr == nil {
		return nil, false
	}
	valueType := a.exprTypes[expr]
	if valueType == nil {
		valueType = a.analyzeExpr(expr)
	}
	fnType, ok := valueType.(*FuncType)
	if !ok {
		return nil, false
	}
	cloned := a.cloneFunctionValueType(fnType)
	if cloned == nil {
		return nil, false
	}
	if !cloned.ReturnProvenanceKnown {
		a.inferFuncReturnProvenanceForExpr(expr, cloned)
	}
	if !cloned.ReturnBorrowedOwnerRefsKnown {
		a.inferFuncReturnBorrowedOwnerRefsForExpr(expr, cloned)
	}
	return cloned, true
}

func (a *Analyzer) lookupCurrentFunctionValueType(sym *Symbol) (*FuncType, bool) {
	if a.currentFunctionValues == nil || sym == nil {
		return nil, false
	}
	fnType, ok := a.currentFunctionValues[sym]
	if !ok || fnType == nil {
		return nil, false
	}
	return a.cloneFunctionValueType(fnType), true
}

func (a *Analyzer) lookupCurrentSpecializedValueType(sym *Symbol) (Type, bool) {
	if a.currentSpecializedValueTypes == nil || sym == nil {
		return nil, false
	}
	valueType, ok := a.currentSpecializedValueTypes[sym]
	if !ok || valueType == nil {
		return nil, false
	}
	return a.cloneTrackedValueType(valueType), true
}

func projectBorrowedOwnerRefFieldState(state borrowedOwnerRefState, field string) (borrowedOwnerRefState, bool) {
	if len(state.Fields) == 0 {
		return borrowedOwnerRefState{}, false
	}
	child, ok := state.Fields[field]
	if !ok || !hasBorrowedOwnerRefState(child) {
		return borrowedOwnerRefState{}, false
	}
	return cloneBorrowedOwnerRefState(child), true
}

func projectBorrowedOwnerRefIndexKeyState(state borrowedOwnerRefState, key string) (borrowedOwnerRefState, bool) {
	if len(state.Fields) == 0 {
		return borrowedOwnerRefState{}, false
	}
	child, ok := state.Fields[key]
	if !ok || !hasBorrowedOwnerRefState(child) {
		return borrowedOwnerRefState{}, false
	}
	return cloneBorrowedOwnerRefState(child), true
}

func projectBorrowedOwnerRefIndexState(state borrowedOwnerRefState, index ast.Expr, evalConst func(ast.Expr) (ConstValue, bool)) (borrowedOwnerRefState, bool) {
	if len(state.Fields) == 0 {
		return borrowedOwnerRefState{}, false
	}
	if evalConst != nil {
		if value, ok := evalConst(index); ok && value.Kind == ConstInt {
			if child, ok := projectBorrowedOwnerRefIndexKeyState(state, regionIndexFieldKey(value.Int)); ok {
				return child, true
			}
		}
	}
	return projectBorrowedOwnerRefIndexKeyState(state, regionAnyIndexFieldKey())
}

func (a *Analyzer) borrowedOwnerRefStateForExpr(expr ast.Expr) (borrowedOwnerRefState, bool) {
	if expr == nil {
		return borrowedOwnerRefState{}, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.borrowedOwnerRefStateForExpr(n.Inner)
	case *ast.CastExpr:
		return a.borrowedOwnerRefStateForExpr(n.Operand)
	case *ast.MoveExpr:
		return a.borrowedOwnerRefStateForExpr(n.Operand)
	case *ast.AddrOfExpr:
		operandType := a.exprTypes[n.Operand]
		if operandType == nil {
			operandType = a.analyzeExpr(n.Operand)
		}
		if !isBorrowableAffineOwnerType(operandType) {
			return borrowedOwnerRefState{}, false
		}
		key, ok := a.lookupAffineValueKey(n.Operand)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		return borrowedOwnerRefState{HasDirect: true, Direct: key}, true
	case *ast.Ident:
		if a.currentScope == nil {
			return borrowedOwnerRefState{}, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		if state, ok := a.currentBorrowedOwnerRefs[sym]; ok && hasBorrowedOwnerRefState(state) {
			return cloneBorrowedOwnerRefState(state), true
		}
		if root := symbolAliasRoot(sym); root != nil && root != sym {
			if state, ok := a.currentBorrowedOwnerRefs[root]; ok && hasBorrowedOwnerRefState(state) {
				return cloneBorrowedOwnerRefState(state), true
			}
			if _, ok := borrowableOwnerRefElemType(root.Type); ok {
				return borrowedOwnerRefState{HasDirect: true, Direct: affineValueKey{Root: root}}, true
			}
		}
		if _, ok := borrowableOwnerRefElemType(sym.Type); ok {
			return borrowedOwnerRefState{HasDirect: true, Direct: affineValueKey{Root: sym}}, true
		}
		return borrowedOwnerRefState{}, false
	case *ast.StructLitExpr:
		actual := a.exprTypes[n]
		fields, ok := a.resolvedStructFields(actual)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		state := borrowedOwnerRefState{}
		for i, field := range fields {
			if i >= len(n.Args) {
				break
			}
			fieldState, ok := a.borrowedOwnerRefStateForExpr(n.Args[i])
			if !ok || !hasBorrowedOwnerRefState(fieldState) {
				continue
			}
			if state.Fields == nil {
				state.Fields = map[string]borrowedOwnerRefState{}
			}
			state.Fields[field.Name] = fieldState
		}
		if !hasBorrowedOwnerRefState(state) {
			return borrowedOwnerRefState{}, false
		}
		return state, true
	case *ast.ListLitExpr:
		state := borrowedOwnerRefState{}
		for i, elem := range n.Elems {
			elemState, ok := a.borrowedOwnerRefStateForExpr(elem)
			if !ok || !hasBorrowedOwnerRefState(elemState) {
				continue
			}
			if state.Fields == nil {
				state.Fields = map[string]borrowedOwnerRefState{}
			}
			key := regionIndexFieldKey(int64(i))
			state.Fields[key] = elemState
			if anyState, ok := state.Fields[regionAnyIndexFieldKey()]; ok {
				if merged, ok := mergeBorrowedOwnerRefState(anyState, elemState); ok {
					state.Fields[regionAnyIndexFieldKey()] = merged
				} else {
					delete(state.Fields, regionAnyIndexFieldKey())
				}
			} else {
				state.Fields[regionAnyIndexFieldKey()] = cloneBorrowedOwnerRefState(elemState)
			}
		}
		if !hasBorrowedOwnerRefState(state) {
			return borrowedOwnerRefState{}, false
		}
		return state, true
	case *ast.FieldExpr:
		state, ok := a.borrowedOwnerRefStateForExpr(n.Object)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		return projectBorrowedOwnerRefFieldState(state, n.Field)
	case *ast.IndexExpr:
		state, ok := a.borrowedOwnerRefStateForExpr(n.Object)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		return projectBorrowedOwnerRefIndexState(state, n.Index, a.evalConstExpr)
	case *ast.CallExpr:
		if state, ok := a.borrowedOwnerRefStateForProofCarryingViewCall(n); ok {
			return state, true
		}
		if _, ok := a.freezeStoreArg(n); ok {
			return borrowedOwnerRefState{}, false
		}
		fnType, _ := a.exprTypes[n.Func].(*FuncType)
		if fnType == nil {
			if analyzed := a.analyzeExpr(n.Func); analyzed != nil {
				fnType, _ = analyzed.(*FuncType)
			}
		}
		if fnType != nil {
			if !fnType.ReturnBorrowedOwnerRefsKnown {
				a.inferFuncReturnBorrowedOwnerRefsForExpr(n.Func, fnType)
			}
			if fnType.ReturnBorrowedOwnerRefsKnown {
				return a.instantiateBorrowedOwnerRefSummary(fnType.ReturnBorrowedOwnerRefs, n.Args)
			}
		}
		return borrowedOwnerRefState{}, false
	case *ast.TryExpr:
		return a.borrowedOwnerRefStateForRecoveredExpr(n.Value, n.Fallback)
	case *ast.UnwrapElseExpr:
		return a.borrowedOwnerRefStateForRecoveredExpr(n.Value, n.Fallback)
	case *ast.TernaryExpr:
		left, leftOK := a.borrowedOwnerRefStateForExpr(n.Value)
		right, rightOK := a.borrowedOwnerRefStateForExpr(n.Alt)
		if !leftOK || !rightOK {
			return borrowedOwnerRefState{}, false
		}
		return mergeBorrowedOwnerRefState(left, right)
	default:
		return borrowedOwnerRefState{}, false
	}
}

func (a *Analyzer) borrowedOwnerRefStateForProofCarryingViewCall(call *ast.CallExpr) (borrowedOwnerRefState, bool) {
	if a == nil || call == nil || len(call.Args) == 0 {
		return borrowedOwnerRefState{}, false
	}
	sourceState, ok := a.borrowedOwnerRefStateForExpr(call.Args[0])
	if !ok || !hasBorrowedOwnerRefState(sourceState) {
		return borrowedOwnerRefState{}, false
	}
	switch callIdentName(call) {
	case "readonly":
		return cloneBorrowedOwnerRefState(sourceState), true
	case "split_at":
		summarized, summaryOK := summarizeBorrowedOwnerRefIndexStates(sourceState)
		if !summaryOK {
			summarized = cloneBorrowedOwnerRefState(sourceState)
		}
		state := cloneBorrowedOwnerRefState(summarized)
		state.Fields = map[string]borrowedOwnerRefState{
			"left":  cloneBorrowedOwnerRefState(summarized),
			"right": cloneBorrowedOwnerRefState(summarized),
		}
		return state, true
	case "chunks_exact":
		summarized, summaryOK := summarizeBorrowedOwnerRefIndexStates(sourceState)
		if !summaryOK {
			summarized = cloneBorrowedOwnerRefState(sourceState)
		}
		state := cloneBorrowedOwnerRefState(summarized)
		state.Fields = map[string]borrowedOwnerRefState{
			"source": cloneBorrowedOwnerRefState(summarized),
		}
		return state, true
	case "reduce_sum":
		return borrowedOwnerRefState{}, true
	default:
		return borrowedOwnerRefState{}, false
	}
}

func (a *Analyzer) borrowedOwnerRefStateForRecoveredExpr(value ast.Expr, fallback ast.Expr) (borrowedOwnerRefState, bool) {
	valueState, valueOK := a.borrowedOwnerRefStateForExpr(value)
	if fallback == nil || a.exprDefinitelyNever(fallback) {
		if !valueOK {
			return borrowedOwnerRefState{}, false
		}
		return cloneBorrowedOwnerRefState(valueState), true
	}
	fallbackState, fallbackOK := a.borrowedOwnerRefStateForExpr(fallback)
	if !valueOK || !fallbackOK {
		return borrowedOwnerRefState{}, false
	}
	return mergeBorrowedOwnerRefState(valueState, fallbackState)
}

func (a *Analyzer) lookupBorrowedOwnerRefTargetPath(expr ast.Expr) (*Symbol, []borrowReturnAnnotationStep, bool) {
	if expr == nil {
		return nil, nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.lookupBorrowedOwnerRefTargetPath(n.Inner)
	case *ast.Ident:
		if a.currentScope == nil {
			return nil, nil, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		return sym, nil, ok
	case *ast.FieldExpr:
		root, steps, ok := a.lookupBorrowedOwnerRefTargetPath(n.Object)
		if !ok {
			return nil, nil, false
		}
		return root, append(steps, borrowReturnAnnotationStep{Field: n.Field}), true
	case *ast.IndexExpr:
		root, steps, ok := a.lookupBorrowedOwnerRefTargetPath(n.Object)
		if !ok {
			return nil, nil, false
		}
		step := borrowReturnAnnotationStep{Wildcard: true}
		if value, ok := a.evalConstExpr(n.Index); ok && value.Kind == ConstInt {
			valueCopy := value.Int
			step = borrowReturnAnnotationStep{Index: &valueCopy}
		}
		return root, append(steps, step), true
	default:
		return nil, nil, false
	}
}

func (a *Analyzer) lookupBorrowedOwnerRefKey(expr ast.Expr) (affineValueKey, bool) {
	state, ok := a.borrowedOwnerRefStateForExpr(expr)
	if !ok || !state.HasDirect {
		return affineValueKey{}, false
	}
	return state.Direct, true
}

func (a *Analyzer) trackAffineValueTarget(expr ast.Expr, expected Type) {
	if expr == nil || !a.containsAffineHandleValues(expected, map[string]bool{}) {
		return
	}
	key, ok := a.lookupAffineValueKey(expr)
	if !ok {
		return
	}
	a.registerLiveProtocolValuePaths(key, expected)
}

func (a *Analyzer) registerLiveProtocolValuePaths(baseKey affineValueKey, t Type) {
	if baseKey.Root == nil {
		return
	}
	paths := a.protocolLiveLeafPaths(t, "", map[string]bool{})
	if len(paths) == 0 {
		return
	}
	if a.currentAffineValues == nil {
		a.currentAffineValues = map[affineValueKey]affineValueState{}
	}
	for path, liveType := range paths {
		key := affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, path)}
		state := a.currentAffineValues[key]
		state.LiveProtocolType = liveType
		state.LiveProtocolDescription = ""
		state.ConsumedBy = ""
		a.currentAffineValues[key] = state
	}
}

func (a *Analyzer) markLiveProtocolDescription(key affineValueKey, description string) {
	if key.Root == nil || description == "" {
		return
	}
	if a.currentAffineValues == nil {
		a.currentAffineValues = map[affineValueKey]affineValueState{}
	}
	state := a.currentAffineValues[key]
	state.LiveProtocolType = nil
	state.LiveProtocolDescription = description
	a.currentAffineValues[key] = state
}

func (a *Analyzer) markCreatedProtocolSymbol(sym *Symbol, value ast.Expr) {
	if sym == nil {
		return
	}
	description := protocolCreationDescription(value, sym.Type)
	if description == "" {
		return
	}
	a.markLiveProtocolDescription(affineValueKey{Root: sym}, description)
}

func (a *Analyzer) markCreatedProtocolTarget(target ast.Expr, value ast.Expr, expected Type) {
	description := protocolCreationDescription(value, expected)
	if description == "" {
		return
	}
	key, ok := a.lookupAffineValueKey(target)
	if !ok {
		return
	}
	a.markLiveProtocolDescription(key, description)
}

func protocolCreationDescription(value ast.Expr, expected Type) string {
	if !isBuiltinProtocolOwnerType(expected, "ThreadPool") {
		return ""
	}
	if !callExprHasName(value, "pool_new") {
		return ""
	}
	return "thread pool requiring shutdown"
}

func callExprHasName(expr ast.Expr, name string) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return callExprHasName(n.Inner, name)
	case *ast.CastExpr:
		return callExprHasName(n.Operand, name)
	case *ast.CallExpr:
		return callIdentName(n) == name
	default:
		return false
	}
}

func (a *Analyzer) clearLiveProtocolTracking(key affineValueKey) {
	if key.Root == nil || a.currentAffineValues == nil {
		return
	}
	for existingKey, existingState := range a.currentAffineValues {
		if existingKey.Root != key.Root {
			continue
		}
		if key.Path != "" && !affinePathContains(key.Path, existingKey.Path) {
			continue
		}
		existingState.LiveProtocolType = nil
		existingState.LiveProtocolDescription = ""
		a.currentAffineValues[existingKey] = existingState
	}
}

func joinAffinePath(base, suffix string) string {
	if base == "" {
		return suffix
	}
	if suffix == "" {
		return base
	}
	return base + "." + suffix
}

func directProtocolLeakKind(t Type) string {
	instance, ok := t.(*GenericInstanceType)
	if !ok {
		return ""
	}
	switch instance.Name {
	case "Thread":
		if len(instance.Args) >= 2 && instance.Args[1].String() == "Joinable" {
			return "joinable thread handle"
		}
	case "Task":
		if len(instance.Args) >= 2 && instance.Args[1].String() == "Pending" {
			return "pending task handle"
		}
	case "MutexGuard":
		if len(instance.Args) >= 1 && instance.Args[0].String() == "Held" {
			return "held mutex guard"
		}
	}
	return ""
}

func directProtocolCarrierType(t Type) bool {
	return directProtocolLeakKind(t) != "" || isBuiltinProtocolOwnerType(t, "TaskGroup") || isBuiltinProtocolOwnerType(t, "ThreadPool")
}

func isBuiltinProtocolOwnerType(t Type, name string) bool {
	switch tt := t.(type) {
	case *StructType:
		return tt.Builtin && tt.Name == name
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		return ok && base.Builtin && base.Name == name
	default:
		return false
	}
}

func (a *Analyzer) containsTrackedProtocolCarrierValues(t Type, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	if isAffineHandleType(t) || directProtocolCarrierType(t) {
		return true
	}
	key := t.String()
	if seen[key] {
		return false
	}
	seen[key] = true
	switch tt := t.(type) {
	case *ArrayType:
		return a.containsTrackedProtocolCarrierValues(tt.Elem, seen)
	case *DArrayType:
		return a.containsTrackedProtocolCarrierValues(tt.Elem, seen)
	case *ViewType:
		return a.containsTrackedProtocolCarrierValues(tt.Elem, seen)
	case *DArrayViewType:
		return a.containsTrackedProtocolCarrierValues(tt.Elem, seen)
	case *DictType:
		return a.containsTrackedProtocolCarrierValues(tt.Key, seen) || a.containsTrackedProtocolCarrierValues(tt.Value, seen)
	case *EnumType:
		for _, field := range tt.Common {
			if a.containsTrackedProtocolCarrierValues(field.Type, seen) {
				return true
			}
		}
		for _, variant := range tt.Variants {
			for _, payloadType := range variant.Payload {
				if a.containsTrackedProtocolCarrierValues(payloadType, seen) {
					return true
				}
			}
		}
		return false
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				if a.containsTrackedProtocolCarrierValues(fieldType, seen) {
					return true
				}
			}
			return false
		}
		for _, arg := range tt.Args {
			if a.containsTrackedProtocolCarrierValues(arg, seen) {
				return true
			}
		}
		return a.containsTrackedProtocolCarrierValues(tt.Base, seen)
	case *StructType:
		for _, field := range tt.Fields {
			if a.containsTrackedProtocolCarrierValues(field.Type, seen) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func joinProtocolLeakKinds(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " or " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", or " + parts[len(parts)-1]
	}
}

func (a *Analyzer) protocolKindsInType(t Type, seen map[string]bool) (bool, bool, bool) {
	if t == nil {
		return false, false, false
	}
	switch directProtocolLeakKind(t) {
	case "joinable thread handle":
		return true, false, false
	case "pending task handle":
		return false, true, false
	case "held mutex guard":
		return false, false, true
	}
	key := t.String()
	if seen[key] {
		return false, false, false
	}
	seen[key] = true
	switch tt := t.(type) {
	case *ArrayType:
		return a.protocolKindsInType(tt.Elem, seen)
	case *DArrayType:
		return a.protocolKindsInType(tt.Elem, seen)
	case *ViewType:
		return a.protocolKindsInType(tt.Elem, seen)
	case *DArrayViewType:
		return a.protocolKindsInType(tt.Elem, seen)
	case *DictType:
		leftThread, leftTask, leftGuard := a.protocolKindsInType(tt.Key, seen)
		rightThread, rightTask, rightGuard := a.protocolKindsInType(tt.Value, seen)
		return leftThread || rightThread, leftTask || rightTask, leftGuard || rightGuard
	case *StructType:
		var hasThread, hasTask, hasGuard bool
		for _, field := range tt.Fields {
			fieldThread, fieldTask, fieldGuard := a.protocolKindsInType(field.Type, seen)
			hasThread = hasThread || fieldThread
			hasTask = hasTask || fieldTask
			hasGuard = hasGuard || fieldGuard
		}
		return hasThread, hasTask, hasGuard
	case *EnumType:
		var hasThread, hasTask, hasGuard bool
		for _, field := range tt.Common {
			fieldThread, fieldTask, fieldGuard := a.protocolKindsInType(field.Type, seen)
			hasThread = hasThread || fieldThread
			hasTask = hasTask || fieldTask
			hasGuard = hasGuard || fieldGuard
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				payloadThread, payloadTask, payloadGuard := a.protocolKindsInType(payload, seen)
				hasThread = hasThread || payloadThread
				hasTask = hasTask || payloadTask
				hasGuard = hasGuard || payloadGuard
			}
		}
		return hasThread, hasTask, hasGuard
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			var hasThread, hasTask, hasGuard bool
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				fieldThread, fieldTask, fieldGuard := a.protocolKindsInType(fieldType, seen)
				hasThread = hasThread || fieldThread
				hasTask = hasTask || fieldTask
				hasGuard = hasGuard || fieldGuard
			}
			return hasThread, hasTask, hasGuard
		}
		var hasThread, hasTask, hasGuard bool
		for _, arg := range tt.Args {
			argThread, argTask, argGuard := a.protocolKindsInType(arg, seen)
			hasThread = hasThread || argThread
			hasTask = hasTask || argTask
			hasGuard = hasGuard || argGuard
		}
		baseThread, baseTask, baseGuard := a.protocolKindsInType(tt.Base, seen)
		return hasThread || baseThread, hasTask || baseTask, hasGuard || baseGuard
	default:
		return false, false, false
	}
}

func (a *Analyzer) containsProtocolLeakValues(t Type) bool {
	hasThread, hasTask, hasGuard := a.protocolKindsInType(t, map[string]bool{})
	return hasThread || hasTask || hasGuard
}

func (a *Analyzer) protocolLeakDescription(t Type) string {
	if kind := directProtocolLeakKind(t); kind != "" {
		return kind
	}
	hasThread, hasTask, hasGuard := a.protocolKindsInType(t, map[string]bool{})
	parts := make([]string, 0, 3)
	if hasThread {
		parts = append(parts, "joinable thread handles")
	}
	if hasTask {
		parts = append(parts, "pending task handles")
	}
	if hasGuard {
		parts = append(parts, "held mutex guards")
	}
	if len(parts) == 0 {
		return "affine value"
	}
	return "value containing " + joinProtocolLeakKinds(parts)
}

func (a *Analyzer) protocolLiveLeafPaths(t Type, prefix string, seen map[string]bool) map[string]Type {
	if t == nil {
		return nil
	}
	if kind := directProtocolLeakKind(t); kind != "" {
		return map[string]Type{prefix: t}
	}
	key := t.String()
	if seen[key] {
		return nil
	}
	seen[key] = true
	switch tt := t.(type) {
	case *StructType:
		paths := map[string]Type{}
		for _, field := range tt.Fields {
			if !a.containsProtocolLeakValues(field.Type) {
				continue
			}
			for childPath, liveType := range a.protocolLiveLeafPaths(field.Type, field.Name, mapsCloneBool(seen)) {
				paths[joinAffinePath(prefix, childPath)] = liveType
			}
		}
		return paths
	case *EnumType:
		paths := map[string]Type{}
		for _, field := range tt.Common {
			if !a.containsProtocolLeakValues(field.Type) {
				continue
			}
			for childPath, liveType := range a.protocolLiveLeafPaths(field.Type, field.Name, mapsCloneBool(seen)) {
				paths[joinAffinePath(prefix, childPath)] = liveType
			}
		}
		for _, variant := range tt.Variants {
			for i, payloadType := range variant.Payload {
				label := variant.PayloadLabel(i)
				if label == "" || !a.containsProtocolLeakValues(payloadType) {
					continue
				}
				for childPath, liveType := range a.protocolLiveLeafPaths(payloadType, label, mapsCloneBool(seen)) {
					paths[joinAffinePath(prefix, childPath)] = liveType
				}
			}
		}
		if len(paths) != 0 {
			return paths
		}
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			paths := map[string]Type{}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				if !a.containsProtocolLeakValues(fieldType) {
					continue
				}
				for childPath, liveType := range a.protocolLiveLeafPaths(fieldType, field.Name, mapsCloneBool(seen)) {
					paths[joinAffinePath(prefix, childPath)] = liveType
				}
			}
			return paths
		}
	}
	if a.containsProtocolLeakValues(t) {
		return map[string]Type{prefix: t}
	}
	return nil
}

func (a *Analyzer) recordAffineDestructureConsumption(expr ast.Expr, actual Type, reason string) {
	if expr == nil || actual == nil {
		return
	}
	if !a.containsAffineHandleValues(actual, map[string]bool{}) {
		return
	}
	key, ok := a.lookupAffineValueKey(expr)
	if !ok {
		return
	}
	a.recordAffineConsumption(key, reason)
}

func mapsCloneBool(src map[string]bool) map[string]bool {
	if src == nil {
		return nil
	}
	cloned := make(map[string]bool, len(src))
	for key, value := range src {
		cloned[key] = value
	}
	return cloned
}

func (a *Analyzer) reportUnconsumedProtocolValues() {
	if a.currentAffineValues == nil {
		return
	}
	for key, state := range a.currentAffineValues {
		if key.Root == nil || (state.LiveProtocolType == nil && state.LiveProtocolDescription == "") {
			continue
		}
		pos := lexer.Pos{}
		if key.Root.Node != nil {
			pos = key.Root.Node.Pos()
		}
		description := state.LiveProtocolDescription
		if description == "" {
			description = a.protocolLeakDescription(state.LiveProtocolType)
		}
		a.errorf(pos, "%s %q must be consumed before scope exit", description, affineValueDisplayNameFromKey(key))
	}
}

func affineValueDisplayNameFromKey(key affineValueKey) string {
	if key.Root == nil {
		return "<value>"
	}
	if key.Path == "" {
		return key.Root.Name
	}
	return key.Root.Name + "." + key.Path
}

func (a *Analyzer) clonePackedStores() map[string]*PackedEnumStoreType {
	if a.currentPackedStores == nil {
		return nil
	}
	cloned := make(map[string]*PackedEnumStoreType, len(a.currentPackedStores))
	for name, store := range a.currentPackedStores {
		cloned[name] = store
	}
	return cloned
}

func (a *Analyzer) clonePackedStoreResolutions() map[*Symbol]packedStoreResolution {
	if a.currentPackedStoreResolutions == nil {
		return nil
	}
	cloned := make(map[*Symbol]packedStoreResolution, len(a.currentPackedStoreResolutions))
	for sym, resolution := range a.currentPackedStoreResolutions {
		cloned[sym] = resolution
	}
	return cloned
}

func (a *Analyzer) bindActivePackedStoreType(t Type) {
	storeType, ok := t.(*PackedEnumStoreType)
	if !ok || storeType == nil || storeType.Enum == nil {
		return
	}
	if a.currentPackedStores == nil {
		a.currentPackedStores = map[string]*PackedEnumStoreType{}
	}
	a.currentPackedStores[storeType.Enum.Name] = storeType
}

func (a *Analyzer) lookupPackedStore(enumType *EnumType) (*PackedEnumStoreType, bool) {
	if a.currentPackedStores == nil || enumType == nil {
		return nil, false
	}
	store, ok := a.currentPackedStores[enumType.Name]
	if !ok || store == nil {
		return nil, false
	}
	return store, true
}

func (a *Analyzer) lookupRegionMark(name string) (*Symbol, regionMarkState) {
	if a.currentScope == nil {
		return nil, regionMarkState{}
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym.Kind != SymbolRegionMark {
		return nil, regionMarkState{}
	}
	state, ok := a.currentRegionMarks[sym]
	if !ok {
		return nil, regionMarkState{}
	}
	return sym, state
}

func (a *Analyzer) lookupRegionState(name string) (*Symbol, regionState) {
	if a.currentScope == nil {
		return nil, regionState{}
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym.Kind != SymbolRegion {
		return nil, regionState{}
	}
	state, ok := a.currentRegions[sym]
	if !ok {
		return nil, regionState{}
	}
	return sym, state
}

func (a *Analyzer) recordRegionRefBinding(sym *Symbol, value ast.Expr) {
	if a.currentRegionRefs == nil || sym == nil {
		return
	}
	if storeType, ok := sym.Type.(*PackedEnumStoreType); ok {
		if state, ok := a.regionRefStateForExpr(value); ok && hasPackedStoreDependencies(state) {
			a.recordResolvedRegionRefBinding(sym, state)
			return
		}
		a.recordResolvedRegionRefBinding(sym, regionRefStateFromPackedStoreDependency(sym, storeType))
		return
	}
	if state, ok := a.regionRefStateForExpr(value); ok {
		a.recordResolvedRegionRefBinding(sym, state)
		return
	}
	delete(a.currentRegionRefs, sym)
}

func (a *Analyzer) freezeMovedPackedStoreSource(expr ast.Expr) (*Symbol, *PackedEnumStoreType, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	arg, ok := a.freezeStoreArg(call)
	if !ok {
		return nil, nil, false
	}
	moved, ok := explicitMoveOperand(arg)
	if !ok {
		return nil, nil, false
	}
	ident, ok := moved.(*ast.Ident)
	if !ok || a.currentScope == nil {
		return nil, nil, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return nil, nil, false
	}
	storeType, ok := sym.Type.(*PackedEnumStoreType)
	if !ok {
		return nil, nil, false
	}
	return sym, storeType, true
}

func (a *Analyzer) resolvePackedStoreDependency(store *Symbol, dep packedStoreDependencyState) (*Symbol, packedStoreDependencyState, bool) {
	if store == nil || len(a.currentPackedStoreResolutions) == 0 {
		return store, dep, false
	}
	resolvedStore := store
	resolvedDep := dep
	changed := false
	seen := map[*Symbol]bool{}
	for resolvedStore != nil {
		if seen[resolvedStore] {
			break
		}
		seen[resolvedStore] = true
		resolution, ok := a.currentPackedStoreResolutions[resolvedStore]
		if !ok {
			break
		}
		if resolution.Type != nil && resolution.Type != resolvedDep.Type {
			resolvedDep.Type = resolution.Type
			changed = true
		}
		if resolution.Symbol == nil || resolution.Symbol == resolvedStore {
			break
		}
		resolvedStore = resolution.Symbol
		changed = true
	}
	if changed && a.currentPackedStoreResolutions != nil {
		a.currentPackedStoreResolutions[store] = packedStoreResolution{Symbol: resolvedStore, Type: resolvedDep.Type}
	}
	return resolvedStore, resolvedDep, changed
}

func (a *Analyzer) resolvePackedStoreDependenciesInState(state regionRefState) (regionRefState, bool) {
	if len(a.currentPackedStoreResolutions) == 0 || !hasPackedStoreDependencies(state) {
		return state, false
	}
	changed := false
	storeDepsCloned := false
	fieldsCloned := false
	for store, dep := range state.StoreDeps {
		nextStore, nextDep, depChanged := a.resolvePackedStoreDependency(store, dep)
		if !depChanged {
			continue
		}
		if !storeDepsCloned {
			state.StoreDeps = clonePackedStoreDependencyStates(state.StoreDeps)
			storeDepsCloned = true
		}
		delete(state.StoreDeps, store)
		state.StoreDeps[nextStore] = nextDep
		changed = true
	}
	for name, fieldState := range state.Fields {
		if !hasPackedStoreDependencies(fieldState) {
			continue
		}
		nextField, fieldChanged := a.resolvePackedStoreDependenciesInState(fieldState)
		if !fieldChanged {
			continue
		}
		if !fieldsCloned {
			state.Fields = cloneRegionRefFields(state.Fields)
			fieldsCloned = true
		}
		state.Fields[name] = nextField
		changed = true
	}
	if changed {
		state.PackedStoreSummaryKnown = false
		state = withPackedStoreProvenanceSummary(state)
	}
	return state, changed
}

func (a *Analyzer) canonicalizeStoredRegionRefBinding(sym *Symbol, state regionRefState) regionRefState {
	nextState, changed := a.resolvePackedStoreDependenciesInState(state)
	if changed && a.currentRegionRefs != nil && sym != nil {
		a.currentRegionRefs[sym] = nextState
	}
	return nextState
}

func (a *Analyzer) recordResolvedRegionRefBinding(sym *Symbol, state regionRefState) {
	if a.currentRegionRefs == nil || sym == nil {
		return
	}
	if !hasRegionProvenance(state) {
		delete(a.currentRegionRefs, sym)
		return
	}
	state, _ = a.resolvePackedStoreDependenciesInState(state)
	state = withPackedStoreProvenanceSummary(state)
	a.currentRegionRefs[sym] = cloneRegionRefStateSharedFields(state)
}

func (a *Analyzer) recordRegionRefAssignment(target ast.Expr, value ast.Expr) {
	ident, ok := target.(*ast.Ident)
	if !ok || a.currentScope == nil {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return
	}
	a.recordRegionRefBinding(sym, value)
}

func (a *Analyzer) remapPackedStoreDependencies(from *Symbol, to *Symbol, nextType *PackedEnumStoreType) {
	if from == nil || to == nil || nextType == nil {
		return
	}
	resolvedTo, resolvedDep, _ := a.resolvePackedStoreDependency(to, packedStoreDependencyState{Type: nextType})
	if a.currentPackedStoreResolutions == nil {
		a.currentPackedStoreResolutions = map[*Symbol]packedStoreResolution{}
	}
	a.currentPackedStoreResolutions[from] = packedStoreResolution{Symbol: resolvedTo, Type: resolvedDep.Type}
}

func remapPackedStoreDependencyInState(state regionRefState, from *Symbol, to *Symbol, nextType *PackedEnumStoreType) (regionRefState, bool) {
	changed := false
	storeDepsCloned := false
	fieldsCloned := false
	if dep, ok := state.StoreDeps[from]; ok {
		if !storeDepsCloned {
			state.StoreDeps = clonePackedStoreDependencyStates(state.StoreDeps)
			storeDepsCloned = true
		}
		delete(state.StoreDeps, from)
		dep.Type = nextType
		state.StoreDeps[to] = dep
		changed = true
	}
	for name, fieldState := range state.Fields {
		nextField, fieldChanged := remapPackedStoreDependencyInState(fieldState, from, to, nextType)
		if !fieldChanged {
			continue
		}
		if !fieldsCloned {
			state.Fields = cloneRegionRefFields(state.Fields)
			fieldsCloned = true
		}
		state.Fields[name] = nextField
		changed = true
	}
	if changed {
		state.PackedStoreSummaryKnown = false
		state = withPackedStoreProvenanceSummary(state)
	}
	return state, changed
}

func (a *Analyzer) invalidateRegionRefs(region *Symbol, predicate func(regionDependencyState) bool, reason string) {
	if a.currentRegionRefs == nil || region == nil {
		return
	}
	for sym, state := range a.currentRegionRefs {
		nextState, changed := invalidateRegionDependencyInState(state, region, predicate, reason)
		if changed {
			a.currentRegionRefs[sym] = nextState
		}
	}
}

func (a *Analyzer) invalidateRegionMarks(region *Symbol, predicate func(regionMarkState) bool, reason string) {
	if a.currentRegionMarks == nil || region == nil {
		return
	}
	for sym, state := range a.currentRegionMarks {
		if state.Region != region || !state.Valid {
			continue
		}
		if predicate != nil && !predicate(state) {
			continue
		}
		state.Valid = false
		state.InvalidatedBy = reason
		a.currentRegionMarks[sym] = state
	}
}

func (a *Analyzer) refinedScopeForCondition(parent *Scope, cond ast.Expr, truthy bool) *Scope {
	scope := NewScope(parent)
	a.applyConditionRefinementsInternal(scope, cond, truthy, false)
	a.bindConditionPatternLocals(scope, cond, truthy)
	return scope
}

func (a *Analyzer) applyConditionRefinements(scope *Scope, expr ast.Expr, truthy bool) {
	a.applyConditionRefinementsInternal(scope, expr, truthy, true)
}

func (a *Analyzer) applyConditionRefinementsInternal(scope *Scope, expr ast.Expr, truthy bool, persistTracked bool) {
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			if truthy {
				a.applyConditionRefinementsInternal(scope, n.Left, true, persistTracked)
				a.applyConditionRefinementsInternal(scope, n.Right, true, persistTracked)
			}
		case lexer.TOKEN_OR:
			if !truthy {
				a.applyConditionRefinementsInternal(scope, n.Left, false, persistTracked)
				a.applyConditionRefinementsInternal(scope, n.Right, false, persistTracked)
			}
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
			targetExpr, nonNull, ok := refinedExprNullState(n, truthy)
			if ok {
				a.shadowRefinedExpr(scope, targetExpr, nonNull)
			}
		case lexer.TOKEN_IS:
			targetExpr, viewType, ok := a.refinedExprPackedVariantView(n, truthy)
			if ok {
				a.bindRefinedExprType(scope, targetExpr, viewType)
				break
			}
			targetExpr, treeViewType, ok := a.refinedExprTreeVariantView(n, truthy)
			if ok {
				a.bindRefinedExprType(scope, targetExpr, treeViewType)
				break
			}
			targetExpr, refinedType, ok := a.refinedExprNamedStateType(n, truthy)
			if ok {
				a.bindRefinedExprType(scope, targetExpr, refinedType)
			}
		}
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			a.applyConditionRefinementsInternal(scope, n.Operand, !truthy, persistTracked)
		}
	case *ast.CallExpr:
		a.applyGuardCallConditionRefinements(scope, n, truthy)
		a.applyConditionalCallConditionRefinements(scope, n, truthy, persistTracked)
	case *ast.ParenExpr:
		a.applyConditionRefinementsInternal(scope, n.Inner, truthy, persistTracked)
	}
}

func refinedExprNullState(expr *ast.BinaryExpr, truthy bool) (ast.Expr, bool, bool) {
	_, leftNull := expr.Left.(*ast.NullLit)
	_, rightNull := expr.Right.(*ast.NullLit)

	targetExpr := ast.Expr(nil)
	switch {
	case rightNull:
		targetExpr = expr.Left
	case leftNull:
		targetExpr = expr.Right
	default:
		return nil, false, false
	}

	if _, ok := exprRefinementKey(targetExpr); !ok {
		return nil, false, false
	}

	if expr.Op == lexer.TOKEN_EQEQ {
		return targetExpr, !truthy, true
	}
	return targetExpr, truthy, true
}

func (a *Analyzer) refinedExprPackedVariantView(expr *ast.BinaryExpr, truthy bool) (ast.Expr, *PackedVariantViewType, bool) {
	if a == nil || !truthy || expr == nil || expr.Op != lexer.TOKEN_IS {
		return nil, nil, false
	}
	enumType, variant, ok := a.resolveEnumVariantIsTarget(expr.Right)
	if !ok || enumType == nil || variant == nil {
		return nil, nil, false
	}
	if !enumType.Packed {
		return nil, nil, false
	}
	leftType := a.exprTypes[expr.Left]
	if leftType == nil {
		leftType = a.analyzeExpr(expr.Left)
	}
	matchableEnum, _, ok := resolveMatchableEnumType(leftType)
	if !ok || matchableEnum == nil || matchableEnum.Name != enumType.Name {
		return nil, nil, false
	}
	return expr.Left, variant.PackedViewType(enumType), true
}

func (a *Analyzer) refinedExprTreeVariantView(expr *ast.BinaryExpr, truthy bool) (ast.Expr, *TreeVariantViewType, bool) {
	if a == nil || !truthy || expr == nil || expr.Op != lexer.TOKEN_IS {
		return nil, nil, false
	}
	treeType, variant, ok := a.resolveTreeVariantIsTarget(expr.Right)
	if !ok || treeType == nil || variant == nil {
		return nil, nil, false
	}
	leftType := a.exprTypes[expr.Left]
	if leftType == nil {
		leftType = a.analyzeExpr(expr.Left)
	}
	matchableTree, _, ok := resolveMatchableTreeCategoryType(leftType)
	if !ok || matchableTree == nil || matchableTree.Name != treeType.Name {
		return nil, nil, false
	}
	return expr.Left, treeType.VariantViewType(variant), true
}

func (a *Analyzer) refinedExprNamedStateType(expr *ast.BinaryExpr, truthy bool) (ast.Expr, Type, bool) {
	if a == nil || expr == nil || expr.Op != lexer.TOKEN_IS {
		return nil, nil, false
	}
	targetBase, targetState, ok := a.resolveNamedStateIsTarget(expr.Right)
	if !ok || targetBase == nil || targetState == nil {
		return nil, nil, false
	}
	leftType := a.exprTypes[expr.Left]
	if leftType == nil {
		leftType = a.analyzeExpr(expr.Left)
	}
	leftBase, ok := namedStateStructBase(leftType)
	if !ok || leftBase == nil || leftBase.Name != targetBase.Name {
		return nil, nil, false
	}
	currentState, ok := namedStateCurrentArg(leftType)
	if !ok || currentState == nil {
		currentState = fullNamedStateType(leftBase)
	}
	var refinedState Type
	if truthy {
		refinedState = intersectNamedStateType(currentState, targetState, leftBase.NamedStateCases)
	} else {
		refinedState = subtractNamedStateType(currentState, targetState, leftBase.NamedStateCases)
	}
	if refinedState == nil {
		return nil, nil, false
	}
	return expr.Left, instantiateNamedStateStructLiteralType(leftBase, leftType, refinedState), true
}

func (a *Analyzer) applyGuardCallConditionRefinements(scope *Scope, call *ast.CallExpr, truthy bool) {
	if a == nil || scope == nil || call == nil || !truthy {
		return
	}
	_, fnType, ok := a.resolveSinkFuncDecl(call.Func)
	if !ok || fnType == nil || len(fnType.GuardEffects) == 0 {
		return
	}
	for _, effect := range fnType.GuardEffects {
		if effect.ParamIndex < 0 || effect.ParamIndex >= len(call.Args) {
			continue
		}
		argExpr := call.Args[effect.ParamIndex]
		switch effect.Kind {
		case FuncGuardKindNonNull:
			a.shadowRefinedExpr(scope, argExpr, true)
		case FuncGuardKindPackedVariant:
			base, ok := a.namedTypes[effect.EnumName]
			if !ok {
				continue
			}
			switch variantBase := base.(type) {
			case *EnumType:
				if variantBase == nil || !variantBase.Packed {
					continue
				}
				variant, ok := variantBase.Variant(effect.VariantName)
				if !ok || variant == nil {
					continue
				}
				a.bindRefinedExprType(scope, argExpr, variant.PackedViewType(variantBase))
			case *TreeCategoryType:
				variant, ok := variantBase.Variant(effect.VariantName)
				if !ok || variant == nil {
					continue
				}
				a.bindRefinedExprType(scope, argExpr, variantBase.VariantViewType(variant))
			}
		}
	}
}

func (a *Analyzer) applyConditionalCallConditionRefinements(scope *Scope, call *ast.CallExpr, truthy bool, persistTracked bool) {
	if a == nil || scope == nil || call == nil {
		return
	}
	fnType, ok := a.functionValueTypeForExpr(call.Func)
	if !ok || fnType == nil || !funcPoststatesHaveConditional(fnType.Poststates) {
		return
	}
	originalByRoot := a.conditionalCallPoststateOriginals[call]
	if len(originalByRoot) == 0 {
		return
	}
	limit := len(call.Args)
	if len(fnType.Params) < limit {
		limit = len(fnType.Params)
	}
	for i := 0; i < limit; i++ {
		poststates := funcPoststatesForParam(fnType.Poststates, i)
		if len(poststates) == 0 {
			continue
		}
		root, argSteps, ok := a.namedStateMutationTargetPath(call.Args[i])
		if !ok || root == nil {
			continue
		}
		original := originalByRoot[root]
		if original == nil {
			continue
		}
		updated, changed := a.computeCallArgPoststateTrackedType(original, argSteps, poststates, true, truthy)
		if !changed || updated == nil {
			continue
		}
		if persistTracked && a.currentSpecializedValueTypes != nil {
			a.bindTrackedValueType(root, updated)
			continue
		}
		if root.Name != "" {
			scope.Refinements[root.Name] = updated
		}
	}
}

func (a *Analyzer) guardFactsForConditionWithMetadata(expr ast.Expr, truthy bool) GuardFactSet {
	facts := GuardFactsForCondition(expr, truthy)
	a.augmentGuardFactsForCondition(&facts, expr, truthy)
	return facts
}

func (a *Analyzer) augmentGuardFactsForCondition(facts *GuardFactSet, expr ast.Expr, truthy bool) {
	if a == nil || facts == nil || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			if truthy {
				a.augmentGuardFactsForCondition(facts, n.Left, true)
				a.augmentGuardFactsForCondition(facts, n.Right, true)
			}
		case lexer.TOKEN_OR:
			if !truthy {
				a.augmentGuardFactsForCondition(facts, n.Left, false)
				a.augmentGuardFactsForCondition(facts, n.Right, false)
			}
		}
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			a.augmentGuardFactsForCondition(facts, n.Operand, !truthy)
		}
	case *ast.ParenExpr:
		a.augmentGuardFactsForCondition(facts, n.Inner, truthy)
	case *ast.CallExpr:
		a.addGuardCallFacts(facts, n, truthy)
	}
}

func (a *Analyzer) addGuardCallFacts(facts *GuardFactSet, call *ast.CallExpr, truthy bool) {
	if a == nil || facts == nil || call == nil || !truthy {
		return
	}
	_, fnType, ok := a.resolveSinkFuncDecl(call.Func)
	if !ok || fnType == nil || len(fnType.GuardEffects) == 0 {
		return
	}
	for _, effect := range fnType.GuardEffects {
		if effect.ParamIndex < 0 || effect.ParamIndex >= len(call.Args) {
			continue
		}
		argExpr := call.Args[effect.ParamIndex]
		switch effect.Kind {
		case FuncGuardKindNonNull:
			facts.AddNonNull(argExpr)
		case FuncGuardKindPackedVariant:
			base, ok := a.namedTypes[effect.EnumName]
			if !ok {
				continue
			}
			switch variantBase := base.(type) {
			case *EnumType:
				if variantBase == nil || !variantBase.Packed {
					continue
				}
				variant, ok := variantBase.Variant(effect.VariantName)
				if !ok || variant == nil {
					continue
				}
				facts.AddPackedVariant(argExpr, variant.PackedViewType(variantBase))
			case *TreeCategoryType:
				variant, ok := variantBase.Variant(effect.VariantName)
				if !ok || variant == nil {
					continue
				}
				facts.AddVariantProof(argExpr, variantBase.Name, variant.Name)
			}
		}
	}
}

func (a *Analyzer) shadowRefinedExpr(scope *Scope, expr ast.Expr, nonNull bool) {
	if scope == nil {
		return
	}
	baseType := a.analyzeExprInScope(expr, scope)
	refined, ok := refinedNullComparableType(baseType, nonNull)
	if !ok {
		return
	}
	a.bindRefinedExprType(scope, expr, refined)
}

func refinedNullComparableType(baseType Type, nonNull bool) (Type, bool) {
	switch t := baseType.(type) {
	case *RefType:
		desired := RefStateNull
		if nonNull {
			desired = RefStateNonNull
		}
		if !refinementCompatible(t.State, desired) {
			return nil, false
		}
		return cloneRefTypeWithState(t, desired), true
	case *OptionalType:
		if nonNull {
			return t.Value, true
		}
		return nullType, true
	case *NullType:
		if nonNull {
			return nil, false
		}
		return t, true
	default:
		return nil, false
	}
}

func refinementCompatible(current, desired RefState) bool {
	switch desired {
	case RefStateNonNull:
		return current == RefStateNonNull || current == RefStateNullable
	case RefStateNull:
		return current == RefStateNull || current == RefStateNullable
	default:
		return true
	}
}

func exprRefinementKey(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return exprRefinementKey(n.Inner)
	case *ast.Ident:
		return n.Name, true
	case *ast.FieldExpr:
		base, ok := exprRefinementKey(n.Object)
		if !ok {
			return "", false
		}
		return base + "." + n.Field, true
	default:
		return "", false
	}
}

func (a *Analyzer) lookupRefinedExprType(expr ast.Expr) (Type, bool) {
	if a.currentScope == nil {
		return nil, false
	}
	key, ok := exprRefinementKey(expr)
	if ok {
		if refined, ok := a.currentScope.LookupRefinement(key); ok {
			return refined, true
		}
	}
	if aliasKey, ok := a.aliasRootRefinementKey(a.currentScope, expr); ok {
		if refined, ok := a.currentScope.LookupRefinement(aliasKey); ok {
			return refined, true
		}
	}
	if !ok {
		if viewType, ok := a.lookupRefinedPackedVariantView(expr); ok {
			return viewType, true
		}
		return nil, false
	}
	if viewType, ok := a.lookupRefinedPackedVariantView(expr); ok {
		return viewType, true
	}
	return nil, false
}

func (a *Analyzer) applyPostIfFallthroughRefinement(stmt *ast.IfStmt) {
	if a.currentScope == nil || len(stmt.Elifs) > 0 {
		return
	}
	if blockDefinitelyExits(stmt.Then) {
		a.applyConditionRefinements(a.currentScope, stmt.Cond, false)
	}
	if len(stmt.Else) > 0 && blockDefinitelyExits(stmt.Else) {
		a.applyConditionRefinements(a.currentScope, stmt.Cond, true)
	}
}

func blockDefinitelyExits(stmts []ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	return stmtDefinitelyExits(stmts[len(stmts)-1])
}

func stmtDefinitelyExits(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt, *ast.PanicStmt, *ast.StaticErrorStmt:
		return true
	case *ast.ExprStmt:
		_, ok := n.Expr.(*ast.RaiseExpr)
		return ok
	case *ast.IfStmt:
		if !blockDefinitelyExits(n.Then) {
			return false
		}
		for _, elif := range n.Elifs {
			if !blockDefinitelyExits(elif.Body) {
				return false
			}
		}
		return len(n.Else) > 0 && blockDefinitelyExits(n.Else)
	case *ast.MatchStmt:
		if len(n.Arms) == 0 {
			return false
		}
		hasWildcard := false
		for _, arm := range n.Arms {
			if _, ok := arm.Pattern.(*ast.MatchWildcardPattern); ok {
				hasWildcard = true
			}
			if !blockDefinitelyExits(arm.Body) {
				return false
			}
		}
		return hasWildcard
	case *ast.StaticIfStmt:
		if !blockDefinitelyExits(n.Then) {
			return false
		}
		for _, elif := range n.Elifs {
			if !blockDefinitelyExits(elif.Body) {
				return false
			}
		}
		return len(n.Else) > 0 && blockDefinitelyExits(n.Else)
	case *ast.PoolStmt:
		return blockDefinitelyExits(n.Body)
	case *ast.LockStmt:
		return blockDefinitelyExits(n.Body)
	default:
		return false
	}
}

func (a *Analyzer) recordAssignmentRefinement(target ast.Expr, targetType Type, valueType Type) {
	if a.currentScope == nil {
		return
	}
	key, ok := exprRefinementKey(target)
	if !ok {
		return
	}
	refined := assignedRefinementType(targetType, valueType)
	if refined == nil {
		delete(a.currentScope.Refinements, key)
		return
	}
	a.currentScope.Refinements[key] = refined
}

func assignedRefinementType(targetType Type, valueType Type) Type {
	targetRef, ok := targetType.(*RefType)
	if ok {
		if IsNullType(valueType) {
			return cloneRefTypeWithState(targetRef, RefStateNull)
		}
		if valueRef, ok := valueType.(*RefType); ok {
			return cloneRefType(valueRef)
		}
		return targetRef
	}
	targetOptional, ok := targetType.(*OptionalType)
	if !ok {
		return nil
	}
	if IsNullType(valueType) {
		return nullType
	}
	if _, ok := valueType.(*OptionalType); ok {
		return targetOptional
	}
	if AssignableTo(targetOptional.Value, valueType) {
		return targetOptional.Value
	}
	return targetOptional
}

func assertedCondition(expr ast.Expr) (ast.Expr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident.Name != "assert" {
		return nil, false
	}
	return call.Args[0], true
}

func (a *Analyzer) analyzeExprInScope(expr ast.Expr, scope *Scope) Type {
	saved := a.currentScope
	a.currentScope = scope
	result := a.analyzeExpr(expr)
	a.currentScope = saved
	return result
}

func (a *Analyzer) analyzeExprInAffineScope(expr ast.Expr, scope *Scope) (Type, affineFlowSnapshot) {
	return a.analyzeExprInAffineScopePrepared(expr, scope, nil)
}

func (a *Analyzer) analyzeExprInConditionAffineScope(expr ast.Expr, parent *Scope, cond ast.Expr, truthy bool) (Type, affineFlowSnapshot) {
	scope := a.refinedScopeForCondition(parent, cond, truthy)
	return a.analyzeExprInAffineScopePrepared(expr, scope, func() {
		a.applyConditionRefinementsInternal(scope, cond, truthy, true)
	})
}

func (a *Analyzer) analyzeExprInAffineScopePrepared(expr ast.Expr, scope *Scope, prepare func()) (Type, affineFlowSnapshot) {
	savedAffine := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	a.currentAffineValues = a.cloneAffineValueStates()
	a.currentBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
	a.currentFunctionValues = a.cloneFunctionValueBindings()
	a.currentSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
	a.currentValueBindings = a.cloneValueBindings()
	if prepare != nil {
		prepare()
	}
	result := a.analyzeExprInScope(expr, scope)
	snapshot := affineFlowSnapshot{Affine: a.cloneAffineValueStates(), BorrowedOwnerRefs: a.cloneBorrowedOwnerRefBindings(), FunctionValues: a.cloneFunctionValueBindings(), SpecializedValueTypes: a.cloneSpecializedValueTypeBindings(), ValueBindings: a.cloneValueBindings()}
	a.currentAffineValues = savedAffine
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	return result, snapshot
}

func (a *Analyzer) analyzeMatchExprArmBodyWithAffineSnapshot(body []ast.Stmt, scope *Scope) (Type, affineFlowSnapshot) {
	savedAffine := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	a.currentAffineValues = a.cloneAffineValueStates()
	a.currentBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
	a.currentFunctionValues = a.cloneFunctionValueBindings()
	a.currentSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
	a.currentValueBindings = a.cloneValueBindings()
	result := a.analyzeMatchExprArmBody(body, scope)
	snapshot := affineFlowSnapshot{Affine: a.cloneAffineValueStates(), BorrowedOwnerRefs: a.cloneBorrowedOwnerRefBindings(), FunctionValues: a.cloneFunctionValueBindings(), SpecializedValueTypes: a.cloneSpecializedValueTypeBindings(), ValueBindings: a.cloneValueBindings()}
	a.currentAffineValues = savedAffine
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	return result, snapshot
}

func isAffineHandleType(t Type) bool {
	switch tt := t.(type) {
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		if !ok {
			return false
		}
		return base.Affine
	case *StructType:
		return tt.Affine
	default:
		return false
	}
}

func isBorrowableAffineOwnerType(t Type) bool {
	return isBuiltinProtocolOwnerType(t, "ThreadPool") || isBuiltinProtocolOwnerType(t, "TaskGroup")
}

func affineHandleKind(t Type) string {
	if !isAffineHandleType(t) {
		return "value containing affine handles"
	}
	switch tt := t.(type) {
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			switch base.Name {
			case "Thread":
				return "thread handle"
			case "Task":
				return "task handle"
			case "MutexGuard":
				return "mutex guard"
			case "ThreadPool":
				return "thread pool owner"
			case "TaskGroup":
				return "task group owner"
			}
		}
	case *StructType:
		switch tt.Name {
		case "Thread":
			return "thread handle"
		case "Task":
			return "task handle"
		case "MutexGuard":
			return "mutex guard"
		case "ThreadPool":
			return "thread pool owner"
		case "TaskGroup":
			return "task group owner"
		}
	}
	return "affine value"
}

func (a *Analyzer) consumeAffineValueExpr(expr ast.Expr, expected Type, reason string) {
	if expr == nil || !a.containsAffineHandleValues(expected, map[string]bool{}) {
		return
	}
	if moved, ok := explicitMoveOperand(expr); ok {
		key, ok := a.lookupAffineValueKey(moved)
		if !ok {
			return
		}
		if a.currentAffineValues == nil {
			a.currentAffineValues = map[affineValueKey]affineValueState{}
		}
		a.recordAffineConsumption(key, reason)
		return
	}
	if _, ok := a.lookupAffineValueKey(expr); ok {
		affineType := expected
		if actual := a.exprTypes[expr]; actual != nil && a.containsAffineHandleValues(actual, map[string]bool{}) {
			affineType = actual
		}
		a.errorf(expr.Pos(), "%s %q must be moved explicitly before %s", affineHandleKind(affineType), affineValueDisplayName(expr), reason)
	}
}

func (a *Analyzer) recordAffineConsumption(key affineValueKey, reason string) {
	if a.currentAffineValues == nil {
		a.currentAffineValues = map[affineValueKey]affineValueState{}
	}
	state := a.currentAffineValues[key]
	if state.ConsumedBy == "" {
		state.ConsumedBy = reason
	}
	state.LiveProtocolType = nil
	state.LiveProtocolDescription = ""
	a.currentAffineValues[key] = state
	for existingKey, existingState := range a.currentAffineValues {
		if existingKey == key || existingKey.Root != key.Root {
			continue
		}
		if key.Path != "" && !affinePathContains(key.Path, existingKey.Path) {
			continue
		}
		existingState.LiveProtocolType = nil
		existingState.LiveProtocolDescription = ""
		if existingState.ConsumedBy == "" {
			existingState.ConsumedBy = reason
		}
		a.currentAffineValues[existingKey] = existingState
	}
}

func (a *Analyzer) lookupProtocolTargetKey(expr ast.Expr) (affineValueKey, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.lookupProtocolTargetKey(n.Inner)
	case *ast.MoveExpr:
		return a.lookupProtocolTargetKey(n.Operand)
	case *ast.CastExpr:
		return a.lookupProtocolTargetKey(n.Operand)
	case *ast.AddrOfExpr:
		if key, ok := a.lookupAffineValueKey(n.Operand); ok {
			return key, true
		}
		return a.lookupBorrowedOwnerRefKey(n.Operand)
	default:
		if key, ok := a.lookupBorrowedOwnerRefKey(expr); ok {
			return key, true
		}
		return a.lookupAffineValueKey(expr)
	}
}

func (a *Analyzer) clearAffineValueTarget(expr ast.Expr) {
	key, ok := a.lookupAffineValueKey(expr)
	if !ok || a.currentAffineValues == nil {
		return
	}
	for existing := range a.currentAffineValues {
		if existing.Root != key.Root {
			continue
		}
		if key.Path == "" {
			delete(a.currentAffineValues, existing)
			continue
		}
		if existing.Path == key.Path || strings.HasPrefix(existing.Path, key.Path+".") {
			delete(a.currentAffineValues, existing)
		}
	}
}

func (a *Analyzer) lookupAffineValueState(expr ast.Expr) (affineValueState, bool) {
	key, ok := a.lookupAffineValueKey(expr)
	if !ok {
		return affineValueState{}, false
	}
	return a.lookupAffineValueStateForKey(key)
}

func (a *Analyzer) lookupAffineValueStateForKey(key affineValueKey) (affineValueState, bool) {
	if a.currentAffineValues == nil {
		return affineValueState{}, false
	}
	state, ok := a.currentAffineValues[key]
	if ok && state.ConsumedBy != "" {
		return state, true
	}
	for existing, existingState := range a.currentAffineValues {
		if existing.Root != key.Root {
			continue
		}
		if existingState.ConsumedBy != "" && (affinePathContains(existing.Path, key.Path) || affinePathContains(key.Path, existing.Path)) {
			return existingState, true
		}
	}
	return affineValueState{}, false
}

func (a *Analyzer) lookupAffineValueKey(expr ast.Expr) (affineValueKey, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.lookupAffineValueKey(n.Inner)
	case *ast.MoveExpr:
		return a.lookupAffineValueKey(n.Operand)
	case *ast.CastExpr:
		return a.lookupAffineValueKey(n.Operand)
	case *ast.Ident:
		if a.currentScope == nil {
			return affineValueKey{}, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok {
			return affineValueKey{}, false
		}
		if sym.Kind != SymbolLocal && sym.Kind != SymbolParam {
			return affineValueKey{}, false
		}
		return affineValueKey{Root: sym}, true
	case *ast.FieldExpr:
		base, ok := a.lookupAffineValueKey(n.Object)
		if !ok {
			return affineValueKey{}, false
		}
		objType := a.exprTypes[n.Object]
		if objType == nil {
			objType = a.analyzeExpr(n.Object)
		}
		field, ok := a.lookupField(objType, n.Field, n.Pos())
		if !ok || !a.containsTrackedProtocolCarrierValues(field.Type, map[string]bool{}) {
			return affineValueKey{}, false
		}
		if base.Path == "" {
			base.Path = n.Field
		} else {
			base.Path = base.Path + "." + n.Field
		}
		return base, true
	case *ast.IndexExpr:
		base, ok := a.lookupAffineValueKey(n.Object)
		if !ok {
			return affineValueKey{}, false
		}
		objType := a.exprTypes[n.Object]
		if objType == nil {
			objType = a.analyzeExpr(n.Object)
		}
		if elemType, ok := affineIndexedElemType(objType); ok && a.containsTrackedProtocolCarrierValues(elemType, map[string]bool{}) {
			return base, true
		}
		return affineValueKey{}, false
	default:
		return affineValueKey{}, false
	}
}

func affinePathContains(ancestor, descendant string) bool {
	if ancestor == "" {
		return true
	}
	if descendant == ancestor {
		return true
	}
	if strings.HasPrefix(descendant, ancestor+".") {
		return true
	}
	if strings.HasPrefix(descendant, ancestor+"[") {
		return true
	}
	return false
}

func affineIndexedElemType(t Type) (Type, bool) {
	switch tt := t.(type) {
	case *ArrayType:
		return tt.Elem, true
	case *DArrayType:
		return tt.Elem, true
	case *ViewType:
		return tt.Elem, true
	case *DArrayViewType:
		return tt.Elem, true
	case *RefType:
		return affineIndexedElemType(tt.Elem)
	default:
		return nil, false
	}
}

func affineValueDisplayName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return affineValueDisplayName(n.Inner)
	case *ast.MoveExpr:
		return affineValueDisplayName(n.Operand)
	case *ast.CastExpr:
		return affineValueDisplayName(n.Operand)
	case *ast.Ident:
		return n.Name
	case *ast.FieldExpr:
		base := affineValueDisplayName(n.Object)
		if base == "" {
			return n.Field
		}
		return base + "." + n.Field
	default:
		return "<value>"
	}
}

func explicitMoveOperand(expr ast.Expr) (ast.Expr, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return explicitMoveOperand(n.Inner)
	case *ast.MoveExpr:
		return n.Operand, true
	default:
		return nil, false
	}
}

func (a *Analyzer) analyzeCondExpr(expr ast.Expr) Type {
	return a.analyzeCondExprInScope(expr, a.currentScope)
}

func (a *Analyzer) analyzeCondExprInScope(expr ast.Expr, scope *Scope) Type {
	saved := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = saved }()

	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.analyzeCondExprInScope(n.Inner, scope)
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			operand := a.analyzeCondExprInScope(n.Operand, scope)
			if !IsBoolType(operand) {
				a.errorf(n.Pos(), "not operator requires bool operand")
			}
			return a.namedTypes["bool"]
		}
		return a.analyzeExpr(n)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			left := a.analyzeCondExprInScope(n.Left, scope)
			right := a.analyzeCondExprInScope(n.Right, a.refinedScopeForCondition(scope, n.Left, true))
			if !IsBoolType(left) || !IsBoolType(right) {
				a.errorf(n.Pos(), "logical operator requires bool operands")
			}
			return a.namedTypes["bool"]
		case lexer.TOKEN_OR:
			left := a.analyzeCondExprInScope(n.Left, scope)
			right := a.analyzeCondExprInScope(n.Right, a.refinedScopeForCondition(scope, n.Left, false))
			if !IsBoolType(left) || !IsBoolType(right) {
				a.errorf(n.Pos(), "logical operator requires bool operands")
			}
			return a.namedTypes["bool"]
		default:
			return a.analyzeExpr(n)
		}
	default:
		return a.analyzeExpr(expr)
	}
}
