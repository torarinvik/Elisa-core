//go:build cgo

package backend

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func (s *functionState) emitBuiltinCloneCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil || callSpecializedIdentName(expr) != "clone" || s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, "clone") {
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

// emitBuiltinCopyCall lowers copy[array[T, N]](src): a fixed-size, stack-owned
// array materialized element-for-element from a fixed-size array source. The
// semantic layer has already proven the size is a compile-time constant and the
// elements are pure value data (no region owner needed), so this is a plain
// elementwise value copy with no allocation.
func (s *functionState) emitBuiltinCopyCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil || callSpecializedIdentName(expr) != "copy" || s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, "copy") {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("copy expects 1 argument, got %d", len(expr.Args))
	}
	targetType := s.exprType(expr)
	sourceType := s.exprType(expr.Args[0])
	if targetType == nil || sourceType == nil {
		return nil, nil, true, fmt.Errorf("copy requires resolved source and target types")
	}
	sourceValue, _, err := s.emitExpr(expr.Args[0], sourceType)
	if err != nil {
		return nil, nil, true, err
	}
	arrayTarget, ok := semantic.StripAggregateStateType(targetType).(*semantic.ArrayType)
	if !ok || arrayTarget == nil {
		return nil, nil, true, fmt.Errorf("copy supports only fixed-size array targets in v1, got %s", targetType.String())
	}
	value, err := s.emitCloneArrayValue(sourceValue, semantic.StripAggregateStateType(sourceType), arrayTarget, "copy")
	if err != nil {
		return nil, nil, true, err
	}
	return value, targetType, true, nil
}

func (s *functionState) emitCloneValue(sourceValue llvmIRValueRef, sourceType semantic.Type, targetType semantic.Type, name string) (llvmIRValueRef, error) {
	targetType = semantic.StripAggregateStateType(targetType)
	sourceType = semantic.StripAggregateStateType(sourceType)
	if targetType == nil || sourceType == nil {
		return nil, fmt.Errorf("clone requires concrete source and target types")
	}
	switch tt := targetType.(type) {
	case *semantic.BuiltinType, *semantic.IDType, *semantic.AddressSpaceType, *semantic.ConstEnumType, *semantic.ErrorSetType, *semantic.NullType, *semantic.CStrType, *semantic.SViewType:
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
	case *semantic.EnumType:
		// Store-backed enum: the value IS the opaque handle (an integer); clone copies it.
		// The semantic layer only admits Packed enums here.
		if tt.Packed {
			return s.coerceValue(sourceValue, sourceType, targetType)
		}
		return nil, fmt.Errorf("clone does not support value enum %s in v1", targetType.String())
	default:
		return nil, fmt.Errorf("clone does not support %s in v1", targetType.String())
	}
}

func (s *functionState) emitCloneStructLikeValue(sourceValue llvmIRValueRef, targetType semantic.Type, name string) (llvmIRValueRef, error) {
	fields, err := s.g.structLiteralFields(targetType)
	if err != nil {
		return nil, err
	}
	llvmType, err := s.g.lowerType(targetType)
	if err != nil {
		return nil, err
	}
	value := llvmGetUndef(llvmType)
	for _, field := range fields {
		sourceField := llvmBuildExtractValue(s.builder, sourceValue, llvmIRUnsigned(field.Index), cStringFree(name+"."+field.Decl.Name+".src"))
		clonedField, err := s.emitCloneValue(sourceField, field.Type, field.Type, name+"."+field.Decl.Name)
		if err != nil {
			return nil, err
		}
		value = llvmBuildInsertValue(s.builder, value, clonedField, llvmIRUnsigned(field.Index), cStringFree(name+"."+field.Decl.Name+".dst"))
	}
	return value, nil
}

func (s *functionState) emitCloneArrayValue(sourceValue llvmIRValueRef, sourceType semantic.Type, targetType *semantic.ArrayType, name string) (llvmIRValueRef, error) {
	sourceArray, ok := sourceType.(*semantic.ArrayType)
	if !ok || sourceArray == nil || !targetType.HasConstSize {
		return nil, fmt.Errorf("clone array expects a fixed array source")
	}
	llvmType, err := s.g.lowerType(targetType)
	if err != nil {
		return nil, err
	}
	value := llvmGetUndef(llvmType)
	for i := int64(0); i < targetType.ConstSize; i++ {
		sourceElem := llvmBuildExtractValue(s.builder, sourceValue, llvmIRUnsigned(i), cStringFree(name+".src.elem"))
		clonedElem, err := s.emitCloneValue(sourceElem, sourceArray.Elem, targetType.Elem, name+".elem")
		if err != nil {
			return nil, err
		}
		value = llvmBuildInsertValue(s.builder, value, clonedElem, llvmIRUnsigned(i), cStringFree(name+".dst.elem"))
	}
	return value, nil
}

