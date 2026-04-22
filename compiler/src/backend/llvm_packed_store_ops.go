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

func (ops *packedStoreOps) storeTagAt(handleValue C.LLVMValueRef, enumType *semantic.EnumType, name string) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum tag metadata")
	}
	tagType := ops.s.g.result.NamedTypes["u32"]
	switch ops.s.g.packedModeForEnum(enumType) {
	case packedEnumABIIndexSOA:
		stateValue, err := ops.stateValue(name + ".state")
		if err != nil {
			return nil, err
		}
		helperType := ops.cachedRuntimeHelperType("ctx_packed_store_read_index_tag", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "ctx_packed_store_read_index_tag", Params: []semantic.Type{ops.voidRefType(), tagType}, Return: tagType}
		})
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_read_index_tag", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		coercedHandle, err := ops.s.coerceValue(handleValue, enumType, tagType)
		if err != nil {
			return nil, err
		}
		if ops.canCacheDenseHandleReads(enumType) {
			if ops.s.packedDenseTagReads == nil {
				ops.s.packedDenseTagReads = map[packedDenseTagReadCacheKey]C.LLVMValueRef{}
			}
			key := packedDenseTagReadCacheKey{block: ops.currentBlock(), storeType: ops.storeType, state: stateValue, handle: ops.canonicalizeDenseHandleKey(coercedHandle)}
			if cached, ok := ops.s.packedDenseTagReads[key]; ok && cached != nil {
				return cached, nil
			}
			var tagValue C.LLVMValueRef
			if ops.canUseOptimizedIndexSOADirectMetadataRead(enumType) {
				tagValue, err = ops.loadIndexSOATagDirect(stateValue, coercedHandle, name+".direct")
				if err != nil {
					return nil, err
				}
			} else {
				tagValue = ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, coercedHandle}, name)
			}
			ops.s.packedDenseTagReads[key] = tagValue
			return tagValue, nil
		}
		if ops.canUseOptimizedIndexSOADirectMetadataRead(enumType) {
			return ops.loadIndexSOATagDirect(stateValue, coercedHandle, name+".direct")
		}
		return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, coercedHandle}, name), nil
	case packedEnumABIVariantSparse:
		stateValue, err := ops.stateValue(name + ".state")
		if err != nil {
			return nil, err
		}
		helperType := ops.cachedRuntimeHelperType("ctx_packed_store_read_variant_sparse_tag", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "ctx_packed_store_read_variant_sparse_tag", Params: []semantic.Type{ops.voidRefType(), tagType}, Return: tagType}
		})
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_read_variant_sparse_tag", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		coercedHandle, err := ops.s.coerceValue(handleValue, enumType, tagType)
		if err != nil {
			return nil, err
		}
		if ops.canCacheDenseHandleReads(enumType) {
			if ops.s.packedVariantSparseTagReads == nil {
				ops.s.packedVariantSparseTagReads = map[packedVariantSparseTagReadCacheKey]C.LLVMValueRef{}
			}
			key := packedVariantSparseTagReadCacheKey{block: ops.currentBlock(), storeType: ops.storeType, state: stateValue, handle: ops.canonicalizeDenseHandleKey(coercedHandle)}
			if cached, ok := ops.s.packedVariantSparseTagReads[key]; ok && cached != nil {
				return cached, nil
			}
			tagValue := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, coercedHandle}, name)
			ops.s.packedVariantSparseTagReads[key] = tagValue
			return tagValue, nil
		}
		return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, coercedHandle}, name), nil
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", ops.s.g.packedModeForEnum(enumType))
	}
}

