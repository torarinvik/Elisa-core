package semantic

import (
	"elisacore/src/ast"
)

// exprAsSpecializeTypeArg converts an index subexpression into a type argument for the `fn[T]`
// value-specialization reinterpretation. Only a bare identifier (a type name) is accepted -- that
// covers the value form; multi-arg and generic-type-arg forms are parsed as SpecializeExpr directly.
func exprAsSpecializeTypeArg(e ast.Expr) (ast.TypeExpr, bool) {
	if ident, ok := e.(*ast.Ident); ok && ident != nil {
		return &ast.NamedType{Position: ident.Position, Name: ident.Name}, true
	}
	return nil, false
}

func (a *Analyzer) analyzeIndexExpr(expr *ast.IndexExpr) Type {
	objType := a.analyzeExpr(expr.Object)
	// `fn[T]` over a generic function is not an index (functions are not indexable) -- it is a
	// single-type-arg value specialization, the counterpart of the multi-arg `fn[A, B]` the parser
	// produces directly. Reinterpret it as a SpecializeExpr so it materializes the generic function
	// as a value (the modern replacement for `fn.specialize[T]()`).
	if expr.Fallback == nil {
		if fnType, isFunc := objType.(*FuncType); isFunc && len(genericParamsForFuncType(fnType)) > 0 {
			if typeArg, ok := exprAsSpecializeTypeArg(expr.Index); ok {
				spec := &ast.SpecializeExpr{Position: expr.Position, Operand: expr.Object, TypeArgs: []ast.TypeExpr{typeArg}}
				result := a.analyzeSpecializeExpr(spec)
				expr.AsSpecialize = spec
				a.reportInvalidRegionUse(expr, result)
				a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
				return result
			}
		}
	}
	if flagType, ok := FlagsInstanceType(objType); ok {
		indexType := a.analyzeValueExpr(expr.Index, flagType)
		if expr.Fallback != nil {
			a.errorf(expr.Pos(), "flags membership indexing does not support fallback values")
			_ = a.analyzeValueExpr(expr.Fallback, a.namedTypes["bool"])
		}
		if !AssignableTo(flagType, indexType) {
			a.errorf(expr.Index.Pos(), "flags index expects %s, got %s", flagType, indexType)
			a.reportShapeMismatchNotes(expr.Index.Pos(), flagType, indexType)
		}
		result := a.namedTypes["bool"]
		a.reportInvalidRegionUse(expr, result)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
		return result
	}
	indexExpected := a.namedTypes["usize"]
	if indexExpected == nil {
		indexExpected = builtinUsizeType()
	}
	if storeType, _, ok := builtinStoreReceiverType(objType); ok && storeType != nil && storeType.StoreDecl != nil && storeType.StoreDecl.Soa {
		indexExpected = &IDType{Tag: storeType, Storage: a.namedTypes["u32"]}
	}
	indexType := a.analyzeValueExpr(expr.Index, indexExpected)
	a.markIndexBoundsProof(expr, objType)
	if a.enforceUnsafePermissions && a.indexExprRequiresUncheckedIndexPermission(expr) {
		a.recordFunctionPermissionRefs(unsafeUncheckedIndexRefs(expr.Position))
	}
	finish := func(result Type) Type {
		if expr.Fallback != nil {
			if !a.getWrappedIndexExprs[expr] {
				a.deprecatedf(expr.Pos(), "implicit `else` index fallback is deprecated; write `get <expr>[<index>] else ...` to make the bounds check explicit")
			}
			if !safeIndexFallbackOperandType(objType) {
				a.errorf(expr.Pos(), "index fallback requires an array, darray, view, packed store, or a proven non-null reference to one, got %s", objType)
			}
			if _, ok := NodeKeyEnumType(indexType); ok {
				a.errorf(expr.Index.Pos(), "index fallback does not support dense node-key indices")
			}
			fallbackType := a.analyzeValueExpr(expr.Fallback, result)
			if result != nil && !IsInvalidType(result) && !IsNeverType(fallbackType) && !AssignableTo(result, fallbackType) {
				a.errorf(expr.Pos(), "index fallback expects %s, got %s", result, fallbackType)
				a.reportShapeMismatchNotes(expr.Pos(), result, fallbackType)
			}
		}
		a.reportInvalidRegionUse(expr, result)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, result)
		return result
	}
	indexCheckType := indexType
	if idType, ok := indexType.(*IDType); ok && idType != nil {
		indexCheckType = idType.Storage
	}
	if _, ok := NodeKeyEnumType(indexType); !ok {
		if !IsNumericType(indexCheckType) {
			a.errorf(expr.Index.Pos(), "index must be numeric, got %s", indexType)
		} else if !IsIntegralStorageType(indexCheckType) {
			a.errorf(expr.Index.Pos(), "index must be integral, got %s", indexType)
		}
	}
	if keyEnum, ok := NodeKeyEnumType(indexType); ok {
		if result, handled := a.analyzeDenseNodeKeyIndexExpr(expr, objType, keyEnum); handled {
			return finish(result)
		}
	}
	if storeType, _, ok := builtinStoreReceiverType(objType); ok && storeType != nil && storeType.StoreDecl != nil && storeType.StoreDecl.Soa {
		rowType := &IDType{Tag: storeType, Storage: a.namedTypes["u32"]}
		if !AssignableTo(rowType, indexType) {
			a.errorf(expr.Index.Pos(), "soa row index expects %s, got %s", rowType, indexType)
		}
		return finish(&StoreRowViewType{Store: storeType})
	}
	if arr, ok := objType.(*ArrayType); ok {
		if expr.Fallback == nil {
			a.checkConstantArrayIndexBounds(arr, expr.Index)
		}
		if isStringArrayType(arr) {
			return finish(a.namedTypes["char"])
		}
		return finish(arr.Elem)
	}
	if darray, ok := objType.(*DArrayType); ok {
		return finish(darray.Elem)
	}
	if view, ok := objType.(*ViewType); ok {
		return finish(view.Elem)
	}
	if view, ok := objType.(*DArrayViewType); ok {
		return finish(view.Elem)
	}
	if tuple, ok := StripAggregateStateType(objType).(*TupleType); ok {
		if fieldType, ok := a.tupleIndexResultType(tuple, expr.Index); ok {
			return finish(fieldType)
		}
		return finish(invalidType)
	}
	if itemType, ok := ChunksExactViewItemType(objType); ok {
		return finish(itemType)
	}
	if storeType, ok := objType.(*PackedEnumStoreType); ok && storeType.Enum != nil {
		return finish(storeType.Enum)
	}
	if _, ok := objType.(*DStrType); ok {
		return finish(a.namedTypes["char"])
	}
	if isStringViewType(objType) {
		return finish(a.namedTypes["char"])
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			a.errorf(expr.Pos(), "indexing requires proven non-null reference, got %s", objType)
			return finish(invalidType)
		}
		if arr, ok := ref.Elem.(*ArrayType); ok {
			if expr.Fallback == nil {
				a.checkConstantArrayIndexBounds(arr, expr.Index)
			}
			if isStringArrayType(arr) {
				return finish(a.namedTypes["char"])
			}
			return finish(arr.Elem)
		}
		if darray, ok := ref.Elem.(*DArrayType); ok {
			return finish(darray.Elem)
		}
		if view, ok := ref.Elem.(*ViewType); ok {
			return finish(view.Elem)
		}
		if view, ok := ref.Elem.(*DArrayViewType); ok {
			return finish(view.Elem)
		}
		if tuple, ok := StripAggregateStateType(ref.Elem).(*TupleType); ok {
			if fieldType, ok := a.tupleIndexResultType(tuple, expr.Index); ok {
				return finish(fieldType)
			}
			return finish(invalidType)
		}
		if itemType, ok := ChunksExactViewItemType(ref.Elem); ok {
			return finish(itemType)
		}
		if storeType, ok := ref.Elem.(*PackedEnumStoreType); ok && storeType.Enum != nil {
			return finish(storeType.Enum)
		}
		if _, ok := ref.Elem.(*DStrType); ok {
			return finish(a.namedTypes["char"])
		}
		if isStringViewType(ref.Elem) {
			return finish(a.namedTypes["char"])
		}
		return finish(ref.Elem)
	}
	a.errorf(expr.Pos(), "indexing requires string, array, view, packed store, or reference type, got %s", objType)
	return finish(invalidType)
}

