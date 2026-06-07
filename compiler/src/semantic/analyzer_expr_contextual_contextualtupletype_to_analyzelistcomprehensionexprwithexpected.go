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
	snapshot := affineFlowSnapshot{Affine: a.cloneAffineValueStates(), BorrowedOwnerRefs: a.cloneBorrowedOwnerRefBindings(), FunctionValues: a.cloneFunctionValueBindings(), SpecializedValueTypes: a.cloneSpecializedValueTypeBindings(), ValueBindings: a.cloneValueBindings()}
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
	if !IsBoolType(condType) {
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
	if expr.Owner == nil && len(expr.Elems) > 0 && useExpectedDArray && expectedDArray.Region != "" {
		// A `darray[T] @r` binding type supplies a NON-EMPTY literal's allocation owner,
		// so `xs: darray[T] @r = [a, b]` allocates in r. This replaces the removed
		// `[...] in r` expression-owner form (docs/68 §5/§7). Empty literals allocate
		// nothing, so they keep relying on the binding type's provenance alone.
		expr.Owner = &ast.Ident{Position: expr.Position, Name: expectedDArray.Region}
	}
	if expr.Owner != nil {
		owner, _, ok := a.classifyTreeAllocOwnerExpr(expr.Owner)
		if !ok || owner.Kind != treeAllocOwnerArena {
			a.errorf(expr.Owner.Pos(), "darray literal owner must be an Arena")
		}
		if !useExpectedDArray {
			a.errorf(expr.Owner.Pos(), "list literal allocation owner requires an expected darray type")
		}
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
				a.errorf(elem.Pos(), "spread darray literal element expects a compatible darray, dview, or array source of %s, got %s", expectedDArray.Elem, sourceType)
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
	if expr.Owner != nil {
		owner, ownerType, ok := a.classifyTreeAllocOwnerExpr(expr.Owner)
		if !ok || owner.Kind != treeAllocOwnerArena {
			a.errorf(expr.Owner.Pos(), "list comprehension owner must be an Arena or mutable Arena&, got %s", ownerType)
		}
	} else if a.currentTreeAllocOwner.Kind != treeAllocOwnerArena && !(useExpectedDArray && a.regionAvailableForContainer(expectedDArray)) {
		a.errorf(expr.Pos(), "list comprehension requires an active in <arena>: scope")
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
		loopSym := &Symbol{Name: expr.Name, Kind: SymbolLocal, Type: itemType, Node: expr, Mutable: false}
		a.defineLocalInScope(loopScope, loopSym, expr.Pos())
	} else {
		sourceType := a.analyzeExpr(expr.Source)
		info, ok := a.resolveIterLoopSourceInfo(expr.Source, sourceType)
		if !ok {
			a.errorf(expr.Source.Pos(), "list comprehension currently requires an array, dynamic array, view, store.rows(), frozen tree row view, string-like iterable, ChunksExactView, source.enumerate(), children(node), or a projected tree attribute sequence, got %s", sourceType)
			info.ItemType = invalidType
		}
		if a.containsAffineHandleValues(info.ItemType, map[string]bool{}) {
			a.errorf(expr.Pos(), "list comprehension value iteration does not support affine element type %s; use an explicit loop with ref binding", info.ItemType)
		}
		itemType = info.ItemType
		pattern := &ast.MoveBindNamePattern{Position: expr.Pos(), Name: expr.Name}
		a.bindIterLoopPattern(loopScope, pattern, ast.IterBindValue, info.ItemType, info.ItemFacts, info.HasItemFacts)
	}
	if expr.Filter != nil {
		condType := a.analyzeCondExprInScope(expr.Filter, loopScope)
		if !IsBoolType(condType) {
			a.errorf(expr.Filter.Pos(), "list comprehension filter must be bool, got %s", condType)
		}
	}
	var expectedElem Type
	if useExpectedDArray {
		expectedElem = expectedDArray.Elem
	}
	savedScope := a.currentScope
	a.currentScope = loopScope
	valueType := a.analyzeValueExpr(expr.Value, expectedElem)
	a.currentScope = savedScope
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
	result := &DArrayType{Elem: valueType, Shape: &WildcardShape{}, SurfaceName: "darray"}
	a.exprTypes[expr] = result
	return result
}
