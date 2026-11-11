//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitSliceExpr(expr *ast.SliceExpr) (llvmIRValueRef, semantic.Type, error) {
	if value, resultType, handled, err := s.emitPackedStoreSliceExpr(expr); handled {
		return value, resultType, err
	}
	if value, resultType, handled, err := s.emitFixedArraySliceExpr(expr); handled {
		return value, resultType, err
	}
	if value, resultType, handled, err := s.emitDArraySliceExpr(expr); handled {
		return value, resultType, err
	}
	if value, resultType, handled, err := s.emitRawU8RefSliceExpr(expr); handled {
		return value, resultType, err
	}
	info, ok := runtimeSliceOperandInfo(s.exprType(expr.Object), s.exprType(expr))
	if !ok {
		return nil, nil, fmt.Errorf("slice is not implemented for %s", s.exprType(expr.Object).String())
	}
	var (
		objectValue llvmIRValueRef
		err         error
	)
	if info.useAddress {
		objectValue, _, err = s.emitAddressOrTemp(expr.Object)
	} else {
		objectValue, _, err = s.emitExpr(expr.Object, info.operandType)
	}
	if err != nil {
		return nil, nil, err
	}
	startValue, _, err := s.emitExpr(expr.Start, info.indexType)
	if err != nil {
		return nil, nil, err
	}
	endValue, _, err := s.emitExpr(expr.End, info.indexType)
	if err != nil {
		return nil, nil, err
	}
	if info.helperName == "arena_da_view_slice" {
		call, err := s.emitDenseViewSliceValue(objectValue, info.resultType, startValue, endValue, "slicetmp")
		return call, info.resultType, err
	}
	helperType := &semantic.FuncType{
		Name:   info.helperName,
		Params: []semantic.Type{info.operandType, info.indexType, info.indexType},
		Return: info.resultType,
	}
	callee, err := s.g.ensureFunctionDeclared(info.helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []llvmIRValueRef{objectValue, startValue, endValue}
	callName := "slicetmp"
	if isVoidType(info.resultType) {
		callName = ""
	}
	call := s.buildCall(llvmFnType, callee, args, callName)
	return call, info.resultType, nil
}