// analyzeGetExpr type-checks the `get` prefix: the optional analog of `try`.
//
//   - `get arr[i]`  — a bounds-checked container access. The bounds obligation is
//     discharged by the check `get` itself performs, so it never demands
//     Unsafe.UncheckedIndex; the absence path is the recovery (or propagation).
//   - `get opt`     — unwraps an Optional / nullable reference.
//
// With a trailing `else` the recovery supplies a fallback value / return / raise.
// Without `else`, `get` propagates absence by early-returning the enclosing
// function's None — which therefore must return an optional or nullable ref.
func (a *Analyzer) analyzeGetExpr(n *ast.GetExpr) Type {
	if n == nil {
		return invalidType
	}
	// Checked-index form. When the inner index already carries a value fallback
	// (the postfix parser grabbed `else <value>`), the access is total on its own;
	// otherwise the `get` itself is the bounds check, so pre-mark it proven so it
	// does not also demand an Unsafe.UncheckedIndex grant.
	idx, isIndex := n.Value.(*ast.IndexExpr)
	indexAlreadyTotal := isIndex && idx.Fallback != nil
	if isIndex {
		// The explicit `get` head owns this index's recovery, so it must not get
		// the bare-`else` deprecation warning.
		a.getWrappedIndexExprs[idx] = true
		if idx.Fallback == nil {
			a.indexBoundsProven[idx] = true
		}
	}
	// `get arr[a:b]` is a bounds-checked slice: the `get` performs the runtime
	// bounds test, yielding the bounded view on success and taking the recovery /
	// propagation path when the bounds fall outside the source.
	slice, isSlice := n.Value.(*ast.SliceExpr)
	if isSlice {
		a.checkedSliceExprs[slice] = true
	}

	valueType := a.analyzeExpr(n.Value)
	if IsInvalidType(valueType) {
		return invalidType
	}

	if indexAlreadyTotal {
		// `get arr[i] else <value>`: the index fallback supplies the absence path,
		// so the access is total and there is nothing left for `get` to propagate.
		return valueType
	}

	var resultType Type
	switch {
	case isIndex:
		// The element type produced by analyzeIndexExpr is the unwrapped result;
		// `get` performs the bounds check, so the access is total.
		resultType = valueType
	case isSlice:
		// The bounded view is the result; `get` performs the runtime bounds
		// check, taking the recovery / propagation path when out of range.
		resultType = valueType
	case isOptionalValueType(valueType):
		resultType = optionalValueInner(valueType)
	default:
		if refType, ok := valueType.(*RefType); ok && refType.State == RefStateNullable {
			resultType = cloneRefTypeWithState(refType, RefStateNonNull)
			break
		}
		a.errorf(n.Pos(), "get requires an optional, a nullable reference, or a container index, got %s", valueType)
		return invalidType
	}

	recovery := recoveryClauseForExpr(n.Recovery, n.Fallback, n.Position)
	if recovery == nil {
		// Propagation form: absence early-returns the enclosing function's None.
		if !a.currentFuncReturnAcceptsAbsence() {
			a.errorf(n.Pos(), "get without else requires the enclosing function to return an optional or nullable reference")
		}
		return resultType
	}
	fallbackType := a.analyzeRecoveryClause(recovery, resultType, nil, "get fallback")
	if !IsNeverType(fallbackType) && !AssignableTo(resultType, fallbackType) {
		a.errorf(n.Pos(), "get fallback expects %s, got %s", resultType, fallbackType)
		a.reportShapeMismatchNotes(n.Pos(), resultType, fallbackType)
	}
	return resultType
}

