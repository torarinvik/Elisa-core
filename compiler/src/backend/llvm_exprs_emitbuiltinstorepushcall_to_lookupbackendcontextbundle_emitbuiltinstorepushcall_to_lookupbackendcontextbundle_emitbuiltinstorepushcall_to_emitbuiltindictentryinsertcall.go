//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int elisacoreLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef elisa_coreAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef elisa_coreAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned elisa_coreMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void elisa_coreSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = elisa_coreAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, elisa_coreMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef elisa_coreCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = elisa_coreAliasMDString(ctx, domainName);
	return elisa_coreAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef elisa_coreCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = elisa_coreAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return elisa_coreAliasMDNode(ctx, operands, 2);
}

static void elisa_coreAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = elisa_coreCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = elisa_coreCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	elisa_coreSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		elisa_coreSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitBuiltinStorePushCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("store push requires an active in <arena>: scope")
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
	var rowIndexValue C.LLVMValueRef
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
		currentCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("store."+name+".push.count"))
		if i == 0 {
			rowIndexValue = currentCount
			rowIndexType = usizeType
		}
		neededValue := C.LLVMBuildAdd(s.builder, currentCount, C.LLVMConstInt(usizeLLVMType, 1, 0), cStringFree("store."+name+".push.needed"))
		if err := s.emitBuiltinDArrayEnsureCapacity(fieldPtr, darrayType, owner.arenaRef, neededValue, "store."+name+".push"); err != nil {
			return nil, nil, true, err
		}
		itemsPtr, err := s.emitBuiltinDArrayItemsPtr(fieldPtr, darrayType)
		if err != nil {
			return nil, nil, true, err
		}
		voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
		itemsValue := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("store."+name+".push.items"))
		elemLLVMType, err := s.g.lowerType(darrayType.Elem)
		if err != nil {
			return nil, nil, true, err
		}
		slotPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]C.LLVMValueRef{currentCount}), 1, cStringFree("store."+name+".push.slot"))
		C.LLVMBuildStore(s.builder, itemValue, slotPtr)
		C.LLVMBuildStore(s.builder, neededValue, countPtr)
	}
	resultType := builtinStorePushResultType(storeType, s.g.result.NamedTypes["u32"])
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
func (s *functionState) emitBuiltinStoreReserveCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("store reserve requires an active in <arena>: scope")
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
	neededValue, _, err := s.emitExpr(expr.Args[0], s.g.result.NamedTypes["usize"])
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
func (s *functionState) emitBuiltinStoreClearCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
		C.LLVMBuildStore(s.builder, C.LLVMConstInt(usizeLLVMType, 0, 0), countPtr)
	}
	return storePtr, builtinStoreResultRefType(storeType), true, nil
}
func (s *functionState) emitBuiltinStoreTruncateCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	limitValue, _, err := s.emitExpr(expr.Args[0], s.g.result.NamedTypes["usize"])
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
		currentCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("store."+name+".truncate.count"))
		shouldStore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), limitValue, currentCount, cStringFree("store."+name+".truncate.lt"))
		storeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("store."+name+".truncate.store"))
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("store."+name+".truncate.merge"))
		C.LLVMBuildCondBr(s.builder, shouldStore, storeBB, mergeBB)
		C.LLVMPositionBuilderAtEnd(s.builder, storeBB)
		C.LLVMBuildStore(s.builder, limitValue, countPtr)
		C.LLVMBuildBr(s.builder, mergeBB)
		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	}
	return storePtr, builtinStoreResultRefType(storeType), true, nil
}
func (s *functionState) emitBuiltinStoreRowsCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	value := C.LLVMGetUndef(rowViewLLVMType)
	value = C.LLVMBuildInsertValue(s.builder, value, storePtr, 0, cStringFree("store.rows.store"))
	return value, resultType, true, nil
}
func (s *functionState) emitBuiltinStoreRowsFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	value := C.LLVMGetUndef(rowViewLLVMType)
	value = C.LLVMBuildInsertValue(s.builder, value, storePtr, 0, cStringFree("soa.rows.store"))
	return value, resultType, true, nil
}
func (s *functionState) emitBuiltinSOACountFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	return countValue, s.g.result.NamedTypes["usize"], true, nil
}
func (s *functionState) emitBuiltinSOARowIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	rowIDType := &semantic.IDType{Tag: storeType, Storage: s.g.result.NamedTypes["u32"]}
	indexValue, _, err := s.emitExpr(expr.Index, rowIDType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
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
	rowValue := C.LLVMGetUndef(rowLLVMType)
	rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, storePtr, 0, cStringFree("soa.row.store"))
	rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, indexValue, 1, cStringFree("soa.row.index"))
	return rowValue, resultType, true, nil
}
func (s *functionState) emitBuiltinSOAValidCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	rowIDType := &semantic.IDType{Tag: storeType, Storage: s.g.result.NamedTypes["u32"]}
	rowValue, _, err := s.emitExpr(expr.Args[0], rowIDType)
	if err != nil {
		return nil, nil, true, err
	}
	rowValue, err = s.coerceValue(rowValue, rowIDType, s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	value := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), rowValue, countValue, cStringFree("soa.valid"))
	return value, s.g.result.NamedTypes["bool"], true, nil
}
func (s *functionState) emitReadableStoreReceiverPtr(receiver ast.Expr, receiverType semantic.Type, receiverRefType *semantic.RefType) (C.LLVMValueRef, error) {
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
	C.LLVMBuildStore(s.builder, value, tempAlloca)
	return tempAlloca, nil
}
func (s *functionState) emitBuiltinStoreCountValue(storePtr C.LLVMValueRef, storeType *semantic.StructType, name string) (C.LLVMValueRef, error) {
	if storeType == nil || len(storeType.StoreFieldOrder) == 0 {
		usizeType, err := s.g.lowerType(s.g.result.NamedTypes["usize"])
		if err != nil {
			return nil, err
		}
		return C.LLVMConstInt(usizeType, 0, 0), nil
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
func (s *functionState) emitBuiltinDictReceiverValue(receiver ast.Expr, receiverType semantic.Type) (C.LLVMValueRef, *semantic.DictType, error) {
	dictType, receiverRefType, ok := builtinDictReceiverType(receiverType)
	if !ok || dictType == nil {
		return nil, nil, fmt.Errorf("dict receiver is not a dict")
	}
	if receiverRefType != nil {
		value, _, err := s.emitExpr(receiver, receiverRefType)
		return value, dictType, err
	}
	ptr, _, err := s.emitAddress(receiver)
	return ptr, dictType, err
}
func (s *functionState) emitBuiltinDictEntryValue(entryExpr ast.Expr, entryType *semantic.DictEntryType, receiverRefType *semantic.RefType) (C.LLVMValueRef, error) {
	if receiverRefType != nil {
		entryPtr, _, err := s.emitExpr(entryExpr, receiverRefType)
		if err != nil {
			return nil, err
		}
		entryLLVMType, err := s.g.lowerType(entryType)
		if err != nil {
			return nil, err
		}
		return C.LLVMBuildLoad2(s.builder, entryLLVMType, entryPtr, cStringFree("dict.entry.load")), nil
	}
	entryValue, _, err := s.emitExpr(entryExpr, entryType)
	return entryValue, err
}
func (s *functionState) emitBuiltinDictEntryCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "entry" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	resultType, ok := s.exprType(expr).(*semantic.DictEntryType)
	if !ok || resultType == nil || resultType.Dict == nil {
		return nil, nil, true, fmt.Errorf("dict entry call missing semantic entry type")
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("dict entry expects 1 argument, got %d", len(expr.Args))
	}
	dictValue, dictType, err := s.emitBuiltinDictReceiverValue(fieldExpr.Object, s.exprType(fieldExpr.Object))
	if err != nil {
		return nil, nil, true, err
	}
	keyValue, _, err := s.emitExpr(expr.Args[0], dictType.Key)
	if err != nil {
		return nil, nil, true, err
	}
	getCallee, getType, err := s.ensureRuntimeFunction("arena_dict_get", map[string]semantic.Type{"K": dictType.Key, "T": dictType.Value})
	if err != nil {
		return nil, nil, true, err
	}
	getLLVMType, err := s.g.lowerFunctionType(getType)
	if err != nil {
		return nil, nil, true, err
	}
	valuePtr := s.buildCall(getLLVMType, getCallee, []C.LLVMValueRef{dictValue, keyValue}, "dict.entry.get")
	entryLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	entryValue := C.LLVMGetUndef(entryLLVMType)
	entryValue = C.LLVMBuildInsertValue(s.builder, entryValue, dictValue, 0, cStringFree("dict.entry.dict"))
	entryValue = C.LLVMBuildInsertValue(s.builder, entryValue, keyValue, 1, cStringFree("dict.entry.key"))
	entryValue = C.LLVMBuildInsertValue(s.builder, entryValue, valuePtr, 2, cStringFree("dict.entry.value"))
	return entryValue, resultType, true, nil
}
func (s *functionState) emitBuiltinDictEntryInsertCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "insert" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	entryType, receiverRefType, ok := builtinDictEntryReceiverType(s.exprType(fieldExpr.Object))
	if !ok || entryType == nil || entryType.Dict == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("dict entry insert expects 1 argument, got %d", len(expr.Args))
	}
	// Region-parameterized dict: prefer the arena of the dict's own region (if
	// live in s.regions) over the ambient scope, mirroring darray push. Today a
	// dict's region == its ambient scope, so this is behavior-preserving; it
	// becomes load-bearing once insert happens through a region-param dict.
	owner, ok := s.regionArenaOwner(entryType.Dict.Region)
	if !ok {
		owner, ok = s.lookupTreeAllocOwner()
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("dict entry insert requires an active in <arena>: scope")
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "dict.entry.insert.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	var entryPtr C.LLVMValueRef
	var err error
	if receiverRefType != nil {
		entryPtr, _, err = s.emitExpr(fieldExpr.Object, receiverRefType)
	} else {
		entryPtr, _, err = s.emitAddress(fieldExpr.Object)
		if err != nil {
			entryPtr = nil
			err = nil
		}
	}
	entryLLVMType, err := s.g.lowerType(entryType)
	if err != nil {
		return nil, nil, true, err
	}
	dictRefType := &semantic.RefType{Elem: entryType.Dict, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	dictRefLLVMType, err := s.g.lowerType(dictRefType)
	if err != nil {
		return nil, nil, true, err
	}
	keyLLVMType, err := s.g.lowerType(entryType.Dict.Key)
	if err != nil {
		return nil, nil, true, err
	}
	valueRefType := builtinDictEntryValueRefType(entryType.Dict)
	valueRefLLVMType, err := s.g.lowerType(valueRefType)
	if err != nil {
		return nil, nil, true, err
	}
	var dictValue, keyValue, cachedValue C.LLVMValueRef
	var valuePtrPtr C.LLVMValueRef
	if entryPtr != nil {
		dictPtr := C.LLVMBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 0, cStringFree("dict.entry.insert.dict.ptr"))
		keyPtr := C.LLVMBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 1, cStringFree("dict.entry.insert.key.ptr"))
		valuePtrPtr = C.LLVMBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 2, cStringFree("dict.entry.insert.value.ptr"))
		dictValue = C.LLVMBuildLoad2(s.builder, dictRefLLVMType, dictPtr, cStringFree("dict.entry.insert.dict"))
		keyValue = C.LLVMBuildLoad2(s.builder, keyLLVMType, keyPtr, cStringFree("dict.entry.insert.key"))
		cachedValue = C.LLVMBuildLoad2(s.builder, valueRefLLVMType, valuePtrPtr, cStringFree("dict.entry.insert.cached"))
	} else {
		entryValue, err := s.emitBuiltinDictEntryValue(fieldExpr.Object, entryType, receiverRefType)
		if err != nil {
			return nil, nil, true, err
		}
		dictValue = C.LLVMBuildExtractValue(s.builder, entryValue, 0, cStringFree("dict.entry.insert.dict"))
		keyValue = C.LLVMBuildExtractValue(s.builder, entryValue, 1, cStringFree("dict.entry.insert.key"))
		cachedValue = C.LLVMBuildExtractValue(s.builder, entryValue, 2, cStringFree("dict.entry.insert.cached"))
	}
	nullValue := C.LLVMConstNull(valueRefLLVMType)
	hasCached := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), cachedValue, nullValue, cStringFree("dict.entry.insert.has"))
	currentBB := C.LLVMGetInsertBlock(s.builder)
	nonNullBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.insert.nonnull"))
	insertBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.insert.insert"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.insert.merge"))
	C.LLVMBuildCondBr(s.builder, hasCached, nonNullBB, insertBB)
	C.LLVMPositionBuilderAtEnd(s.builder, nonNullBB)
	C.LLVMBuildBr(s.builder, mergeBB)
	nonNullEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMPositionBuilderAtEnd(s.builder, insertBB)
	insertedArg, _, err := s.emitExpr(expr.Args[0], entryType.Dict.Value)
	if err != nil {
		return nil, nil, true, err
	}
	putCallee, putType, err := s.ensureRuntimeFunction(s.dictMutationHelperName("arena_dict_put"), map[string]semantic.Type{"K": entryType.Dict.Key, "T": entryType.Dict.Value})
	if err != nil {
		return nil, nil, true, err
	}
	putLLVMType, err := s.g.lowerFunctionType(putType)
	if err != nil {
		return nil, nil, true, err
	}
	insertedValue := s.buildCall(putLLVMType, putCallee, []C.LLVMValueRef{owner.arenaRef, dictValue, keyValue, insertedArg}, "dict.entry.insert.result")
	if valuePtrPtr != nil {
		C.LLVMBuildStore(s.builder, insertedValue, valuePtrPtr)
	}
	C.LLVMBuildBr(s.builder, mergeBB)
	insertEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, valueRefLLVMType, cStringFree("dict.entry.insert.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr([]C.LLVMValueRef{cachedValue, insertedValue}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{nonNullEnd, insertEnd}), 2)
	_ = currentBB
	return phi, valueRefType, true, nil
}

