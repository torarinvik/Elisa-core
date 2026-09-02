package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"strconv"
)

func contextualTupleType(expected Type) (*TupleType, bool) {
	if unionType, ok := expected.(*ErrorUnionType); ok {
		expected = unionType.Value
	}
	tupleType, ok := StripAggregateStateType(expected).(*TupleType)
	if !ok || tupleType == nil {
		return nil, false
	}
	return tupleType, true
}
func (a *Analyzer) analyzeTupleExprWithExpected(expr *ast.TupleExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	expectedTuple, useExpected := contextualTupleType(expected)
	mismatchedArity := false
	if useExpected && len(expectedTuple.Fields) != len(expr.Elems) {
		a.errorf(expr.Pos(), "tuple expects %d elements, got %d", len(expectedTuple.Fields), len(expr.Elems))
		mismatchedArity = true
	}
	fields := make([]TupleField, 0, len(expr.Elems))
	for i, elem := range expr.Elems {
		fieldName := fmt.Sprintf("_%d", i)
		var expectedElem Type
		if useExpected && i < len(expectedTuple.Fields) {
			expectedElem = expectedTuple.Fields[i].Type
			if expectedTuple.Fields[i].Name != "" {
				fieldName = expectedTuple.Fields[i].Name
			}
		}
		itemType := a.analyzeValueExpr(elem, expectedElem)
		moveType := itemType
		if expectedElem != nil {
			moveType = expectedElem
			if !AssignableTo(expectedElem, itemType) {
				a.errorf(elem.Pos(), "tuple element %d (%s) expects %s, got %s", i+1, fieldName, expectedElem, itemType)
				a.reportShapeMismatchNotes(elem.Pos(), expectedElem, itemType)
			}
		}
		a.consumeAffineValueExpr(elem, moveType, "move into tuple element "+strconv.Quote(fieldName))
		fields = append(fields, TupleField{Name: fieldName, Type: itemType})
	}
	if useExpected {
		if mismatchedArity {
			a.recordAnalyzedExprType(expr, invalidType)
			return invalidType
		}
		a.recordAnalyzedExprType(expr, expectedTuple)
		return expectedTuple
	}
	result := &TupleType{Fields: fields}
	a.recordAnalyzedExprType(expr, result)
	return result
}
func (a *Analyzer) analyzeValueExpr(expr ast.Expr, expected Type) Type {
	if _, ok := expr.(*ast.ZeroedLit); ok && expected != nil {
		if dictType, ok := StripAggregateStateType(expected).(*DictType); ok {
			if !a.ensureRuntimeBackedDictSupported(expr.Pos(), dictType) {
				a.recordAnalyzedExprType(expr, invalidType)
				return invalidType
			}
		}
		a.recordAnalyzedExprType(expr, expected)
		return expected
	}
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		result := a.analyzeValueExpr(paren.Inner, expected)
		a.recordAnalyzedExprType(paren, result)
		return result
	}
	if shorthandType, ok := a.analyzeContextualShorthandValueExpr(expr, expected); ok {
		return shorthandType
	}
	if alloc, ok := expr.(*ast.AllocExpr); ok {
		result := a.analyzeAllocExprWithExpected(alloc, expected)
		a.recordAnalyzedExprType(alloc, result)
		return result
	}
	if lit, ok := expr.(*ast.StructLitExpr); ok {
		result := a.analyzeStructLiteralExpr(lit, expected)
		a.recordAnalyzedExprType(lit, result)
		return result
	}
	if list, ok := expr.(*ast.ListLitExpr); ok {
		return a.analyzeListLitExprWithExpected(list, expected)
	}
	if comp, ok := expr.(*ast.ListComprehensionExpr); ok {
		return a.analyzeListComprehensionExprWithExpected(comp, expected)
	}
	if query, ok := expr.(*ast.QueryExpr); ok {
		return a.analyzeQueryExpr(query, expected)
	}
	if fold, ok := expr.(*ast.FoldExpr); ok && fold.Keyword == "rewrite" {
		if _, ok := sequenceRewriteTargetTypeExpr(fold.Root); ok {
			return a.analyzeFoldExprWithExpected(fold, expected)
		}
	}
	if tuple, ok := expr.(*ast.TupleExpr); ok {
		return a.analyzeTupleExprWithExpected(tuple, expected)
	}
	if lambda, ok := expr.(*ast.LambdaExpr); ok {
		result := a.analyzeLambdaExpr(lambda, expected)
		a.recordAnalyzedExprType(lambda, result)
		return result
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		result := a.analyzeCallExprWithExpected(call, expected)
		a.recordAnalyzedExprType(call, result)
		return result
	}
	if ternary, ok := expr.(*ast.TernaryExpr); ok && expected != nil {
		return a.analyzeContextualTernaryExpr(ternary, expected)
	}
	if matchExpr, ok := expr.(*ast.MatchExpr); ok && expected != nil {
		return a.analyzeContextualMatchExpr(matchExpr, expected)
	}
	if mf, ok := expr.(*ast.MachineFromExpr); ok {
		return a.analyzeMachineFromExpr(mf, expected)
	}
	if contextualExpected, ok := contextualIntLiteralType(expected); ok {
		// A compile-time integer being adapted to a narrower integer type must be
		// representable: `y: i8 = 1000` silently truncated to -24 before. There is no
		// explicit cast here (the literal is implicitly taking the target type), so an
		// out-of-range value is unambiguously a bug — reject it (cf. Rust's overflowing
		// literal error). Explicit truncation must go through a cast.
		//
		// Only sub-64-bit targets are checked: a 64-bit literal that exceeds int64
		// (e.g. a u64 hash constant 0xcbf2...) is stored as a negative bit pattern in the
		// int64 const value, so the fit test would be wrong; and any literal the lexer
		// accepted always fits a 64-bit type anyway.
		if _, width, ok := BitIntInfo(contextualExpected); ok && width < 64 {
			if value, ok := a.evalConstExpr(expr); ok && value.Kind == ConstInt && !IntegerTypeFitsValue(contextualExpected, value.Int) {
				a.errorf(expr.Pos(), "integer literal %d does not fit in %s", value.Int, contextualExpected)
			}
		}
		if contextualType, ok := a.analyzeContextualIntValueExpr(expr, contextualExpected); ok {
			return contextualType
		}
	}
	if contextualExpected, ok := contextualFloatLiteralType(expected); ok {
		if contextualType, ok := a.analyzeContextualFloatValueExpr(expr, contextualExpected); ok {
			return contextualType
		}
	}
	if contextualExpected, ok := contextualStringLiteralType(expected); ok {
		if contextualType, ok := a.analyzeContextualStringValueExpr(expr, contextualExpected); ok {
			return contextualType
		}
	}
	result := a.analyzeExpr(expr)
	if expectedRef, ok := expected.(*RefType); ok && expectedRef.Mutable {
		if actualRef, ok := result.(*RefType); ok && !actualRef.Mutable {
			if a.mutationPathWritable(expr) || a.exprCanYieldWritableRef(expr) {
				cloned := cloneRefType(actualRef)
				cloned.Mutable = true
				result = cloned
				a.recordAnalyzedExprType(expr, result)
			}
		}
	}
	return result
}
func contextualStringLiteralType(expected Type) (Type, bool) {
	if expected == nil {
		return nil, false
	}
	if _, ok := expected.(*DStrType); ok {
		return expected, true
	}
	if isStringViewType(expected) {
		return expected, true
	}
	if _, ok := u8RuntimeRef(expected); ok {
		return expected, true
	}
	return nil, false
}
func (a *Analyzer) analyzeContextualStringValueExpr(expr ast.Expr, expected Type) (Type, bool) {
	switch n := expr.(type) {
	case *ast.StringLit:
		a.recordAnalyzedExprType(n, expected)
		return expected, true
	case *ast.ParenExpr:
		innerType, ok := a.analyzeContextualStringValueExpr(n.Inner, expected)
		if !ok {
			return nil, false
		}
		a.recordAnalyzedExprType(n, innerType)
		return innerType, true
	default:
		return nil, false
	}
}
func implicitCallLikeRefUpcastType(expected *RefType, actual Type) (Type, bool) {
	if expected == nil {
		return nil, false
	}
	actualRef, ok := actual.(*RefType)
	if !ok || actualRef == nil {
		return nil, false
	}
	if expected.Storage != RefStorageAny || actualRef.Storage == RefStorageAny {
		return nil, false
	}
	if expected.Mutable && !actualRef.Mutable {
		return nil, false
	}
	if expected.State != actualRef.State || expected.Region != actualRef.Region {
		return nil, false
	}
	if !AssignableTo(expected.Elem, actualRef.Elem) {
		return nil, false
	}
	coerced := cloneRefType(actualRef)
	coerced.Storage = RefStorageAny
	coerced.ExplicitStorage = expected.ExplicitStorage
	return coerced, true
}
func (a *Analyzer) analyzeCallLikeValueExpr(expr ast.Expr, expected Type) (ast.Expr, Type) {
	actual := a.analyzeValueExpr(expr, expected)
	if expected == nil || AssignableTo(expected, actual) {
		return expr, actual
	}
	expectedRef, ok := expected.(*RefType)
	if !ok || expectedRef == nil {
		return expr, actual
	}
	if upcastType, ok := implicitCallLikeRefUpcastType(expectedRef, actual); ok {
		return expr, upcastType
	}
	if !a.exprCanYieldAddressableValue(expr) || !AssignableTo(expectedRef.Elem, actual) {
		return expr, actual
	}
	autoref := &ast.AddrOfExpr{Position: expr.Pos(), Operand: expr}
	autorefType := a.analyzeExpr(autoref)
	if AssignableTo(expected, autorefType) {
		return autoref, autorefType
	}
	if upcastType, ok := implicitCallLikeRefUpcastType(expectedRef, autorefType); ok {
		return autoref, upcastType
	}
	return expr, actual
}
func (a *Analyzer) recordAnalyzedExprType(expr ast.Expr, result Type) {
	if expr == nil {
		return
	}
	a.exprTypes[expr] = result
	a.recordExprOptimizationFacts(expr, result)
}
func (a *Analyzer) recordExprOptimizationFacts(expr ast.Expr, result Type) {
	if a == nil || expr == nil || a.exprFacts == nil || a.suppressOptimizationFacts {
		return
	}
	baseFacts := optimizationFactsForType(result)
	if !exprRequiresOptimizationFactInference(expr) && !typeMayCarryRegionProvenanceForOptimization(result) {
		if baseFacts.HasAnyFacts() {
			a.exprFacts[expr] = baseFacts
			return
		}
		delete(a.exprFacts, expr)
		return
	}
	savedSuppressLazyFuncSummaryInference := a.suppressLazyFuncSummaryInference
	a.suppressLazyFuncSummaryInference = true
	defer func() {
		a.suppressLazyFuncSummaryInference = savedSuppressLazyFuncSummaryInference
	}()
	facts := a.inferExprOptimizationFactsWithBase(expr, result, baseFacts)
	if facts.HasAnyFacts() {
		a.exprFacts[expr] = facts
		return
	}
	delete(a.exprFacts, expr)
}
func contextualIntLiteralType(expected Type) (Type, bool) {
	if expected == nil || !IsIntegralStorageType(expected) {
		return nil, false
	}
	return expected, true
}
func (a *Analyzer) analyzeContextualIntValueExpr(expr ast.Expr, expected Type) (Type, bool) {
	switch n := expr.(type) {
	case *ast.IntLit:
		if n.Suffix != "" {
			return nil, false
		}
		a.recordAnalyzedExprType(n, expected)
		return expected, true
	case *ast.ParenExpr:
		innerType, ok := a.analyzeContextualIntValueExpr(n.Inner, expected)
		if !ok {
			return nil, false
		}
		a.recordAnalyzedExprType(n, innerType)
		return innerType, true
	case *ast.UnaryExpr:
		if n.Op != lexer.TOKEN_MINUS {
			return nil, false
		}
		operandType, ok := a.analyzeContextualIntValueExpr(n.Operand, expected)
		if !ok {
			return nil, false
		}
		if !IsNumericType(operandType) {
			a.errorf(n.Pos(), "unary operator requires numeric operand")
			a.recordAnalyzedExprType(n, invalidType)
			return invalidType, true
		}
		a.recordAnalyzedExprType(n, operandType)
		return operandType, true
	case *ast.TernaryExpr:
		return a.analyzeContextualIntTernaryExpr(n, expected), true
	default:
		return nil, false
	}
}
func contextualFloatLiteralType(expected Type) (Type, bool) {
	if expected == nil || !IsFloatType(expected) {
		return nil, false
	}
	return expected, true
}
func (a *Analyzer) analyzeContextualFloatValueExpr(expr ast.Expr, expected Type) (Type, bool) {
	switch n := expr.(type) {
	case *ast.FloatLit:
		if n.Suffix != "" {
			return nil, false
		}
		a.recordAnalyzedExprType(n, expected)
		return expected, true
	case *ast.ParenExpr:
		innerType, ok := a.analyzeContextualFloatValueExpr(n.Inner, expected)
		if !ok {
			return nil, false
		}
		a.recordAnalyzedExprType(n, innerType)
		return innerType, true
	case *ast.UnaryExpr:
		if n.Op != lexer.TOKEN_MINUS {
			return nil, false
		}
		operandType, ok := a.analyzeContextualFloatValueExpr(n.Operand, expected)
		if !ok {
			return nil, false
		}
		if !IsNumericType(operandType) {
			a.errorf(n.Pos(), "unary operator requires numeric operand")
			a.recordAnalyzedExprType(n, invalidType)
			return invalidType, true
		}
		a.recordAnalyzedExprType(n, operandType)
		return operandType, true
	case *ast.BinaryExpr:
		return a.analyzeContextualFloatBinaryExpr(n, expected), true
	case *ast.TernaryExpr:
		return a.analyzeContextualFloatTernaryExpr(n, expected), true
	default:
		return nil, false
	}
}
func (a *Analyzer) analyzeContextualFloatBinaryExpr(expr *ast.BinaryExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	if !isContextualFloatArithmeticOp(expr.Op) {
		return a.analyzeBinaryExpr(expr)
	}
	left := a.analyzeValueExpr(expr.Left, expected)
	right := a.analyzeValueExpr(expr.Right, expected)
	if !IsNumericType(left) || !IsNumericType(right) {
		a.errorf(expr.Pos(), "operator requires numeric operands")
		a.recordAnalyzedExprType(expr, invalidType)
		return invalidType
	}
	result := CommonNumericType(left, right)
	a.recordAnalyzedExprType(expr, result)
	return result
}
func isContextualFloatArithmeticOp(op lexer.TokenKind) bool {
	switch op {
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH:
		return true
	default:
		return false
	}
}
func (a *Analyzer) analyzeValueExprInScope(expr ast.Expr, expected Type, scope *Scope) Type {
	saved := a.currentScope
	a.currentScope = scope
	result := a.analyzeValueExpr(expr, expected)
	a.currentScope = saved
	return result
}
func (a *Analyzer) analyzeValueExprInAffineScope(expr ast.Expr, expected Type, scope *Scope) (Type, affineFlowSnapshot) {
	return a.analyzeValueExprInAffineScopePrepared(expr, expected, scope, nil)
}
func (a *Analyzer) analyzeValueExprInConditionAffineScope(expr ast.Expr, expected Type, parent *Scope, cond ast.Expr, truthy bool) (Type, affineFlowSnapshot) {
	scope := a.refinedScopeForCondition(parent, cond, truthy)
	return a.analyzeValueExprInAffineScopePrepared(expr, expected, scope, func() {
		a.applyConditionRefinementsInternal(scope, cond, truthy, true)
	})
}
func (a *Analyzer) analyzeValueExprInAffineScopePrepared(expr ast.Expr, expected Type, scope *Scope, prepare func()) (Type, affineFlowSnapshot) {
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
	result := a.analyzeValueExprInScope(expr, expected, scope)
	snapshot := affineFlowSnapshot{Affine: a.cloneAffineValueStates(), BorrowedOwnerRefs: a.cloneBorrowedOwnerRefBindings(), FunctionValues: a.cloneFunctionValueBindings(), SpecializedValueTypes: a.cloneSpecializedValueTypeBindings(), ValueBindings: a.cloneValueBindings(), AliasCarriers: a.cloneAliasCarriers(), AliasCarrierFieldOverrides: a.cloneAliasCarrierFieldOverrides()}
	a.currentAffineValues = savedAffine
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	return result, snapshot
}
func (a *Analyzer) analyzeContextualIntTernaryExpr(expr *ast.TernaryExpr, expected Type) Type {
	return a.analyzeContextualTernaryExpr(expr, expected)
}
func (a *Analyzer) analyzeContextualTernaryExpr(expr *ast.TernaryExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	condType := a.analyzeCondExpr(expr.Cond)
	if !IsBoolType(condType) && !IsInvalidType(condType) {
		a.errorf(expr.Pos(), "ternary condition must be bool, got %s", condType)
	}
	mergedAffine := a.cloneAffineValueStates()
	mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
	left, leftSnapshot := a.analyzeValueExprInConditionAffineScope(expr.Value, expected, a.currentScope, expr.Cond, true)
	right, rightSnapshot := a.analyzeValueExprInConditionAffineScope(expr.Alt, expected, a.currentScope, expr.Cond, false)
	mergedAffine = mergeAffineValueStates(mergedAffine, leftSnapshot.Affine)
	mergedAffine = mergeAffineValueStates(mergedAffine, rightSnapshot.Affine)
	mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, leftSnapshot.BorrowedOwnerRefs)
	mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, rightSnapshot.BorrowedOwnerRefs)
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	if mergedFunctionValues, ok := a.intersectFunctionValueFlows(leftSnapshot.FunctionValues, rightSnapshot.FunctionValues); ok {
		a.currentFunctionValues = mergedFunctionValues
	}
	if expected != nil && AssignableTo(expected, left) && AssignableTo(expected, right) {
		a.recordAnalyzedExprType(expr, expected)
		return expected
	}
	merged := MergeTypes(left, right)
	if IsInvalidType(merged) {
		a.errorf(expr.Pos(), "ternary branches are incompatible: %s and %s", left, right)
	}
	a.recordAnalyzedExprType(expr, merged)
	return merged
}
func (a *Analyzer) analyzeContextualFloatTernaryExpr(expr *ast.TernaryExpr, expected Type) Type {
	return a.analyzeContextualTernaryExpr(expr, expected)
}
func (a *Analyzer) analyzeListLitExprWithExpected(expr *ast.ListLitExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	if expr.Brace {
		// `{k: v, ...}` is a dict literal: build a populated dict. Like `{}`/`[]` it is an
		// allocating literal, so it triggers auto-region inference and its key/value types are
		// either taken from the expected dict type or inferred from the first pair.
		if len(expr.Keys) > 0 {
			return a.analyzeDictLiteralExpr(expr, expected)
		}
		if setType, ok := contextualSetLiteralType(expected); ok {
			_ = setType
			return a.analyzeSetLiteralExpr(expr, expected)
		}
		// An empty `{}` against an expected dict type is an empty (zero-initialized) dict —
		// the dict analogue of `[]` for darray. It allocates its backing lazily on first
		// insert, and being a literal it triggers auto-region inference (functionBodyNeedsAutoRegion)
		// exactly like `[]`, so `d: dict[cstr, V] = {}; d.put(...)` needs no explicit region scope.
		// A non-empty `{a, b}` remains a set-membership literal (valid only on the RHS of `in`).
		if len(expr.Elems) == 0 {
			if dictType, ok := contextualDictLiteralType(expected); ok {
				a.recordAnalyzedExprType(expr, dictType)
				return dictType
			}
		}
		a.errorf(expr.Pos(), "brace membership set literals are only valid on the right-hand side of `in`")
		a.exprTypes[expr] = invalidType
		return invalidType
	}
	expectedArray, useExpectedArray := contextualArrayLiteralType(expected)
	expectedDArray, useExpectedDArray := contextualDArrayLiteralType(expected)
	hasSpread := false
	for _, spread := range expr.Spreads {
		if spread {
			hasSpread = true
			break
		}
	}
	if expr.Owner != nil {
		ownerType := a.analyzeExpr(expr.Owner)
		arenaType := a.namedTypes["Arena"]
		isArena := arenaType != nil && AssignableTo(arenaType, ownerType)
		if !isArena {
			if refT, ok := ownerType.(*RefType); ok && refT != nil && arenaType != nil {
				isArena = AssignableTo(arenaType, refT.Elem)
			}
		}
		if !isArena {
			a.errorf(expr.Owner.Pos(), "darray literal owner must be an Arena")
		}
		if !useExpectedDArray {
			a.errorf(expr.Owner.Pos(), "list literal allocation owner requires an expected darray type")
		}
	}
	if expr.Owner == nil && len(expr.Elems) > 0 && useExpectedDArray && expectedDArray.Region != "" && !a.lookupRegionParam(expectedDArray.Region) {
		expr.Owner = &ast.Ident{Position: expr.Position, Name: expectedDArray.Region}
	}
	// A region-less expected darray (e.g. a global's declared type) under the perm
	// ambient region allocates in perm: route the literal there explicitly, since the
	// expected type carries no region for the backend to resolve.
	if expr.Owner == nil && len(expr.Elems) > 0 && useExpectedDArray && expectedDArray.Region == "" && a.activeContainerRegionName() == "perm" {
		expr.Owner = &ast.Ident{Position: expr.Position, Name: "perm"}
	}
	if len(expr.Elems) == 0 {
		switch {
		case useExpectedArray:
			if expectedArray.HasConstSize && expectedArray.ConstSize != 0 {
				a.errorf(expr.Pos(), "array literal expects %d elements, got 0", expectedArray.ConstSize)
			}
			a.exprTypes[expr] = expectedArray
			return expectedArray
		case useExpectedDArray:
			a.exprTypes[expr] = expectedDArray
			return expectedDArray
		}
		a.errorf(expr.Pos(), "empty list literal requires an expected array or darray type")
		a.exprTypes[expr] = invalidType
		return invalidType
	}

	var elemType Type
	if useExpectedArray {
		elemType = expectedArray.Elem
		if hasSpread {
			a.errorf(expr.Pos(), "array literals do not support spread elements; use an expected darray type")
		}
		if expectedArray.HasConstSize && expectedArray.ConstSize != int64(len(expr.Elems)) {
			a.errorf(expr.Pos(), "array literal expects %d elements, got %d", expectedArray.ConstSize, len(expr.Elems))
		}
	} else if useExpectedDArray {
		elemType = expectedDArray.Elem
	} else if hasSpread {
		a.errorf(expr.Pos(), "spread list literal requires an expected darray type")
	}

	for i, elem := range expr.Elems {
		spread := i < len(expr.Spreads) && expr.Spreads[i]
		if spread {
			sourceType := a.analyzeValueExpr(elem, nil)
			if !useExpectedDArray {
				continue
			}
			if !builtinDArrayExtendSourceCompatible(expectedDArray.Elem, sourceType) {
				a.errorf(elem.Pos(), "spread darray literal element expects a compatible darray or array source of %s, got %s", expectedDArray.Elem, sourceType)
			}
			continue
		}
		itemType := a.analyzeValueExpr(elem, elemType)
		if useExpectedArray {
			if !AssignableTo(expectedArray.Elem, itemType) {
				a.errorf(elem.Pos(), "array literal element expects %s, got %s", expectedArray.Elem, itemType)
			}
			a.consumeAffineValueExpr(elem, expectedArray.Elem, "move into array literal element")
			continue
		}
		if useExpectedDArray {
			if !AssignableTo(expectedDArray.Elem, itemType) {
				a.errorf(elem.Pos(), "darray literal element expects %s, got %s", expectedDArray.Elem, itemType)
			}
			a.consumeAffineValueExpr(elem, expectedDArray.Elem, "move into darray literal element")
			continue
		}
		a.consumeAffineValueExpr(elem, itemType, "move into array literal element")
		if elemType == nil {
			elemType = itemType
			continue
		}
		merged := MergeTypes(elemType, itemType)
		if IsInvalidType(merged) {
			a.errorf(elem.Pos(), "array literal elements are incompatible: %s and %s", elemType, itemType)
			a.exprTypes[expr] = invalidType
			return invalidType
		}
		elemType = merged
	}

	if useExpectedArray {
		a.exprTypes[expr] = expectedArray
		return expectedArray
	}
	if useExpectedDArray {
		a.exprTypes[expr] = expectedDArray
		return expectedDArray
	}
	if elemType == nil || IsInvalidType(elemType) {
		a.exprTypes[expr] = invalidType
		return invalidType
	}
	result := &ArrayType{Elem: elemType, Size: strconv.Itoa(len(expr.Elems)), HasConstSize: true, ConstSize: int64(len(expr.Elems))}
	a.exprTypes[expr] = result
	return result
}
func (a *Analyzer) analyzeListComprehensionExprWithExpected(expr *ast.ListComprehensionExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	expectedDArray, useExpectedDArray := contextualDArrayLiteralType(expected)
	expectedDict, useExpectedDict := contextualDictLiteralType(expected)
	expectedSet, useExpectedSet := contextualSetLiteralType(expected)
	regionAvailable := (useExpectedDArray && a.regionAvailableForContainer(expectedDArray)) || (useExpectedDict && a.regionAvailableForContainer(expectedDict)) || (useExpectedSet && a.regionAvailableForContainer(expectedSet))
	if expr.Owner != nil {
		ownerType := a.analyzeExpr(expr.Owner)
		arenaType := a.namedTypes["Arena"]
		isArena := arenaType != nil && AssignableTo(arenaType, ownerType)
		if !isArena {
			if refT, ok := ownerType.(*RefType); ok && refT != nil && arenaType != nil {
				isArena = AssignableTo(arenaType, refT.Elem)
			}
		}
		if !isArena {
			a.errorf(expr.Owner.Pos(), "comprehension owner must be an Arena or mutable Arena&, got %s", ownerType)
		}
	} else if a.activeContainerRegionName() == "" && !regionAvailable {
		a.errorf(expr.Pos(), "comprehension requires an active in <arena>: scope")
	}
	var itemType Type
	loopScope := NewScope(a.currentScope)
	if expr.RangeEnd != nil {
		startType := a.analyzeExpr(expr.Source)
		endType := a.analyzeExpr(expr.RangeEnd)
		itemType = CommonNumericType(startType, endType)
		if !IsIntegralType(itemType) {
			a.errorf(expr.Pos(), "list comprehension range requires integral bounds, got %s and %s", startType, endType)
			itemType = invalidType
		}
		if expr.RangeStep != nil {
			stepType := a.analyzeExpr(expr.RangeStep)
			itemType = CommonNumericType(itemType, stepType)
			if !IsIntegralType(stepType) || !IsIntegralType(itemType) {
				a.errorf(expr.RangeStep.Pos(), "list comprehension range step must be integral, got %s", stepType)
				itemType = invalidType
			}
			if value, ok := a.evalConstExpr(expr.RangeStep); ok && value.Kind == ConstInt && value.Int == 0 {
				a.errorf(expr.RangeStep.Pos(), "list comprehension range step cannot be zero")
			}
		}
		if expr.RangeOp != lexer.TOKEN_RANGE && expr.RangeOp != lexer.TOKEN_RANGE_LT && expr.RangeOp != lexer.TOKEN_RANGE_GT {
			a.errorf(expr.Pos(), "list comprehension uses unsupported range operator %s", lexer.TokenName(expr.RangeOp))
		}
		if expr.SecondName != "" {
			a.errorf(expr.Pos(), "a range comprehension yields a single integer per step; a two-binder head `for %s, %s in ...` requires a tuple-yielding source (e.g. a dict)", expr.Name, expr.SecondName)
		}
		loopSym := &Symbol{Name: expr.Name, Kind: SymbolLocal, Type: itemType, Node: expr, Mutable: false}
		a.defineLocalInScope(loopScope, loopSym, expr.Pos())
	} else {
		sourceType := a.analyzeExpr(expr.Source)
		info, ok := a.resolveIterLoopSourceInfo(expr.Source, sourceType)
		if !ok {
			if !IsInvalidType(sourceType) {
				a.errorf(expr.Source.Pos(), "list comprehension currently requires an array, dynamic array, view, store.rows(), frozen tree row view, string-like iterable, ChunksExactView, source.enumerate(), children(node), or a projected tree attribute sequence, got %s", sourceType)
			}
			info.ItemType = invalidType
		}
		if a.containsAffineHandleValues(info.ItemType, map[string]bool{}) {
			a.errorf(expr.Pos(), "list comprehension value iteration does not support affine element type %s; use an explicit `for` loop (read-only iteration borrows affine elements automatically)", info.ItemType)
		}
		itemType = info.ItemType
		// A two-binder head `for k, v in src` destructures a tuple-yielding source
		// (dict entries, darray-of-tuples) exactly like the statement-level tuple loop.
		var pattern ast.MoveBindPattern = &ast.MoveBindNamePattern{Position: expr.Pos(), Name: expr.Name}
		if expr.SecondName != "" {
			pattern = &ast.MoveBindTuplePattern{Position: expr.Pos(), Args: []ast.MoveBindArg{
				{Position: expr.Pos(), Name: expr.Name},
				{Position: expr.Pos(), Name: expr.SecondName},
			}}
		}
		a.bindIterLoopPattern(loopScope, pattern, ast.IterBindValue, info.ItemType, info.ItemFacts, info.HasItemFacts)
	}
	// Comma-head bindings: per-element `name [:T] = e` lets, declared in the loop scope
	// before the filter/value/key are checked. Later bindings (and the body/filter) see
	// earlier ones. Each is recomputed per iteration (the backend emits them in the loop body).
	if len(expr.Bindings) > 0 {
		savedScope := a.currentScope
		a.currentScope = loopScope
		for _, b := range expr.Bindings {
			vd, ok := b.(*ast.VarDeclStmt)
			if !ok {
				continue
			}
			var expectedT Type
			if vd.Type != nil {
				expectedT = a.resolveType(vd.Type)
			}
			valT := a.analyzeValueExpr(vd.Value, expectedT)
			declT := valT
			if expectedT != nil && !IsInvalidType(expectedT) {
				if !AssignableTo(expectedT, valT) {
					a.errorf(vd.Value.Pos(), "comprehension binding %s expects %s, got %s", vd.Name, expectedT, valT)
				}
				declT = expectedT
			}
			a.defineLocalInScope(loopScope, &Symbol{Name: vd.Name, Kind: SymbolLocal, Type: declT, Node: vd, Mutable: false}, vd.Position)
		}
		a.currentScope = savedScope
	}
	if expr.Filter != nil {
		condType := a.analyzeCondExprInScope(expr.Filter, loopScope)
		if !IsBoolType(condType) && !IsInvalidType(condType) {
			a.errorf(expr.Filter.Pos(), "comprehension filter must be bool, got %s", condType)
		}
	}
	if expr.Key != nil {
		// Dict comprehension: result is dict[KeyType, ValueType].
		var expectedKey, expectedVal Type
		if useExpectedDict {
			expectedKey = expectedDict.Key
			expectedVal = expectedDict.Value
		}
		savedScope := a.currentScope
		a.currentScope = loopScope
		keyType := a.analyzeValueExpr(expr.Key, expectedKey)
		valueType := a.analyzeValueExpr(expr.Value, expectedVal)
		a.currentScope = savedScope
		if useExpectedDict {
			if !AssignableTo(expectedDict.Key, keyType) {
				a.errorf(expr.Key.Pos(), "dict comprehension key expects %s, got %s", expectedDict.Key, keyType)
			}
			if !AssignableTo(expectedDict.Value, valueType) {
				a.errorf(expr.Value.Pos(), "dict comprehension value expects %s, got %s", expectedDict.Value, valueType)
			}
			a.consumeAffineValueExpr(expr.Key, expectedDict.Key, "move into dict comprehension key")
			a.consumeAffineValueExpr(expr.Value, expectedDict.Value, "move into dict comprehension value")
			a.exprTypes[expr] = expectedDict
			return expectedDict
		}
		if keyType == nil || IsInvalidType(keyType) || valueType == nil || IsInvalidType(valueType) {
			a.exprTypes[expr] = invalidType
			return invalidType
		}
		a.consumeAffineValueExpr(expr.Key, keyType, "move into dict comprehension key")
		a.consumeAffineValueExpr(expr.Value, valueType, "move into dict comprehension value")
		result, _ := a.stampContainerRegion(&DictType{Key: keyType, Value: valueType, SurfaceName: "dict"}).(*DictType)
		a.exprTypes[expr] = result
		return result
	}
	if expr.Set {
		// Set comprehension: result is set[ValueType].
		var expectedSetElem Type
		if useExpectedSet {
			expectedSetElem = expectedSet.Elem
		}
		savedScope := a.currentScope
		a.currentScope = loopScope
		valueType := a.analyzeValueExpr(expr.Value, expectedSetElem)
		a.currentScope = savedScope
		if useExpectedSet {
			if !AssignableTo(expectedSet.Elem, valueType) {
				a.errorf(expr.Value.Pos(), "set comprehension element expects %s, got %s", expectedSet.Elem, valueType)
			}
			a.consumeAffineValueExpr(expr.Value, expectedSet.Elem, "move into set comprehension element")
			a.exprTypes[expr] = expectedSet
			return expectedSet
		}
		if valueType == nil || IsInvalidType(valueType) {
			a.exprTypes[expr] = invalidType
			return invalidType
		}
		a.consumeAffineValueExpr(expr.Value, valueType, "move into set comprehension element")
		result, _ := a.stampContainerRegion(&SetType{Elem: valueType, SurfaceName: "set"}).(*SetType)
		a.exprTypes[expr] = result
		return result
	}
	var expectedElem Type
	if useExpectedDArray {
		expectedElem = expectedDArray.Elem
	}
	savedScope := a.currentScope
	a.currentScope = loopScope
	valueType := a.analyzeValueExpr(expr.Value, expectedElem)
	a.currentScope = savedScope
	if expr.Parallel {
		return a.lowerParallelListComprehension(expr, valueType, expectedDArray, useExpectedDArray)
	}
	if useExpectedDArray {
		if !AssignableTo(expectedDArray.Elem, valueType) {
			a.errorf(expr.Value.Pos(), "list comprehension element expects %s, got %s", expectedDArray.Elem, valueType)
		}
		a.consumeAffineValueExpr(expr.Value, expectedDArray.Elem, "move into list comprehension element")
		a.exprTypes[expr] = expectedDArray
		return expectedDArray
	}
	if valueType == nil || IsInvalidType(valueType) {
		a.exprTypes[expr] = invalidType
		return invalidType
	}
	a.consumeAffineValueExpr(expr.Value, valueType, "move into list comprehension element")
	result, _ := a.stampContainerRegion(&DArrayType{Elem: valueType, Shape: &WildcardShape{}, SurfaceName: "darray"}).(*DArrayType)
	a.exprTypes[expr] = result
	return result
}

