package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

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
		if p.EnumName != errorSetType.Name {
			a.errorf(p.Pos(), "match arm expects error set %q, got %q", errorSetType.Name, p.EnumName)
			return false
		}
		if !errorSetType.HasQualifiedTag(errorSetType.Name, p.Variant) {
			a.errorf(p.Pos(), "error set %q has no tag %q", errorSetType.Name, p.Variant)
			return false
		}
		if len(p.Args) != 0 {
			a.errorf(p.Pos(), "match arm %q expects 0 payload patterns, got %d", errorSetType.Name+"."+p.Variant, len(p.Args))
			return false
		}
		if covered != nil {
			covered[QualifyErrorTag(errorSetType.Name, p.Variant)] = true
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
		patternCategory, variant, ok := a.resolveTreeMatchPatternCategory(treeType, p)
		if !ok {
			return false
		}
		qualified := patternCategory.Name + "." + variant.Name
		if covered != nil {
			covered[qualified] = true
		}
		a.bindRefinedExprType(scope, valueExpr, patternCategory.VariantViewType(variant))
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

func (a *Analyzer) resolveTreeMatchPatternCategory(expected *TreeCategoryType, pattern *ast.MatchVariantPattern) (*TreeCategoryType, *EnumVariant, bool) {
	if expected == nil || pattern == nil {
		return nil, nil, false
	}
	category := expected
	if pattern.EnumName != expected.Name {
		base, _, ok := a.lookupVisibleType(pattern.EnumName)
		if !ok {
			a.errorf(pattern.Pos(), "match arm expects tree category %q or nested tree category, got %q", expected.Name, pattern.EnumName)
			return nil, nil, false
		}
		resolvedCategory, ok := StripAggregateStateType(base).(*TreeCategoryType)
		if !ok || resolvedCategory == nil || !treeCategoryDescendsFrom(resolvedCategory, expected) {
			a.errorf(pattern.Pos(), "match arm expects tree category %q or nested tree category, got %q", expected.Name, pattern.EnumName)
			return nil, nil, false
		}
		category = resolvedCategory
	}
	variant, ok := category.Variant(pattern.Variant)
	if !ok {
		a.errorf(pattern.Pos(), "tree category %q has no variant %q", category.Name, pattern.Variant)
		return nil, nil, false
	}
	return category, variant, true
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
		merged := MergeTypes(resultType, armType)
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
		merged := MergeTypes(resultType, armType)
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
			a.errorf(p.Pos(), "match arm expects a string value, got %s", valueType)
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

func (a *Analyzer) analyzeTopLevelTupleMatchPattern(pattern ast.MatchPattern, valueType Type, valueExpr ast.Expr, scope *Scope, index int, armCount int) bool {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		if index != armCount-1 {
			a.errorf(p.Pos(), "wildcard match arm must be the final arm")
		}
		return true
	case *ast.MatchTuplePattern:
		fields, ok := a.resolveMatchTuplePattern(p, valueType)
		if !ok {
			return false
		}
		limit := len(p.Elems)
		if len(fields) < limit {
			limit = len(fields)
		}
		for i := 0; i < limit; i++ {
			fieldExpr := &ast.FieldExpr{Position: p.Elems[i].Pos(), Object: valueExpr, Field: fields[i].Name}
			a.analyzeNestedMatchPattern(p.Elems[i], fields[i].Type, fieldExpr, scope)
		}
		return false
	case *ast.MatchBindPattern:
		a.errorf(p.Pos(), "top-level tuple match arm must use a tuple pattern or _")
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported top-level tuple match pattern %T", pattern)
		return false
	}
}

func (a *Analyzer) reportNonExhaustiveStructMatchExpr(pos lexer.Pos, hasWildcard bool) {
	if hasWildcard {
		return
	}
	a.errorf(pos, "non-exhaustive struct match expression; add a final _ arm")
}

func (a *Analyzer) reportNonExhaustiveTupleMatchExpr(pos lexer.Pos, hasWildcard bool) {
	if hasWildcard {
		return
	}
	a.errorf(pos, "non-exhaustive tuple match expression; add a final _ arm")
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
