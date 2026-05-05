package semantic

import (
	"llcontext/src/ast"
)

func (a *Analyzer) analyzeIndexExpr(expr *ast.IndexExpr) Type {
	objType := a.analyzeExpr(expr.Object)
	indexExpected := a.namedTypes["usize"]
	if indexExpected == nil {
		indexExpected = builtinUsizeType()
	}
	indexType := a.analyzeValueExpr(expr.Index, indexExpected)
	finish := func(result Type) Type {
		if expr.Fallback != nil {
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
	if _, ok := NodeKeyEnumType(indexType); !ok {
		if !IsNumericType(indexType) {
			a.errorf(expr.Index.Pos(), "index must be numeric, got %s", indexType)
		} else if !IsIntegralStorageType(indexType) {
			a.errorf(expr.Index.Pos(), "index must be integral, got %s", indexType)
		}
	}
	if keyEnum, ok := NodeKeyEnumType(indexType); ok {
		if result, handled := a.analyzeDenseNodeKeyIndexExpr(expr, objType, keyEnum); handled {
			return finish(result)
		}
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

func safeIndexFallbackOperandType(t Type) bool {
	switch tt := StripAggregateStateType(t).(type) {
	case *ArrayType, *DArrayType, *ViewType, *DArrayViewType, *PackedEnumStoreType:
		return true
	case *RefType:
		if tt.State != RefStateNonNull {
			return false
		}
		switch StripAggregateStateType(tt.Elem).(type) {
		case *ArrayType, *DArrayType, *ViewType, *DArrayViewType, *PackedEnumStoreType:
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
	ownerType, ok := borrowableOwnerRefElemType(valueType)
	if !ok {
		return
	}
	key, ok := a.lookupBorrowedOwnerRefKey(expr)
	if !ok {
		return
	}
	state, ok := a.lookupAffineValueStateForKey(key)
	if !ok || state.ConsumedBy == "" {
		return
	}
	a.errorf(expr.Pos(), consumedFactUseMessage(affineHandleKind(ownerType), affineValueDisplayName(expr), state.ConsumedBy))
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
		return &ViewType{Elem: array.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
	}
	if view, ok := objType.(*DArrayType); ok {
		return &DArrayViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "dview"}
	}
	if view, ok := objType.(*ViewType); ok {
		return &ViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
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
			return &ViewType{Elem: array.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
		if view, ok := ref.Elem.(*DArrayType); ok {
			return &DArrayViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End), SurfaceName: "dview"}
		}
		if view, ok := ref.Elem.(*ViewType); ok {
			return &ViewType{Elem: view.Elem, Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
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