func (s *functionState) emitBuiltinDictRegionMutationCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	method := fieldExpr.Field
	if method != "put" && method != "get_or_insert" && method != "reserve" {
		return nil, nil, false, nil
	}
	dictType, _, ok := builtinDictReceiverType(s.exprType(fieldExpr.Object))
	if !ok || dictType == nil {
		return nil, nil, false, nil
	}
	expectedArgs := 2
	if method == "reserve" {
		expectedArgs = 1
	}
	if len(expr.Args) != expectedArgs {
		return nil, nil, true, fmt.Errorf("dict %s expects %d arguments, got %d", method, expectedArgs, len(expr.Args))
	}
	owner, ok := s.regionArenaOwner(dictType.Region)
	if !ok {
		owner, ok = s.lookupTreeAllocOwner()
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("dict %s requires an active in <arena>: scope", method)
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "dict."+method+".owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	dictValue, dictType, err := s.emitBuiltinDictReceiverValue(fieldExpr.Object, s.exprType(fieldExpr.Object))
	if err != nil {
		return nil, nil, true, err
	}
	if method == "reserve" {
		helperBase := "arena_dict_reserve"
		callee, helperType, err := s.ensureRuntimeFunction(s.dictMutationHelperName(helperBase), map[string]semantic.Type{"K": dictType.Key, "T": dictType.Value})
		if err != nil {
			return nil, nil, true, err
		}
		llvmType, err := s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeType := s.g.result.NamedTypes["usize"]
		minCapacity, _, err := s.emitExpr(expr.Args[0], usizeType)
		if err != nil {
			return nil, nil, true, err
		}
		value := s.buildCall(llvmType, callee, []C.LLVMValueRef{owner.arenaRef, dictValue, minCapacity}, "")
		return value, s.g.result.NamedTypes["void"], true, nil
	}
	keyValue, _, err := s.emitExpr(expr.Args[0], dictType.Key)
	if err != nil {
		return nil, nil, true, err
	}
	insertedArg, _, err := s.emitExpr(expr.Args[1], dictType.Value)
	if err != nil {
		return nil, nil, true, err
	}
	helperBase := "arena_dict_put"
	resultName := "dict.put.result"
	if method == "get_or_insert" {
		helperBase = "arena_dict_get_or_insert"
		resultName = "dict.get_or_insert.result"
	}
	callee, helperType, err := s.ensureRuntimeFunction(s.dictMutationHelperName(helperBase), map[string]semantic.Type{"K": dictType.Key, "T": dictType.Value})
	if err != nil {
		return nil, nil, true, err
	}
	llvmType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, true, err
	}
	value := s.buildCall(llvmType, callee, []C.LLVMValueRef{owner.arenaRef, dictValue, keyValue, insertedArg}, resultName)
	return value, builtinDictEntryValueRefType(dictType), true, nil
}
