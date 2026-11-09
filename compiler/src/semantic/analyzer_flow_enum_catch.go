package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"sort"
	"strings"
)

func (a *Analyzer) analyzeEnumMatchStmt(stmt *ast.MatchStmt, valueType Type, enumType *EnumType) {
	a.validateMatchStore(stmt.Pos(), stmt.Value, valueType, enumType, stmt.Store)
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
	// Storage-view deps merge like an if: start from the pre-match state and union in every
	// fallthrough arm, so a view assigned inside an arm (`v <- text`) keeps its backing
	// dependency after the match.
	mergedStorageViewDeps := a.cloneStorageViewDeps()
	hasFallthrough := false
	priorPatterns := make([]ast.MatchPattern, 0, len(stmt.Arms))
	covered := map[string]bool{}
	hasWildcard := false
	var fallthroughPatterns []ast.MatchPattern
	fallthroughGuarded := false
	for i, arm := range stmt.Arms {
		if a.matchPatternShadowedByPrevious(arm.Pattern, enumType, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		armHasWildcard := false
		armSnapshot := a.analyzeBlockWithAffineClonePrepared(arm.Body, scope, func() {
			armHasWildcard = a.analyzeTopLevelMatchPattern(arm.Pattern, enumType, stmt.Value, scope, i, len(stmt.Arms), matchArmCoverageSink(arm.Guard, covered))
			if armHasWildcard && arm.Guard == nil {
				hasWildcard = true
			}
			a.analyzeMatchArmGuard(arm.Guard, scope)
			a.recordEnumMatchArmFacts(stmt.Value, arm, priorPatterns, scope)
			a.bindPackedVariantViewAliasForBody(arm.Pattern, enumType, stmt.Value, arm.Body, scope)
			if variantPattern, ok := arm.Pattern.(*ast.MatchVariantPattern); ok {
				a.refineAffineEnumVariant(stmt.Value, enumType, variantPattern)
			}
			// A payload binder is an ownership alias of the matched enum's affine
			// field. Resolve it only after installing the packed-variant view so the
			// source projection (for example, `job.thread`) uses the refined payload
			// type rather than the enum's common-field table.
			a.recordAffineMatchPatternBindings(arm.Pattern, enumType, stmt.Value, scope)
		})
		if !blockDefinitelyExits(arm.Body) {
			fallthroughPatterns = append(fallthroughPatterns, arm.Pattern)
			if arm.Guard != nil {
				fallthroughGuarded = true
			}
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
			mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, armSnapshot.StorageViewDeps)
		}
		if arm.Guard == nil {
			// A guarded arm can fail at runtime, so it never shadows later arms.
			priorPatterns = append(priorPatterns, arm.Pattern)
		}
	}
	if !a.matchCoversAllVariants(enumType, covered, hasWildcard) {
		a.reportNonExhaustiveMatch(stmt.Pos(), enumType, covered, hasWildcard)
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
	a.currentStorageViewDeps = mergedStorageViewDeps
	if a.matchCoversAllVariants(enumType, covered, hasWildcard) && !fallthroughGuarded {
		a.recordEnumMatchFallthroughFact(stmt.Value, fallthroughPatterns)
	}
	a.recordAffineDestructureConsumption(stmt.Value, valueType, "match over affine enum")
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
	if optionalType, ok := valueType.(*OptionalType); ok {
		return a.analyzeOptionalMatchExpr(expr, optionalType)
	}
	if isStringMatchableType(valueType) {
		return a.analyzeStringMatchExpr(expr, valueType)
	}
	if isIntegerMatchableType(valueType) {
		return a.analyzeIntegerMatchExpr(expr, valueType)
	}
	if _, ok := StripAggregateStateType(valueType).(*TupleType); ok {
		return a.analyzeTupleMatchExpr(expr, valueType)
	}
	if _, ok := a.resolvedStructFields(valueType); ok {
		return a.analyzeStructMatchExpr(expr, valueType)
	}
	if !IsInvalidType(valueType) {
		a.errorf(expr.Pos(), "match requires an enum, const enum, error set, optional, integer, string, tuple, or struct value, got %s", valueType)
	}
	for _, arm := range expr.Arms {
		a.analyzeMatchExprArmBody(arm.Body, NewScope(a.currentScope))
	}
	return invalidType

}

// bindCatchArmPayload defines the payload binders of a catch arm (`E.Bad(x, y):`) in the
// arm scope, typed by the matched variant's payload fields. Binding is optional — an arm
// may ignore the payload by omitting the parenthesised binders; but if it binds, the arity
// must match.
func (a *Analyzer) bindCatchArmPayload(scope *Scope, arm ast.CatchArm, errs *ErrorSetType, tag string) {
	if len(arm.Payload) == 0 {
		return
	}
	var fieldTypes []Type
	if errs != nil && errs.Payloads != nil {
		fieldTypes = errs.Payloads[tag]
	}
	if len(arm.Payload) != len(fieldTypes) {
		a.errorf(arm.Position, "catch arm %q binds %d payload value(s) but the variant carries %d", arm.Name, len(arm.Payload), len(fieldTypes))
	}
	for i, binder := range arm.Payload {
		if binder == "_" {
			continue
		}
		fieldType := Type(invalidType)
		if i < len(fieldTypes) {
			fieldType = fieldTypes[i]
		}
		a.defineLocalInScope(scope, &Symbol{Name: binder, Kind: SymbolLocal, Type: fieldType, Mutable: false}, arm.Position)
	}
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
			if unionType.Errors.HasParams() {
				a.errorf(arm.Position, "catch arm %q does not match %s; use `error %s:` to catch the opaque error-set parameter component", arm.Name, ErrorSetDiagnosticName(unionType.Errors), arm.Name)
			} else {
				a.errorf(arm.Position, "catch arm %q does not match %s", arm.Name, ErrorSetDiagnosticName(unionType.Errors))
			}
			mergeArm(arm.Position, arm.Body, NewScope(a.currentScope))
			continue
		}
		if covered[matchedTag] {
			a.errorf(arm.Position, "catch arm %q is unreachable because an earlier arm already matches it", arm.Name)
		}
		covered[matchedTag] = true
		armScope := NewScope(a.currentScope)
		a.bindCatchArmPayload(armScope, arm, unionType.Errors, matchedTag)
		mergeArm(arm.Position, arm.Body, armScope)
	}
	if !hasErrorBinding && unionType.Errors.HasParams() {
		a.errorf(expr.Pos(), "non-exhaustive catch over %s; the error-set parameter component requires a catch-all `error e:` arm", ErrorSetDiagnosticName(unionType.Errors))
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
	a.consumeHandledErrorUnionExpr(expr.Value, unionType, "catch")
	if resultType == nil {
		return neverType
	}
	return resultType
}
