//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

void elisa_coreSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitIterLoopChunksExactItemValue(carrierValue C.LLVMValueRef, carrierType *semantic.GenericInstanceType, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	chunkType, ok := semantic.ChunksExactViewItemType(carrierType)
	if !ok || chunkType == nil {
		return nil, nil, fmt.Errorf("iterable loop chunks_exact item type is not supported for %s", carrierType.String())
	}
	sourceValue := C.LLVMBuildExtractValue(s.builder, carrierValue, 0, cStringFree(name+".chunks.source"))
	chunkSizeValue := C.LLVMBuildExtractValue(s.builder, carrierValue, 1, cStringFree(name+".chunks.chunk_size"))
	startValue := C.LLVMBuildMul(s.builder, indexValue, chunkSizeValue, cStringFree(name+".chunks.start"))
	endValue := C.LLVMBuildAdd(s.builder, startValue, chunkSizeValue, cStringFree(name+".chunks.end"))
	value, err := s.emitArenaViewSliceValue(sourceValue, chunkType, startValue, endValue, name+".chunks.item")
	if err != nil {
		return nil, nil, err
	}
	return value, chunkType, nil
}

func iterLoopIsDictSource(t semantic.Type) bool {
	switch tt := t.(type) {
	case *semantic.DictType:
		return true
	case *semantic.RefType:
		_, ok := tt.Elem.(*semantic.DictType)
		return tt.State == semantic.RefStateNonNull && ok
	default:
		return false
	}
}

func (s *functionState) iterLoopDictBucketType(dictType *semantic.DictType) (*semantic.GenericInstanceType, C.LLVMTypeRef, error) {
	base, ok := s.g.result.NamedTypes["DictBucket"]
	if !ok {
		return nil, nil, fmt.Errorf("missing runtime struct DictBucket")
	}
	bucketType := &semantic.GenericInstanceType{Name: "DictBucket", Base: base, Args: []semantic.Type{dictType.Key, dictType.Value}}
	llvmType, err := s.g.lowerType(bucketType)
	if err != nil {
		return nil, nil, err
	}
	return bucketType, llvmType, nil
}

func (s *functionState) iterLoopDictSourcePtr(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, sourceName string) (*semantic.DictType, C.LLVMValueRef, error) {
	switch tt := sourceType.(type) {
	case *semantic.DictType:
		return tt, sourceAlloca, nil
	case *semantic.RefType:
		if tt.State != semantic.RefStateNonNull {
			return nil, nil, fmt.Errorf("iterable loop requires a non-null reference source, got %s", sourceType.String())
		}
		dictType, ok := tt.Elem.(*semantic.DictType)
		if !ok || dictType == nil {
			return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
		}
		ptr, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.dict.ref")
		if err != nil {
			return nil, nil, err
		}
		return dictType, ptr, nil
	default:
		return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
	}
}

func (s *functionState) emitIterLoopDictCapacity(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, sourceName string) (C.LLVMValueRef, error) {
	dictType, dictPtr, err := s.iterLoopDictSourcePtr(sourceAlloca, sourceType, sourceName)
	if err != nil {
		return nil, err
	}
	dictLLVMType, err := s.g.lowerType(dictType)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	capacityPtr := C.LLVMBuildStructGEP2(s.builder, dictLLVMType, dictPtr, 3, cStringFree(sourceName+".iter.dict.capacity.ptr"))
	return s.loadValue(capacityPtr, usizeType, sourceName+".iter.dict.capacity")
}

func (s *functionState) emitIterLoopDictBucketPtr(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, indexValue C.LLVMValueRef, sourceName string) (*semantic.DictType, *semantic.GenericInstanceType, C.LLVMTypeRef, C.LLVMValueRef, error) {
	dictType, dictPtr, err := s.iterLoopDictSourcePtr(sourceAlloca, sourceType, sourceName)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	bucketType, bucketLLVMType, err := s.iterLoopDictBucketType(dictType)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	dictLLVMType, err := s.g.lowerType(dictType)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	itemsPtrPtr := C.LLVMBuildStructGEP2(s.builder, dictLLVMType, dictPtr, 0, cStringFree(sourceName+".iter.dict.items.ptr"))
	itemsPtr, err := s.loadValue(itemsPtrPtr, &semantic.RefType{Elem: bucketType, Mutable: true, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny}, sourceName+".iter.dict.items")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	bucketPtr := C.LLVMBuildGEP2(s.builder, bucketLLVMType, itemsPtr, llvmValueSlicePtr([]C.LLVMValueRef{indexValue}), 1, cStringFree(sourceName+".iter.dict.bucket"))
	return dictType, bucketType, bucketLLVMType, bucketPtr, nil
}