// lowerParallelListComprehension desugars a `[ value for name in src by par ]` map into a single
// call to the `par_map_collect` combinator and stashes it on expr.LoweredParallel (the backend emits
// it in place of the sequential comprehension). The desugar is just:
//
//	par_map_collect(slice(&src), lambda(name) => value)
//
// par_map_collect builds the result darray in its own inferred region and returns it; the caller
// adopts that region (region-return inference), so it lands in the comprehension's enclosing region
// — no caller-side scratch, no presize/header threading. (This replaced an earlier inline
// `{ out=[]; out.resize(...); par_map(slice(&src), slice(&out), ...); out }` block that existed only
// because a function couldn't build-and-return an owned container; now one can.) Gated to a no-filter
// map over a plain darray identifier with no head bindings;
// otherwise a clear diagnostic.
func (a *Analyzer) lowerParallelListComprehension(expr *ast.ListComprehensionExpr, elemType Type, expectedDArray *DArrayType, useExpectedDArray bool) Type {
	result := Type(invalidType)
	if useExpectedDArray {
		result = expectedDArray
	} else if elemType != nil && !IsInvalidType(elemType) {
		result, _ = a.stampContainerRegion(&DArrayType{Elem: elemType, Shape: &WildcardShape{}, SurfaceName: "darray"}).(*DArrayType)
	}
	fail := func(pos lexer.Pos, reason string) Type {
		a.errorf(pos, "`by par` map %s; it must be `[ f(%s) for %s in <darray> by par ]` (no filter or head bindings and a darray source)", reason, expr.Name, expr.Name)
		a.exprTypes[expr] = result
		return result
	}
	if expr.Filter != nil {
		return fail(expr.Pos(), "does not support an `if` filter")
	}
	if expr.SecondName != "" {
		return fail(expr.Pos(), "does not support a two-binder head")
	}
	if len(expr.Bindings) > 0 {
		return fail(expr.Pos(), "does not support head bindings")
	}
	if expr.RangeEnd != nil {
		return fail(expr.Pos(), "requires a darray source, not a range")
	}
	srcIdent, ok := expr.Source.(*ast.Ident)
	if !ok {
		return fail(expr.Source.Pos(), "source must be a plain darray variable")
	}
	if elemType == nil || IsInvalidType(elemType) {
		a.exprTypes[expr] = result
		return result
	}
	// Pass the comprehension result as the expected return type below. The ordinary generic-call
	// path then binds par_map_collect's U directly from darray[U] -> result, and uses that binding
	// to contextually type the synthesized lambda. Do not reconstruct a surface TypeExpr here:
	// that was only needed by the old inline out-buffer lowering and incorrectly rejected valid
	// element types (notably generic instances) even though the current call lowering is fully
	// structural.
	pos := expr.Position
	transform := &ast.LambdaExpr{Position: pos, Keyword: "lambda", UsesShorthandParams: true,
		Params: []ast.ParamDecl{{Position: pos, Name: expr.Name}}, BodyExpr: expr.Value}
	// `slice` wants a reference to the darray. A source that is ALREADY a reference (a
	// `darray[T]&` parameter) is passed as-is; taking its address again produced a
	// reference-to-reference that no `slice` overload accepts.
	// The map only READS its source. An owned darray is borrowed as a Slice through
	// `slice(&src)`; a source that is already a reference is passed as-is (taking its
	// address again gave a reference-to-reference nothing accepts), and an IMMUTABLE
	// reference goes through `read_view` -- the ReadView overload of par_map_collect --
	// because `slice` would hand the workers a writable view the caller never granted.
	viewName := "slice"
	var srcArg ast.Expr = &ast.AddrOfExpr{Position: pos, Operand: srcIdent}
	if ref, isRef := a.analyzeExpr(srcIdent).(*RefType); isRef {
		srcArg = srcIdent
		if !ref.Mutable {
			viewName = "read_view"
		}
	}
	srcSlice := &ast.CallExpr{Position: pos, Func: &ast.Ident{Position: pos, Name: viewName},
		Args: []ast.Expr{srcArg}}
	lowered := &ast.CallExpr{Position: pos, Func: &ast.Ident{Position: pos, Name: "par_map_collect"},
		Args: []ast.Expr{srcSlice, transform}}

	loweredType := a.analyzeValueExpr(lowered, result)
	if IsInvalidType(loweredType) {
		a.exprTypes[expr] = result
		return result
	}
	expr.LoweredParallel = lowered
	a.exprTypes[expr] = result
	return result
}