func (s *functionState) emitCloneDArrayValue(sourceValue llvmIRValueRef, sourceType semantic.Type, targetType *semantic.DArrayType, name string) (llvmIRValueRef, error) {
	owner, ok := s.regionArenaOwner(targetType.Region)
	if !ok {
		owner, ok = s.lookupTreeAllocOwner()
	}
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
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	zeroCount := llvmConstInt(usizeLLVMType, 0, 0)

	cloneDynamicElems := func(sourceData llvmIRValueRef, sourceElemType semantic.Type, countValue llvmIRValueRef) (llvmIRValueRef, error) {
		elemSizeBytes, err := s.sizeOfType(targetType.Elem)
		if err != nil {
			return nil, err
		}
		byteCount, err := s.emitCheckedElemByteCount(countValue, elemSizeBytes, name)
		if err != nil {
			return nil, err
		}
		allocPtr, err := s.emitTreeOwnerAllocBytes(owner, byteCount, name)
		if err != nil {
			return nil, err
		}
		elemLLVMType, err := s.g.lowerType(targetType.Elem)
		if err != nil {
			return nil, err
		}
		indexPtr := llvmBuildAlloca(s.builder, usizeLLVMType, cStringFree(name+".index"))
		one := llvmConstInt(usizeLLVMType, 1, 0)
		llvmBuildStore(s.builder, zeroCount, indexPtr)
		loopBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".loop"))
		bodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".body"))
		endBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".end"))
		llvmBuildBr(s.builder, loopBB)
		llvmPositionBuilderAtEnd(s.builder, loopBB)
		indexValue := llvmBuildLoad2(s.builder, usizeLLVMType, indexPtr, cStringFree(name+".index.load"))
		more := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), indexValue, countValue, cStringFree(name+".cond"))
		llvmBuildCondBr(s.builder, more, bodyBB, endBB)

		llvmPositionBuilderAtEnd(s.builder, bodyBB)
		sourcePtr := llvmBuildGEP2(s.builder, elemLLVMType, sourceData, llvmValueSlicePtr([]llvmIRValueRef{indexValue}), 1, cStringFree(name+".src.ptr"))
		sourceElem := llvmBuildLoad2(s.builder, elemLLVMType, sourcePtr, cStringFree(name+".src.elem"))
		clonedElem, err := s.emitCloneValue(sourceElem, sourceElemType, targetType.Elem, name+".elem")
		if err != nil {
			return nil, err
		}
		destPtr := llvmBuildGEP2(s.builder, elemLLVMType, allocPtr, llvmValueSlicePtr([]llvmIRValueRef{indexValue}), 1, cStringFree(name+".dst.ptr"))
		llvmBuildStore(s.builder, clonedElem, destPtr)
		next := llvmBuildAdd(s.builder, indexValue, one, cStringFree(name+".next"))
		llvmBuildStore(s.builder, next, indexPtr)
		llvmBuildBr(s.builder, loopBB)

		llvmPositionBuilderAtEnd(s.builder, endBB)
		materialized := llvmGetUndef(llvmResultType)
		materialized = llvmBuildInsertValue(s.builder, materialized, allocPtr, 0, cStringFree(name+".items"))
		materialized = llvmBuildInsertValue(s.builder, materialized, countValue, 1, cStringFree(name+".count"))
		materialized = llvmBuildInsertValue(s.builder, materialized, countValue, 2, cStringFree(name+".capacity"))
		return materialized, nil
	}

	buildMaterializedFromDynamicSource := func(sourceData llvmIRValueRef, sourceElemType semantic.Type, countValue llvmIRValueRef) (llvmIRValueRef, error) {
		zeroCond := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), countValue, zeroCount, cStringFree(name+".count.zero"))
		allocBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".alloc"))
		mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".merge"))
		entryBlock := llvmGetInsertBlock(s.builder)
		llvmBuildCondBr(s.builder, zeroCond, mergeBB, allocBB)

		llvmPositionBuilderAtEnd(s.builder, allocBB)
		materialized, err := cloneDynamicElems(sourceData, sourceElemType, countValue)
		if err != nil {
			return nil, err
		}
		allocEnd := llvmGetInsertBlock(s.builder)
		llvmBuildBr(s.builder, mergeBB)

		llvmPositionBuilderAtEnd(s.builder, mergeBB)
		phi := llvmBuildPhi(s.builder, llvmResultType, cStringFree(name+".result"))
		llvmAddIncoming(phi, llvmValueSlicePtr([]llvmIRValueRef{zeroResult, materialized}), llvmBlockSlicePtr([]llvmIRBlockRef{entryBlock, allocEnd}), 2)
		return phi, nil
	}

	switch st := sourceType.(type) {
	case *semantic.DArrayType:
		sourceData := llvmBuildExtractValue(s.builder, sourceValue, 0, cStringFree(name+".src.data"))
		countValue := llvmBuildExtractValue(s.builder, sourceValue, 1, cStringFree(name+".src.count"))
		return buildMaterializedFromDynamicSource(sourceData, st.Elem, countValue)
	case *semantic.ViewType:
		sourceData := llvmBuildExtractValue(s.builder, sourceValue, 0, cStringFree(name+".src.data"))
		countValue := llvmBuildExtractValue(s.builder, sourceValue, 1, cStringFree(name+".src.len"))
		return buildMaterializedFromDynamicSource(sourceData, st.Elem, countValue)
	case *semantic.SViewType:
		// sview = { data ptr (field 0), len i64 (field 1) }. Deep-copy the viewed
		// u8 bytes into the region, producing an owned darray[u8] / dstr.
		sourceData := llvmBuildExtractValue(s.builder, sourceValue, 0, cStringFree(name+".src.data"))
		lenValue := llvmBuildExtractValue(s.builder, sourceValue, 1, cStringFree(name+".src.len"))
		countValue, err := s.coerceValue(lenValue, s.g.namedType("i64"), usizeType)
		if err != nil {
			return nil, err
		}
		return buildMaterializedFromDynamicSource(sourceData, s.g.namedType("u8"), countValue)
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
		countConst := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(st.ConstSize), 0)
		byteCount := llvmBuildMul(s.builder, countConst, llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(elemSizeBytes), 0), cStringFree(name+".bytes"))
		allocPtr, err := s.emitTreeOwnerAllocBytes(owner, byteCount, name)
		if err != nil {
			return nil, err
		}
		elemLLVMType, err := s.g.lowerType(targetType.Elem)
		if err != nil {
			return nil, err
		}
		for i := int64(0); i < st.ConstSize; i++ {
			sourceElem := llvmBuildExtractValue(s.builder, sourceValue, llvmIRUnsigned(i), cStringFree(name+".src.elem"))
			clonedElem, err := s.emitCloneValue(sourceElem, st.Elem, targetType.Elem, name+".elem")
			if err != nil {
				return nil, err
			}
			indexValue := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(i), 0)
			destPtr := llvmBuildGEP2(s.builder, elemLLVMType, allocPtr, llvmValueSlicePtr([]llvmIRValueRef{indexValue}), 1, cStringFree(name+".dst.ptr"))
			llvmBuildStore(s.builder, clonedElem, destPtr)
		}
		materialized := llvmGetUndef(llvmResultType)
		materialized = llvmBuildInsertValue(s.builder, materialized, allocPtr, 0, cStringFree(name+".items"))
		materialized = llvmBuildInsertValue(s.builder, materialized, countConst, 1, cStringFree(name+".count"))
		materialized = llvmBuildInsertValue(s.builder, materialized, countConst, 2, cStringFree(name+".capacity"))
		return materialized, nil
	default:
		return nil, fmt.Errorf("clone cannot materialize %s into %s", sourceType.String(), targetType.String())
	}
}