func isOptionalValueType(t Type) bool {
	_, ok := t.(*OptionalType)
	return ok
}

func optionalValueInner(t Type) Type {
	if opt, ok := t.(*OptionalType); ok && opt != nil {
		return opt.Value
	}
	return t
}

// currentFuncReturnAcceptsAbsence reports whether the enclosing function's return
// type can carry a None — i.e. an Optional or a nullable reference — which is the
// requirement for the propagating (`else`-less) form of `get`.
func (a *Analyzer) currentFuncReturnAcceptsAbsence() bool {
	if a == nil || a.currentFuncType == nil {
		return false
	}
	switch ret := a.currentFuncType.Return.(type) {
	case *OptionalType:
		return true
	case *RefType:
		return ret != nil && ret.State == RefStateNullable
	}
	return false
}

func (a *Analyzer) tupleIndexResultType(tuple *TupleType, indexExpr ast.Expr) (Type, bool) {
	if tuple == nil {
		return nil, false
	}
	value, ok := a.evalConstExpr(indexExpr)
	if !ok || value.Kind != ConstInt {
		a.errorf(indexExpr.Pos(), "tuple index must be a compile-time integer")
		return invalidType, false
	}
	if value.Int < 0 || value.Int >= int64(len(tuple.Fields)) {
		a.errorf(indexExpr.Pos(), "constant tuple index %d out of bounds for %s", value.Int, tuple)
		return invalidType, false
	}
	return tuple.Fields[value.Int].Type, true
}