func (ops *packedStoreOps) loadPayloadWordAtOrigin(handleValue C.LLVMValueRef, enumType *semantic.EnumType, wordOffset C.LLVMValueRef, origin packedReadOriginKey, _ string) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum payload metadata")
	}
	uintptrType := ops.s.g.result.NamedTypes["uintptr"]
	switch ops.s.g.packedModeForEnum(enumType) {
	case packedEnumABIIndexSOA:
		arenaValue, err := ops.arenaValue("packed.arena")
		if err != nil {
			return nil, err
		}
		stateValue, err := ops.stateValue("packed.state")
		if err != nil {
			return nil, err
		}
		u32Type := ops.s.g.result.NamedTypes["u32"]
		helperType := ops.cachedRuntimeHelperType("ctx_packed_store_read_index_word", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "ctx_packed_store_read_index_word", Params: []semantic.Type{ops.arenaRefType(), u32Type, ops.voidRefType(), ops.s.g.result.NamedTypes["usize"]}, Return: uintptrType}
		})
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_read_index_word", helperType)
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
		if prefixWordOffset, ok := ops.constantUint64(wordOffset); ok && ops.canUseOptimizedIndexSOADirectPrefixRead(enumType) {
			prefixWordCount, err := ops.s.g.packedEnumConfiguredPrefixWordCount(enumType)
			if err != nil {
				return nil, err
			}
			if prefixWordOffset < prefixWordCount {
				if ops.canCacheDenseHandleReads(enumType) {
					if ops.s.packedDenseWordReads == nil {
						ops.s.packedDenseWordReads = map[packedDenseWordReadCacheKey]C.LLVMValueRef{}
					}
					originKey, cacheHandle := ops.denseReadCacheIdentity(origin, coercedHandle)
					key := packedDenseWordReadCacheKey{block: ops.currentBlock(), storeType: ops.storeType, state: stateValue, origin: originKey, handle: cacheHandle, offset: wordOffset}
					if cached, ok := ops.s.packedDenseWordReads[key]; ok && cached != nil {
						return cached, nil
					}
					wordValue, err := ops.loadIndexSOAPrefixWordDirect(stateValue, coercedHandle, prefixWordOffset, "packed.index.prefix.word")
					if err != nil {
						return nil, err
					}
					ops.s.packedDenseWordReads[key] = wordValue
					return wordValue, nil
				}
				return ops.loadIndexSOAPrefixWordDirect(stateValue, coercedHandle, prefixWordOffset, "packed.index.prefix.word")
			}
		}
		if ops.canCacheDenseHandleReads(enumType) {
			if ops.s.packedDenseWordReads == nil {
				ops.s.packedDenseWordReads = map[packedDenseWordReadCacheKey]C.LLVMValueRef{}
			}
			originKey, cacheHandle := ops.denseReadCacheIdentity(origin, coercedHandle)
			key := packedDenseWordReadCacheKey{block: ops.currentBlock(), storeType: ops.storeType, state: stateValue, origin: originKey, handle: cacheHandle, offset: wordOffset}
			if cached, ok := ops.s.packedDenseWordReads[key]; ok && cached != nil {
				return cached, nil
			}
			wordValue := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, coercedHandle, stateValue, wordOffset}, "")
			ops.s.packedDenseWordReads[key] = wordValue
			return wordValue, nil
		}
		return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, coercedHandle, stateValue, wordOffset}, ""), nil
	case packedEnumABIVariantSparse:
		stateValue, err := ops.stateValue("packed.state")
		if err != nil {
			return nil, err
		}
		u32Type := ops.s.g.result.NamedTypes["u32"]
		helperType := ops.cachedRuntimeHelperType("ctx_packed_store_read_variant_sparse_word", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "ctx_packed_store_read_variant_sparse_word", Params: []semantic.Type{u32Type, ops.voidRefType(), ops.s.g.result.NamedTypes["usize"]}, Return: uintptrType}
		})
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_read_variant_sparse_word", helperType)
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
		if ops.canCacheDenseHandleReads(enumType) {
			if ops.s.packedVariantSparseWordReads == nil {
				ops.s.packedVariantSparseWordReads = map[packedVariantSparseWordReadCacheKey]C.LLVMValueRef{}
			}
			key := packedVariantSparseWordReadCacheKey{block: ops.currentBlock(), storeType: ops.storeType, state: stateValue, handle: ops.canonicalizeDenseHandleKey(coercedHandle), offset: wordOffset}
			if cached, ok := ops.s.packedVariantSparseWordReads[key]; ok && cached != nil {
				return cached, nil
			}
			wordValue := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{coercedHandle, stateValue, wordOffset}, "")
			ops.s.packedVariantSparseWordReads[key] = wordValue
			return wordValue, nil
		}
		return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{coercedHandle, stateValue, wordOffset}, ""), nil
	default:
		arenaValue, err := ops.arenaValue("packed.arena")
		if err != nil {
			return nil, err
		}
		stateValue, err := ops.stateValue("packed.state")
		if err != nil {
			return nil, err
		}
		helperType := ops.cachedRuntimeHelperType("ctx_packed_store_read_word", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "ctx_packed_store_read_word", Params: []semantic.Type{ops.arenaRefType(), uintptrType, ops.voidRefType(), ops.s.g.result.NamedTypes["usize"]}, Return: uintptrType}
		})
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_read_word", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		coercedHandle, err := ops.s.coerceValue(handleValue, enumType, uintptrType)
		if err != nil {
			return nil, err
		}
		return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, coercedHandle, stateValue, wordOffset}, ""), nil
	}
}

