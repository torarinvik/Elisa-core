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

// mergeMatchExprArmTypes merges a match-expression's accumulated result type with the
// next arm's type. On top of the plain MergeTypes lattice it adapts STRING-LITERAL
// arms to a string-view result, in either arm order:
//
//	s: sview = match k:
//	    1: "one"        # static u8& literal arm — adapts to sview
//	    _: v            # sview
//
// This mirrors the contextual ternary (`s: sview = "lit" if c else v`, which adapts
// literal branches via contextualStringLiteralType); without it, expression-form match
// rejects the mix that its statement/ternary equivalents accept. Only LITERAL-tailed
// arms adapt — a non-literal `static u8&` value (a static byte-ref binding) has no
// knowable length, so it stays incompatible, exactly as in the ternary. The backend
// needs no change: each arm body is emitted with the merged type as its expected type,
// which triggers the same literal→view lowering as a typed declaration.
func (a *Analyzer) mergeMatchExprArmTypes(resultType, armType Type, arms []ast.MatchArm, index int) Type {
	merged := MergeTypes(resultType, armType)
	if !IsInvalidType(merged) {
		return merged
	}
	if isStringViewType(resultType) && isStaticStringLiteralRefType(armType) && matchArmTailIsStringLiteral(arms[index].Body) {
		return resultType
	}
	if isStaticStringLiteralRefType(resultType) && isStringViewType(armType) && matchArmTailsAllStringLiterals(arms[:index]) {
		return armType
	}
	return invalidType
}

// isStaticStringLiteralRefType reports the TYPE a string literal carries:
// `static u8&` (RefType{Elem: u8, Storage: static}).
func isStaticStringLiteralRefType(t Type) bool {
	ref, ok := t.(*RefType)
	if !ok || ref == nil || ref.Storage != RefStorageStatic {
		return false
	}
	elem, ok := ref.Elem.(*BuiltinType)
	return ok && elem != nil && elem.Name == "u8"
}

// matchArmTailIsStringLiteral reports whether an arm body's value expression is a
// bare string literal (the only static-u8& shape the literal→view adaptation covers).
func matchArmTailIsStringLiteral(body []ast.Stmt) bool {
	if len(body) == 0 {
		return false
	}
	exprStmt, ok := body[len(body)-1].(*ast.ExprStmt)
	if !ok || exprStmt == nil {
		return false
	}
	_, ok = exprStmt.Expr.(*ast.StringLit)
	return ok
}

// matchArmTailsAllStringLiterals reports whether EVERY given arm yields a bare string
// literal — required before adapting an accumulated static-u8& result to a view (the
// accumulated type alone cannot distinguish literal arms from static byte-ref arms).
func matchArmTailsAllStringLiterals(arms []ast.MatchArm) bool {
	for _, arm := range arms {
		if !matchArmTailIsStringLiteral(arm.Body) {
			return false
		}
	}
	return len(arms) > 0
}

// mergeTernaryBranchTypes is the ternary's literal-aware fallback when MergeTypes
// rejects its branches: a bare string-LITERAL branch adapts to a string-view branch
// (either side), the same rule mergeMatchExprArmTypes applies to match arms. Used by
// the PLAIN ternary path — the contextual path (expected type known) already adapts
// literals via contextualStringLiteralType.
func mergeTernaryBranchTypes(left, right Type, leftExpr, rightExpr ast.Expr) Type {
	if isStringViewType(left) && isStaticStringLiteralRefType(right) && isBareStringLit(rightExpr) {
		return left
	}
	if isStaticStringLiteralRefType(left) && isStringViewType(right) && isBareStringLit(leftExpr) {
		return right
	}
	return invalidType
}

func isBareStringLit(expr ast.Expr) bool {
	_, ok := expr.(*ast.StringLit)
	return ok
}