func safeIndexFallbackOperandType(t Type) bool {
	switch tt := StripAggregateStateType(t).(type) {
	case *ArrayType, *DArrayType, *DArrayViewType, *PackedEnumStoreType:
		return true
	case *RefType:
		if tt.State != RefStateNonNull {
			return false
		}
		switch StripAggregateStateType(tt.Elem).(type) {
		case *ArrayType, *DArrayType, *DArrayViewType, *PackedEnumStoreType:
			return true
		}
	}
	return false
}

func (a *Analyzer) analyzeDenseNodeKeyIndexExpr(expr *ast.IndexExpr, objType Type, keyEnum *EnumType) (Type, bool) {
	if expr == nil || objType == nil || keyEnum == nil {
		return nil, false
	}
	keyInfo, ok := a.denseNodeKeyInfoForExpr(expr.Index)
	if !ok || keyInfo.Enum == nil || keyInfo.StoreRoot == nil {
		a.errorf(expr.Index.Pos(), "node-key indexing requires a key produced by dense_key from an exact frozen store root")
		return invalidType, true
	}
	if keyInfo.Enum != keyEnum {
		a.errorf(expr.Index.Pos(), "node key enum %q does not match indexed object enum %q", keyInfo.Enum.Name, keyEnum.Name)
		return invalidType, true
	}
	storeRoot, storePath, storeType, ok := a.resolveFrozenPackedStoreRootPath(expr.Object)
	if ok && storeType != nil && storeType.Enum != nil {
		if storeType.Enum != keyEnum {
			a.errorf(expr.Pos(), "node key enum %q does not match frozen store %q", keyEnum.Name, storeType.Enum.Name)
			return invalidType, true
		}
		if !samePackedStoreRootPath(storeRoot, storePath, keyInfo.StoreRoot, keyInfo.StorePath) {
			a.errorf(expr.Pos(), "node key and frozen store must share the same exact frozen store root")
			return invalidType, true
		}
		return keyEnum, true
	}
	if tableInfo, ok := a.nodeTableInfoForExpr(expr.Object); ok && tableInfo.Enum != nil && tableInfo.StoreRoot != nil {
		if tableInfo.Enum != keyEnum {
			a.errorf(expr.Pos(), "node key enum %q does not match node table %q", keyEnum.Name, tableInfo.Enum.Name)
			return invalidType, true
		}
		if !samePackedStoreRootPath(tableInfo.StoreRoot, tableInfo.StorePath, keyInfo.StoreRoot, keyInfo.StorePath) {
			a.errorf(expr.Pos(), "node key and node table must share the same exact frozen store root")
			return invalidType, true
		}
		return tableInfo.Elem, true
	}
	if refType, ok := objType.(*RefType); ok && refType.State == RefStateNonNull {
		if storeType, ok := refType.Elem.(*PackedEnumStoreType); ok && storeType != nil && storeType.Enum != nil {
			a.errorf(expr.Object.Pos(), "node-key packed-store indexing requires the exact frozen store root value, not a store reference")
			return invalidType, true
		}
		if _, _, ok := NodeTableParts(refType.Elem); ok {
			a.errorf(expr.Object.Pos(), "node-key table indexing requires the exact node-table value, not a node-table reference")
			return invalidType, true
		}
	}
	a.errorf(expr.Pos(), "node-key indexing requires Expr.Store[Frozen] or NodeTable[Expr, T], got %s", objType)
	return invalidType, true
}

func (a *Analyzer) reportInvalidRegionUse(expr ast.Expr, valueType Type) {
	if expr == nil || valueType == nil {
		return
	}
	refState, ok := a.regionRefStateForExpr(expr)
	if !ok {
		return
	}
	if _, dep, invalid := firstInvalidRegionDependency(refState); invalid {
		label := "value"
		if _, isRef := valueType.(*RefType); isRef {
			label = "reference"
		}
		a.errorf(expr.Pos(), invalidatedRegionFactUseMessage(label, affineValueDisplayName(expr), dep.InvalidatedBy))
	}
}

