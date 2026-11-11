//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitBuiltinStorePushCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != len(storeType.StoreFieldOrder) {
		return nil, nil, true, fmt.Errorf("store push expects %d arguments, got %d", len(storeType.StoreFieldOrder), len(expr.Args))
	}
	owner, ok := s.lookupTreeAllocOwner()
	if !ok {
		owner, ok, _ = s.permGrowthOwner(expr)
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("%s", semantic.NoGrowthRegionMessage("store push"))
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "store.push.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	storePtr, loweredStoreType, err := s.emitBuiltinStoreReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	var rowIndexValue llvmIRValueRef
	var rowIndexType semantic.Type
	for i, name := range storeType.StoreFieldOrder {
		fieldPtr, darrayType, err := s.emitBuiltinStoreFieldDArrayPtr(storePtr, loweredStoreType, name)
		if err != nil {
			return nil, nil, true, err
		}
		itemValue, _, err := s.emitExpr(expr.Args[i], darrayType.Elem)
		if err != nil {
			return nil, nil, true, err
		}
		countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(fieldPtr, darrayType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, nil, true, err
		}
		currentCount := llvmBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("store."+name+".push.count"))
		if i == 0 {
			rowIndexValue = currentCount
			rowIndexType = usizeType
		}
		neededValue, err := s.emitCheckedUSizeAdd(currentCount, llvmConstInt(usizeLLVMType, 1, 0), "store."+name+".push.needed")
		if err != nil {
			return nil, nil, true, err
		}
		if err := s.emitBuiltinDArrayEnsureCapacity(fieldPtr, darrayType, owner.arenaRef, neededValue, "store."+name+".push"); err != nil {
			return nil, nil, true, err
		}
		itemsPtr, err := s.emitBuiltinDArrayItemsPtr(fieldPtr, darrayType)
		if err != nil {
			return nil, nil, true, err
		}
		voidPtrType := llvmPointerTypeInContext(s.g.context, 0)
		itemsValue := llvmBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("store."+name+".push.items"))
		elemLLVMType, err := s.g.lowerType(darrayType.Elem)
		if err != nil {
			return nil, nil, true, err
		}
		slotPtr := llvmBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]llvmIRValueRef{currentCount}), 1, cStringFree("store."+name+".push.slot"))
		llvmBuildStore(s.builder, itemValue, slotPtr)
		llvmBuildStore(s.builder, neededValue, countPtr)
	}
	resultType := builtinStorePushResultType(storeType, s.g.namedType("u32"))
	if storeType.StoreDecl != nil && storeType.StoreDecl.Soa {
		if rowIndexValue == nil {
			return nil, nil, true, fmt.Errorf("soa push requires at least one field")
		}
		rowValue, err := s.coerceValue(rowIndexValue, rowIndexType, resultType)
		if err != nil {
			return nil, nil, true, err
		}
		return rowValue, resultType, true, nil
	}
	return storePtr, resultType, true, nil
}
func (s *functionState) emitBuiltinStoreReserveCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "reserve" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("store reserve expects 1 argument, got %d", len(expr.Args))
	}
	owner, ok := s.lookupTreeAllocOwner()
	if !ok {
		owner, ok, _ = s.permGrowthOwner(expr)
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("%s", semantic.NoGrowthRegionMessage("store reserve"))
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "store.reserve.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	storePtr, loweredStoreType, err := s.emitBuiltinStoreReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	neededValue, _, err := s.emitExpr(expr.Args[0], s.g.namedType("usize"))
	if err != nil {
		return nil, nil, true, err
	}
	for _, name := range storeType.StoreFieldOrder {
		fieldPtr, darrayType, err := s.emitBuiltinStoreFieldDArrayPtr(storePtr, loweredStoreType, name)
		if err != nil {
			return nil, nil, true, err
		}
		if err := s.emitBuiltinDArrayEnsureCapacity(fieldPtr, darrayType, owner.arenaRef, neededValue, "store."+name+".reserve"); err != nil {
			return nil, nil, true, err
		}
	}
	return storePtr, builtinStoreResultRefType(storeType), true, nil
}
func (s *functionState) emitBuiltinStoreClearCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "clear" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 0 {
		return nil, nil, true, fmt.Errorf("store clear expects 0 arguments, got %d", len(expr.Args))
	}
	storePtr, loweredStoreType, err := s.emitBuiltinStoreReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	for _, name := range storeType.StoreFieldOrder {
		fieldPtr, darrayType, err := s.emitBuiltinStoreFieldDArrayPtr(storePtr, loweredStoreType, name)
		if err != nil {
			return nil, nil, true, err
		}
		countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(fieldPtr, darrayType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, nil, true, err
		}
		llvmBuildStore(s.builder, llvmConstInt(usizeLLVMType, 0, 0), countPtr)
	}
	return storePtr, builtinStoreResultRefType(storeType), true, nil
}
func (s *functionState) emitBuiltinStoreTruncateCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "truncate" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("store truncate expects 1 argument, got %d", len(expr.Args))
	}
	storePtr, loweredStoreType, err := s.emitBuiltinStoreReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	limitValue, _, err := s.emitExpr(expr.Args[0], s.g.namedType("usize"))
	if err != nil {
		return nil, nil, true, err
	}
	for _, name := range storeType.StoreFieldOrder {
		fieldPtr, darrayType, err := s.emitBuiltinStoreFieldDArrayPtr(storePtr, loweredStoreType, name)
		if err != nil {
			return nil, nil, true, err
		}
		countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(fieldPtr, darrayType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, nil, true, err
		}
		currentCount := llvmBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("store."+name+".truncate.count"))
		shouldStore := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), limitValue, currentCount, cStringFree("store."+name+".truncate.lt"))
		storeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("store."+name+".truncate.store"))
		mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("store."+name+".truncate.merge"))
		llvmBuildCondBr(s.builder, shouldStore, storeBB, mergeBB)
		llvmPositionBuilderAtEnd(s.builder, storeBB)
		llvmBuildStore(s.builder, limitValue, countPtr)
		llvmBuildBr(s.builder, mergeBB)
		llvmPositionBuilderAtEnd(s.builder, mergeBB)
	}
	return storePtr, builtinStoreResultRefType(storeType), true, nil
}
func (s *functionState) emitBuiltinStoreRowsCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "rows" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 0 {
		return nil, nil, true, fmt.Errorf("store rows expects 0 arguments, got %d", len(expr.Args))
	}
	storePtr, err := s.emitReadableStoreReceiverPtr(fieldExpr.Object, receiverType, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	resultType, ok := s.exprType(expr).(*semantic.StoreRowsViewType)
	if !ok || resultType == nil {
		resultType = &semantic.StoreRowsViewType{Store: storeType}
	}
	rowViewLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	value := llvmGetUndef(rowViewLLVMType)
	value = llvmBuildInsertValue(s.builder, value, storePtr, 0, cStringFree("store.rows.store"))
	return value, resultType, true, nil
}
func (s *functionState) emitBuiltinStoreRowsFieldExpr(expr *ast.FieldExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Field != "rows" || expr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(expr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil || storeType.StoreDecl == nil || !storeType.StoreDecl.Soa {
		return nil, nil, false, nil
	}
	storePtr, err := s.emitReadableStoreReceiverPtr(expr.Object, receiverType, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	resultType, ok := s.exprType(expr).(*semantic.StoreRowsViewType)
	if !ok || resultType == nil {
		resultType = &semantic.StoreRowsViewType{Store: storeType}
	}
	rowViewLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	value := llvmGetUndef(rowViewLLVMType)
	value = llvmBuildInsertValue(s.builder, value, storePtr, 0, cStringFree("soa.rows.store"))
	return value, resultType, true, nil
}
func (s *functionState) emitBuiltinSOACountFieldExpr(expr *ast.FieldExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Field != "count" || expr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(expr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil || storeType.StoreDecl == nil || !storeType.StoreDecl.Soa {
		return nil, nil, false, nil
	}
	storePtr, err := s.emitReadableStoreReceiverPtr(expr.Object, receiverType, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	countValue, err := s.emitBuiltinStoreCountValue(storePtr, storeType, "soa.count")
	if err != nil {
		return nil, nil, true, err
	}
	return countValue, s.g.namedType("usize"), true, nil
}
func (s *functionState) emitBuiltinSOARowIndexExpr(expr *ast.IndexExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Fallback != nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(expr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil || storeType.StoreDecl == nil || !storeType.StoreDecl.Soa {
		return nil, nil, false, nil
	}
	storePtr, err := s.emitReadableStoreReceiverPtr(expr.Object, receiverType, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	rowIDType := &semantic.IDType{Tag: storeType, Storage: s.g.namedType("u32")}
	indexValue, _, err := s.emitExpr(expr.Index, rowIDType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.namedType("usize")
	indexValue, err = s.coerceValue(indexValue, rowIDType, usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	resultType, ok := s.exprType(expr).(*semantic.StoreRowViewType)
	if !ok || resultType == nil {
		resultType = &semantic.StoreRowViewType{Store: storeType}
	}
	rowLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	rowValue := llvmGetUndef(rowLLVMType)
	rowValue = llvmBuildInsertValue(s.builder, rowValue, storePtr, 0, cStringFree("soa.row.store"))
	rowValue = llvmBuildInsertValue(s.builder, rowValue, indexValue, 1, cStringFree("soa.row.index"))
	return rowValue, resultType, true, nil
}
func (s *functionState) emitBuiltinSOAValidCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "valid" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil || storeType.StoreDecl == nil || !storeType.StoreDecl.Soa {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("soa valid expects 1 argument, got %d", len(expr.Args))
	}
	storePtr, err := s.emitReadableStoreReceiverPtr(fieldExpr.Object, receiverType, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	countValue, err := s.emitBuiltinStoreCountValue(storePtr, storeType, "soa.valid.count")
	if err != nil {
		return nil, nil, true, err
	}
	rowIDType := &semantic.IDType{Tag: storeType, Storage: s.g.namedType("u32")}
	rowValue, _, err := s.emitExpr(expr.Args[0], rowIDType)
	if err != nil {
		return nil, nil, true, err
	}
	rowValue, err = s.coerceValue(rowValue, rowIDType, s.g.namedType("usize"))
	if err != nil {
		return nil, nil, true, err
	}
	value := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), rowValue, countValue, cStringFree("soa.valid"))
	return value, s.g.namedType("bool"), true, nil
}
func (s *functionState) emitReadableStoreReceiverPtr(receiver ast.Expr, receiverType semantic.Type, receiverRefType *semantic.RefType) (llvmIRValueRef, error) {
	if receiverRefType != nil {
		ptr, _, err := s.emitExpr(receiver, receiverRefType)
		return ptr, err
	}
	if ptr, _, err := s.emitValueAddress(receiver); err == nil {
		return ptr, nil
	}
	value, _, err := s.emitExpr(receiver, receiverType)
	if err != nil {
		return nil, err
	}
	tempName := s.g.nextSyntheticName("store.rows.tmp.")
	tempAlloca, err := s.createEntryAlloca(tempName, receiverType)
	if err != nil {
		return nil, err
	}
	llvmBuildStore(s.builder, value, tempAlloca)
	return tempAlloca, nil
}
func (s *functionState) emitBuiltinStoreCountValue(storePtr llvmIRValueRef, storeType *semantic.StructType, name string) (llvmIRValueRef, error) {
	if storeType == nil || len(storeType.StoreFieldOrder) == 0 {
		usizeType, err := s.g.lowerType(s.g.namedType("usize"))
		if err != nil {
			return nil, err
		}
		return llvmConstInt(usizeType, 0, 0), nil
	}
	firstField := storeType.StoreFieldOrder[0]
	columnPtr, darrayType, err := s.emitBuiltinStoreFieldDArrayPtr(storePtr, storeType, firstField)
	if err != nil {
		return nil, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(columnPtr, darrayType)
	if err != nil {
		return nil, err
	}
	return s.loadValue(countPtr, usizeType, name)
}