func (ops *packedStoreOps) loadPayloadWord(handleValue C.LLVMValueRef, enumType *semantic.EnumType, wordOffset C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	return ops.loadPayloadWordAtOrigin(handleValue, enumType, wordOffset, packedReadOriginKey{}, name)
}

func (ops *packedStoreOps) canDirectWordRead() bool {
	if ops == nil || ops.s == nil || ops.s.g == nil {
		return false
	}
	mode := ops.s.g.packedLoweringForStore(ops.storeType)
	return packedModeUsesDirectWordReads(mode)
}

func (ops *packedStoreOps) canDirectTagRead() bool {
	return ops.canDirectWordRead()
}

func (ops *packedStoreOps) loadPayloadWordsAsTypes(handleValue C.LLVMValueRef, enumType *semantic.EnumType, wordOffsets []C.LLVMValueRef, types []semantic.Type, namePrefix string) ([]C.LLVMValueRef, bool, error) {
	if !ops.canDirectWordRead() || enumType == nil || !enumType.Packed {
		return nil, false, nil
	}
	if len(wordOffsets) != len(types) {
		return nil, false, fmt.Errorf("packed payload word/type arity mismatch")
	}
	values := make([]C.LLVMValueRef, 0, len(types))
	uintptrType := ops.s.g.result.NamedTypes["uintptr"]
	for i, payloadType := range types {
		wordValue, err := ops.loadPayloadWord(handleValue, enumType, wordOffsets[i], namePrefix)
		if err != nil {
			return nil, false, err
		}
		coerced, err := ops.s.coerceValue(wordValue, uintptrType, payloadType)
		if err != nil {
			return nil, false, err
		}
		values = append(values, coerced)
	}
	return values, true, nil
}

