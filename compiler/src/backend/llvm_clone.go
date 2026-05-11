//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func (s *functionState) emitBuiltinCloneCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil || callSpecializedIdentName(expr) != "clone" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("clone expects 1 argument, got %d", len(expr.Args))
	}
	targetType := s.exprType(expr)
	sourceType := s.exprType(expr.Args[0])
	if targetType == nil || sourceType == nil {
		return nil, nil, true, fmt.Errorf("clone requires resolved source and target types")
	}
	sourceValue, _, err := s.emitExpr(expr.Args[0], sourceType)
	if err != nil {
		return nil, nil, true, err
	}
	value, err := s.emitCloneValue(sourceValue, sourceType, targetType, "clone")
	if err != nil {
		return nil, nil, true, err
	}
	return value, targetType, true, nil
}

func (s *functionState) emitCloneValue(sourceValue C.LLVMValueRef, sourceType semantic.Type, targetType semantic.Type, name string) (C.LLVMValueRef, error) {
	targetType = semantic.StripAggregateStateType(targetType)
	sourceType = semantic.StripAggregateStateType(sourceType)
	if targetType == nil || sourceType == nil {
		return nil, fmt.Errorf("clone requires concrete source and target types")
	}
	switch tt := targetType.(type) {
	case *semantic.BuiltinType, *semantic.IDType, *semantic.ConstEnumType, *semantic.ErrorSetType, *semantic.NullType, *semantic.DStrType, *semantic.SViewType:
		return s.coerceValue(sourceValue, sourceType, targetType)
	case *semantic.ArrayType:
		return s.emitCloneArrayValue(sourceValue, sourceType, tt, name)
	case *semantic.DArrayType:
		return s.emitCloneDArrayValue(sourceValue, sourceType, tt, name)
	case *semantic.OptionalType:
		return s.emitCloneOptionalValue(sourceValue, sourceType, tt, name)
	case *semantic.ErrorUnionType:
		return s.emitCloneErrorUnionValue(sourceValue, sourceType, tt, name)
	case *semantic.TupleType, *semantic.StructType, *semantic.GenericInstanceType:
		return s.emitCloneStructLikeValue(sourceValue, targetType, name)
	case *semantic.TreeNodeType, *semantic.TreeCategoryType, *semantic.TreeBlockType, *semantic.TreeStructType:
		return s.emitCloneTreeValue(sourceValue, sourceType, targetType, name)
	default:
		return nil, fmt.Errorf("clone does not support %s in v1", targetType.String())
	}
}

func (s *functionState) emitCloneStructLikeValue(sourceValue C.LLVMValueRef, targetType semantic.Type, name string) (C.LLVMValueRef, error) {
	fields, err := s.g.structLiteralFields(targetType)
	if err != nil {
		return nil, err
	}
	llvmType, err := s.g.lowerType(targetType)
	if err != nil {
		return nil, err
	}
	value := C.LLVMGetUndef(llvmType)
	for _, field := range fields {
		sourceField := C.LLVMBuildExtractValue(s.builder, sourceValue, C.unsigned(field.Index), cStringFree(name+"."+field.Decl.Name+".src"))
		clonedField, err := s.emitCloneValue(sourceField, field.Type, field.Type, name+"."+field.Decl.Name)
		if err != nil {
			return nil, err
		}
		value = C.LLVMBuildInsertValue(s.builder, value, clonedField, C.unsigned(field.Index), cStringFree(name+"."+field.Decl.Name+".dst"))
	}
	return value, nil
}

func (s *functionState) emitCloneArrayValue(sourceValue C.LLVMValueRef, sourceType semantic.Type, targetType *semantic.ArrayType, name string) (C.LLVMValueRef, error) {
	sourceArray, ok := sourceType.(*semantic.ArrayType)
	if !ok || sourceArray == nil || !targetType.HasConstSize {
		return nil, fmt.Errorf("clone array expects a fixed array source")
	}
	llvmType, err := s.g.lowerType(targetType)
	if err != nil {
		return nil, err
	}
	value := C.LLVMGetUndef(llvmType)
	for i := int64(0); i < targetType.ConstSize; i++ {
		sourceElem := C.LLVMBuildExtractValue(s.builder, sourceValue, C.unsigned(i), cStringFree(name+".src.elem"))
		clonedElem, err := s.emitCloneValue(sourceElem, sourceArray.Elem, targetType.Elem, name+".elem")
		if err != nil {
			return nil, err
		}
		value = C.LLVMBuildInsertValue(s.builder, value, clonedElem, C.unsigned(i), cStringFree(name+".dst.elem"))
	}
	return value, nil
}