func (a *Analyzer) reportBorrowedOwnerRefUseAfterConsume(expr ast.Expr, valueType Type) {
	refState, hasRefState := a.borrowedOwnerRefStateForExpr(expr)
	if !hasRefState || !refState.HasDirect {
		return
	}
	consumed, ok := a.lookupAffineValueStateForKey(refState.Direct)
	if !ok || consumed.ConsumedBy == "" {
		return
	}
	// A raw interior pointer aliased out of a Pooled handle (e.g. stashed in a struct
	// field, `holder.p`) dangles once the handle is released, regardless of the value's
	// own type. Genuine owner borrows keep their owner-typed diagnostic.
	if refState.RawInteriorAffineAlias {
		a.errorf(expr.Pos(), consumedFactUseMessage("interior reference", affineValueDisplayName(expr), consumed.ConsumedBy))
		return
	}
	if ownerType, ok := borrowableOwnerRefElemType(valueType); ok {
		a.errorf(expr.Pos(), consumedFactUseMessage(affineHandleKind(ownerType), affineValueDisplayName(expr), consumed.ConsumedBy))
	}
}

func (a *Analyzer) analyzeSliceExpr(expr *ast.SliceExpr) Type {
	objType := a.analyzeExpr(expr.Object)
	indexExpected := a.namedTypes["usize"]
	if indexExpected == nil {
		indexExpected = builtinUsizeType()
	}
	startType := a.analyzeValueExpr(expr.Start, indexExpected)
	endType := a.analyzeValueExpr(expr.End, indexExpected)
	if !IsNumericType(startType) {
		a.errorf(expr.Start.Pos(), "slice start must be numeric, got %s", startType)
	} else if !IsIntegralStorageType(startType) {
		a.errorf(expr.Start.Pos(), "slice start must be integral, got %s", startType)
	}
	if !IsNumericType(endType) {
		a.errorf(expr.End.Pos(), "slice end must be numeric, got %s", endType)
	} else if !IsIntegralStorageType(endType) {
		a.errorf(expr.End.Pos(), "slice end must be integral, got %s", endType)
	}
	if array, ok := objType.(*ArrayType); ok {
		a.checkConstantArraySliceBounds(array, expr.Start, expr.End)
		if isStringArrayType(array) {
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
		return &ViewType{Elem: array.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "view"}
	}
	if view, ok := objType.(*DArrayType); ok {
		return &DArrayViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "dview"}
	}
	if view, ok := objType.(*ViewType); ok {
		return &ViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "view"}
	}
	if view, ok := objType.(*DArrayViewType); ok {
		return &DArrayViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: view.SurfaceName}
	}
	if storeType, ok := objType.(*PackedEnumStoreType); ok && storeType.Enum != nil {
		return &DArrayViewType{Elem: storeType.Enum, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "packedview"}
	}
	if cstr, ok := objType.(*DStrType); ok {
		_ = cstr
		return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
	}
	if isStringViewType(objType) {
		return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			a.errorf(expr.Pos(), "slicing requires proven non-null reference, got %s", objType)
			return invalidType
		}
		if array, ok := ref.Elem.(*ArrayType); ok {
			a.checkConstantArraySliceBounds(array, expr.Start, expr.End)
			if isStringArrayType(array) {
				return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
			}
			return &ViewType{Elem: array.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "view"}
		}
		if view, ok := ref.Elem.(*DArrayType); ok {
			return &DArrayViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "dview"}
		}
		if view, ok := ref.Elem.(*ViewType); ok {
			return &ViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "view"}
		}
		if view, ok := ref.Elem.(*DArrayViewType); ok {
			return &DArrayViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: view.SurfaceName}
		}
		if storeType, ok := ref.Elem.(*PackedEnumStoreType); ok && storeType.Enum != nil {
			return &DArrayViewType{Elem: storeType.Enum, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "packedview"}
		}
		if _, ok := ref.Elem.(*DStrType); ok {
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
		if isStringViewType(ref.Elem) {
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
	}
	a.errorf(expr.Pos(), "slicing requires string, array, view, or packed store type, got %s", objType)
	return invalidType
}