func (ops *packedStoreOps) loadTailView(handleValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, payloadIndex int, name string) (C.LLVMValueRef, bool, error) {
	if ops == nil || enumType == nil || !enumType.Packed || variant == nil {
		return nil, false, nil
	}
	if ops.s == nil || ops.s.g == nil || ops.s.g.packedModeForEnum(enumType) != packedEnumABIIndexSOA {
		return nil, false, nil
	}
	tailIndex, ok := variant.TailPayloadIndex()
	if !ok || tailIndex != payloadIndex {
		return nil, false, nil
	}
	viewType, ok := variant.TailPayloadViewType()
	if !ok || viewType == nil {
		return nil, false, nil
	}
	wordBytes := uint64(ops.s.g.wordBits / 8)
	if wordBytes == 0 {
		wordBytes = 8
	}
	payloadFieldIndex, err := ops.s.g.packedEnumPayloadFieldIndex(enumType)
	if err != nil {
		return nil, false, err
	}
	rowType, err := ops.s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return nil, false, err
	}
	usizeType := ops.s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := ops.s.g.lowerType(usizeType)
	if err != nil {
		return nil, false, err
	}
	i32Type := C.LLVMInt32TypeInContext(ops.s.g.context)
	zeroIndex := C.LLVMConstInt(i32Type, 0, 0)
	payloadFieldIndexValue := C.LLVMConstInt(i32Type, C.ulonglong(payloadFieldIndex), 0)
	nullPtr := C.LLVMConstNull(C.LLVMPointerTypeInContext(ops.s.g.context, 0))
	payloadIndices := []C.LLVMValueRef{zeroIndex, payloadFieldIndexValue}
	payloadPtr := C.LLVMBuildGEP2(ops.s.builder, rowType, nullPtr, llvmValueSlicePtr(payloadIndices), C.unsigned(len(payloadIndices)), cStringFree(name+".payload.word.ptr"))
	payloadOffsetBytes := C.LLVMBuildPtrToInt(ops.s.builder, payloadPtr, usizeLLVMType, cStringFree(name+".payload.word.bytes"))
	wordBytesValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(wordBytes), 0)
	baseWordOffset := C.LLVMBuildUDiv(ops.s.builder, payloadOffsetBytes, wordBytesValue, cStringFree(name+".payload.word.offset"))
	payloadType, err := ops.s.g.lowerEnumVariantPayloadType(variant)
	if err != nil {
		return nil, false, err
	}
	fieldIndexValue := C.LLVMConstInt(i32Type, C.ulonglong(payloadIndex), 0)
	fieldIndices := []C.LLVMValueRef{zeroIndex, fieldIndexValue}
	fieldPtr := C.LLVMBuildGEP2(ops.s.builder, payloadType, nullPtr, llvmValueSlicePtr(fieldIndices), C.unsigned(len(fieldIndices)), cStringFree(name+".field.word.ptr"))
	fieldOffsetBytes := C.LLVMBuildPtrToInt(ops.s.builder, fieldPtr, usizeLLVMType, cStringFree(name+".field.word.bytes"))
	fieldWordOffset := C.LLVMBuildUDiv(ops.s.builder, fieldOffsetBytes, wordBytesValue, cStringFree(name+".field.word.offset"))
	viewBaseWordOffset := C.LLVMBuildAdd(ops.s.builder, baseWordOffset, fieldWordOffset, cStringFree(name+".field.word.base"))
	oneWord := C.LLVMConstInt(usizeLLVMType, 1, 0)
	twoWords := C.LLVMConstInt(usizeLLVMType, 2, 0)
	dataWord, err := ops.loadPayloadWord(handleValue, enumType, viewBaseWordOffset, name+".data.word")
	if err != nil {
		return nil, false, err
	}
	lenWordOffset := C.LLVMBuildAdd(ops.s.builder, viewBaseWordOffset, oneWord, cStringFree(name+".len.word.offset"))
	lenWord, err := ops.loadPayloadWord(handleValue, enumType, lenWordOffset, name+".len.word")
	if err != nil {
		return nil, false, err
	}
	elemSizeWordOffset := C.LLVMBuildAdd(ops.s.builder, viewBaseWordOffset, twoWords, cStringFree(name+".elem_size.word.offset"))
	elemSizeWord, err := ops.loadPayloadWord(handleValue, enumType, elemSizeWordOffset, name+".elem_size.word")
	if err != nil {
		return nil, false, err
	}
	voidRefType := ops.voidRefType()
	dataValue, err := ops.s.coerceValue(dataWord, ops.s.g.result.NamedTypes["uintptr"], voidRefType)
	if err != nil {
		return nil, false, err
	}
	lenValue, err := ops.s.coerceValue(lenWord, ops.s.g.result.NamedTypes["uintptr"], usizeType)
	if err != nil {
		return nil, false, err
	}
	elemSizeValue, err := ops.s.coerceValue(elemSizeWord, ops.s.g.result.NamedTypes["uintptr"], usizeType)
	if err != nil {
		return nil, false, err
	}
	viewLLVMType, err := ops.s.g.lowerType(viewType)
	if err != nil {
		return nil, false, err
	}
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(ops.s.builder, viewValue, dataValue, 0, cStringFree(name+".data"))
	viewValue = C.LLVMBuildInsertValue(ops.s.builder, viewValue, lenValue, 1, cStringFree(name+".len"))
	viewValue = C.LLVMBuildInsertValue(ops.s.builder, viewValue, elemSizeValue, 2, cStringFree(name+".elem_size"))
	return viewValue, true, nil
}