func (s *functionState) emitCloneDArrayValue(sourceValue C.LLVMValueRef, sourceType semantic.Type, targetType *semantic.DArrayType, name string) (C.LLVMValueRef, error) {
	owner, ok := s.lookupTreeAllocOwner()
	if !ok {
		return nil, fmt.Errorf("clone of %s requires an active in <owner>: scope", targetType.String())
	}
	llvmResultType, err := s.g.lowerType(targetType)
	if err != nil {
		return nil, err
	}
	zeroResult, err := s.zeroValue(targetType)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	zeroCount := C.LLVMConstInt(usizeLLVMType, 0, 0)

	cloneDynamicElems := func(sourceData C.LLVMValueRef, sourceElemType semantic.Type, countValue C.LLVMValueRef) (C.LLVMValueRef, error) {
		elemSizeBytes, err := s.sizeOfType(targetType.Elem)
		if err != nil {
			return nil, err
		}
		byteCount := C.LLVMBuildMul(s.builder, countValue, C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSizeBytes), 0), cStringFree(name+".bytes"))
		allocPtr, err := s.emitTreeOwnerAllocBytes(owner, byteCount, name)
		if err != nil {
			return nil, err
		}
		elemLLVMType, err := s.g.lowerType(targetType.Elem)
		if err != nil {
			return nil, err
		}
		indexPtr := C.LLVMBuildAlloca(s.builder, usizeLLVMType, cStringFree(name+".index"))
		one := C.LLVMConstInt(usizeLLVMType, 1, 0)
		C.LLVMBuildStore(s.builder, zeroCount, indexPtr)
		loopBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".loop"))
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".body"))
		endBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".end"))
		C.LLVMBuildBr(s.builder, loopBB)
		C.LLVMPositionBuilderAtEnd(s.builder, loopBB)
		indexValue := C.LLVMBuildLoad2(s.builder, usizeLLVMType, indexPtr, cStringFree(name+".index.load"))
		more := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, countValue, cStringFree(name+".cond"))
		C.LLVMBuildCondBr(s.builder, more, bodyBB, endBB)

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		sourcePtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, sourceData, llvmValueSlicePtr([]C.LLVMValueRef{indexValue}), 1, cStringFree(name+".src.ptr"))
		sourceElem := C.LLVMBuildLoad2(s.builder, elemLLVMType, sourcePtr, cStringFree(name+".src.elem"))
		clonedElem := sourceElem
		if !treeCloneCanReuseDenseHandle(sourceElemType, targetType.Elem) {
			cloned, err := s.emitCloneValue(sourceElem, sourceElemType, targetType.Elem, name+".elem")
			if err != nil {
				return nil, err
			}
			clonedElem = cloned
		}
		destPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, allocPtr, llvmValueSlicePtr([]C.LLVMValueRef{indexValue}), 1, cStringFree(name+".dst.ptr"))
		C.LLVMBuildStore(s.builder, clonedElem, destPtr)
		next := C.LLVMBuildAdd(s.builder, indexValue, one, cStringFree(name+".next"))
		C.LLVMBuildStore(s.builder, next, indexPtr)
		C.LLVMBuildBr(s.builder, loopBB)

		C.LLVMPositionBuilderAtEnd(s.builder, endBB)
		materialized := C.LLVMGetUndef(llvmResultType)
		materialized = C.LLVMBuildInsertValue(s.builder, materialized, allocPtr, 0, cStringFree(name+".items"))
		materialized = C.LLVMBuildInsertValue(s.builder, materialized, countValue, 1, cStringFree(name+".count"))
		materialized = C.LLVMBuildInsertValue(s.builder, materialized, countValue, 2, cStringFree(name+".capacity"))
		return materialized, nil
	}

	buildMaterializedFromDynamicSource := func(sourceData C.LLVMValueRef, sourceElemType semantic.Type, countValue C.LLVMValueRef) (C.LLVMValueRef, error) {
		zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), countValue, zeroCount, cStringFree(name+".count.zero"))
		allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".alloc"))
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".merge"))
		entryBlock := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, allocBB)

		C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
		materialized, err := cloneDynamicElems(sourceData, sourceElemType, countValue)
		if err != nil {
			return nil, err
		}
		allocEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)

		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		phi := C.LLVMBuildPhi(s.builder, llvmResultType, cStringFree(name+".result"))
		C.LLVMAddIncoming(phi, llvmValueSlicePtr([]C.LLVMValueRef{zeroResult, materialized}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{entryBlock, allocEnd}), 2)
		return phi, nil
	}

	switch st := sourceType.(type) {
	case *semantic.DArrayType:
		sourceData := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree(name+".src.data"))
		countValue := C.LLVMBuildExtractValue(s.builder, sourceValue, 1, cStringFree(name+".src.count"))
		return buildMaterializedFromDynamicSource(sourceData, st.Elem, countValue)
	case *semantic.DArrayViewType:
		sourceData := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree(name+".src.data"))
		countValue := C.LLVMBuildExtractValue(s.builder, sourceValue, 1, cStringFree(name+".src.len"))
		return buildMaterializedFromDynamicSource(sourceData, st.Elem, countValue)
	case *semantic.ArrayType:
		if !st.HasConstSize {
			return nil, fmt.Errorf("clone does not support non-constant array sources")
		}
		if st.ConstSize == 0 {
			return zeroResult, nil
		}
		elemSizeBytes, err := s.sizeOfType(targetType.Elem)
		if err != nil {
			return nil, err
		}
		countConst := C.LLVMConstInt(usizeLLVMType, C.ulonglong(st.ConstSize), 0)
		byteCount := C.LLVMBuildMul(s.builder, countConst, C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSizeBytes), 0), cStringFree(name+".bytes"))
		allocPtr, err := s.emitTreeOwnerAllocBytes(owner, byteCount, name)
		if err != nil {
			return nil, err
		}
		elemLLVMType, err := s.g.lowerType(targetType.Elem)
		if err != nil {
			return nil, err
		}
		for i := int64(0); i < st.ConstSize; i++ {
			sourceElem := C.LLVMBuildExtractValue(s.builder, sourceValue, C.unsigned(i), cStringFree(name+".src.elem"))
			clonedElem := sourceElem
			if !treeCloneCanReuseDenseHandle(st.Elem, targetType.Elem) {
				cloned, err := s.emitCloneValue(sourceElem, st.Elem, targetType.Elem, name+".elem")
				if err != nil {
					return nil, err
				}
				clonedElem = cloned
			}
			indexValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(i), 0)
			destPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, allocPtr, llvmValueSlicePtr([]C.LLVMValueRef{indexValue}), 1, cStringFree(name+".dst.ptr"))
			C.LLVMBuildStore(s.builder, clonedElem, destPtr)
		}
		materialized := C.LLVMGetUndef(llvmResultType)
		materialized = C.LLVMBuildInsertValue(s.builder, materialized, allocPtr, 0, cStringFree(name+".items"))
		materialized = C.LLVMBuildInsertValue(s.builder, materialized, countConst, 1, cStringFree(name+".count"))
		materialized = C.LLVMBuildInsertValue(s.builder, materialized, countConst, 2, cStringFree(name+".capacity"))
		return materialized, nil
	default:
		return nil, fmt.Errorf("clone cannot materialize %s into %s", sourceType.String(), targetType.String())
	}
}

