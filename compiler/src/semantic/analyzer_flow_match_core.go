package semantic

import (
	"sort"
	"strings"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) analyzeMatchStmt(stmt *ast.MatchStmt) {
	if stmt.DeprecatedIfStorePatternBinder {
		a.deprecatedf(stmt.Pos(), "`if value in store as Pattern` is deprecated; use `match value in store:` with pattern arms instead")
	}
	valueType := a.analyzeExpr(stmt.Value)
	enumType, _, ok := resolveMatchableEnumType(valueType)
	if ok {
		a.analyzeEnumMatchStmt(stmt, valueType, enumType)
		return
	}
	constEnumType, ok := resolveMatchableConstEnumType(valueType)
	if ok {
		a.analyzeConstEnumMatchStmt(stmt, valueType, constEnumType)
		return
	}
	errorSetType, ok := resolveMatchableErrorSetType(valueType)
	if ok {
		a.analyzeErrorSetMatchStmt(stmt, valueType, errorSetType)
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
	if _, ok := StripAggregateStateType(valueType).(*TupleType); ok {
		a.analyzeTupleMatchStmt(stmt, valueType)
		return
	}
	if _, ok := a.resolvedStructFields(valueType); ok {
		a.analyzeStructMatchStmt(stmt, valueType)
		return
	}
	a.errorf(stmt.Pos(), "match requires an enum, const enum, error set, tree-category, string, tuple, or struct value, got %s", valueType)
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
	constEnumType, ok := resolveMatchableConstEnumType(valueType)
	if ok {
		return a.analyzeConstEnumMatchExpr(expr, valueType, constEnumType)
	}
	errorSetType, ok := resolveMatchableErrorSetType(valueType)
	if ok {
		return a.analyzeErrorSetMatchExpr(expr, valueType, errorSetType)
	}
	treeType, _, ok := resolveMatchableTreeCategoryType(valueType)
	if ok {
		return a.analyzeTreeMatchExpr(expr, treeType)
	}
	if isStringMatchableType(valueType) {
		return a.analyzeStringMatchExpr(expr, valueType)
	}
	if _, ok := StripAggregateStateType(valueType).(*TupleType); ok {
		return a.analyzeTupleMatchExpr(expr, valueType)
	}
	if _, ok := a.resolvedStructFields(valueType); ok {
		return a.analyzeStructMatchExpr(expr, valueType)
	}
	a.errorf(expr.Pos(), "match requires an enum, const enum, error set, tree-category, string, tuple, or struct value, got %s", valueType)
	for _, arm := range expr.Arms {
		a.analyzeMatchExprArmBody(arm.Body, NewScope(a.currentScope))
	}
	return invalidType

}

func (a *Analyzer) analyzeCatchExpr(expr *ast.CatchExpr) Type {
	valueType := a.analyzeExpr(expr.Value)
	unionType, ok := valueType.(*ErrorUnionType)
	if !ok {
		a.errorf(expr.Pos(), "catch requires an error union, got %s", valueType)
		successScope := NewScope(a.currentScope)
		a.defineLocalInScope(successScope, &Symbol{Name: expr.Success.Name, Kind: SymbolLocal, Type: invalidType, Mutable: false}, expr.Success.Position)
		a.analyzeMatchExprArmBody(expr.Success.Body, successScope)
		for _, arm := range expr.Arms {
			a.analyzeMatchExprArmBody(arm.Body, NewScope(a.currentScope))
		}
		return invalidType
	}
	if expr.Success.Name != "value" {
		a.errorf(expr.Success.Position, "catch success arm must be `value:`")
	}
	if len(expr.Arms) == 0 {
		a.errorf(expr.Pos(), "catch requires at least one error arm")
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
	mergeArm := func(pos lexer.Pos, body []ast.Stmt, scope *Scope) {
		armType, armSnapshot := a.analyzeMatchExprArmBodyWithAffineSnapshot(body, scope)
		if !blockDefinitelyExits(body) {
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
			return
		}
		merged := MergeTypes(resultType, armType)
		if IsInvalidType(merged) {
			a.errorf(pos, "catch expression arms are incompatible: %s and %s", resultType, armType)
			resultType = invalidType
			return
		}
		resultType = merged
	}
	successScope := NewScope(a.currentScope)
	valueSym := &Symbol{Name: expr.Success.Name, Kind: SymbolLocal, Type: unionType.Value, Mutable: false}
	a.defineLocalInScope(successScope, valueSym, expr.Success.Position)
	savedValueBindings := a.currentValueBindings
	a.currentValueBindings = a.cloneValueBindings()
	a.recordValueBinding(valueSym, expr.Value)
	mergeArm(expr.Success.Position, expr.Success.Body, successScope)
	a.currentValueBindings = savedValueBindings
	covered := map[string]bool{}
	for _, arm := range expr.Arms {
		matchedTag, ok := MatchErrorTag(unionType.Errors, arm.Name)
		if !ok {
			a.errorf(arm.Position, "catch arm %q does not match %s", arm.Name, ErrorSetDiagnosticName(unionType.Errors))
			mergeArm(arm.Position, arm.Body, NewScope(a.currentScope))
			continue
		}
		if covered[matchedTag] {
			a.errorf(arm.Position, "catch arm %q is unreachable because an earlier arm already matches it", arm.Name)
		}
		covered[matchedTag] = true
		mergeArm(arm.Position, arm.Body, NewScope(a.currentScope))
	}
	if len(covered) != len(unionType.Errors.Tags) {
		missing := make([]string, 0, len(unionType.Errors.Tags))
		for _, tag := range unionType.Errors.Tags {
			if !covered[tag] {
				missing = append(missing, ErrorTagDiagnosticName(tag))
			}
		}
		sort.Strings(missing)
		a.errorf(expr.Pos(), "non-exhaustive catch over %s; missing %s", ErrorSetDiagnosticName(unionType.Errors), strings.Join(missing, ", "))
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
	if resultType == nil {
		return neverType
	}
	return resultType
}
