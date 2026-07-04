package semantic

import "elisacore/src/ast"

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
		// Hierarchy scrutinee (docs/77): an arm may name the scrutinee enum itself or any refinement
		// of it (`BinaryExpression.Add` when matching an `Expression`). For a plain enum this collapses
		// to the original same-name check.
		var owner *EnumType
		var variant *EnumVariant
		if enumIsHierarchical(enumType) {
			var ok bool
			owner, variant, ok = a.resolveEnumMatchPatternCategory(enumType, p)
			if !ok {
				return false
			}
		} else {
			p.EnumName = a.canonicalizeMatchEnumName(p.EnumName, enumType.Name)
			if p.EnumName != enumType.Name {
				a.errorf(p.Pos(), "match arm expects enum %q, got %q", enumType.Name, p.EnumName)
				return false
			}
			v, ok := enumType.Variant(p.Variant)
			if !ok {
				a.errorf(p.Pos(), "enum %q has no variant %q", enumType.Name, p.Variant)
				return false
			}
			owner, variant = enumType, v
		}
		qualified := owner.Name + "." + variant.Name
		if covered != nil {
			// Bare key preserves the flat-enum convention; qualified key drives hierarchy
			// exhaustiveness (leaves from different refinements never collide).
			covered[variant.Name] = true
			covered[qualified] = true
		}
		if owner.Packed {
			a.bindMatchedPackedVariantView(valueExpr, &PackedVariantViewType{Enum: owner, Variant: variant})
		}
		// docs/77: in an arm matching a refinement, narrow the scrutinee to that sub-category for the
		// arm body, so it can be passed where the narrower type is required (mirrors tree narrowing).
		if owner != enumType {
			a.bindRefinedExprType(scope, valueExpr, owner)
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
		// docs/77 §2 category arm: `Statement:` matches the sub-category's whole leaf range;
		// `Statement s:` additionally binds the scrutinee at the narrowed type. Gated to
		// hierarchical enums, so flat-enum arms keep the variants-or-_ rule.
		if category, ok := a.resolveEnumCategoryArm(enumType, p.Name); ok {
			if covered != nil {
				for _, leaf := range enumDescendantLeaves(category) {
					covered[leaf.Variant.Name] = true
					covered[leaf.Qualified] = true
				}
			}
			if p.Binder != "" {
				sym := &Symbol{Name: p.Binder, Kind: SymbolLocal, Type: category, Node: p, Mutable: false}
				a.defineLocal(sym, p.Pos())
				a.recordValueBinding(sym, valueExpr)
			}
			if category != enumType {
				a.bindRefinedExprType(scope, valueExpr, category)
			}
			return false
		}
		if p.Binder != "" {
			a.errorf(p.Pos(), "match arm binder %q requires a sub-category of %q, but %q does not name one", p.Binder, enumType.Name, p.Name)
			return false
		}
		a.errorf(p.Pos(), "top-level match arm must use %q variants or _", enumType.Name)
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported match pattern %T", pattern)
		return false
	}
}