func (s *functionState) emitCloneOptionalValue(sourceValue C.LLVMValueRef, sourceType semantic.Type, targetType *semantic.OptionalType, name string) (C.LLVMValueRef, error) {
	sourceOptional, ok := sourceType.(*semantic.OptionalType)
	if !ok || sourceOptional == nil {
		return nil, fmt.Errorf("clone optional expects optional source")
	}
	presentValue, err := s.extractOptionalPresent(sourceValue, sourceOptional)
	if err != nil {
		return nil, err
	}
	presentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".present"))
	absentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".absent"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".merge"))
	C.LLVMBuildCondBr(s.builder, presentValue, presentBB, absentBB)

	C.LLVMPositionBuilderAtEnd(s.builder, presentBB)
	payloadValue, err := s.extractOptionalPayload(sourceValue, sourceOptional)
	if err != nil {
		return nil, err
	}
	clonedPayload, err := s.emitCloneValue(payloadValue, sourceOptional.Value, targetType.Value, name+".payload")
	if err != nil {
		return nil, err
	}
	someValue, err := s.buildOptionalSome(targetType, clonedPayload)
	if err != nil {
		return nil, err
	}
	presentEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, absentBB)
	noneValue, err := s.buildOptionalNone(targetType)
	if err != nil {
		return nil, err
	}
	absentEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	optionalLLVMType, err := s.g.lowerType(targetType)
	if err != nil {
		return nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, optionalLLVMType, cStringFree(name+".result"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr([]C.LLVMValueRef{someValue, noneValue}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{presentEnd, absentEnd}), 2)
	return phi, nil
}