func (s *functionState) emitIterLoopDictOccupied(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, indexValue C.LLVMValueRef, sourceName string) (C.LLVMValueRef, error) {
	_, _, bucketLLVMType, bucketPtr, err := s.emitIterLoopDictBucketPtr(sourceAlloca, sourceType, indexValue, sourceName)
	if err != nil {
		return nil, err
	}
	statePtr := C.LLVMBuildStructGEP2(s.builder, bucketLLVMType, bucketPtr, 2, cStringFree(sourceName+".iter.dict.state.ptr"))
	stateValue, err := s.loadValue(statePtr, s.g.result.NamedTypes["u8"], sourceName+".iter.dict.state")
	if err != nil {
		return nil, err
	}
	occupied := C.LLVMConstInt(C.LLVMInt8TypeInContext(s.g.context), 1, 0)
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), stateValue, occupied, cStringFree(sourceName+".iter.dict.occupied")), nil
}

func (s *functionState) emitIterLoopDictEntryValue(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, indexValue C.LLVMValueRef, sourceName string) (C.LLVMValueRef, semantic.Type, error) {
	dictType, _, bucketLLVMType, bucketPtr, err := s.emitIterLoopDictBucketPtr(sourceAlloca, sourceType, indexValue, sourceName)
	if err != nil {
		return nil, nil, err
	}
	itemType := semantic.DictEntryTupleType(dictType)
	itemLLVMType, err := s.g.lowerType(itemType)
	if err != nil {
		return nil, nil, err
	}
	keyPtr := C.LLVMBuildStructGEP2(s.builder, bucketLLVMType, bucketPtr, 0, cStringFree(sourceName+".iter.dict.key.ptr"))
	valuePtr := C.LLVMBuildStructGEP2(s.builder, bucketLLVMType, bucketPtr, 1, cStringFree(sourceName+".iter.dict.value.ptr"))
	keyValue, err := s.loadValue(keyPtr, dictType.Key, sourceName+".iter.dict.key")
	if err != nil {
		return nil, nil, err
	}
	valueValue, err := s.loadValue(valuePtr, dictType.Value, sourceName+".iter.dict.value")
	if err != nil {
		return nil, nil, err
	}
	itemValue := C.LLVMGetUndef(itemLLVMType)
	itemValue = C.LLVMBuildInsertValue(s.builder, itemValue, keyValue, 0, cStringFree(sourceName+".iter.dict.item.key"))
	itemValue = C.LLVMBuildInsertValue(s.builder, itemValue, valueValue, 1, cStringFree(sourceName+".iter.dict.item.value"))
	return itemValue, itemType, nil
}