func (s *functionState) emitRawU8RefSliceExpr(expr *ast.SliceExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	objectType := s.exprType(expr.Object)
	refType, ok := objectType.(*semantic.RefType)
	if !ok || refType.State != semantic.RefStateNonNull {
		return nil, nil, false, nil
	}
	builtin, ok := refType.Elem.(*semantic.BuiltinType)
	if !ok || builtin.Name != "u8" {
		return nil, nil, false, nil
	}
	resultType, ok := s.exprType(expr).(*semantic.SViewType)
	if !ok {
		return nil, nil, true, fmt.Errorf("u8& slice must produce sview, got %s", s.exprType(expr).String())
	}
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	sourceValue, _, err := s.emitExpr(expr.Object, objectType)
	if err != nil {
		return nil, nil, true, err
	}
	startValue, _, err := s.emitExpr(expr.Start, usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	endValue, _, err := s.emitExpr(expr.End, usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	u8LLVMType, err := s.g.lowerType(refType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	dataPtr := llvmBuildGEP2(s.builder, u8LLVMType, sourceValue, llvmValueSlicePtr([]llvmIRValueRef{startValue}), 1, cStringFree("rawstrslice.data"))
	sliceLen := llvmBuildSub(s.builder, endValue, startValue, cStringFree("rawstrslice.len"))
	viewLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	lenFieldType := llvmStructGetTypeAtIndex(viewLLVMType, 1)
	lenValue := sliceLen
	if lenFieldType != usizeLLVMType {
		lenValue = llvmBuildIntCast2(s.builder, sliceLen, lenFieldType, 0, cStringFree("rawstrslice.len.cast"))
	}
	viewValue := llvmGetUndef(viewLLVMType)
	viewValue = llvmBuildInsertValue(s.builder, viewValue, dataPtr, 0, cStringFree("rawstrslice.view.data"))
	viewValue = llvmBuildInsertValue(s.builder, viewValue, lenValue, 1, cStringFree("rawstrslice.view.len"))
	return viewValue, resultType, true, nil
}

func (s *functionState) emitDArraySliceExpr(expr *ast.SliceExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	objectType := s.exprType(expr.Object)
	darrayType, objectIsRef := darraySliceBaseType(objectType)
	if darrayType == nil {
		return nil, nil, false, nil
	}
	resultType, ok := s.exprType(expr).(*semantic.ViewType)
	if !ok {
		return nil, nil, true, fmt.Errorf("dynamic array slice must produce a view, got %s", s.exprType(expr).String())
	}
	usizeType := s.g.namedType("usize")
	startValue, _, err := s.emitExpr(expr.Start, usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	endValue, _, err := s.emitExpr(expr.End, usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	var arrayValue llvmIRValueRef
	if objectIsRef {
		objectPtr, _, err := s.emitExpr(expr.Object, objectType)
		if err != nil {
			return nil, nil, true, err
		}
		arrayLLVMType, err := s.g.lowerType(darrayType)
		if err != nil {
			return nil, nil, true, err
		}
		arrayValue = llvmBuildLoad2(s.builder, arrayLLVMType, objectPtr, cStringFree("darrayslice.array"))
	} else {
		arrayValue, _, err = s.emitExpr(expr.Object, objectType)
		if err != nil {
			return nil, nil, true, err
		}
	}
	viewValue, err := s.buildDynArrayViewValue(arrayValue, darrayType, resultType, "darrayslice")
	if err != nil {
		return nil, nil, true, err
	}
	sliceValue, err := s.emitArenaViewSliceValue(viewValue, resultType, startValue, endValue, "darrayslice.view")
	if err != nil {
		return nil, nil, true, err
	}
	return sliceValue, resultType, true, nil
}
func darraySliceBaseType(objectType semantic.Type) (*semantic.DArrayType, bool) {
	if darrayType, ok := objectType.(*semantic.DArrayType); ok {
		return darrayType, false
	}
	refType, ok := objectType.(*semantic.RefType)
	if !ok || refType.State != semantic.RefStateNonNull {
		return nil, false
	}
	darrayType, ok := refType.Elem.(*semantic.DArrayType)
	if !ok {
		return nil, false
	}
	return darrayType, true
}
func (s *functionState) emitFixedArraySliceExpr(expr *ast.SliceExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	arrayType, arrayPtr, handled, err := s.fixedArraySliceBase(expr.Object)
	if err != nil || !handled {
		return nil, nil, handled, err
	}
	resultType := s.exprType(expr)
	usizeSemanticType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeSemanticType)
	if err != nil {
		return nil, nil, true, err
	}
	startValue, _, err := s.emitExpr(expr.Start, usizeSemanticType)
	if err != nil {
		return nil, nil, true, err
	}
	endValue, _, err := s.emitExpr(expr.End, usizeSemanticType)
	if err != nil {
		return nil, nil, true, err
	}
	arrayLen := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(arrayType.ConstSize), 0)
	startClamped := s.emitUnsignedMin(startValue, arrayLen, usizeLLVMType, "arrayslice.start.clamped")
	endClamped := s.emitUnsignedMin(endValue, arrayLen, usizeLLVMType, "arrayslice.end.clamped")
	boundedStart := s.emitUnsignedMin(startClamped, endClamped, usizeLLVMType, "arrayslice.start.bounded")
	sliceLen := llvmBuildSub(s.builder, endClamped, boundedStart, cStringFree("arrayslice.len"))

	llvmArrayType, err := s.g.lowerType(arrayType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroIndex := llvmConstInt(usizeLLVMType, 0, 0)
	indices := []llvmIRValueRef{zeroIndex, boundedStart}
	dataPtr := llvmBuildGEP2(s.builder, llvmArrayType, arrayPtr, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree("arrayslice.data"))

	viewLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	if _, ok := resultType.(*semantic.SViewType); ok {
		viewValue := llvmGetUndef(viewLLVMType)
		viewValue = llvmBuildInsertValue(s.builder, viewValue, dataPtr, 0, cStringFree("strslice.view.data"))
		viewValue = llvmBuildInsertValue(s.builder, viewValue, sliceLen, 1, cStringFree("strslice.view.len"))
		return viewValue, resultType, true, nil
	}
	viewValue := llvmGetUndef(viewLLVMType)
	viewValue = llvmBuildInsertValue(s.builder, viewValue, dataPtr, 0, cStringFree("arrayslice.view.data"))
	viewValue = llvmBuildInsertValue(s.builder, viewValue, sliceLen, 1, cStringFree("arrayslice.view.len"))
	return viewValue, resultType, true, nil
}
func (s *functionState) fixedArraySliceBase(object ast.Expr) (*semantic.ArrayType, llvmIRValueRef, bool, error) {
	objectType := s.exprType(object)
	if arrayType, ok := objectType.(*semantic.ArrayType); ok {
		arrayPtr, _, err := s.emitAddressOrTemp(object)
		if err != nil {
			return nil, nil, true, err
		}
		return arrayType, arrayPtr, true, nil
	}
	refType, ok := objectType.(*semantic.RefType)
	if !ok || refType.State != semantic.RefStateNonNull {
		return nil, nil, false, nil
	}
	arrayType, ok := refType.Elem.(*semantic.ArrayType)
	if !ok {
		return nil, nil, false, nil
	}
	arrayPtr, _, err := s.emitExpr(object, objectType)
	if err != nil {
		return nil, nil, true, err
	}
	return arrayType, arrayPtr, true, nil
}
func (s *functionState) emitUnsignedMin(left llvmIRValueRef, right llvmIRValueRef, llvmType llvmIRTypeRef, name string) llvmIRValueRef {
	cmp := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULE), left, right, cStringFree(name+".cmp"))
	mask := llvmBuildZExt(s.builder, cmp, llvmType, cStringFree(name+".mask"))
	negMask := llvmBuildSub(s.builder, llvmConstNull(llvmType), mask, cStringFree(name+".negmask"))
	diff := llvmBuildXor(s.builder, left, right, cStringFree(name+".diff"))
	maskedDiff := llvmBuildAnd(s.builder, diff, negMask, cStringFree(name+".masked"))
	return llvmBuildXor(s.builder, right, maskedDiff, cStringFree(name))
}
func (s *functionState) emitRuntimeStringLenExpr(object ast.Expr, fieldType semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	stringType, ok := cstrFieldOperandType(s.exprType(object))
	if !ok {
		return nil, nil, fmt.Errorf("string len requires cstr operand")
	}
	stringValue, _, err := s.emitExpr(object, stringType)
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   "ctx_strlen",
		Params: []semantic.Type{stringType},
		Return: fieldType,
	}
	callee, err := s.g.ensureFunctionDeclared("ctx_strlen", helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []llvmIRValueRef{stringValue}
	call := s.buildCall(llvmFnType, callee, args, "strlen")
	return call, fieldType, nil
}
func (s *functionState) emitIndexExpr(expr *ast.IndexExpr) (llvmIRValueRef, semantic.Type, error) {
	if expr != nil && expr.AsSpecialize != nil {
		// `fn[T]` over a generic function: the analyzer reinterpreted this as a single-type-arg
		// value specialization. Emit the materialized generic-function value, not an index.
		return s.emitSpecializeExpr(expr.AsSpecialize)
	}
	if expr != nil && expr.AsBuiltinDictGet != nil {
		if expr.Fallback != nil {
			recovery := &ast.RecoveryClause{Position: expr.Fallback.Pos(), Kind: ast.RecoveryValue, Value: expr.Fallback}
			return s.emitGetDictIndexExpr(expr, recovery, s.exprType(expr))
		}
		return s.emitCallExpr(expr.AsBuiltinDictGet)
	}
	if value, actualType, handled, err := s.emitConstDictIndexExpr(expr); handled {
		return value, actualType, err
	}
	if expr != nil {
		if value, ok := s.evalConstExpr(expr); ok {
			resultType := s.exprType(expr)
			if resultType == nil {
				resultType = semantic.ConstValueStaticType(s.g.namedTypesMap("const-value-static-type"), value)
			}
			llvmValue, err := s.g.constValueAsLLVM(value, resultType)
			return llvmValue, resultType, err
		}
	}
	if expr != nil && expr.AsOverlayCall != nil {
		// docs/107 typed guest-memory overlay: `base.field[mem]` over a `GuestVAddr[L]` carrier was
		// rewritten by the analyzer to the equivalent MemoryManager_ReadU<N>(mem, base + offset)
		// call. Emit that call so codegen is byte-identical to the hand-written read (§(e)). docs/108
		// reuses the same hook for the fixed-stride table read `table.field[mem, i]`, whose lowered
		// call carries the `i*stride + offset` address arithmetic.
		return s.emitCallExpr(expr.AsOverlayCall)
	}
	if expr != nil && expr.Fallback != nil {
		return s.emitIndexFallbackExpr(expr)
	}
	if value, actualType, handled, err := s.emitTupleIndexExpr(expr); handled {
		return value, actualType, err
	}
	if flagType, ok := semantic.FlagsInstanceType(s.exprType(expr.Object)); ok {
		return s.emitFlagsIndexExpr(expr, flagType)
	}
	if value, actualType, handled, err := s.emitPackedStoreIndexExpr(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitChunksExactIndexExpr(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinSOARowIndexExpr(expr); handled {
		return value, actualType, err
	}
	if _, ok := semanticStringArrayType(s.exprType(expr.Object)); ok {
		return s.emitStaticStringIndexExpr(expr)
	}
	if helperName, operandType, ok := runtimeStringIndexedOperand(s.exprType(expr.Object)); ok {
		return s.emitRuntimeStringIndexExpr(expr, helperName, operandType)
	}
	ptr, elemType, err := s.emitIndexAddress(expr, true, true)
	if err != nil {
		return nil, nil, err
	}
	value, err := s.loadValue(ptr, elemType, "idx")
	return value, elemType, err
}

func (s *functionState) emitConstDictIndexExpr(expr *ast.IndexExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	objectValue, ok := s.evalConstExpr(expr.Object)
	if !ok || objectValue.Kind != semantic.ConstDict {
		return nil, nil, false, nil
	}
	dictType, _, ok := builtinDictReceiverType(s.exprType(expr.Object))
	if !ok || dictType == nil {
		return nil, nil, false, nil
	}
	keyValue, ok := s.evalConstExpr(expr.Index)
	if !ok {
		return nil, nil, false, nil
	}
	key, ok := backendConstDictKeyFingerprint(dictType.Key, keyValue)
	if !ok {
		return nil, nil, true, fmt.Errorf("const dict index key is not supported")
	}
	for _, entry := range objectValue.Dict {
		entryKey, ok := backendConstDictKeyFingerprint(dictType.Key, entry.Key)
		if ok && entryKey == key {
			llvmValue, err := s.g.constValueAsLLVM(entry.Value, dictType.Value)
			return llvmValue, dictType.Value, true, err
		}
	}
	return nil, nil, true, fmt.Errorf("const dict has no key %s", key)
}

func backendConstDictKeyFingerprint(keyType semantic.Type, value semantic.ConstValue) (string, bool) {
	if _, ok := semantic.StripAggregateStateType(keyType).(*semantic.CStrType); ok {
		if value.Kind != semantic.ConstString {
			return "", false
		}
		return "s:" + value.String, true
	}
	if semantic.IsBoolType(semantic.StripAggregateStateType(keyType)) {
		if value.Kind != semantic.ConstBool {
			return "", false
		}
		return fmt.Sprintf("b:%t", value.Bool), true
	}
	if _, ok := semantic.ConstEnumStorageType(semantic.StripAggregateStateType(keyType)); ok {
		if value.Kind != semantic.ConstInt {
			return "", false
		}
		return fmt.Sprintf("i:%d", value.Int), true
	}
	if semantic.IsIntegralType(semantic.StripAggregateStateType(keyType)) {
		if value.Kind != semantic.ConstInt {
			return "", false
		}
		return fmt.Sprintf("i:%d", value.Int), true
	}
	return "", false
}

func (s *functionState) emitTupleIndexExpr(expr *ast.IndexExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	tupleType, ok := semantic.StripAggregateStateType(s.exprType(expr.Object)).(*semantic.TupleType)
	if !ok || tupleType == nil {
		return nil, nil, false, nil
	}
	indexValue, ok := s.evalConstExpr(expr.Index)
	if !ok || indexValue.Kind != semantic.ConstInt {
		return nil, nil, true, fmt.Errorf("tuple index requires a compile-time integer")
	}
	if indexValue.Int < 0 || indexValue.Int >= int64(len(tupleType.Fields)) {
		return nil, nil, true, fmt.Errorf("constant tuple index %d out of bounds for %s", indexValue.Int, tupleType.String())
	}
	tupleValue, _, err := s.emitExpr(expr.Object, nil)
	if err != nil {
		return nil, nil, true, err
	}
	fieldType := tupleType.Fields[indexValue.Int].Type
	value := llvmBuildExtractValue(s.builder, tupleValue, llvmIRUnsigned(indexValue.Int), cStringFree("tuple.idx"))
	return value, fieldType, true, nil
}

func (s *functionState) emitIndexFallbackExpr(expr *ast.IndexExpr) (llvmIRValueRef, semantic.Type, error) {
	if expr == nil || expr.Fallback == nil {
		return nil, nil, fmt.Errorf("missing safe index fallback expression")
	}
	resultType := s.exprType(expr)
	if resultType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for safe index fallback result")
	}
	usizeType := s.g.namedType("usize")
	indexValue, _, err := s.emitExpr(expr.Index, usizeType)
	if err != nil {
		return nil, nil, err
	}
	countValue, loadValue, err := s.prepareSafeIndexFallback(expr, indexValue)
	if err != nil {
		return nil, nil, err
	}

	condValue := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), indexValue, countValue, cStringFree("safe.index.in.range"))
	inRangeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.index.in_range"))
	fallbackBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.index.fallback"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.index.end"))
	llvmBuildCondBr(s.builder, condValue, inRangeBB, fallbackBB)

	llvmPositionBuilderAtEnd(s.builder, inRangeBB)
	inRangeValue, actualType, err := loadValue()
	if err != nil {
		return nil, nil, err
	}
	if !semantic.SameType(actualType, resultType) {
		inRangeValue, err = s.coerceValue(inRangeValue, actualType, resultType)
		if err != nil {
			return nil, nil, err
		}
	}
	inRangeEnd := llvmGetInsertBlock(s.builder)
	if llvmGetBasicBlockTerminator(inRangeEnd) == nil {
		llvmBuildBr(s.builder, mergeBB)
	}

	llvmPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackValue, _, err := s.emitExpr(expr.Fallback, resultType)
	if err != nil {
		return nil, nil, err
	}
	fallbackEnd := llvmGetInsertBlock(s.builder)
	if llvmGetBasicBlockTerminator(fallbackEnd) == nil {
		llvmBuildBr(s.builder, mergeBB)
	}

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := llvmBuildPhi(s.builder, phiType, cStringFree("safe.index.result"))
	values := []llvmIRValueRef{inRangeValue, fallbackValue}
	blocks := []llvmIRBlockRef{inRangeEnd, fallbackEnd}
	llvmAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), llvmIRUnsigned(len(values)))
	return phi, resultType, nil
}
