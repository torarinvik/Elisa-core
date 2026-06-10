package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

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
			targetExpr, refinedType, ok := a.refinedExprNamedStateType(n, truthy)
			if ok {
				a.bindRefinedExprType(scope, targetExpr, refinedType)
			}
		}
	case *ast.OptionalBindExpr:
		a.shadowRefinedExpr(scope, n.Value, truthy)
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
	case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt, *ast.PanicStmt, *ast.StaticErrorStmt:
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
