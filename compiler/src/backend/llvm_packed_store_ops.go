//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
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
	return &semantic.RefType{
		Elem:            ops.s.g.result.NamedTypes["void"],
		State:           semantic.RefStateNonNull,
		Storage:         semantic.RefStorageAny,
		ExplicitStorage: true,
	}
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

func (ops *packedStoreOps) storeCount(name string) (C.LLVMValueRef, error) {
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, err
	}
	usizeType := ops.s.g.result.NamedTypes["usize"]
	helperType := &semantic.FuncType{Name: "ctx_packed_store_count", Params: []semantic.Type{ops.voidRefType()}, Return: usizeType}
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
	case packedEnumABIRowHandle:
		return rowPtr, nil
	case packedEnumABIWordHandle:
		arenaValue, err := ops.arenaValue(name + ".arena")
		if err != nil {
			return nil, err
		}
		stateValue, err := ops.stateValue(name + ".state")
		if err != nil {
			return nil, err
		}
		arenaType := ops.s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		helperType := &semantic.FuncType{Name: "ctx_packed_store_encode", Params: []semantic.Type{arenaRefType, ops.voidRefType(), ops.voidRefType()}, Return: ops.s.g.result.NamedTypes["uintptr"]}
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_encode", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		encoded := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, rowPtr, stateValue}, name)
		return ops.s.coerceValue(encoded, ops.s.g.result.NamedTypes["uintptr"], enumType)
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
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", ops.s.g.packedModeForEnum(enumType))
	}
}

func (ops *packedStoreOps) decodeHandle(handleValue C.LLVMValueRef, enumType *semantic.EnumType, name string) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum handle metadata")
	}
	switch ops.s.g.packedModeForEnum(enumType) {
	case packedEnumABIRowHandle:
		return handleValue, nil
	case packedEnumABIWordHandle:
		arenaValue, err := ops.arenaValue(name + ".arena")
		if err != nil {
			return nil, err
		}
		stateValue, err := ops.stateValue(name + ".state")
		if err != nil {
			return nil, err
		}
		arenaType := ops.s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		helperType := &semantic.FuncType{Name: "ctx_packed_store_decode", Params: []semantic.Type{arenaRefType, ops.s.g.result.NamedTypes["uintptr"], ops.voidRefType()}, Return: ops.voidRefType()}
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_decode", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		coercedHandle, err := ops.s.coerceValue(handleValue, enumType, ops.s.g.result.NamedTypes["uintptr"])
		if err != nil {
			return nil, err
		}
		return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, coercedHandle, stateValue}, name), nil
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
	case packedEnumABIRowHandle:
		helperType := &semantic.FuncType{Name: "ctx_packed_store_row_ptr_at", Params: []semantic.Type{ops.voidRefType(), usizeType}, Return: ops.voidRefType()}
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_row_ptr_at", helperType)
		if err != nil {
			return nil, nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, nil, err
		}
		value := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, indexValue}, name)
		coerced, err := ops.s.coerceValue(value, ops.voidRefType(), ops.storeType.Enum)
		if err != nil {
			return nil, nil, err
		}
		return coerced, ops.storeType.Enum, nil
	case packedEnumABIWordHandle:
		uintptrType := ops.s.g.result.NamedTypes["uintptr"]
		helperType := &semantic.FuncType{Name: "ctx_packed_store_word_handle_at", Params: []semantic.Type{ops.voidRefType(), usizeType}, Return: uintptrType}
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_word_handle_at", helperType)
		if err != nil {
			return nil, nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, nil, err
		}
		value := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, indexValue}, name)
		coerced, err := ops.s.coerceValue(value, uintptrType, ops.storeType.Enum)
		if err != nil {
			return nil, nil, err
		}
		return coerced, ops.storeType.Enum, nil
	case packedEnumABIIndexSOA:
		u32Type := ops.s.g.result.NamedTypes["u32"]
		helperType := &semantic.FuncType{Name: "ctx_packed_store_index_at", Params: []semantic.Type{ops.voidRefType(), usizeType}, Return: u32Type}
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
	arenaValue, err := ops.arenaValue(name + ".arena")
	if err != nil {
		return nil, err
	}
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, err
	}
	arenaType := ops.s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	tagType := ops.s.g.result.NamedTypes["u32"]
	switch ops.s.g.packedModeForEnum(enumType) {
	case packedEnumABIIndexSOA:
		helperType := &semantic.FuncType{Name: "ctx_packed_store_read_index_tag", Params: []semantic.Type{ops.voidRefType(), tagType}, Return: tagType}
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
		return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, coercedHandle}, name), nil
	default:
		helperType := &semantic.FuncType{Name: "ctx_packed_store_read_tag", Params: []semantic.Type{arenaRefType, ops.s.g.result.NamedTypes["uintptr"], ops.voidRefType()}, Return: tagType}
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_read_tag", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		coercedHandle, err := ops.s.coerceValue(handleValue, enumType, ops.s.g.result.NamedTypes["uintptr"])
		if err != nil {
			return nil, err
		}
		return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, coercedHandle, stateValue}, name), nil
	}
}