func (ops *packedStoreOps) storeSlice(startValue C.LLVMValueRef, endValue C.LLVMValueRef, resultType semantic.Type, name string) (C.LLVMValueRef, semantic.Type, error) {
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, nil, err
	}
	helperName := "ctx_packed_store_view"
	if packedModeUsesDenseIndexHandle(ops.s.g.packedLoweringForStore(ops.storeType)) {
		helperName = "ctx_packed_store_indices_view"
	}
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{ops.voidRefType(), ops.s.g.result.NamedTypes["usize"], ops.s.g.result.NamedTypes["usize"]},
		Return: resultType,
	}
	callee, err := ops.s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	value := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, startValue, endValue}, name)
	return value, resultType, nil
}

func (ops *packedStoreOps) allocateStorage(enumType *semantic.EnumType, totalSizeValue C.LLVMValueRef, hasTail bool, fixedTagValue C.LLVMValueRef, name string) (C.LLVMValueRef, C.LLVMValueRef, C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, nil, nil, fmt.Errorf("missing packed enum storage metadata")
	}
	rowSizeValue, err := ops.rowBytesValue("packed.row_bytes")
	if err != nil {
		return nil, nil, nil, err
	}
	switch ops.s.g.packedModeForEnum(enumType) {
	case packedEnumABIIndexSOA:
		arenaValue, err := ops.arenaValue("packed.arena")
		if err != nil {
			return nil, nil, nil, err
		}
		stateValue, err := ops.stateValue("packed.state")
		if err != nil {
			return nil, nil, nil, err
		}
		allocResultType := ops.s.g.result.NamedTypes["PackedStoreIndexAllocResult"]
		if allocResultType == nil {
			return nil, nil, nil, fmt.Errorf("missing builtin PackedStoreIndexAllocResult type for packed enum allocation")
		}
		allocHelperName := "ctx_packed_store_alloc_fixed_tagged_index_result"
		allocArgs := []C.LLVMValueRef{arenaValue, stateValue, fixedTagValue}
		if hasTail {
			allocHelperName = "ctx_packed_store_alloc_index_result"
			allocArgs = []C.LLVMValueRef{arenaValue, totalSizeValue, stateValue}
		}
		allocHelperType := ops.cachedRuntimeHelperType(allocHelperName, func() *semantic.FuncType {
			params := []semantic.Type{ops.arenaRefType(), ops.voidRefType(), ops.s.g.result.NamedTypes["u32"]}
			if hasTail {
				params = []semantic.Type{ops.arenaRefType(), ops.s.g.result.NamedTypes["usize"], ops.voidRefType()}
			}
			return &semantic.FuncType{Name: allocHelperName, Params: params, Return: allocResultType}
		})
		allocCallee, err := ops.s.g.ensureFunctionDeclared(allocHelperName, allocHelperType)
		if err != nil {
			return nil, nil, nil, err
		}
		allocLLVMFnType, err := ops.s.g.lowerFunctionType(allocHelperType)
		if err != nil {
			return nil, nil, nil, err
		}
		allocResult := ops.s.buildCall(allocLLVMFnType, allocCallee, allocArgs, "packed.index.alloc")
		allocPtr := C.LLVMBuildExtractValue(ops.s.builder, allocResult, 0, cStringFree("packed.alloc.ptr"))
		indexValue := C.LLVMBuildExtractValue(ops.s.builder, allocResult, 1, cStringFree("packed.alloc.index"))
		enumValue, err := ops.s.coerceValue(indexValue, ops.s.g.result.NamedTypes["u32"], enumType)
		if err != nil {
			return nil, nil, nil, err
		}
		return allocPtr, enumValue, rowSizeValue, nil
	case packedEnumABIVariantSparse:
		arenaValue, err := ops.arenaValue("packed.arena")
		if err != nil {
			return nil, nil, nil, err
		}
		stateValue, err := ops.stateValue("packed.state")
		if err != nil {
			return nil, nil, nil, err
		}
		allocResultType := ops.s.g.result.NamedTypes["PackedStoreIndexAllocResult"]
		if allocResultType == nil {
			return nil, nil, nil, fmt.Errorf("missing builtin PackedStoreIndexAllocResult type for variant-sparse packed enum allocation")
		}
		allocHelperName := "ctx_packed_store_alloc_fixed_tagged_variant_sparse_result"
		allocArgs := []C.LLVMValueRef{arenaValue, stateValue, fixedTagValue}
		if hasTail {
			allocHelperName = "ctx_packed_store_alloc_tagged_variant_sparse_result"
			allocArgs = []C.LLVMValueRef{arenaValue, totalSizeValue, stateValue, fixedTagValue}
		}
		allocHelperType := ops.cachedRuntimeHelperType(allocHelperName, func() *semantic.FuncType {
			params := []semantic.Type{ops.arenaRefType(), ops.voidRefType(), ops.s.g.result.NamedTypes["u32"]}
			if hasTail {
				params = []semantic.Type{ops.arenaRefType(), ops.s.g.result.NamedTypes["usize"], ops.voidRefType(), ops.s.g.result.NamedTypes["u32"]}
			}
			return &semantic.FuncType{Name: allocHelperName, Params: params, Return: allocResultType}
		})
		allocCallee, err := ops.s.g.ensureFunctionDeclared(allocHelperName, allocHelperType)
		if err != nil {
			return nil, nil, nil, err
		}
		allocLLVMFnType, err := ops.s.g.lowerFunctionType(allocHelperType)
		if err != nil {
			return nil, nil, nil, err
		}
		allocResult := ops.s.buildCall(allocLLVMFnType, allocCallee, allocArgs, "packed.variant_sparse.alloc")
		allocPtr := C.LLVMBuildExtractValue(ops.s.builder, allocResult, 0, cStringFree("packed.alloc.ptr"))
		indexValue := C.LLVMBuildExtractValue(ops.s.builder, allocResult, 1, cStringFree("packed.alloc.index"))
		enumValue, err := ops.s.coerceValue(indexValue, ops.s.g.result.NamedTypes["u32"], enumType)
		if err != nil {
			return nil, nil, nil, err
		}
		return allocPtr, enumValue, rowSizeValue, nil
	default:
		return nil, nil, nil, fmt.Errorf("unsupported packed enum ABI mode %d", ops.s.g.packedModeForEnum(enumType))
	}
}