func (s *functionState) emitIterLoopCount(sourceExpr ast.Expr, sourceAlloca C.LLVMValueRef, sourceType semantic.Type, sourceName string) (C.LLVMValueRef, error) {
	if colExpr, ok := sourceExpr.(*ast.EnumColumnExpr); ok {
		return s.emitEnumColumnScanCount(colExpr, sourceName)
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	switch tt := sourceType.(type) {
	case *semantic.ArrayType:
		if !tt.HasConstSize {
			return nil, fmt.Errorf("iterable loop over %s requires constant array extent metadata", sourceType.String())
		}
		return C.LLVMConstInt(usizeLLVMType, C.ulonglong(tt.ConstSize), 0), nil
	case *semantic.DArrayType, *semantic.ViewType:
		containerLLVMType, err := s.g.lowerType(sourceType)
		if err != nil {
			return nil, err
		}
		lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceAlloca, 1, cStringFree(sourceName+".iter.len.ptr"))
		lenValue, err := s.loadValue(lenPtr, usizeType, sourceName+".iter.len")
		if err != nil {
			return nil, err
		}
		return lenValue, nil
	case *semantic.DictType:
		return s.emitIterLoopDictCapacity(sourceAlloca, sourceType, sourceName)
	case *semantic.StoreRowsViewType:
		return s.emitStoreRowsCount(sourceAlloca, sourceType, sourceName)
	case *semantic.DStrType:
		sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.source")
		if err != nil {
			return nil, err
		}
		lenValue, err := s.emitRuntimeStringLengthValue(sourceValue, sourceType, s.g.result.NamedTypes["i64"], sourceName+".iter.len")
		if err != nil {
			return nil, err
		}
		return s.coerceValue(lenValue, s.g.result.NamedTypes["i64"], usizeType)
	case *semantic.SViewType:
		containerLLVMType, err := s.g.lowerType(sourceType)
		if err != nil {
			return nil, err
		}
		lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceAlloca, 1, cStringFree(sourceName+".iter.len.ptr"))
		lenValue, err := s.loadValue(lenPtr, s.g.result.NamedTypes["i64"], sourceName+".iter.len")
		if err != nil {
			return nil, err
		}
		return s.coerceValue(lenValue, s.g.result.NamedTypes["i64"], usizeType)
	case *semantic.GenericInstanceType:
		if projectedSourceType, ok := semantic.TreeAttributeSequenceSourceType(tt); ok {
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.projected.source")
			if err != nil {
				return nil, err
			}
			projectedLLVMType, err := s.g.lowerType(sourceType)
			if err != nil {
				return nil, err
			}
			innerSourceAlloca, err := s.createEntryAlloca(sourceName+".iter.projected.inner", projectedSourceType)
			if err != nil {
				return nil, err
			}
			innerSourceValue := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree(sourceName+".iter.projected.inner.extract"))
			_ = projectedLLVMType
			C.LLVMBuildStore(s.builder, innerSourceValue, innerSourceAlloca)
			return s.emitIterLoopCount(nil, innerSourceAlloca, projectedSourceType, sourceName+".iter.projected")
		}
		if _, ok := semantic.ChunksExactViewItemType(tt); !ok {
			return nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
		}
		containerLLVMType, err := s.g.lowerType(sourceType)
		if err != nil {
			return nil, err
		}
		lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceAlloca, 2, cStringFree(sourceName+".iter.len.ptr"))
		lenValue, err := s.loadValue(lenPtr, usizeType, sourceName+".iter.len")
		if err != nil {
			return nil, err
		}
		return lenValue, nil
	case *semantic.StructType:
		if !isIterLoopRuntimeStringViewType(tt) {
			return nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
		}
		containerLLVMType, err := s.g.lowerType(sourceType)
		if err != nil {
			return nil, err
		}
		lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceAlloca, 1, cStringFree(sourceName+".iter.len.ptr"))
		lenValue, err := s.loadValue(lenPtr, s.g.result.NamedTypes["i64"], sourceName+".iter.len")
		if err != nil {
			return nil, err
		}
		return s.coerceValue(lenValue, s.g.result.NamedTypes["i64"], usizeType)
	case *semantic.RefType:
		if tt.State != semantic.RefStateNonNull {
			return nil, fmt.Errorf("iterable loop requires a non-null reference source, got %s", sourceType.String())
		}
		sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.ref")
		if err != nil {
			return nil, err
		}
		switch elem := tt.Elem.(type) {
		case *semantic.ArrayType:
			if !elem.HasConstSize {
				return nil, fmt.Errorf("iterable loop over %s requires constant array extent metadata", sourceType.String())
			}
			return C.LLVMConstInt(usizeLLVMType, C.ulonglong(elem.ConstSize), 0), nil
		case *semantic.DArrayType, *semantic.ViewType:
			containerLLVMType, err := s.g.lowerType(tt.Elem)
			if err != nil {
				return nil, err
			}
			lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceValue, 1, cStringFree(sourceName+".iter.len.ptr"))
			lenValue, err := s.loadValue(lenPtr, usizeType, sourceName+".iter.len")
			if err != nil {
				return nil, err
			}
			return lenValue, nil
		case *semantic.DictType:
			return s.emitIterLoopDictCapacity(sourceAlloca, sourceType, sourceName)
		case *semantic.StoreRowsViewType:
			return s.emitStoreRowsCount(sourceAlloca, sourceType, sourceName)
		case *semantic.DStrType:
			lenValue, err := s.emitRuntimeStringLengthValue(sourceValue, tt.Elem, s.g.result.NamedTypes["i64"], sourceName+".iter.len")
			if err != nil {
				return nil, err
			}
			return s.coerceValue(lenValue, s.g.result.NamedTypes["i64"], usizeType)
		case *semantic.SViewType:
			containerLLVMType, err := s.g.lowerType(tt.Elem)
			if err != nil {
				return nil, err
			}
			lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceValue, 1, cStringFree(sourceName+".iter.len.ptr"))
			lenValue, err := s.loadValue(lenPtr, s.g.result.NamedTypes["i64"], sourceName+".iter.len")
			if err != nil {
				return nil, err
			}
			return s.coerceValue(lenValue, s.g.result.NamedTypes["i64"], usizeType)
		case *semantic.StructType:
			if !isIterLoopRuntimeStringViewType(elem) {
				return nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
			}
			containerLLVMType, err := s.g.lowerType(tt.Elem)
			if err != nil {
				return nil, err
			}
			lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceValue, 1, cStringFree(sourceName+".iter.len.ptr"))
			lenValue, err := s.loadValue(lenPtr, s.g.result.NamedTypes["i64"], sourceName+".iter.len")
			if err != nil {
				return nil, err
			}
			return s.coerceValue(lenValue, s.g.result.NamedTypes["i64"], usizeType)
		case *semantic.GenericInstanceType:
			if _, ok := semantic.ChunksExactViewItemType(elem); !ok {
				return nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
			}
			containerLLVMType, err := s.g.lowerType(tt.Elem)
			if err != nil {
				return nil, err
			}
			lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceValue, 2, cStringFree(sourceName+".iter.len.ptr"))
			lenValue, err := s.loadValue(lenPtr, usizeType, sourceName+".iter.len")
			if err != nil {
				return nil, err
			}
			return lenValue, nil
		default:
			return nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
		}
	default:
		return nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
	}
}
func (s *functionState) emitIterLoopElementAddress(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, indexValue C.LLVMValueRef, sourceName string) (C.LLVMValueRef, semantic.Type, error) {
	zero := C.LLVMConstInt(C.LLVMInt32TypeInContext(s.g.context), 0, 0)
	switch tt := sourceType.(type) {
	case *semantic.ArrayType:
		arrayLLVMType, err := s.g.lowerType(tt)
		if err != nil {
			return nil, nil, err
		}
		indices := []C.LLVMValueRef{zero, indexValue}
		ptr := C.LLVMBuildGEP2(s.builder, arrayLLVMType, sourceAlloca, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(sourceName+".iter.ptr"))
		return ptr, tt.Elem, nil
	case *semantic.DArrayType:
		containerLLVMType, err := s.g.lowerType(tt)
		if err != nil {
			return nil, nil, err
		}
		return s.emitRuntimePointerIndexedAddressWithType(sourceAlloca, containerLLVMType, tt.Elem, indexValue)
	case *semantic.ViewType:
		containerLLVMType, err := s.g.lowerType(tt)
		if err != nil {
			return nil, nil, err
		}
		return s.emitRuntimePointerIndexedAddressWithType(sourceAlloca, containerLLVMType, tt.Elem, indexValue)
	case *semantic.StoreRowsViewType:
		return nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
	case *semantic.GenericInstanceType:
		if _, ok := semantic.TreeChildrenItemType(tt); ok {
			return nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
		}
		return nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
	case *semantic.RefType:
		if tt.State != semantic.RefStateNonNull {
			return nil, nil, fmt.Errorf("iterable loop requires a non-null reference source, got %s", sourceType.String())
		}
		sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.ref")
		if err != nil {
			return nil, nil, err
		}
		switch elem := tt.Elem.(type) {
		case *semantic.ArrayType:
			arrayLLVMType, err := s.g.lowerType(elem)
			if err != nil {
				return nil, nil, err
			}
			indices := []C.LLVMValueRef{zero, indexValue}
			ptr := C.LLVMBuildGEP2(s.builder, arrayLLVMType, sourceValue, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(sourceName+".iter.ptr"))
			return ptr, elem.Elem, nil
		case *semantic.DArrayType:
			containerLLVMType, err := s.g.lowerType(tt.Elem)
			if err != nil {
				return nil, nil, err
			}
			return s.emitRuntimePointerIndexedAddressWithType(sourceValue, containerLLVMType, elem.Elem, indexValue)
		case *semantic.ViewType:
			containerLLVMType, err := s.g.lowerType(tt.Elem)
			if err != nil {
				return nil, nil, err
			}
			return s.emitRuntimePointerIndexedAddressWithType(sourceValue, containerLLVMType, elem.Elem, indexValue)
		case *semantic.StoreRowsViewType:
			return nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
		default:
			return nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
		}
	default:
		return nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
	}
}
func (s *functionState) emitIterLoopStringIndexValue(stringValue C.LLVMValueRef, operandType semantic.Type, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	helperName, _, ok := runtimeStringIndexedOperand(operandType)
	if !ok {
		return nil, nil, fmt.Errorf("iterable loop string index is not supported for %s", operandType.String())
	}
	indexType := s.g.result.NamedTypes["i64"]
	coercedIndex, err := s.coerceValue(indexValue, s.g.result.NamedTypes["usize"], indexType)
	if err != nil {
		return nil, nil, err
	}
	resultType := s.g.result.NamedTypes["char"]
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{operandType, indexType},
		Return: resultType,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stringValue, coercedIndex}, name)
	return call, resultType, nil
}
func (s *functionState) emitIterLoopElementValue(sourceExpr ast.Expr, sourceAlloca C.LLVMValueRef, sourceType semantic.Type, indexValue C.LLVMValueRef, sourceName string) (C.LLVMValueRef, semantic.Type, error) {
	if colExpr, ok := sourceExpr.(*ast.EnumColumnExpr); ok {
		return s.emitEnumColumnScanElement(colExpr, indexValue, sourceName)
	}
	switch tt := sourceType.(type) {
	case *semantic.ArrayType:
		ptr, elemType, err := s.emitIterLoopElementAddress(sourceAlloca, sourceType, indexValue, sourceName)
		if err != nil {
			return nil, nil, err
		}
		if tt.SurfaceName == "str" || tt.SurfaceName == "string" {
			loaded, err := s.loadValue(ptr, elemType, sourceName+".iter.byte")
			if err != nil {
				return nil, nil, err
			}
			llvmResultType, err := s.g.lowerType(s.g.result.NamedTypes["char"])
			if err != nil {
				return nil, nil, err
			}
			return C.LLVMBuildZExt(s.builder, loaded, llvmResultType, cStringFree(sourceName+".iter.char")), s.g.result.NamedTypes["char"], nil
		}
		value, err := s.loadValue(ptr, elemType, sourceName+".iter.value")
		return value, elemType, err
	case *semantic.DArrayType, *semantic.ViewType:
		ptr, elemType, err := s.emitIterLoopElementAddress(sourceAlloca, sourceType, indexValue, sourceName)
		if err != nil {
			return nil, nil, err
		}
		value, err := s.loadValue(ptr, elemType, sourceName+".iter.value")
		return value, elemType, err
	case *semantic.DictType:
		return s.emitIterLoopDictEntryValue(sourceAlloca, sourceType, indexValue, sourceName)
	case *semantic.StoreRowsViewType:
		return s.emitStoreRowItemValue(sourceAlloca, sourceType, indexValue, sourceName)
	case *semantic.DStrType, *semantic.SViewType:
		sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.source")
		if err != nil {
			return nil, nil, err
		}
		return s.emitIterLoopStringIndexValue(sourceValue, sourceType, indexValue, sourceName+".iter.char")
	case *semantic.GenericInstanceType:
		if _, ok := semantic.ChunksExactViewItemType(tt); !ok {
			return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
		}
		sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.source")
		if err != nil {
			return nil, nil, err
		}
		return s.emitIterLoopChunksExactItemValue(sourceValue, tt, indexValue, sourceName)
	case *semantic.StructType:
		if !isIterLoopRuntimeStringViewType(tt) {
			return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
		}
		sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.source")
		if err != nil {
			return nil, nil, err
		}
		return s.emitIterLoopStringIndexValue(sourceValue, sourceType, indexValue, sourceName+".iter.char")
	case *semantic.RefType:
		switch elem := tt.Elem.(type) {
		case *semantic.ArrayType, *semantic.DArrayType, *semantic.ViewType:
			ptr, elemType, err := s.emitIterLoopElementAddress(sourceAlloca, sourceType, indexValue, sourceName)
			if err != nil {
				return nil, nil, err
			}
			if arrayElem, ok := elem.(*semantic.ArrayType); ok && (arrayElem.SurfaceName == "str" || arrayElem.SurfaceName == "string") {
				loaded, err := s.loadValue(ptr, elemType, sourceName+".iter.byte")
				if err != nil {
					return nil, nil, err
				}
				llvmResultType, err := s.g.lowerType(s.g.result.NamedTypes["char"])
				if err != nil {
					return nil, nil, err
				}
				return C.LLVMBuildZExt(s.builder, loaded, llvmResultType, cStringFree(sourceName+".iter.char")), s.g.result.NamedTypes["char"], nil
			}
			value, err := s.loadValue(ptr, elemType, sourceName+".iter.value")
			return value, elemType, err
		case *semantic.DictType:
			return s.emitIterLoopDictEntryValue(sourceAlloca, sourceType, indexValue, sourceName)
		case *semantic.StoreRowsViewType:
			return s.emitStoreRowItemValue(sourceAlloca, sourceType, indexValue, sourceName)
		case *semantic.DStrType, *semantic.SViewType:
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.ref")
			if err != nil {
				return nil, nil, err
			}
			if _, ok := elem.(*semantic.SViewType); ok {
				loadedView, err := s.loadValue(sourceValue, tt.Elem, sourceName+".iter.view")
				if err != nil {
					return nil, nil, err
				}
				return s.emitIterLoopStringIndexValue(loadedView, tt.Elem, indexValue, sourceName+".iter.char")
			}
			return s.emitIterLoopStringIndexValue(sourceValue, tt.Elem, indexValue, sourceName+".iter.char")
		case *semantic.StructType:
			if !isIterLoopRuntimeStringViewType(elem) {
				return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
			}
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.ref")
			if err != nil {
				return nil, nil, err
			}
			loadedView, err := s.loadValue(sourceValue, tt.Elem, sourceName+".iter.view")
			if err != nil {
				return nil, nil, err
			}
			return s.emitIterLoopStringIndexValue(loadedView, tt.Elem, indexValue, sourceName+".iter.char")
		case *semantic.GenericInstanceType:
			if _, ok := semantic.ChunksExactViewItemType(elem); !ok {
				return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
			}
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.ref")
			if err != nil {
				return nil, nil, err
			}
			loadedCarrier, err := s.loadValue(sourceValue, tt.Elem, sourceName+".iter.chunks")
			if err != nil {
				return nil, nil, err
			}
			return s.emitIterLoopChunksExactItemValue(loadedCarrier, elem, indexValue, sourceName)
		default:
			return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
		}
	default:
		return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
	}
}
func (s *functionState) emitIterLoopRefLocal(name string, refType *semantic.RefType, ptrValue C.LLVMValueRef) error {
	if name == "_" {
		return nil
	}
	alloca, err := s.createEntryAlloca(name, refType)
	if err != nil {
		return err
	}
	s.defineBinding(name, valueBinding{ptr: alloca, typ: refType})
	C.LLVMBuildStore(s.builder, ptrValue, alloca)
	return nil
}
