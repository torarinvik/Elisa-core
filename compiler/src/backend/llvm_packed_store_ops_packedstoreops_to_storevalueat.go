//go:build cgo

package backend

/*
#include <llvm-c/Core.h>

static int llcontextConstIntGetZExtValue(LLVMValueRef Value, unsigned long long* Out) {
	if (LLVMIsAConstantInt(Value) == NULL) {
		return 0;
	}
	*Out = LLVMConstIntGetZExtValue(Value);
	return 1;
}
*/
import "C"

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/semantic"
)

type packedStoreOps struct {
	s          *functionState
	storeValue C.LLVMValueRef
	storeType  *semantic.PackedEnumStoreType
}

func (s *functionState) packedStoreOpsFromBinding(store *packedStoreBinding) (*packedStoreOps, bool) {
	if store == nil || store.typ == nil {
		return nil, false
	}
	return &packedStoreOps{s: s, storeValue: store.value, storeType: store.typ}, true
}
func (s *functionState) packedStoreOpsFromExpr(expr ast.Expr) (*packedStoreOps, bool, error) {
	if expr == nil {
		return nil, false, nil
	}
	if _, ok := packedStoreOperandType(s.exprType(expr)); !ok {
		return nil, false, nil
	}
	storeValue, storeType, err := s.emitPackedStoreValueFromExpr(expr)
	if err != nil {
		return nil, false, err
	}
	if storeType == nil || storeType.Enum == nil {
		return nil, false, nil
	}
	return &packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}, true, nil
}
func (ops *packedStoreOps) voidRefType() *semantic.RefType {
	if ops == nil || ops.s == nil || ops.s.g == nil {
		return &semantic.RefType{}
	}
	if ops.s.g.cachedVoidRefType == nil {
		ops.s.g.cachedVoidRefType = &semantic.RefType{
			Elem:            ops.s.g.result.NamedTypes["void"],
			State:           semantic.RefStateNonNull,
			Storage:         semantic.RefStorageAny,
			ExplicitStorage: true,
		}
	}
	return ops.s.g.cachedVoidRefType
}
func (ops *packedStoreOps) arenaRefType() *semantic.RefType {
	if ops == nil || ops.s == nil || ops.s.g == nil {
		return &semantic.RefType{}
	}
	if ops.s.g.cachedArenaRefType == nil {
		ops.s.g.cachedArenaRefType = &semantic.RefType{
			Elem:            ops.s.g.result.NamedTypes["Arena"],
			State:           semantic.RefStateNonNull,
			Storage:         semantic.RefStorageAny,
			ExplicitStorage: true,
		}
	}
	return ops.s.g.cachedArenaRefType
}
func (ops *packedStoreOps) cachedRuntimeHelperType(name string, build func() *semantic.FuncType) *semantic.FuncType {
	if ops == nil || ops.s == nil || ops.s.g == nil {
		return build()
	}
	if cached := ops.s.g.runtimeHelperTypes[name]; cached != nil {
		return cached
	}
	helperType := build()
	ops.s.g.runtimeHelperTypes[name] = helperType
	return helperType
}
func (ops *packedStoreOps) stateValue(name string) (C.LLVMValueRef, error) {
	return ops.s.emitPackedStoreStateValueNamed(ops.storeValue, ops.storeType, name)
}
func (ops *packedStoreOps) arenaValue(name string) (C.LLVMValueRef, error) {
	return ops.s.emitPackedStoreArenaValueNamed(ops.storeValue, ops.storeType, name)
}
func (ops *packedStoreOps) rowBytesValue(name string) (C.LLVMValueRef, error) {
	return ops.s.emitPackedStoreRowBytesValueNamed(ops.storeValue, ops.storeType, name)
}
func (ops *packedStoreOps) currentBlock() C.LLVMBasicBlockRef {
	if ops == nil || ops.s == nil || ops.s.builder == nil {
		return nil
	}
	return C.LLVMGetInsertBlock(ops.s.builder)
}
func (ops *packedStoreOps) canCacheDenseHandleReads(enumType *semantic.EnumType) bool {
	if ops == nil || ops.s == nil || ops.s.g == nil || enumType == nil || !enumType.Packed {
		return false
	}
	if !packedModeUsesDenseIndexHandle(ops.s.g.packedModeForEnum(enumType)) {
		return false
	}
	if ops.storeType == nil || !semantic.IsFrozenPackedEnumStoreType(ops.storeType) {
		return false
	}
	return ops.currentBlock() != nil
}
func (ops *packedStoreOps) canCacheDirectReadValues(enumType *semantic.EnumType) bool {
	if ops == nil || ops.s == nil || ops.s.g == nil || enumType == nil || !enumType.Packed {
		return false
	}
	if !packedModeUsesDirectWordReads(ops.s.g.packedModeForEnum(enumType)) {
		return false
	}
	if ops.storeType == nil || !semantic.IsFrozenPackedEnumStoreType(ops.storeType) {
		return false
	}
	return ops.currentBlock() != nil
}
func (ops *packedStoreOps) canonicalizeDenseHandleKey(handleValue C.LLVMValueRef) C.LLVMValueRef {
	if handleValue == nil {
		return nil
	}
	if C.LLVMIsALoadInst(handleValue) != nil {
		if sourcePtr := C.LLVMGetOperand(handleValue, 0); sourcePtr != nil {
			return sourcePtr
		}
	}
	return handleValue
}
func (ops *packedStoreOps) denseReadCacheIdentity(origin packedReadOriginKey, handleValue C.LLVMValueRef) (packedReadOriginKey, C.LLVMValueRef) {
	if origin.root != nil {
		return origin, nil
	}
	return packedReadOriginKey{}, ops.canonicalizeDenseHandleKey(handleValue)
}
func (ops *packedStoreOps) directReadCacheIdentity(enumType *semantic.EnumType, origin packedReadOriginKey, handleValue C.LLVMValueRef) (packedReadOriginKey, C.LLVMValueRef) {
	if ops == nil {
		return packedReadOriginKey{}, handleValue
	}
	if enumType != nil && packedModeUsesDenseIndexHandle(ops.s.g.packedModeForEnum(enumType)) {
		return ops.denseReadCacheIdentity(origin, handleValue)
	}
	return packedReadOriginKey{}, ops.canonicalizeDenseHandleKey(handleValue)
}
func (ops *packedStoreOps) constantUint64(value C.LLVMValueRef) (uint64, bool) {
	if value == nil {
		return 0, false
	}
	var raw C.ulonglong
	if C.llcontextConstIntGetZExtValue(value, &raw) == 0 {
		return 0, false
	}
	return uint64(raw), true
}
func (ops *packedStoreOps) canUseOptimizedIndexSOADirectPrefixRead(enumType *semantic.EnumType) bool {
	if ops == nil || ops.s == nil || ops.s.g == nil || enumType == nil {
		return false
	}
	if ops.s.g.optLevel == OptimizationLevel0 {
		return false
	}
	if ops.s.g.packedModeForEnum(enumType) != packedEnumABIIndexSOA {
		return false
	}
	return ops.storeType != nil && semantic.IsFrozenPackedEnumStoreType(ops.storeType)
}
func (ops *packedStoreOps) canUseOptimizedIndexSOADirectMetadataRead(enumType *semantic.EnumType) bool {
	return ops.canUseOptimizedIndexSOADirectPrefixRead(enumType)
}
func (ops *packedStoreOps) loadPackedStoreDArrayElementDirect(stateValue C.LLVMValueRef, fieldOffsetBytes uint64, elemType semantic.Type, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if ops == nil || ops.s == nil || ops.s.g == nil {
		return nil, fmt.Errorf("missing packed store lowering state")
	}
	darrayType := &semantic.DArrayType{Elem: elemType}
	darrayLLVMType, err := ops.s.g.lowerType(darrayType)
	if err != nil {
		return nil, err
	}
	itemsOffsetBytes, err := ops.s.g.abiOffsetOfLLVMElement(darrayLLVMType, 0)
	if err != nil {
		return nil, err
	}
	itemsDataPtr, err := ops.loadPackedStoreDArrayItemsDirect(stateValue, fieldOffsetBytes+itemsOffsetBytes, name+".items")
	if err != nil {
		return nil, err
	}
	elemLLVMType, err := ops.s.g.lowerType(elemType)
	if err != nil {
		return nil, err
	}
	usizeType := ops.s.g.result.NamedTypes["usize"]
	coercedIndex, err := ops.s.coerceValue(indexValue, ops.s.g.result.NamedTypes["u32"], usizeType)
	if err != nil {
		return nil, err
	}
	elemPtr := C.LLVMBuildGEP2(ops.s.builder, elemLLVMType, itemsDataPtr, llvmValueSlicePtr([]C.LLVMValueRef{coercedIndex}), 1, cStringFree(name+".elem.ptr"))
	return C.LLVMBuildLoad2(ops.s.builder, elemLLVMType, elemPtr, cStringFree(name+".elem")), nil
}
func (ops *packedStoreOps) loadPackedStoreDArrayItemsDirect(stateValue C.LLVMValueRef, fieldOffsetBytes uint64, name string) (C.LLVMValueRef, error) {
	if ops == nil || ops.s == nil || ops.s.g == nil {
		return nil, fmt.Errorf("missing packed store lowering state")
	}
	if ops.canCacheDenseHandleReads(ops.storeType.Enum) {
		if ops.s.packedDenseDArrayItemsReads == nil {
			ops.s.packedDenseDArrayItemsReads = map[packedDenseDArrayItemsReadCacheKey]C.LLVMValueRef{}
		}
		key := packedDenseDArrayItemsReadCacheKey{block: ops.currentBlock(), storeType: ops.storeType, state: stateValue, fieldOffsetBytes: fieldOffsetBytes}
		if cached, ok := ops.s.packedDenseDArrayItemsReads[key]; ok && cached != nil {
			return cached, nil
		}
		itemsDataPtr, err := ops.loadPackedStoreDArrayItemsDirectUncached(stateValue, fieldOffsetBytes, name)
		if err != nil {
			return nil, err
		}
		ops.s.packedDenseDArrayItemsReads[key] = itemsDataPtr
		return itemsDataPtr, nil
	}
	return ops.loadPackedStoreDArrayItemsDirectUncached(stateValue, fieldOffsetBytes, name)
}
func (ops *packedStoreOps) loadPackedStoreDArrayItemsDirectUncached(stateValue C.LLVMValueRef, fieldOffsetBytes uint64, name string) (C.LLVMValueRef, error) {
	itemsPtrPtr, err := ops.s.emitByteOffsetPtr(stateValue, fieldOffsetBytes, name+".ptr")
	if err != nil {
		return nil, err
	}
	opaquePtrType := C.LLVMPointerTypeInContext(ops.s.g.context, 0)
	return C.LLVMBuildLoad2(ops.s.builder, opaquePtrType, itemsPtrPtr, cStringFree(name)), nil
}
func (ops *packedStoreOps) loadIndexSOAHandleDirect(stateValue C.LLVMValueRef, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	usizeType := ops.s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := ops.s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	wordBytes, err := ops.s.g.abiSizeOfLLVMType(usizeLLVMType)
	if err != nil {
		return nil, err
	}
	uintptrArrayType := &semantic.DArrayType{Elem: ops.s.g.result.NamedTypes["uintptr"]}
	uintptrArrayLLVMType, err := ops.s.g.lowerType(uintptrArrayType)
	if err != nil {
		return nil, err
	}
	darrayBytes, err := ops.s.g.abiSizeOfLLVMType(uintptrArrayLLVMType)
	if err != nil {
		return nil, err
	}
	handlesOffsetBytes := uint64(6)*wordBytes + darrayBytes
	return ops.loadPackedStoreDArrayElementDirect(stateValue, handlesOffsetBytes, ops.s.g.result.NamedTypes["uintptr"], indexValue, name)
}
func (ops *packedStoreOps) loadIndexSOATagDirect(stateValue C.LLVMValueRef, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	usizeType := ops.s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := ops.s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	wordBytes, err := ops.s.g.abiSizeOfLLVMType(usizeLLVMType)
	if err != nil {
		return nil, err
	}
	uintptrArrayType := &semantic.DArrayType{Elem: ops.s.g.result.NamedTypes["uintptr"]}
	uintptrArrayLLVMType, err := ops.s.g.lowerType(uintptrArrayType)
	if err != nil {
		return nil, err
	}
	darrayBytes, err := ops.s.g.abiSizeOfLLVMType(uintptrArrayLLVMType)
	if err != nil {
		return nil, err
	}
	tagsOffsetBytes := uint64(6)*wordBytes + uint64(3)*darrayBytes
	return ops.loadPackedStoreDArrayElementDirect(stateValue, tagsOffsetBytes, ops.s.g.result.NamedTypes["u32"], indexValue, name)
}
func (ops *packedStoreOps) loadIndexSOAPrefixWordDirect(stateValue C.LLVMValueRef, indexValue C.LLVMValueRef, wordOffset uint64, name string) (C.LLVMValueRef, error) {
	if ops == nil || ops.s == nil || ops.s.g == nil {
		return nil, fmt.Errorf("missing packed store lowering state")
	}
	uintptrType := ops.s.g.result.NamedTypes["uintptr"]
	usizeType := ops.s.g.result.NamedTypes["usize"]
	u32Type := ops.s.g.result.NamedTypes["u32"]
	uintptrLLVMType, err := ops.s.g.lowerType(uintptrType)
	if err != nil {
		return nil, err
	}
	usizeLLVMType, err := ops.s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	wordBytes, err := ops.s.g.abiSizeOfLLVMType(usizeLLVMType)
	if err != nil {
		return nil, err
	}
	prefixColumnsType := &semantic.DArrayType{Elem: uintptrType}
	prefixColumnsLLVMType, err := ops.s.g.lowerType(prefixColumnsType)
	if err != nil {
		return nil, err
	}
	darrayBytes, err := ops.s.g.abiSizeOfLLVMType(prefixColumnsLLVMType)
	if err != nil {
		return nil, err
	}
	prefixColumnsOffsetBytes := uint64(6)*wordBytes + uint64(6)*darrayBytes + wordBytes
	columnsDataPtr, err := ops.loadPackedStoreDArrayItemsDirect(stateValue, prefixColumnsOffsetBytes, name+".prefix.columns.items")
	if err != nil {
		return nil, err
	}
	opaquePtrType := C.LLVMPointerTypeInContext(ops.s.g.context, 0)
	columnOffsetValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(wordOffset), 0)
	columnSlotPtr := C.LLVMBuildGEP2(ops.s.builder, uintptrLLVMType, columnsDataPtr, llvmValueSlicePtr([]C.LLVMValueRef{columnOffsetValue}), 1, cStringFree(name+".prefix.column.slot.ptr"))
	columnBaseValue := C.LLVMBuildLoad2(ops.s.builder, uintptrLLVMType, columnSlotPtr, cStringFree(name+".prefix.column.base"))
	columnDataPtr := C.LLVMBuildIntToPtr(ops.s.builder, columnBaseValue, opaquePtrType, cStringFree(name+".prefix.column.ptr"))
	columnIndex, err := ops.s.coerceValue(indexValue, u32Type, usizeType)
	if err != nil {
		return nil, err
	}
	columnWordPtr := C.LLVMBuildGEP2(ops.s.builder, uintptrLLVMType, columnDataPtr, llvmValueSlicePtr([]C.LLVMValueRef{columnIndex}), 1, cStringFree(name+".prefix.word.ptr"))
	return C.LLVMBuildLoad2(ops.s.builder, uintptrLLVMType, columnWordPtr, cStringFree(name+".prefix.word")), nil
}
func (ops *packedStoreOps) storeCount(name string) (C.LLVMValueRef, error) {
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, err
	}
	usizeType := ops.s.g.result.NamedTypes["usize"]
	helperType := ops.cachedRuntimeHelperType("ctx_packed_store_count", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "ctx_packed_store_count", Params: []semantic.Type{ops.voidRefType()}, Return: usizeType}
	})
	callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_count", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue}, name), nil
}
func (ops *packedStoreOps) encodeDenseIndex(rowPtr C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	arenaValue, err := ops.arenaValue(name + ".arena")
	if err != nil {
		return nil, err
	}
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, err
	}
	u32Type := ops.s.g.result.NamedTypes["u32"]
	arenaType := ops.s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "ctx_packed_store_encode_index", Params: []semantic.Type{arenaRefType, ops.voidRefType(), ops.voidRefType()}, Return: u32Type}
	callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_encode_index", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, rowPtr, stateValue}, name), nil
}
func (ops *packedStoreOps) decodeDenseIndex(indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	arenaValue, err := ops.arenaValue(name + ".arena")
	if err != nil {
		return nil, err
	}
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, err
	}
	u32Type := ops.s.g.result.NamedTypes["u32"]
	arenaType := ops.s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "ctx_packed_store_decode_index", Params: []semantic.Type{arenaRefType, u32Type, ops.voidRefType()}, Return: ops.voidRefType()}
	callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_decode_index", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, indexValue, stateValue}, name), nil
}
func (ops *packedStoreOps) encodeHandle(rowPtr C.LLVMValueRef, enumType *semantic.EnumType, name string) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum handle metadata")
	}
	switch ops.s.g.packedModeForEnum(enumType) {
	case packedEnumABIIndexSOA:
		arenaValue, err := ops.arenaValue(name + ".arena")
		if err != nil {
			return nil, err
		}
		stateValue, err := ops.stateValue(name + ".state")
		if err != nil {
			return nil, err
		}
		u32Type := ops.s.g.result.NamedTypes["u32"]
		arenaType := ops.s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		helperType := &semantic.FuncType{Name: "ctx_packed_store_encode_index", Params: []semantic.Type{arenaRefType, ops.voidRefType(), ops.voidRefType()}, Return: u32Type}
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_encode_index", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		encoded := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, rowPtr, stateValue}, name)
		return ops.s.coerceValue(encoded, u32Type, enumType)
	case packedEnumABIVariantSparse:
		stateValue, err := ops.stateValue(name + ".state")
		if err != nil {
			return nil, err
		}
		u32Type := ops.s.g.result.NamedTypes["u32"]
		helperType := &semantic.FuncType{Name: "ctx_packed_store_encode_variant_sparse", Params: []semantic.Type{ops.voidRefType(), ops.voidRefType()}, Return: u32Type}
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_encode_variant_sparse", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		encoded := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{rowPtr, stateValue}, name)
		return ops.s.coerceValue(encoded, u32Type, enumType)
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", ops.s.g.packedModeForEnum(enumType))
	}
}
func (ops *packedStoreOps) decodeHandle(handleValue C.LLVMValueRef, enumType *semantic.EnumType, name string) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum handle metadata")
	}
	switch ops.s.g.packedModeForEnum(enumType) {
	case packedEnumABIIndexSOA:
		arenaValue, err := ops.arenaValue(name + ".arena")
		if err != nil {
			return nil, err
		}
		stateValue, err := ops.stateValue(name + ".state")
		if err != nil {
			return nil, err
		}
		u32Type := ops.s.g.result.NamedTypes["u32"]
		arenaType := ops.s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		helperType := &semantic.FuncType{Name: "ctx_packed_store_decode_index", Params: []semantic.Type{arenaRefType, u32Type, ops.voidRefType()}, Return: ops.voidRefType()}
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_decode_index", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		coercedHandle, err := ops.s.coerceValue(handleValue, enumType, u32Type)
		if err != nil {
			return nil, err
		}
		return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, coercedHandle, stateValue}, name), nil
	case packedEnumABIVariantSparse:
		stateValue, err := ops.stateValue(name + ".state")
		if err != nil {
			return nil, err
		}
		u32Type := ops.s.g.result.NamedTypes["u32"]
		helperType := &semantic.FuncType{Name: "ctx_packed_store_decode_variant_sparse", Params: []semantic.Type{u32Type, ops.voidRefType()}, Return: ops.voidRefType()}
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_decode_variant_sparse", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		coercedHandle, err := ops.s.coerceValue(handleValue, enumType, u32Type)
		if err != nil {
			return nil, err
		}
		return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{coercedHandle, stateValue}, name), nil
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", ops.s.g.packedModeForEnum(enumType))
	}
}
func (ops *packedStoreOps) storeValueAt(indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, nil, err
	}
	usizeType := ops.s.g.result.NamedTypes["usize"]
	switch ops.s.g.packedLoweringForStore(ops.storeType) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse:
		u32Type := ops.s.g.result.NamedTypes["u32"]
		helperType := ops.cachedRuntimeHelperType("ctx_packed_store_index_at", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "ctx_packed_store_index_at", Params: []semantic.Type{ops.voidRefType(), usizeType}, Return: u32Type}
		})
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_index_at", helperType)
		if err != nil {
			return nil, nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, nil, err
		}
		value := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, indexValue}, name)
		coerced, err := ops.s.coerceValue(value, u32Type, ops.storeType.Enum)
		if err != nil {
			return nil, nil, err
		}
		return coerced, ops.storeType.Enum, nil
	default:
		return nil, nil, fmt.Errorf("unsupported packed enum ABI mode %d", ops.s.g.packedLoweringForStore(ops.storeType))
	}
}