func (ops *packedStoreOps) recordTag(tagValue C.LLVMValueRef, _ string) error {
	arenaValue, err := ops.arenaValue("packed.arena")
	if err != nil {
		return err
	}
	stateValue, err := ops.stateValue("packed.state")
	if err != nil {
		return err
	}
	tagType := ops.s.g.result.NamedTypes["u32"]
	recordType := ops.cachedRuntimeHelperType("ctx_packed_store_record_tag", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "ctx_packed_store_record_tag", Params: []semantic.Type{ops.arenaRefType(), tagType, ops.voidRefType()}, Return: ops.s.g.result.NamedTypes["void"]}
	})
	recordCallee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_record_tag", recordType)
	if err != nil {
		return err
	}
	recordLLVMType, err := ops.s.g.lowerFunctionType(recordType)
	if err != nil {
		return err
	}
	ops.s.buildCall(recordLLVMType, recordCallee, []C.LLVMValueRef{arenaValue, tagValue, stateValue}, "")
	return nil
}

func (ops *packedStoreOps) recordPrefixWords(rowPtr C.LLVMValueRef, _ string) error {
	if ops == nil || ops.s == nil || ops.s.g == nil || ops.s.g.packedLoweringForStore(ops.storeType) != packedEnumABIIndexSOA {
		return nil
	}
	arenaValue, err := ops.arenaValue("packed.arena")
	if err != nil {
		return err
	}
	stateValue, err := ops.stateValue("packed.state")
	if err != nil {
		return err
	}
	recordType := ops.cachedRuntimeHelperType("ctx_packed_store_record_prefix_words", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "ctx_packed_store_record_prefix_words", Params: []semantic.Type{ops.arenaRefType(), ops.voidRefType(), ops.voidRefType()}, Return: ops.s.g.result.NamedTypes["void"]}
	})
	recordCallee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_record_prefix_words", recordType)
	if err != nil {
		return err
	}
	recordLLVMType, err := ops.s.g.lowerFunctionType(recordType)
	if err != nil {
		return err
	}
	ops.s.buildCall(recordLLVMType, recordCallee, []C.LLVMValueRef{arenaValue, rowPtr, stateValue}, "")
	return nil
}