func (s *functionState) emitCloneOptionalValue(sourceValue llvmIRValueRef, sourceType semantic.Type, targetType *semantic.OptionalType, name string) (llvmIRValueRef, error) {
	sourceOptional, ok := sourceType.(*semantic.OptionalType)
	if !ok || sourceOptional == nil {
		return nil, fmt.Errorf("clone optional expects optional source")
	}
	presentValue, err := s.extractOptionalPresent(sourceValue, sourceOptional)
	if err != nil {
		return nil, err
	}
	presentBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".present"))
	absentBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".absent"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".merge"))
	llvmBuildCondBr(s.builder, presentValue, presentBB, absentBB)

	llvmPositionBuilderAtEnd(s.builder, presentBB)
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
	presentEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, absentBB)
	noneValue, err := s.buildOptionalNone(targetType)
	if err != nil {
		return nil, err
	}
	absentEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	optionalLLVMType, err := s.g.lowerType(targetType)
	if err != nil {
		return nil, err
	}
	phi := llvmBuildPhi(s.builder, optionalLLVMType, cStringFree(name+".result"))
	llvmAddIncoming(phi, llvmValueSlicePtr([]llvmIRValueRef{someValue, noneValue}), llvmBlockSlicePtr([]llvmIRBlockRef{presentEnd, absentEnd}), 2)
	return phi, nil
}

func (s *functionState) emitCloneErrorUnionValue(sourceValue llvmIRValueRef, sourceType semantic.Type, targetType *semantic.ErrorUnionType, name string) (llvmIRValueRef, error) {
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
	zeroCode := llvmConstInt(codeLLVMType, 0, 0)
	success := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), errorCode, zeroCode, cStringFree(name+".ok"))
	successBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".success"))
	failureBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".failure"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".merge"))
	llvmBuildCondBr(s.builder, success, successBB, failureBB)

	llvmPositionBuilderAtEnd(s.builder, successBB)
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
	successEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, failureBB)
	failureValue, err := s.buildErrorUnionFailure(targetType, errorCode)
	if err != nil {
		return nil, err
	}
	failureEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	unionLLVMType, err := s.g.lowerType(targetType)
	if err != nil {
		return nil, err
	}
	phi := llvmBuildPhi(s.builder, unionLLVMType, cStringFree(name+".result"))
	llvmAddIncoming(phi, llvmValueSlicePtr([]llvmIRValueRef{successValue, failureValue}), llvmBlockSlicePtr([]llvmIRBlockRef{successEnd, failureEnd}), 2)
	return phi, nil
}
