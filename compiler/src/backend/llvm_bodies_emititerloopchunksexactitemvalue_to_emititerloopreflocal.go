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
	case *semantic.DArrayType, *semantic.DArrayViewType:
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
	case *semantic.StoreRowsViewType:
		return s.emitStoreRowsCount(sourceAlloca, sourceType, sourceName)
	case *semantic.FrozenTreeRowsViewType:
		return s.emitFrozenTreeRowsCount(sourceAlloca, tt, sourceName)
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
		if sourceNodeType, ok := semantic.TreeChildrenSourceType(tt); ok {
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.source")
			if err != nil {
				return nil, err
			}
			nodeValue := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree(sourceName+".iter.tree.children.node"))
			return s.emitTreeChildrenCount(sourceNodeType, nodeValue, sourceName)
		}
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
		case *semantic.DArrayType, *semantic.DArrayViewType:
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
	case *semantic.DArrayViewType:
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
		case *semantic.DArrayViewType:
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
	case *semantic.DArrayType, *semantic.DArrayViewType:
		ptr, elemType, err := s.emitIterLoopElementAddress(sourceAlloca, sourceType, indexValue, sourceName)
		if err != nil {
			return nil, nil, err
		}
		value, err := s.loadValue(ptr, elemType, sourceName+".iter.value")
		return value, elemType, err
	case *semantic.StoreRowsViewType:
		return s.emitStoreRowItemValue(sourceAlloca, sourceType, indexValue, sourceName)
	case *semantic.FrozenTreeRowsViewType:
		return s.emitFrozenTreeRowsItemValue(sourceAlloca, tt, indexValue, sourceName)
	case *semantic.DStrType, *semantic.SViewType:
		sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.source")
		if err != nil {
			return nil, nil, err
		}
		return s.emitIterLoopStringIndexValue(sourceValue, sourceType, indexValue, sourceName+".iter.char")
	case *semantic.GenericInstanceType:
		if sourceNodeType, ok := semantic.TreeChildrenSourceType(tt); ok {
			itemType, ok := semantic.TreeChildrenItemType(tt)
			if !ok {
				return nil, nil, fmt.Errorf("unsupported tree children iterable %s", sourceType.String())
			}
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.source")
			if err != nil {
				return nil, nil, err
			}
			nodeValue := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree(sourceName+".iter.tree.children.node"))
			value, err := s.emitTreeChildrenValue(sourceNodeType, nodeValue, itemType, indexValue, sourceName)
			if err != nil {
				return nil, nil, err
			}
			return value, itemType, nil
		}
		if projectedSourceType, ok := semantic.TreeAttributeSequenceSourceType(tt); ok {
			attrRef := treeAttributeFieldRefForExpr(s.g.result, sourceExpr)
			if attrRef == nil || attrRef.Attribute == nil {
				return nil, nil, fmt.Errorf("missing projected tree attribute metadata for iterable source")
			}
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.projected.source")
			if err != nil {
				return nil, nil, err
			}
			innerSourceAlloca, err := s.createEntryAlloca(sourceName+".iter.projected.inner", projectedSourceType)
			if err != nil {
				return nil, nil, err
			}
			innerSourceValue := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree(sourceName+".iter.projected.inner.extract"))
			C.LLVMBuildStore(s.builder, innerSourceValue, innerSourceAlloca)
			itemValue, itemType, err := s.emitIterLoopElementValue(nil, innerSourceAlloca, projectedSourceType, indexValue, sourceName+".iter.projected")
			if err != nil {
				return nil, nil, err
			}
			itemValue, err = s.coerceValue(itemValue, itemType, attrRef.Attribute.Receiver)
			if err != nil {
				return nil, nil, err
			}
			helper, err := s.ensureTreeAttributeHelper(attrRef.Attribute)
			if err != nil {
				return nil, nil, err
			}
			projectedValue, err := s.emitTreeAttributeHelperCall(helper, itemValue, sourceName+".iter.projected.attr")
			if err != nil {
				return nil, nil, err
			}
			return projectedValue, attrRef.Attribute.ReturnType, nil
		}
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
		case *semantic.ArrayType, *semantic.DArrayType, *semantic.DArrayViewType:
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