func (ops *packedStoreOps) loadPayloadWord(handleValue C.LLVMValueRef, enumType *semantic.EnumType, wordOffset C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum payload metadata")
	}
	arenaValue, err := ops.arenaValue(name + ".arena")
	if err != nil {
		return nil, err
	}
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, err
	}
	arenaType := ops.s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	uintptrType := ops.s.g.result.NamedTypes["uintptr"]
	switch ops.s.g.packedModeForEnum(enumType) {
	case packedEnumABIIndexSOA:
		u32Type := ops.s.g.result.NamedTypes["u32"]
		helperType := &semantic.FuncType{Name: "ctx_packed_store_read_index_word", Params: []semantic.Type{arenaRefType, u32Type, ops.voidRefType(), ops.s.g.result.NamedTypes["usize"]}, Return: uintptrType}
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
		return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, coercedHandle, stateValue, wordOffset}, name), nil
	default:
		helperType := &semantic.FuncType{Name: "ctx_packed_store_read_word", Params: []semantic.Type{arenaRefType, uintptrType, ops.voidRefType(), ops.s.g.result.NamedTypes["usize"]}, Return: uintptrType}
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
		return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, coercedHandle, stateValue, wordOffset}, name), nil
	}
}

func (ops *packedStoreOps) canDirectWordRead() bool {
	if ops == nil || ops.s == nil || ops.s.g == nil {
		return false
	}
	mode := ops.s.g.packedLoweringForStore(ops.storeType)
	return mode == packedEnumABIWordHandle || mode == packedEnumABIIndexSOA
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
	if ops.s.g.packedLoweringForStore(ops.storeType) == packedEnumABIIndexSOA {
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
	rowSizeValue, err := ops.rowBytesValue(name + ".row_bytes")
	if err != nil {
		return nil, nil, nil, err
	}
	switch ops.s.g.packedModeForEnum(enumType) {
	case packedEnumABIRowHandle:
		arenaValue, err := ops.arenaValue(name + ".arena")
		if err != nil {
			return nil, nil, nil, err
		}
		arenaType := ops.s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		helperType := &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, ops.s.g.result.NamedTypes["usize"]}, Return: ops.voidRefType()}
		callee, err := ops.s.g.ensureFunctionDeclared("arena_alloc", helperType)
		if err != nil {
			return nil, nil, nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, nil, nil, err
		}
		allocPtr := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, totalSizeValue}, "packed.alloc")
		stateValue, err := ops.stateValue(name + ".state")
		if err != nil {
			return nil, nil, nil, err
		}
		recordType := &semantic.FuncType{Name: "ctx_packed_store_record_row_ptr", Params: []semantic.Type{arenaRefType, ops.voidRefType(), ops.voidRefType()}, Return: ops.s.g.result.NamedTypes["void"]}
		recordCallee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_record_row_ptr", recordType)
		if err != nil {
			return nil, nil, nil, err
		}
		recordLLVMType, err := ops.s.g.lowerFunctionType(recordType)
		if err != nil {
			return nil, nil, nil, err
		}
		ops.s.buildCall(recordLLVMType, recordCallee, []C.LLVMValueRef{arenaValue, allocPtr, stateValue}, "")
		return allocPtr, allocPtr, rowSizeValue, nil
	case packedEnumABIWordHandle:
		arenaValue, err := ops.arenaValue(name + ".arena")
		if err != nil {
			return nil, nil, nil, err
		}
		stateValue, err := ops.stateValue(name + ".state")
		if err != nil {
			return nil, nil, nil, err
		}
		arenaType := ops.s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		allocResultType := ops.s.g.result.NamedTypes["PackedStoreAllocResult"]
		if allocResultType == nil {
			return nil, nil, nil, fmt.Errorf("missing builtin PackedStoreAllocResult type for packed enum allocation")
		}
		allocHelperName := "ctx_packed_store_alloc_fixed_tagged_result"
		allocHelperParams := []semantic.Type{arenaRefType, ops.voidRefType(), ops.s.g.result.NamedTypes["u32"]}
		allocArgs := []C.LLVMValueRef{arenaValue, stateValue, fixedTagValue}
		if hasTail {
			allocHelperName = "ctx_packed_store_alloc_result"
			allocHelperParams = []semantic.Type{arenaRefType, ops.s.g.result.NamedTypes["usize"], ops.voidRefType()}
			allocArgs = []C.LLVMValueRef{arenaValue, totalSizeValue, stateValue}
		}
		allocHelperType := &semantic.FuncType{Name: allocHelperName, Params: allocHelperParams, Return: allocResultType}
		allocCallee, err := ops.s.g.ensureFunctionDeclared(allocHelperName, allocHelperType)
		if err != nil {
			return nil, nil, nil, err
		}
		allocLLVMFnType, err := ops.s.g.lowerFunctionType(allocHelperType)
		if err != nil {
			return nil, nil, nil, err
		}
		allocResult := ops.s.buildCall(allocLLVMFnType, allocCallee, allocArgs, "packed.handle.alloc")
		allocPtr := C.LLVMBuildExtractValue(ops.s.builder, allocResult, 0, cStringFree("packed.alloc.ptr"))
		handleValue := C.LLVMBuildExtractValue(ops.s.builder, allocResult, 1, cStringFree("packed.alloc.handle"))
		enumValue, err := ops.s.coerceValue(handleValue, ops.s.g.result.NamedTypes["uintptr"], enumType)
		if err != nil {
			return nil, nil, nil, err
		}
		return allocPtr, enumValue, rowSizeValue, nil
	case packedEnumABIIndexSOA:
		arenaValue, err := ops.arenaValue(name + ".arena")
		if err != nil {
			return nil, nil, nil, err
		}
		stateValue, err := ops.stateValue(name + ".state")
		if err != nil {
			return nil, nil, nil, err
		}
		arenaType := ops.s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		allocResultType := ops.s.g.result.NamedTypes["PackedStoreIndexAllocResult"]
		if allocResultType == nil {
			return nil, nil, nil, fmt.Errorf("missing builtin PackedStoreIndexAllocResult type for packed enum allocation")
		}
		allocHelperName := "ctx_packed_store_alloc_fixed_tagged_index_result"
		allocHelperParams := []semantic.Type{arenaRefType, ops.voidRefType(), ops.s.g.result.NamedTypes["u32"]}
		allocArgs := []C.LLVMValueRef{arenaValue, stateValue, fixedTagValue}
		if hasTail {
			allocHelperName = "ctx_packed_store_alloc_index_result"
			allocHelperParams = []semantic.Type{arenaRefType, ops.s.g.result.NamedTypes["usize"], ops.voidRefType()}
			allocArgs = []C.LLVMValueRef{arenaValue, totalSizeValue, stateValue}
		}
		allocHelperType := &semantic.FuncType{Name: allocHelperName, Params: allocHelperParams, Return: allocResultType}
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
	default:
		return nil, nil, nil, fmt.Errorf("unsupported packed enum ABI mode %d", ops.s.g.packedModeForEnum(enumType))
	}
}

func (ops *packedStoreOps) recordTag(tagValue C.LLVMValueRef, name string) error {
	arenaValue, err := ops.arenaValue(name + ".arena")
	if err != nil {
		return err
	}
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return err
	}
	arenaType := ops.s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	tagType := ops.s.g.result.NamedTypes["u32"]
	recordType := &semantic.FuncType{Name: "ctx_packed_store_record_tag", Params: []semantic.Type{arenaRefType, tagType, ops.voidRefType()}, Return: ops.s.g.result.NamedTypes["void"]}
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

func (ops *packedStoreOps) recordPrefixWords(rowPtr C.LLVMValueRef, name string) error {
	if ops == nil || ops.s == nil || ops.s.g == nil || ops.s.g.packedLoweringForStore(ops.storeType) != packedEnumABIIndexSOA {
		return nil
	}
	arenaValue, err := ops.arenaValue(name + ".arena")
	if err != nil {
		return err
	}
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return err
	}
	arenaType := ops.s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	recordType := &semantic.FuncType{Name: "ctx_packed_store_record_prefix_words", Params: []semantic.Type{arenaRefType, ops.voidRefType(), ops.voidRefType()}, Return: ops.s.g.result.NamedTypes["void"]}
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
