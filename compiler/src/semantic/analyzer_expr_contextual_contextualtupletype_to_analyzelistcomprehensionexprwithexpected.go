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
	if invocation := a.incrementalBodies; invocation != nil && invocation.tracking && !a.suppressDiagnostics {
		invocation.session.stats.PrimaryValueChecks++
	}
	if isZeroedInitializer(expr) && expected != nil {
		// The expected type already failed to resolve and that failure was
		// reported; judging `zeroed` against it would only cascade.
		if IsInvalidType(expected) {
			a.recordAnalyzedExprType(expr, invalidType)
			return invalidType
		}
		if a.zeroedContainsProtocolState(expected, 0) {
			a.errorf(expr.Pos(), "zeroed cannot manufacture a protocol state; use an authorized initial constructor")
			a.recordAnalyzedExprType(expr, invalidType)
			return invalidType
		}
		if a.zeroedTypeContainsSView(expected) {
			a.errorf(expr.Pos(), "`zeroed` cannot construct an `sview`: every view must have a valid, live backing pointer; use an empty string view or a validated byte-view constructor")
			a.recordAnalyzedExprType(expr, invalidType)
			return invalidType
		}
		// Generic runtime storage may intentionally use a zeroed inactive slot, but
		// only inside an explicit Unsafe.PointerCast boundary. Keep sview rejected
		// above even there: its backing-pointer invariant is unconditional.
		if a.currentUnsafePointerCastGrantDepth == 0 && a.zeroedTypeHasInvalidRepresentation(expected) && !a.zeroedPlaceholderCanDefer(expected) {
			a.errorf(expr.Pos(), "cannot initialize %s from `zeroed`: its zero representation may contain a non-null reference; provide a valid value or use an optional reference", expected)
			a.recordAnalyzedExprType(expr, invalidType)
			return invalidType
		}
		a.checkPrivateZeroedType(expected, expr.Pos(), make(map[Type]bool))
		if dictType, ok := StripAggregateStateType(expected).(*DictType); ok {
			if !a.ensureRuntimeBackedDictSupported(expr.Pos(), dictType) {
				a.recordAnalyzedExprType(expr, invalidType)
				return invalidType
			}
		}
		a.recordAnalyzedExprType(expr, expected)
		return expected
	}
	if grant, ok := expr.(*ast.CanExpr); ok && grant != nil {
		result := a.analyzeCanExprWithExpected(grant, expected)
		a.recordAnalyzedExprType(grant, result)
		return result
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
	if block, ok := expr.(*ast.ExprBlock); ok && block != nil && expected != nil {
		// docs/119 §2: the tail is the value, so it sees the destination type the same
		// way a ternary branch or a match arm does.
		result := a.analyzeExprBlockWithExpected(block, expected)
		a.recordAnalyzedExprType(block, result)
		return result
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
			// A reference binding's value keeps its declared capability: a rebindable
			// read-only binding passed as a `mutable T&` argument let the callee write
			// through a read-only referent.
			if !a.refBindingIdent(expr) && (a.mutationPathWritable(expr) || a.exprCanYieldWritableRef(expr)) {
				cloned := cloneRefType(actualRef)
				cloned.Mutable = true
				result = cloned
				a.recordAnalyzedExprType(expr, result)
			}
		}
	}
	return result
}

func (a *Analyzer) zeroedPlaceholderCanDefer(expected Type) bool {
	if a == nil || expected == nil || a.zeroedPlaceholderExpected == nil {
		return false
	}
	if !SameType(expected, a.zeroedPlaceholderExpected) {
		return false
	}
	// The existing definite-assignment lattice tracks a whole local, not individual
	// invalid aggregate fields. Only scalar placeholders can use that proof. An
	// aggregate stays rejected even with one invalid field because a write to a
	// different field would otherwise clear the whole-local uninitialized bit.
	switch StripAggregateStateType(expected).(type) {
	case *RefType, *IDType, *CStrType, *TypeParamType:
		return a.zeroedInvalidRepresentationCountAtDepth(expected, 0) == 1
	default:
		return false
	}
}

func contextualStringLiteralType(expected Type) (Type, bool) {
	if expected == nil {
		return nil, false
	}
	if optional, ok := expected.(*OptionalType); ok && optional.Value != nil {
		// String literals are statically NUL-terminated, so they can safely
		// initialize/pass to an optional cstr just as they can to cstr itself.
		// Return the non-optional payload type; AssignableTo handles the normal
		// optional injection at the surrounding boundary.
		return contextualStringLiteralType(optional.Value)
	}
	if _, ok := expected.(*CStrType); ok {
		return expected, true
	}
	if isStringViewType(expected) {
		return expected, true
	}
	if ref, ok := u8RuntimeRef(expected); ok {
		// A literal's bytes are read-only static data: it takes a byte-POINTER type in place,
		// never a writable one. Typed as `mutable u8&`, a store through it faulted (SIGBUS).
		if ref.Mutable {
			return nil, false
		}
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
	// A reference argument is passed as the reference it holds. Its address (the variable holding
	// the pointer) only type-checked against a scalar `T&` through the referent auto-read, and is
	// never the object meant.
	if _, actualIsRef := actual.(*RefType); actualIsRef {
		if _, elemIsRef := expectedRef.Elem.(*RefType); !elemIsRef {
			return expr, actual
		}
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
	if a == nil || expr == nil || !a.hasExprFactsStorage() || a.suppressOptimizationFacts {
		return
	}
	baseFacts := optimizationFactsForType(result)
	if !exprRequiresOptimizationFactInference(expr) && !typeMayCarryRegionProvenanceForOptimization(result) {
		if baseFacts.HasAnyFacts() {
			a.setExprFacts(expr, baseFacts)
			return
		}
		a.deleteExprFacts(expr)
		return
	}
	savedSuppressLazyFuncSummaryInference := a.suppressLazyFuncSummaryInference
	a.suppressLazyFuncSummaryInference = true
	defer func() {
		a.suppressLazyFuncSummaryInference = savedSuppressLazyFuncSummaryInference
	}()
	facts := a.inferExprOptimizationFactsWithBase(expr, result, baseFacts)
	if facts.HasAnyFacts() {
		a.setExprFacts(expr, facts)
		return
	}
	a.deleteExprFacts(expr)
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
	// Entry clones belong to this path; transfer them before restoring the parent.
	snapshot := affineFlowSnapshot{Affine: a.currentAffineValues, BorrowedOwnerRefs: a.currentBorrowedOwnerRefs, FunctionValues: a.currentFunctionValues, SpecializedValueTypes: a.snapshotPrivateSpecializedValueTypes(), ValueBindings: a.currentValueBindings, AliasCarriers: a.cloneAliasCarriers(), AliasCarrierFieldOverrides: a.cloneAliasCarrierFieldOverrides()}
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
