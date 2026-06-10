package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) analyzeFoldExpr(expr *ast.FoldExpr) Type {
	return a.analyzeFoldExprWithExpected(expr, nil)
}

func (a *Analyzer) analyzeFoldExprWithExpected(expr *ast.FoldExpr, expected Type) Type {
	if expr != nil && expr.Keyword == "rewrite" {
		if _, ok := sequenceRewriteTargetTypeExpr(expr.Root); ok {
			return a.analyzeSequenceRewriteExprWithExpected(expr, expected)
		}
	}
	a.analyzeExpr(expr.Value)
	for _, arm := range expr.Arms {
		a.analyzeMatchExprArmBody(arm.Body, NewScope(a.currentScope))
	}
	a.errorf(expr.Pos(), "fold/visit expressions require a tree type (docs/81: tree construct has been removed)")
	return invalidType
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
			a.errorf(arm.Position, "match expression arms are incompatible: %s and %s", resultType, armType)
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