// resolveEnumCategoryArm resolves a bare match-arm name to a sub-category of the hierarchy
// scrutinee (docs/77 §2). Silent: a name that is not a related enum category is NOT a category
// arm (it stays a plain bind / keeps the original diagnostics). Only downward narrowing —
// including the scrutinee itself (a catch-all category arm) — qualifies.
func (a *Analyzer) resolveEnumCategoryArm(enumType *EnumType, name string) (*EnumType, bool) {
	if !enumIsHierarchical(enumType) || name == "" {
		return nil, false
	}
	base, _, ok := a.lookupVisibleType(name)
	if !ok {
		return nil, false
	}
	category, ok := StripAggregateStateType(base).(*EnumType)
	if !ok || category == nil || !enumDescendsFrom(category, enumType) {
		return nil, false
	}
	return category, true
}
func (a *Analyzer) analyzeTopLevelConstEnumMatchPattern(pattern ast.MatchPattern, constEnumType *ConstEnumType, scope *Scope, index int, armCount int, covered map[string]bool) bool {
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
		p.EnumName = a.canonicalizeMatchEnumName(p.EnumName, constEnumType.Name)
		if p.EnumName != constEnumType.Name {
			a.errorf(p.Pos(), "match arm expects const enum %q, got %q", constEnumType.Name, p.EnumName)
			return false
		}
		if _, ok := constEnumType.Member(p.Variant); !ok {
			a.errorf(p.Pos(), "const enum %q has no member %q", constEnumType.Name, p.Variant)
			return false
		}
		if len(p.Args) != 0 {
			a.errorf(p.Pos(), "match arm %q expects 0 payload patterns, got %d", constEnumType.Name+"."+p.Variant, len(p.Args))
			return false
		}
		if covered != nil {
			covered[p.Variant] = true
		}
		return false
	case *ast.MatchBindPattern:
		a.errorf(p.Pos(), "top-level match arm must use %q members or _", constEnumType.Name)
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported match pattern %T", pattern)
		return false
	}
}
func (a *Analyzer) analyzeTopLevelErrorSetMatchPattern(pattern ast.MatchPattern, errorSetType *ErrorSetType, scope *Scope, index int, armCount int, covered map[string]bool) bool {
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
		p.EnumName = a.canonicalizeMatchEnumName(p.EnumName, errorSetType.Name)
		if p.EnumName != errorSetType.Name {
			a.errorf(p.Pos(), "match arm expects error set %q, got %q", errorSetType.Name, p.EnumName)
			return false
		}
		if !errorSetType.HasQualifiedTag(errorSetType.Name, p.Variant) {
			a.errorf(p.Pos(), "error set %q has no tag %q", errorSetType.Name, p.Variant)
			return false
		}
		qualified := QualifyErrorTag(errorSetType.Name, p.Variant)
		payloadTypes := errorSetType.PayloadForTag(qualified)
		if len(p.Args) != len(payloadTypes) {
			a.errorf(p.Pos(), "match arm %q expects %d payload patterns, got %d", errorSetType.Name+"."+p.Variant, len(payloadTypes), len(p.Args))
			return false
		}
		if covered != nil {
			covered[qualified] = true
		}
		for i := range p.Args {
			arg := &p.Args[i]
			if arg.Name != "" {
				a.errorf(arg.Position, "error-set payload patterns do not support named fields yet")
				continue
			}
			a.analyzeNestedMatchPattern(arg.Pattern, payloadTypes[i], nil, scope)
		}
		return false
	case *ast.MatchBindPattern:
		a.errorf(p.Pos(), "top-level match arm must use %q tags or _", errorSetType.Name)
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported match pattern %T", pattern)
		return false
	}
}
func (a *Analyzer) analyzeConstEnumMatchStmt(stmt *ast.MatchStmt, valueType Type, constEnumType *ConstEnumType) {
	if stmt.Store != nil {
		a.errorf(stmt.Store.Pos(), "const enum match over %q does not take an in-store clause", constEnumType.Name)
	}
	_ = valueType
	baselineCloned := false
	var baselineAffine map[affineValueKey]affineValueState
	var baselineBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var baselineFunctionValues map[*Symbol]*FuncType
	var baselineSpecializedValueTypes map[*Symbol]Type
	var baselineAliasCarriers map[string][]string
	var baselineAliasCarrierFieldOverrides map[string]map[string][]string
	cloneBaseline := func() {
		if baselineCloned {
			return
		}
		baselineAffine = a.cloneAffineValueStates()
		baselineBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
		baselineFunctionValues = a.cloneFunctionValueBindings()
		baselineSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
		baselineAliasCarriers = a.cloneAliasCarriers()
		baselineAliasCarrierFieldOverrides = a.cloneAliasCarrierFieldOverrides()
		baselineCloned = true
	}
	var mergedAffine map[affineValueKey]affineValueState
	var mergedBorrowedOwnerRefs map[*Symbol]borrowedOwnerRefState
	var mergedFunctionValues map[*Symbol]*FuncType
	var mergedSpecializedValueTypes map[*Symbol]Type
	var mergedAliasCarriers map[string][]string
	var mergedAliasCarrierFieldOverrides map[string]map[string][]string
	hasFallthrough := false
	priorPatterns := make([]ast.MatchPattern, 0, len(stmt.Arms))
	covered := map[string]bool{}
	hasWildcard := false
	for i, arm := range stmt.Arms {
		if a.matchPatternShadowedByPrevious(arm.Pattern, constEnumType, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		if a.analyzeTopLevelConstEnumMatchPattern(arm.Pattern, constEnumType, scope, i, len(stmt.Arms), covered) {
			hasWildcard = true
		}
		armSnapshot := a.analyzeBlockWithAffineClone(arm.Body, scope)
		if !blockDefinitelyExits(arm.Body) {
			if !hasFallthrough {
				mergedAffine = armSnapshot.Affine
				mergedBorrowedOwnerRefs = armSnapshot.BorrowedOwnerRefs
				mergedFunctionValues = armSnapshot.FunctionValues
				mergedSpecializedValueTypes = armSnapshot.SpecializedValueTypes
				mergedAliasCarriers = armSnapshot.AliasCarriers
				mergedAliasCarrierFieldOverrides = armSnapshot.AliasCarrierFieldOverrides
				hasFallthrough = true
			} else {
				mergedAffine = mergeAffineValueStates(mergedAffine, armSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, armSnapshot.BorrowedOwnerRefs)
				mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, armSnapshot.FunctionValues)
				mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, armSnapshot.SpecializedValueTypes)
				mergedAliasCarriers, mergedAliasCarrierFieldOverrides = mergeAliasCarrierSnapshot(mergedAliasCarriers, mergedAliasCarrierFieldOverrides, armSnapshot)
			}
		}
		priorPatterns = append(priorPatterns, arm.Pattern)
	}
	if !a.matchCoversAllVariants(constEnumType, covered, hasWildcard) {
		a.reportNonExhaustiveMatch(stmt.Pos(), constEnumType, covered, hasWildcard)
		cloneBaseline()
		if !hasFallthrough {
			mergedAffine = baselineAffine
			mergedBorrowedOwnerRefs = baselineBorrowedOwnerRefs
			mergedFunctionValues = baselineFunctionValues
			mergedSpecializedValueTypes = baselineSpecializedValueTypes
			mergedAliasCarriers = baselineAliasCarriers
			mergedAliasCarrierFieldOverrides = baselineAliasCarrierFieldOverrides
		} else {
			mergedAffine = mergeAffineValueStates(mergedAffine, baselineAffine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, baselineBorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, baselineFunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, baselineSpecializedValueTypes)
			mergedAliasCarriers, mergedAliasCarrierFieldOverrides = mergeAliasCarrierBaseline(mergedAliasCarriers, mergedAliasCarrierFieldOverrides, baselineAliasCarriers, baselineAliasCarrierFieldOverrides)
		}
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	a.currentAliasCarriers = mergedAliasCarriers
	a.currentAliasCarrierFieldOverrides = mergedAliasCarrierFieldOverrides
}
func (a *Analyzer) analyzeErrorSetMatchStmt(stmt *ast.MatchStmt, valueType Type, errorSetType *ErrorSetType) {
	if stmt.Store != nil {
		a.errorf(stmt.Store.Pos(), "error-set match over %q does not take an in-store clause", errorSetType.Name)
	}
	_ = valueType
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
		if a.matchPatternShadowedByPrevious(arm.Pattern, errorSetType, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		if a.analyzeTopLevelErrorSetMatchPattern(arm.Pattern, errorSetType, scope, i, len(stmt.Arms), covered) {
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
	if !a.matchCoversAllVariants(errorSetType, covered, hasWildcard) {
		a.reportNonExhaustiveMatch(stmt.Pos(), errorSetType, covered, hasWildcard)
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
func (a *Analyzer) analyzeConstEnumMatchExpr(expr *ast.MatchExpr, valueType Type, constEnumType *ConstEnumType) Type {
	if expr.Store != nil {
		a.errorf(expr.Store.Pos(), "const enum match over %q does not take an in-store clause", constEnumType.Name)
	}
	_ = valueType
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
		if a.matchPatternShadowedByPrevious(arm.Pattern, constEnumType, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		if a.analyzeTopLevelConstEnumMatchPattern(arm.Pattern, constEnumType, scope, i, len(expr.Arms), covered) {
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
		merged := a.mergeMatchExprArmTypes(resultType, armType, expr.Arms, i)
		if IsInvalidType(merged) {
			a.errorf(arm.Position, "match expression arms are incompatible: %s and %s", resultType, armType)
			resultType = invalidType
			priorPatterns = append(priorPatterns, arm.Pattern)
			continue
		}
		resultType = merged
		priorPatterns = append(priorPatterns, arm.Pattern)
	}
	if !a.matchCoversAllVariants(constEnumType, covered, hasWildcard) {
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
	a.reportNonExhaustiveMatch(expr.Pos(), constEnumType, covered, hasWildcard)
	if resultType == nil {
		return neverType
	}
	return resultType
}
func (a *Analyzer) analyzeErrorSetMatchExpr(expr *ast.MatchExpr, valueType Type, errorSetType *ErrorSetType) Type {
	if expr.Store != nil {
		a.errorf(expr.Store.Pos(), "error-set match over %q does not take an in-store clause", errorSetType.Name)
	}
	_ = valueType
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
		if a.matchPatternShadowedByPrevious(arm.Pattern, errorSetType, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		if a.analyzeTopLevelErrorSetMatchPattern(arm.Pattern, errorSetType, scope, i, len(expr.Arms), covered) {
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
		merged := a.mergeMatchExprArmTypes(resultType, armType, expr.Arms, i)
		if IsInvalidType(merged) {
			a.errorf(arm.Position, "match expression arms are incompatible: %s and %s", resultType, armType)
			resultType = invalidType
			priorPatterns = append(priorPatterns, arm.Pattern)
			continue
		}
		resultType = merged
		priorPatterns = append(priorPatterns, arm.Pattern)
	}
	if !a.matchCoversAllVariants(errorSetType, covered, hasWildcard) {
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
	a.reportNonExhaustiveMatch(expr.Pos(), errorSetType, covered, hasWildcard)
	if resultType == nil {
		return neverType
	}
	return resultType
}
