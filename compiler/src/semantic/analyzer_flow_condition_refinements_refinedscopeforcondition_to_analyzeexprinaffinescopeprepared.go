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
			if n.Op == lexer.TOKEN_EQEQ {
				a.gatherNumericRangeRefinement(scope, n, truthy)
			}
		case lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_GT, lexer.TOKEN_GTEQ:
			// docs/85 1d-2: record an integer range fact for an immutable variable so a refinement
			// like `a is Nat` discharges statically inside `if a > 5:`.
			a.gatherNumericRangeRefinement(scope, n, truthy)
		case lexer.TOKEN_IS:
			// docs/85: `if x is Law:` narrows x by the law inside the truthy branch. For a law in the
			// decidable fragment (`self OP const` conjunctions) over an immutable int, record the
			// implied integer range so a later `x is OtherLaw` / refinement binding discharges.
			a.gatherLawIsRangeRefinement(scope, n, truthy)
			// docs/85 (mutable refinement flow): record a predicate fact for `if x is BareLaw:`,
			// usable to discharge a later obligation on x and invalidated at any mutation of x.
			a.gatherLawIsPredFact(scope, n, truthy)
			// docs/85 (refinement flow-narrowing): inject the law's predicate body, instantiated for the
			// subject, as an assumable SMT flow fact — so a raw comparison/arithmetic obligation inside the
			// branch (`assert x >= 0` under `if x is NonNeg:`) discharges. The negation is recorded in the
			// else branch only when the law is decidable/total. Skipped during the throwaway scope pass
			// (persistTracked=false) so the fact lives in the real branch scope exactly once.
			if persistTracked {
				a.gatherLawIsSMTFact(scope, n, truthy)
			}
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
		a.applyMutableRefinedOutParamConditionRefinements(scope, n, truthy)
	case *ast.StructLitExpr:
		if call, ok := a.structLiteralAsGenericFunctionCall(n); ok {
			a.applyMutableRefinedOutParamConditionRefinements(scope, call, truthy)
		} else if call, ok := a.structLiteralAsPascalCaseFunctionCall(n); ok {
			a.applyMutableRefinedOutParamConditionRefinements(scope, call, truthy)
		}
	case *ast.ParenExpr:
		a.applyConditionRefinementsInternal(scope, n.Inner, truthy, persistTracked)
	}
}

func (a *Analyzer) applyMutableRefinedOutParamConditionRefinements(scope *Scope, call *ast.CallExpr, truthy bool) {
	if a == nil || scope == nil || call == nil || !truthy {
		return
	}
	ft := a.directCallFuncType(call)
	if ft == nil {
		ft, _ = a.exprType(call.Func).(*FuncType)
	}
	if ft == nil || len(ft.ParamRefinementRanges) == 0 {
		return
	}
	args, ok := a.resolveFunctionCallArgs(call, ft)
	if !ok {
		args = call.Args
	}
	limit := len(args)
	if len(ft.Params) < limit {
		limit = len(ft.Params)
	}
	for i := 0; i < limit; i++ {
		rt, ok := ft.Params[i].(*RefType)
		if !ok || rt == nil || !rt.Mutable {
			continue
		}
		fact, ok := ft.ParamRefinementRanges[i]
		if !ok {
			continue
		}
		a.seedMutableRefParamRefinementRangeFactInScope(scope, args[i], fact)
	}
}

func (a *Analyzer) directCallFuncType(call *ast.CallExpr) *FuncType {
	if a == nil || call == nil || a.currentScope == nil {
		return nil
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil {
		return nil
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return nil
	}
	if ft, ok := sym.Type.(*FuncType); ok {
		return ft
	}
	if ft := a.functionTypes[sym.Name]; ft != nil {
		return ft
	}
	return nil
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
	leftType := a.exprType(expr.Left)
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
	leftType := a.exprType(expr.Left)
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
			scope.setRefinement(root.Name, updated)
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
