package semantic

import (
	"sort"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
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
	if optionalType, ok := valueType.(*OptionalType); ok {
		a.analyzeOptionalMatchStmt(stmt, optionalType)
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
	if _, ok := SequenceMatchElementType(valueType); ok {
		a.analyzeSequenceMatchStmt(stmt, valueType)
		return
	}
	if _, ok := a.resolvedStructFields(valueType); ok {
		a.analyzeStructMatchStmt(stmt, valueType)
		return
	}
	a.errorf(stmt.Pos(), "match requires an enum, const enum, error set, optional, tree-category, string, tuple, sequence, or struct value, got %s", valueType)
	for _, arm := range stmt.Arms {
		a.analyzeBlockWithRegionClone(arm.Body, NewScope(a.currentScope))
	}
}

func (a *Analyzer) analyzeExpectPatternStmt(stmt *ast.ExpectPatternStmt) {
	if stmt == nil {
		return
	}
	arms := make([]ast.MatchArm, 0, len(stmt.Patterns)+1)
	for _, pattern := range stmt.Patterns {
		arms = append(arms, ast.MatchArm{Position: pattern.Pos(), Pattern: pattern, Body: []ast.Stmt{&ast.PassStmt{Position: pattern.Pos()}}})
	}
	arms = append(arms, ast.MatchArm{
		Position: stmt.Position,
		Pattern:  &ast.MatchWildcardPattern{Position: stmt.Position},
		Body:     []ast.Stmt{&ast.PanicStmt{Position: stmt.Position, Message: &ast.StringLit{Position: stmt.Position, Value: "expect pattern failed"}}},
	})
	a.analyzeMatchStmt(&ast.MatchStmt{Position: stmt.Position, Value: stmt.Value, Arms: arms})
	if len(stmt.Patterns) == 0 {
		return
	}
	valueType := a.exprTypes[stmt.Value]
	if valueType == nil {
		valueType = a.analyzeExpr(stmt.Value)
	}
	if len(stmt.Patterns) != 1 {
		for _, pattern := range stmt.Patterns {
			bindings := map[string]Type{}
			a.collectConditionStructPatternBindingTypes(pattern, valueType, bindings)
			if len(bindings) != 0 {
				a.errorf(pattern.Pos(), "blockless expect with multiple patterns cannot bind names")
				return
			}
		}
		return
	}
	a.bindExpectPatternLocals(a.currentScope, stmt.Patterns[0], valueType, stmt.Value)
}

func (a *Analyzer) bindExpectPatternLocal(scope *Scope, name string, typ Type, node ast.Node, pos lexer.Pos, valueExpr ast.Expr) {
	if a == nil || scope == nil || name == "" || name == "_" || typ == nil {
		return
	}
	if existing, ok := scope.Symbols[name]; ok {
		if !SameType(existing.Type, typ) {
			existing.Type = typ
		}
		if valueExpr != nil {
			a.recordValueBinding(existing, valueExpr)
			a.recordBorrowedOwnerRefBinding(existing, valueExpr)
			a.recordFunctionValueBinding(existing, valueExpr)
			a.recordImmutableSymbolOptimizationFacts(existing, valueExpr)
			a.recordRegionRefBinding(existing, valueExpr)
		}
		return
	}
	sym := &Symbol{Name: name, Kind: SymbolLocal, Type: typ, Node: node, Mutable: false}
	a.defineLocalInScope(scope, sym, pos)
	if valueExpr != nil {
		a.recordValueBinding(sym, valueExpr)
		a.recordBorrowedOwnerRefBinding(sym, valueExpr)
		a.recordFunctionValueBinding(sym, valueExpr)
		a.recordImmutableSymbolOptimizationFacts(sym, valueExpr)
		a.recordRegionRefBinding(sym, valueExpr)
	}
}

func (a *Analyzer) bindExpectPatternLocals(scope *Scope, pattern ast.MatchPattern, expected Type, valueExpr ast.Expr) {
	if a == nil || scope == nil || pattern == nil || expected == nil {
		return
	}
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern:
		return
	case *ast.MatchBindPattern:
		if a.analyzePredicateMatchPattern(p, expected, valueExpr) {
			return
		}
		a.bindExpectPatternLocal(scope, p.Name, expected, p, p.Pos(), valueExpr)
	case *ast.MatchStructPattern:
		if a.analyzeCountMatchPattern(p, expected) {
			return
		}
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			var fieldExpr ast.Expr
			if valueExpr != nil {
				fieldExpr = &ast.FieldExpr{Position: arg.Position, Object: valueExpr, Field: fields[i].Name}
			}
			a.bindExpectPatternLocals(scope, arg.Pattern, fields[i].Type, fieldExpr)
		}
	case *ast.MatchListPattern:
		elemType, ok := SequenceMatchElementType(expected)
		if !ok {
			return
		}
		for i, elem := range p.Elems {
			if _, ok := elem.(*ast.MatchRestPattern); ok {
				continue
			}
			var indexExpr ast.Expr
			if valueExpr != nil {
				indexExpr = &ast.IndexExpr{
					Position: elem.Pos(),
					Object:   valueExpr,
					Index:    &ast.IntLit{Position: elem.Pos(), Value: strconv.Itoa(i), Suffix: "u"},
				}
			}
			a.bindExpectPatternLocals(scope, elem, elemType, indexExpr)
		}
	case *ast.MatchVariantPattern:
		switch variantBase := expected.(type) {
		case *EnumType:
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				return
			}
			orderedArgs := a.resolvePartialMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				var payloadExpr ast.Expr
				if valueExpr != nil {
					resolvedExpr, ok := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
					if ok {
						payloadExpr = resolvedExpr
					}
				}
				a.bindExpectPatternLocals(scope, arg.Pattern, variant.Payload[i], payloadExpr)
			}
		case *TreeCategoryType:
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				return
			}
			orderedArgs := a.resolvePartialMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				var payloadExpr ast.Expr
				if valueExpr != nil {
					resolvedExpr, ok := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
					if ok {
						payloadExpr = resolvedExpr
					}
				}
				a.bindExpectPatternLocals(scope, arg.Pattern, variant.Payload[i], payloadExpr)
			}
		}
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
	if expr.Success.ErrorBinding {
		a.errorf(expr.Success.Position, "catch success arm cannot be an error binding")
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
	hasErrorBinding := false
	for _, arm := range expr.Arms {
		if arm.ErrorBinding {
			if hasErrorBinding {
				a.errorf(arm.Position, "catch error binding is unreachable because an earlier error binding already handles all errors")
			}
			hasErrorBinding = true
			errorScope := NewScope(a.currentScope)
			errorSym := &Symbol{Name: arm.Name, Kind: SymbolLocal, Type: unionType.Errors, Mutable: false}
			a.defineLocalInScope(errorScope, errorSym, arm.Position)
			mergeArm(arm.Position, arm.Body, errorScope)
			continue
		}
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
	if !hasErrorBinding && len(covered) != len(unionType.Errors.Tags) {
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