func (ops *packedStoreOps) recordSideWords(wordsPtr C.LLVMValueRef, _ string) error {
	if ops == nil || ops.s == nil || ops.s.g == nil || ops.storeType == nil || ops.storeType.Enum == nil {
		return nil
	}
	if !packedModeUsesDenseIndexHandle(ops.s.g.packedLoweringForStore(ops.storeType)) {
		return nil
	}
	arenaValue, err := ops.arenaValue("packed.arena")
	if err != nil {
		return err
	}
	stateValue, err := ops.stateValue("packed.state")
	if err != nil {
		return err
	}
	recordType := ops.cachedRuntimeHelperType("ctx_packed_store_record_side_words", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "ctx_packed_store_record_side_words", Params: []semantic.Type{ops.arenaRefType(), ops.voidRefType(), ops.voidRefType()}, Return: ops.s.g.result.NamedTypes["void"]}
	})
	recordCallee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_record_side_words", recordType)
	if err != nil {
		return err
	}
	recordLLVMType, err := ops.s.g.lowerFunctionType(recordType)
	if err != nil {
		return err
	}
	ops.s.buildCall(recordLLVMType, recordCallee, []C.LLVMValueRef{arenaValue, wordsPtr, stateValue}, "")
	return nil
}

func (ops *packedStoreOps) loadSideWordAtOrigin(indexValue C.LLVMValueRef, wordOffset C.LLVMValueRef, origin packedReadOriginKey, name string) (C.LLVMValueRef, error) {
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, err
	}
	u32Type := ops.s.g.result.NamedTypes["u32"]
	usizeType := ops.s.g.result.NamedTypes["usize"]
	uintptrType := ops.s.g.result.NamedTypes["uintptr"]
	coercedIndex, err := ops.s.coerceValue(indexValue, ops.storeType.Enum, u32Type)
	if err != nil {
		return nil, err
	}
	helperType := ops.cachedRuntimeHelperType("ctx_packed_store_read_side_word", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "ctx_packed_store_read_side_word", Params: []semantic.Type{ops.voidRefType(), u32Type, usizeType}, Return: uintptrType}
	})
	callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_read_side_word", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	if ops.canCacheDenseHandleReads(ops.storeType.Enum) {
		if ops.s.packedDenseSideWordReads == nil {
			ops.s.packedDenseSideWordReads = map[packedDenseSideWordReadCacheKey]C.LLVMValueRef{}
		}
		originKey, cacheIndex := ops.denseReadCacheIdentity(origin, coercedIndex)
		key := packedDenseSideWordReadCacheKey{block: ops.currentBlock(), storeType: ops.storeType, state: stateValue, origin: originKey, index: cacheIndex, offset: wordOffset}
		if cached, ok := ops.s.packedDenseSideWordReads[key]; ok && cached != nil {
			return cached, nil
		}
		wordValue := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, coercedIndex, wordOffset}, name)
		ops.s.packedDenseSideWordReads[key] = wordValue
		return wordValue, nil
	}
	return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, coercedIndex, wordOffset}, name), nil
}

func (ops *packedStoreOps) loadSideWord(indexValue C.LLVMValueRef, wordOffset C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	return ops.loadSideWordAtOrigin(indexValue, wordOffset, packedReadOriginKey{}, name)
}

func (ops *packedStoreOps) storeTagsView(startValue C.LLVMValueRef, endValue C.LLVMValueRef, resultType *semantic.DArrayViewType, name string) (C.LLVMValueRef, semantic.Type, error) {
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   "ctx_packed_store_tags_view",
		Params: []semantic.Type{ops.voidRefType(), ops.s.g.result.NamedTypes["usize"], ops.s.g.result.NamedTypes["usize"]},
		Return: resultType,
	}
	callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_tags_view", helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	value := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, startValue, endValue}, name)
	return value, resultType, nil
}
