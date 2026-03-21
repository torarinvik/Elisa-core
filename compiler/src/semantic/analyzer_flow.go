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
		a.consumeAffineValueExpr(n.Value, bindingType, "move into local "+strconvQuote(n.Name))
	case *ast.MoveBindStmt:
		a.analyzeMoveBindStmt(n)
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
		a.clearAffineValueTarget(n.Target)
		a.trackAffineValueTarget(n.Target, targetType)
		a.markCreatedProtocolTarget(n.Target, n.Value, targetType)
		a.recordBorrowedOwnerRefTarget(n.Target, targetType, n.Value)
		a.recordFunctionValueTarget(n.Target, n.Value)
		a.consumeAffineValueExpr(n.Value, targetType, "assignment")
	case *ast.AugAssignStmt:
		targetType := a.assignmentTargetType(n.Target)
		valueType := a.analyzeExpr(n.Value)
		if !IsNumericType(targetType) || !IsNumericType(valueType) {
			a.errorf(n.Pos(), "augmented assignment requires numeric operands")
		}
	case *ast.AsRefAssignStmt:
		targetType := a.asRefTargetType(n.Target, n.AsKind)
		valueType := a.analyzeValueExpr(n.Value, targetType)
		if !AssignableTo(targetType, valueType) {
			a.errorf(n.Pos(), "cannot assign %s to %s", valueType.String(), targetType.String())
			a.reportShapeMismatchNotes(n.Pos(), targetType, valueType)
		}
		a.recordAssignmentRefinement(n.Target, targetType, targetType)
		a.recordRegionRefAssignment(n.Target, n.Value)
		a.recordSpecializedValueTypeTarget(n.Target, valueType)
		a.clearAffineValueTarget(n.Target)
		a.trackAffineValueTarget(n.Target, targetType)
		a.markCreatedProtocolTarget(n.Target, n.Value, targetType)
		a.recordBorrowedOwnerRefTarget(n.Target, targetType, n.Value)
		a.recordFunctionValueTarget(n.Target, n.Value)
		a.consumeAffineValueExpr(n.Value, targetType, "assignment")
	case *ast.ReturnStmt:
		if n.Value == nil {
			if currentUnion, ok := a.currentReturn.(*ErrorUnionType); ok {
				if !SameType(currentUnion.Value, a.namedTypes["void"]) {
					a.errorf(n.Pos(), "return value required for %s", a.currentReturn.String())
				}
				return
			}
			if a.currentReturn != nil && !SameType(a.currentReturn, a.namedTypes["void"]) {
				a.errorf(n.Pos(), "return value required for %s", a.currentReturn.String())
			}
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
		thenSnapshot := a.analyzeBlockWithAffineClone(n.Then, a.refinedScopeForCondition(a.currentScope, n.Cond, true))
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
			elifSnapshot := a.analyzeBlockWithAffineClone(elif.Body, a.refinedScopeForCondition(a.currentScope, elif.Cond, true))
			if !blockDefinitelyExits(elif.Body) {
				mergedAffine = mergeAffineValueStates(mergedAffine, elifSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, elifSnapshot.BorrowedOwnerRefs)
				functionValueBranches = append(functionValueBranches, elifSnapshot.FunctionValues)
				specializedValueTypeBranches = append(specializedValueTypeBranches, elifSnapshot.SpecializedValueTypes)
			}
		}
		if len(n.Elifs) == 0 {
			elseSnapshot := a.analyzeBlockWithAffineClone(n.Else, a.refinedScopeForCondition(a.currentScope, n.Cond, false))
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
		bodySnapshot := a.analyzeBlockWithAffineClone(n.Body, a.refinedScopeForCondition(a.currentScope, n.Cond, true))
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
		for _, payload := range payloads {
			if payload.BindName == "" || payload.BindName == "_" {
				continue
			}
			sym := &Symbol{Name: payload.BindName, Kind: SymbolLocal, Type: payload.Type, Node: p, Mutable: false}
			a.defineLocal(sym, p.Position)
			if valueExpr, ok := a.resolveMoveBindVariantPayloadValueExpr(stmt.Value, p, payload.Key); ok {
				a.recordValueBinding(sym, valueExpr)
				a.recordFunctionValueBinding(sym, valueExpr)
				a.recordImmutableSymbolOptimizationFacts(sym, valueExpr)
			}
			if hasBorrowedOwnerState {
				if fieldState, ok := projectBorrowedOwnerRefFieldState(borrowedOwnerState, payload.Key); ok {
					a.currentBorrowedOwnerRefs[sym] = fieldState
				}
			}
			if !hasValueState && packedStoreState == nil {
				continue
			}
			if hasValueState {
				if fieldState, ok := projectRegionFieldState(valueState, payload.Key); ok {
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
	default:
		a.errorf(stmt.Pos(), "unsupported move-as pattern %T", stmt.Pattern)
		return
	}
	a.consumeAffineValueExpr(stmt.Value, valueType, "move-as destructure")
}

type moveBindResolvedField struct {
	Name string
	Type Type
}

type moveBindResolvedVariantField struct {
	Key      string
	Type     Type
	BindName string
}

func (a *Analyzer) resolvedStructFields(actual Type) ([]moveBindResolvedField, bool) {
	var (
		base     *StructType
		bindings map[string]Type
	)
	switch tt := actual.(type) {
	case *StructType:
		base = tt
	case *GenericInstanceType:
		structBase, ok := tt.Base.(*StructType)
		if !ok {
			return nil, false
		}
		base = structBase
		if len(base.TypeParams) == len(tt.Args) {
			bindings = make(map[string]Type, len(base.TypeParams))
			for i, name := range base.TypeParams {
				bindings[name] = tt.Args[i]
			}
		}
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
		fields = append(fields, moveBindResolvedField{Name: fieldDecl.Name, Type: fieldType})
	}
	return fields, true
}

func (a *Analyzer) resolveMoveBindStructPattern(pattern *ast.MoveBindStructPattern, actual Type) ([]moveBindResolvedField, bool) {
	if pattern == nil {
		return nil, false
	}
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

func moveBindVariantAsMatchPattern(pattern *ast.MoveBindVariantPattern) *ast.MatchVariantPattern {
	if pattern == nil {
		return nil
	}
	return &ast.MatchVariantPattern{Position: pattern.Position, EnumName: pattern.EnumName, Variant: pattern.Variant, Args: append([]ast.MatchPatternArg(nil), pattern.Args...)}
}

func moveBindVariantArgBindingName(pattern ast.MatchPattern) string {
	switch p := pattern.(type) {
	case *ast.MatchBindPattern:
		return p.Name
	case *ast.MatchWildcardPattern:
		return "_"
	default:
		return ""
	}
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
	enumType, ok := actual.(*EnumType)
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
		if stmt.Store == nil {
			a.errorf(pattern.Pos(), "packed move-as pattern %q.%q requires an in-store clause", pattern.EnumName, pattern.Variant)
			return nil, nil, nil, false
		}
		a.validateMoveBindStore(pattern.Pos(), enumType, stmt.Store)
		if state, ok := a.regionRefStateForExpr(stmt.Store); ok {
			cloned := cloneRegionRefState(state)
			storeState = &cloned
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
	fields := make([]moveBindResolvedVariantField, len(orderedArgs))
	for i, arg := range orderedArgs {
		if arg == nil {
			continue
		}
		switch arg.Pattern.(type) {
		case *ast.MatchBindPattern, *ast.MatchWildcardPattern:
		default:
			a.errorf(arg.Position, "move-as variant patterns only support bind names and _")
			return nil, nil, nil, false
		}
		fields[i] = moveBindResolvedVariantField{Key: moveBindVariantFieldKey(variant, i), Type: variant.Payload[i], BindName: moveBindVariantArgBindingName(arg.Pattern)}
	}
	return fields, enumType, storeState, true
}

func (a *Analyzer) resolveVariantPayloadValueExpr(value ast.Expr, enumName string, variantName string, key string) (ast.Expr, bool) {
	if value == nil || enumName == "" || variantName == "" || key == "" {
		return nil, false
	}
	switch n := value.(type) {
	case *ast.ParenExpr:
		return a.resolveVariantPayloadValueExpr(n.Inner, enumName, variantName, key)
	case *ast.CastExpr:
		return a.resolveVariantPayloadValueExpr(n.Operand, enumName, variantName, key)
	case *ast.MoveExpr:
		return a.resolveVariantPayloadValueExpr(n.Operand, enumName, variantName, key)
	case *ast.CanExpr:
		return a.resolveVariantPayloadValueExpr(n.Expr, enumName, variantName, key)
	case *ast.AllocExpr:
		return a.resolveVariantPayloadValueExpr(n.Value, enumName, variantName, key)
	case *ast.FieldExpr:
		resolved, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field)
		if !ok {
			return nil, false
		}
		return a.resolveVariantPayloadValueExpr(resolved, enumName, variantName, key)
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
		return a.resolveVariantPayloadValueExpr(decl.Value, enumName, variantName, key)
	case *ast.CallExpr:
		enumType, variant, ok := a.enumConstructorCall(n)
		if !ok || enumType == nil || variant == nil {
			return nil, false
		}
		if enumType.Name != enumName || variant.Name != variantName {
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

func (a *Analyzer) resolveMatchVariantPayloadValueExpr(value ast.Expr, pattern *ast.MatchVariantPattern, key string) (ast.Expr, bool) {
	if pattern == nil {
		return nil, false
	}
	return a.resolveVariantPayloadValueExpr(value, pattern.EnumName, pattern.Variant, key)
}

func (a *Analyzer) validateMoveBindStore(pos lexer.Pos, enumType *EnumType, storeExpr ast.Expr) {
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
	savedPackedStores := a.currentPackedStores
	a.currentScope = NewScope(savedScope)
	a.currentRegions = a.cloneRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentRegionRefs = a.cloneRegionRefStates()
	a.currentPackedStores = a.clonePackedStores()
	a.defineLocal(&Symbol{Name: stmt.Name, Kind: SymbolLocal, Type: poolType, Node: stmt, Mutable: false}, stmt.Pos())
	for _, inner := range stmt.Body {
		a.analyzeStmt(inner)
	}
	a.currentScope = savedScope
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentPackedStores = savedPackedStores
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
	savedPackedStores := a.currentPackedStores
	guardSym := &Symbol{Name: stmt.GuardName, Kind: SymbolLocal, Type: guardType, Node: stmt, Mutable: true}
	a.currentScope = NewScope(savedScope)
	a.currentRegions = a.cloneRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentRegionRefs = a.cloneRegionRefStates()
	a.currentPackedStores = a.clonePackedStores()
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
	a.currentPackedStores = savedPackedStores
}

func (a *Analyzer) analyzeInStoreStmt(stmt *ast.InStoreStmt) {
	storeType := a.analyzeExpr(stmt.Store)
	packedStore, ok := storeType.(*PackedEnumStoreType)
	if !ok {
		a.errorf(stmt.Store.Pos(), "in-store block requires a packed enum store, got %s", storeType.String())
		a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
		return
	}
	savedPackedStores := a.currentPackedStores
	a.currentPackedStores = a.clonePackedStores()
	if a.currentPackedStores == nil {
		a.currentPackedStores = map[string]*PackedEnumStoreType{}
	}
	a.currentPackedStores[packedStore.Enum.Name] = packedStore
	a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
	a.currentPackedStores = savedPackedStores
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
	enumType, ok := valueType.(*EnumType)
	if !ok {
		a.errorf(stmt.Pos(), "match requires an enum value, got %s", valueType.String())
		for _, arm := range stmt.Arms {
			a.analyzeBlockWithRegionClone(arm.Body, NewScope(a.currentScope))
		}
		return
	}
	a.validateMatchStore(stmt.Pos(), enumType, stmt.Store)
	baselineAffine := a.cloneAffineValueStates()
	baselineBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
	baselineFunctionValues := a.cloneFunctionValueBindings()
	baselineSpecializedValueTypes := a.cloneSpecializedValueTypeBindings()
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
	enumType, ok := valueType.(*EnumType)
	if !ok {
		a.errorf(expr.Pos(), "match requires an enum value, got %s", valueType.String())
		for _, arm := range expr.Arms {
			a.analyzeMatchExprArmBody(arm.Body, NewScope(a.currentScope))
		}
		return invalidType
	}
	a.validateMatchStore(expr.Pos(), enumType, expr.Store)
	covered := map[string]bool{}
	hasWildcard := false
	resultType := Type(nil)
	baselineAffine := a.cloneAffineValueStates()
	baselineBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
	baselineFunctionValues := a.cloneFunctionValueBindings()
	baselineSpecializedValueTypes := a.cloneSpecializedValueTypeBindings()
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
	a.reportNonExhaustiveMatch(expr.Pos(), enumType, covered, hasWildcard)
	if resultType == nil {
		return neverType
	}
	return resultType
}

func (a *Analyzer) validateMatchStore(pos lexer.Pos, enumType *EnumType, storeExpr ast.Expr) {
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

func (a *Analyzer) matchPatternShadowedByPrevious(pattern ast.MatchPattern, enumType *EnumType, prior []ast.MatchPattern) bool {
	for _, prev := range prior {
		if a.matchPatternCovers(prev, pattern, enumType) {
			return true
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
	case *ast.MatchVariantPattern:
		currVariant, ok := current.(*ast.MatchVariantPattern)
		if !ok {
			return false
		}
		enumType, ok := expected.(*EnumType)
		if !ok || p.EnumName != enumType.Name || currVariant.EnumName != enumType.Name || p.Variant != currVariant.Variant {
			return false
		}
		variant, ok := enumType.Variant(p.Variant)
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

func (a *Analyzer) analyzeMatchExprArmBody(body []ast.Stmt, scope *Scope) Type {
	savedScope := a.currentScope
	savedRegions := a.currentRegions
	savedPackedStores := a.currentPackedStores
	a.currentScope = scope
	a.currentRegions = a.cloneRegionStates()
	a.currentPackedStores = a.clonePackedStores()
	defer func() {
		a.currentScope = savedScope
		a.currentRegions = savedRegions
		a.currentPackedStores = savedPackedStores
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
		enumType, ok := expected.(*EnumType)
		if !ok {
			a.errorf(p.Pos(), "nested variant pattern %q requires an enum payload, got %s", p.EnumName+"."+p.Variant, expected.String())
			return
		}
		if p.EnumName != enumType.Name {
			a.errorf(p.Pos(), "nested match pattern expects enum %q, got %q", enumType.Name, p.EnumName)
			return
		}
		variant, ok := enumType.Variant(p.Variant)
		if !ok {
			a.errorf(p.Pos(), "enum %q has no variant %q", enumType.Name, p.Variant)
			return
		}
		orderedArgs := a.resolveMatchPatternArgs(p, variant, enumType.Name+"."+variant.Name, true)
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			payloadExpr, _ := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
			a.analyzeNestedMatchPattern(arg.Pattern, variant.Payload[i], payloadExpr, scope)
		}
	default:
		a.errorf(pattern.Pos(), "unsupported nested match pattern %T", pattern)
	}
}

func (a *Analyzer) resolveMatchPatternArgs(pattern *ast.MatchVariantPattern, variant *EnumVariant, qualified string, nested bool) []*ast.MatchPatternArg {
	ordered := make([]*ast.MatchPatternArg, len(variant.Payload))
	if len(pattern.Args) == 0 {
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
		return ordered
	}
	if !variant.HasNamedPayloads() {
		a.errorf(pattern.Pos(), "%s does not declare named payload fields", matchPatternContext(qualified, nested))
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
	return ordered
}

func matchPatternContext(qualified string, nested bool) string {
	if nested {
		return "nested match arm " + strconvQuote(qualified)
	}
	return "match arm " + strconvQuote(qualified)
}

func (a *Analyzer) matchCoversAllVariants(enumType *EnumType, covered map[string]bool, hasWildcard bool) bool {
	if enumType == nil {
		return false
	}
	if hasWildcard {
		return true
	}
	for _, variant := range enumType.Variants {
		if !covered[variant.Name] {
			return false
		}
	}
	return true
}

func strconvQuote(s string) string {
	return "\"" + s + "\""
}

func (a *Analyzer) reportNonExhaustiveMatch(pos lexer.Pos, enumType *EnumType, covered map[string]bool, hasWildcard bool) {
	if enumType == nil || hasWildcard {
		return
	}
	missing := make([]string, 0)
	for _, variant := range enumType.Variants {
		if !covered[variant.Name] {
			missing = append(missing, enumType.Name+"."+variant.Name)
		}
	}
	if len(missing) == 0 {
		return
	}
	a.errorf(pos, "non-exhaustive match over %q; missing variants: %s", enumType.Name, strings.Join(missing, ", "))
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
	savedPackedStores := a.currentPackedStores
	a.currentRegions = a.cloneRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentRegionRefs = a.cloneRegionRefStates()
	a.currentPackedStores = a.clonePackedStores()
	a.analyzeBlockInScope(stmts, scope)
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentPackedStores = savedPackedStores
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
	a.analyzeBlockWithRegionClone(stmts, scope)
	snapshot := affineFlowSnapshot{Affine: a.cloneAffineValueStates(), BorrowedOwnerRefs: a.cloneBorrowedOwnerRefBindings(), FunctionValues: a.cloneFunctionValueBindings(), SpecializedValueTypes: a.cloneSpecializedValueTypeBindings(), ValueBindings: a.cloneValueBindings()}
	a.currentAffineValues = savedAffine
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
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
		cloned[sym] = cloneRegionRefState(state)
	}
	return cloned
}

func cloneRegionRefState(state regionRefState) regionRefState {
	cloned := regionRefState{}
	if len(state.Deps) != 0 {
		cloned.Deps = make(map[*Symbol]regionDependencyState, len(state.Deps))
		for region, dep := range state.Deps {
			cloned.Deps[region] = dep
		}
	}
	if len(state.StoreDeps) != 0 {
		cloned.StoreDeps = make(map[*Symbol]packedStoreDependencyState, len(state.StoreDeps))
		for store, dep := range state.StoreDeps {
			cloned.StoreDeps[store] = dep
		}
	}
	if len(state.ParamDeps) != 0 {
		cloned.ParamDeps = make(map[int]bool, len(state.ParamDeps))
		for index, dep := range state.ParamDeps {
			cloned.ParamDeps[index] = dep
		}
	}
	if len(state.Fields) != 0 {
		cloned.Fields = make(map[string]regionRefState, len(state.Fields))
		for name, fieldState := range state.Fields {
			cloned.Fields[name] = cloneRegionRefState(fieldState)
		}
	}
	return cloned
}

func hasRegionDependencies(state regionRefState) bool {
	return len(state.Deps) != 0 || len(state.StoreDeps) != 0
}

func hasRegionProvenance(state regionRefState) bool {
	return hasRegionDependencies(state) || len(state.ParamDeps) != 0 || len(state.Fields) != 0
}

func regionRefStateFromDependency(region *Symbol, generation int) regionRefState {
	if region == nil {
		return regionRefState{}
	}
	return regionRefState{
		Deps: map[*Symbol]regionDependencyState{
			region: {
				Generation: generation,
				Valid:      true,
			},
		},
	}
}

func regionRefStateFromPackedStoreDependency(store *Symbol, storeType *PackedEnumStoreType) regionRefState {
	if store == nil || storeType == nil {
		return regionRefState{}
	}
	return regionRefState{
		StoreDeps: map[*Symbol]packedStoreDependencyState{
			store: {Type: storeType},
		},
	}
}

func regionRefStateFromParamDependency(index int) regionRefState {
	if index < 0 {
		return regionRefState{}
	}
	return regionRefState{
		ParamDeps: map[int]bool{
			index: true,
		},
	}
}

func mergeRegionRefStates(states ...regionRefState) (regionRefState, bool) {
	merged := regionRefState{}
	for _, state := range states {
		if !hasRegionProvenance(state) {
			continue
		}
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
		if len(state.ParamDeps) != 0 {
			if merged.ParamDeps == nil {
				merged.ParamDeps = map[int]bool{}
			}
			for index, dep := range state.ParamDeps {
				merged.ParamDeps[index] = dep
			}
		}
		if len(state.Fields) != 0 {
			if merged.Fields == nil {
				merged.Fields = map[string]regionRefState{}
			}
			for name, fieldState := range state.Fields {
				if existing, ok := merged.Fields[name]; ok {
					if next, ok := mergeRegionRefStates(existing, fieldState); ok {
						merged.Fields[name] = next
					} else {
						delete(merged.Fields, name)
					}
				} else {
					merged.Fields[name] = cloneRegionRefState(fieldState)
				}
			}
		}
	}
	if !hasRegionProvenance(merged) {
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
	summary := cloneRegionRefState(state)
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
		return summary, true
	}
	merged, ok := mergeRegionRefStates(indexStates...)
	if !ok {
		summary.Fields = nil
		return summary, true
	}
	summary.Fields = map[string]regionRefState{
		regionAnyIndexFieldKey(): merged,
	}
	return summary, true
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
	if len(state.ParamDeps) != 0 {
		out.ParamDeps = make(map[int]bool, len(state.ParamDeps))
		for index, dep := range state.ParamDeps {
			out.ParamDeps[index] = dep
		}
	}
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
	return out, true
}

func (a *Analyzer) instantiateReturnProvenance(state regionRefState, args []ast.Expr) (regionRefState, bool) {
	if !hasRegionProvenance(state) {
		return regionRefState{}, false
	}
	instantiated := regionRefState{}
	argStates := make([]regionRefState, 0, len(state.ParamDeps))
	for index := range state.ParamDeps {
		if index < 0 || index >= len(args) {
			continue
		}
		argState, ok := a.regionRefStateForExpr(args[index])
		if !ok {
			continue
		}
		argStates = append(argStates, argState)
	}
	if mergedArgs, ok := mergeRegionRefStates(argStates...); ok {
		instantiated = mergedArgs
	}
	if len(state.Fields) != 0 {
		for name, fieldState := range state.Fields {
			instField, ok := a.instantiateReturnProvenance(fieldState, args)
			if !ok {
				continue
			}
			if instantiated.Fields == nil {
				instantiated.Fields = map[string]regionRefState{}
			}
			instantiated.Fields[name] = instField
		}
	}
	if !hasRegionProvenance(instantiated) {
		return regionRefState{}, false
	}
	return instantiated, true
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
	if region != nil {
		if dep, ok := state.Deps[region]; ok && dep.Valid {
			if predicate == nil || predicate(dep) {
				if state.Deps == nil {
					state.Deps = map[*Symbol]regionDependencyState{}
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
			if state.Fields == nil {
				state.Fields = map[string]regionRefState{}
			}
			state.Fields[name] = nextField
			changed = true
		}
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
	summary := cloneBorrowedOwnerRefState(state)
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
	if len(dst.ParamDeps) != 0 && len(src.ParamDeps) != 0 {
		for index := range dst.ParamDeps {
			if !src.ParamDeps[index] {
				continue
			}
			if merged.ParamDeps == nil {
				merged.ParamDeps = map[int]bool{}
			}
			merged.ParamDeps[index] = true
		}
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

func (a *Analyzer) mergeSpecializedValueTypes(dst Type, src Type) (Type, bool) {
	if dst == nil || src == nil || !SameType(dst, src) {
		return nil, false
	}
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
			mergedFieldType, ok := a.mergeSpecializedValueTypes(field.Type, srcField.Type)
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
		mergedBase, ok := a.mergeSpecializedValueTypes(tt.Base, srcInstance.Base)
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
		mergedElem, ok := a.mergeSpecializedValueTypes(tt.Elem, srcRef.Elem)
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
		mergedElem, ok := a.mergeSpecializedValueTypes(tt.Elem, srcArray.Elem)
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
		mergedElem, ok := a.mergeSpecializedValueTypes(tt.Elem, srcArray.Elem)
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
		mergedValue, ok := a.mergeSpecializedValueTypes(tt.Value, srcOpt.Value)
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
		mergedElem, ok := a.mergeSpecializedValueTypes(tt.Elem, srcView.Elem)
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
		mergedElem, ok := a.mergeSpecializedValueTypes(tt.Elem, srcView.Elem)
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
		mergedKey, ok := a.mergeSpecializedValueTypes(tt.Key, srcDict.Key)
		if !ok {
			return nil, false
		}
		mergedValue, ok := a.mergeSpecializedValueTypes(tt.Value, srcDict.Value)
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
		}
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
		delete(a.currentValueBindings, sym)
		return
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
	if specializedType, ok := a.specializeCallbackCarryingType(sym.Type, valueType); ok {
		a.currentSpecializedValueTypes[sym] = a.cloneTrackedValueType(specializedType)
		return
	}
	delete(a.currentSpecializedValueTypes, sym)
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
	delete(a.currentSpecializedValueTypes, root)
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

func (a *Analyzer) recordResolvedRegionRefBinding(sym *Symbol, state regionRefState) {
	if a.currentRegionRefs == nil || sym == nil {
		return
	}
	if !hasRegionProvenance(state) {
		delete(a.currentRegionRefs, sym)
		return
	}
	a.currentRegionRefs[sym] = cloneRegionRefState(state)
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
	if a.currentRegionRefs == nil || from == nil || to == nil || nextType == nil {
		return
	}
	for sym, state := range a.currentRegionRefs {
		nextState, changed := remapPackedStoreDependencyInState(state, from, to, nextType)
		if changed {
			a.currentRegionRefs[sym] = nextState
		}
	}
}

func remapPackedStoreDependencyInState(state regionRefState, from *Symbol, to *Symbol, nextType *PackedEnumStoreType) (regionRefState, bool) {
	changed := false
	if dep, ok := state.StoreDeps[from]; ok {
		if state.StoreDeps == nil {
			state.StoreDeps = map[*Symbol]packedStoreDependencyState{}
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
		if state.Fields == nil {
			state.Fields = map[string]regionRefState{}
		}
		state.Fields[name] = nextField
		changed = true
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
	a.applyConditionRefinements(scope, cond, truthy)
	return scope
}

func (a *Analyzer) applyConditionRefinements(scope *Scope, expr ast.Expr, truthy bool) {
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			if truthy {
				a.applyConditionRefinements(scope, n.Left, true)
				a.applyConditionRefinements(scope, n.Right, true)
			}
		case lexer.TOKEN_OR:
			if !truthy {
				a.applyConditionRefinements(scope, n.Left, false)
				a.applyConditionRefinements(scope, n.Right, false)
			}
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
			targetExpr, nonNull, ok := refinedExprNullState(n, truthy)
			if ok {
				a.shadowRefinedExpr(scope, targetExpr, nonNull)
			}
		}
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			a.applyConditionRefinements(scope, n.Operand, !truthy)
		}
	case *ast.ParenExpr:
		a.applyConditionRefinements(scope, n.Inner, truthy)
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

func (a *Analyzer) shadowRefinedExpr(scope *Scope, expr ast.Expr, nonNull bool) {
	if scope == nil {
		return
	}
	key, ok := exprRefinementKey(expr)
	if !ok {
		return
	}
	baseType := a.analyzeExprInScope(expr, scope)
	refined, ok := refinedNullComparableType(baseType, nonNull)
	if !ok {
		return
	}
	scope.Refinements[key] = refined
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
	if !ok {
		return nil, false
	}
	return a.currentScope.LookupRefinement(key)
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