func (s *functionState) emitCloneErrorUnionValue(sourceValue C.LLVMValueRef, sourceType semantic.Type, targetType *semantic.ErrorUnionType, name string) (C.LLVMValueRef, error) {
	sourceUnion, ok := sourceType.(*semantic.ErrorUnionType)
	if !ok || sourceUnion == nil {
		return nil, fmt.Errorf("clone error union expects error-union source")
	}
	errorCode, err := s.extractErrorUnionCode(sourceValue, sourceUnion)
	if err != nil {
		return nil, err
	}
	codeLLVMType, err := s.g.lowerType(sourceUnion.Errors)
	if err != nil {
		return nil, err
	}
	zeroCode := C.LLVMConstInt(codeLLVMType, 0, 0)
	success := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), errorCode, zeroCode, cStringFree(name+".ok"))
	successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".success"))
	failureBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".failure"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".merge"))
	C.LLVMBuildCondBr(s.builder, success, successBB, failureBB)

	C.LLVMPositionBuilderAtEnd(s.builder, successBB)
	payloadValue, err := s.extractErrorUnionPayload(sourceValue, sourceUnion)
	if err != nil {
		return nil, err
	}
	clonedPayload, err := s.emitCloneValue(payloadValue, sourceUnion.Value, targetType.Value, name+".payload")
	if err != nil {
		return nil, err
	}
	successValue, err := s.buildErrorUnionSuccess(targetType, clonedPayload)
	if err != nil {
		return nil, err
	}
	successEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, failureBB)
	failureValue, err := s.buildErrorUnionFailure(targetType, errorCode)
	if err != nil {
		return nil, err
	}
	failureEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	unionLLVMType, err := s.g.lowerType(targetType)
	if err != nil {
		return nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, unionLLVMType, cStringFree(name+".result"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr([]C.LLVMValueRef{successValue, failureValue}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{successEnd, failureEnd}), 2)
	return phi, nil
}

func (s *functionState) emitCloneTreeValue(sourceValue C.LLVMValueRef, sourceType semantic.Type, targetType semantic.Type, name string) (C.LLVMValueRef, error) {
	if treeCloneCanReuseDenseHandle(sourceType, targetType) {
		return s.coerceValue(sourceValue, sourceType, targetType)
	}
	rootName := semantic.StripAggregateStateType(targetType).String()
	switch tt := semantic.StripAggregateStateType(targetType).(type) {
	case *semantic.TreeCategoryType:
		if tt != nil && treeCategoryLayoutPlan(tt).isCategoryUnion() {
			rootName = tt.String()
		} else if tt != nil && tt.Family != nil && tt.Family.NodeType != nil {
			rootName = tt.Family.NodeType.String()
		}
	case *semantic.TreeNodeType:
		if tt != nil && tt.Family != nil && tt.Family.NodeType != nil {
			rootName = tt.Family.NodeType.String()
		}
	case *semantic.TreeBlockType:
		if tt != nil && tt.Family != nil && tt.Family.NodeType != nil {
			rootName = tt.Family.NodeType.String()
		}
	case *semantic.TreeStructType:
		if tt != nil && tt.Family != nil && tt.Family.NodeType != nil {
			rootName = tt.Family.NodeType.String()
		}
	}
	rootExpr := &ast.NamedType{Name: rootName}
	root, err := s.resolveTreeFoldRootInfo(sourceType, rootExpr)
	if err != nil {
		return nil, err
	}
	helperExpr := &ast.FoldExpr{Keyword: "rewrite", RewriteDefault: true}
	captures := s.collectTreeFoldCaptures(helperExpr, root.family)
	envValue, envStruct, err := s.buildTreeFoldEnv(captures, name+".tree.clone")
	if err != nil {
		return nil, err
	}
	helper, err := s.newTreeFoldHelper(helperExpr, root, root.bindType(), captures, envStruct, true)
	if err != nil {
		return nil, err
	}
	if err := s.defineTreeFoldHelper(helperExpr, helper); err != nil {
		return nil, err
	}
	return s.emitTreeFoldHelperCall(helper, sourceValue, envValue, name)
}

func treeCloneCanReuseDenseHandle(sourceType semantic.Type, targetType semantic.Type) bool {
	sourceType = semantic.StripAggregateStateType(sourceType)
	targetType = semantic.StripAggregateStateType(targetType)
	if sourceType == nil || targetType == nil || !semantic.SameType(sourceType, targetType) {
		return false
	}
	switch tt := targetType.(type) {
	case *semantic.TreeNodeType:
		return tt != nil && tt.Family != nil && tt.Family.Layout == semantic.TreeLayoutCategoryUnion
	case *semantic.TreeCategoryType:
		return tt != nil && tt.Family != nil && tt.Family.Layout == semantic.TreeLayoutCategoryUnion
	case *semantic.TreeBlockType:
		return tt != nil && tt.Family != nil && tt.Family.Layout == semantic.TreeLayoutCategoryUnion
	case *semantic.TreeStructType:
		return tt != nil && tt.Family != nil && tt.Family.Layout == semantic.TreeLayoutCategoryUnion
	default:
		return false
	}
}
