//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitZipMapHelperCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 4 {
		return nil, nil, true, fmt.Errorf("zip_map expects 4 arguments, got %d", len(expr.Args))
	}
	dstType, dstElemType, ok := zipMapViewInfo(s.exprType(expr.Args[0]))
	if !ok {
		return nil, nil, true, fmt.Errorf("zip_map destination expects a dense view")
	}
	src1Type, src1ElemType, ok := zipMapViewInfo(s.exprType(expr.Args[1]))
	if !ok {
		return nil, nil, true, fmt.Errorf("zip_map source 1 expects a dense view")
	}
	src2Type, src2ElemType, ok := zipMapViewInfo(s.exprType(expr.Args[2]))
	if !ok {
		return nil, nil, true, fmt.Errorf("zip_map source 2 expects a dense view")
	}
	callbackType, ok := s.exprType(expr.Args[3]).(*semantic.FuncType)
	if !ok || callbackType == nil {
		return nil, nil, true, fmt.Errorf("zip_map callback expects a function value")
	}
	dstValue, _, err := s.emitExpr(expr.Args[0], dstType)
	if err != nil {
		return nil, nil, true, err
	}
	src1Value, _, err := s.emitExpr(expr.Args[1], src1Type)
	if err != nil {
		return nil, nil, true, err
	}
	src2Value, _, err := s.emitExpr(expr.Args[2], src2Type)
	if err != nil {
		return nil, nil, true, err
	}
	callbackValue, _, err := s.emitExpr(expr.Args[3], callbackType)
	if err != nil {
		return nil, nil, true, err
	}
	dstDataPtr, err := s.emitDenseViewDataPointer(dstValue, dstElemType, "zip_map.dst")
	if err != nil {
		return nil, nil, true, err
	}
	src1DataPtr, err := s.emitDenseViewDataPointer(src1Value, src1ElemType, "zip_map.src1")
	if err != nil {
		return nil, nil, true, err
	}
	src2DataPtr, err := s.emitDenseViewDataPointer(src2Value, src2ElemType, "zip_map.src2")
	if err != nil {
		return nil, nil, true, err
	}
	dstSrc1Disjoint := s.g.semanticReadView().ExprsAreDisjoint(expr.Args[0], expr.Args[1])
	dstSrc2Disjoint := s.g.semanticReadView().ExprsAreDisjoint(expr.Args[0], expr.Args[2])
	src1Src2Disjoint := s.g.semanticReadView().ExprsAreDisjoint(expr.Args[1], expr.Args[2])
	domainName := ""
	dstScopeName := ""
	src1ScopeName := ""
	src2ScopeName := ""
	hasScopedNoAlias := dstSrc1Disjoint || dstSrc2Disjoint || src1Src2Disjoint
	if hasScopedNoAlias {
		domainName = fmt.Sprintf("elisa_core.zip_map.%p.domain", expr)
		dstScopeName = domainName + ".dst"
		src1ScopeName = domainName + ".src1"
		src2ScopeName = domainName + ".src2"
	}
	totalValue := llvmBuildExtractValue(s.builder, dstValue, 1, cStringFree("zip_map.total"))
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zero := llvmConstInt(usizeLLVMType, 0, 0)
	one := llvmConstInt(usizeLLVMType, 1, 0)

	entryBlock := llvmGetInsertBlock(s.builder)
	loopCondBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("zip_map.cond"))
	loopBodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("zip_map.body"))
	loopEndBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("zip_map.end"))
	llvmBuildBr(s.builder, loopCondBB)

	llvmPositionBuilderAtEnd(s.builder, loopCondBB)
	indexValue := llvmBuildPhi(s.builder, usizeLLVMType, cStringFree("zip_map.index"))
	initIndexValues := []llvmIRValueRef{zero}
	initIndexBlocks := []llvmIRBlockRef{entryBlock}
	llvmAddIncoming(indexValue, llvmValueSlicePtr(initIndexValues), llvmBlockSlicePtr(initIndexBlocks), 1)
	hasMore := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), indexValue, totalValue, cStringFree("zip_map.has_more"))
	llvmBuildCondBr(s.builder, hasMore, loopBodyBB, loopEndBB)

	llvmPositionBuilderAtEnd(s.builder, loopBodyBB)
	dstElemPtr, err := s.emitDenseViewIndexedAddress(dstDataPtr, dstElemType, indexValue, "zip_map.dst")
	if err != nil {
		return nil, nil, true, err
	}
	src1ElemPtr, err := s.emitDenseViewIndexedAddress(src1DataPtr, src1ElemType, indexValue, "zip_map.src1")
	if err != nil {
		return nil, nil, true, err
	}
	src2ElemPtr, err := s.emitDenseViewIndexedAddress(src2DataPtr, src2ElemType, indexValue, "zip_map.src2")
	if err != nil {
		return nil, nil, true, err
	}
	src1Elem, err := s.loadValue(src1ElemPtr, src1ElemType, "zip_map.src1.elem")
	if err != nil {
		return nil, nil, true, err
	}
	if hasScopedNoAlias {
		var noAliasScopes []string
		if dstSrc1Disjoint {
			noAliasScopes = append(noAliasScopes, dstScopeName)
		}
		if src1Src2Disjoint {
			noAliasScopes = append(noAliasScopes, src2ScopeName)
		}
		s.attachAliasScopeMetadataWithNames(src1Elem, domainName, src1ScopeName, noAliasScopes)
	}
	src2Elem, err := s.loadValue(src2ElemPtr, src2ElemType, "zip_map.src2.elem")
	if err != nil {
		return nil, nil, true, err
	}
	if hasScopedNoAlias {
		var noAliasScopes []string
		if dstSrc2Disjoint {
			noAliasScopes = append(noAliasScopes, dstScopeName)
		}
		if src1Src2Disjoint {
			noAliasScopes = append(noAliasScopes, src1ScopeName)
		}
		s.attachAliasScopeMetadataWithNames(src2Elem, domainName, src2ScopeName, noAliasScopes)
	}
	resultValue, err := s.emitFunctionValueCall(callbackValue, callbackType, []llvmIRValueRef{src1Elem, src2Elem}, "zip_map.call")
	if err != nil {
		return nil, nil, true, err
	}
	coerced, err := s.coerceValue(resultValue, callbackType.Return, dstElemType)
	if err != nil {
		return nil, nil, true, err
	}
	store := llvmBuildStore(s.builder, coerced, dstElemPtr)
	if hasScopedNoAlias {
		var noAliasScopes []string
		if dstSrc1Disjoint {
			noAliasScopes = append(noAliasScopes, src1ScopeName)
		}
		if dstSrc2Disjoint {
			noAliasScopes = append(noAliasScopes, src2ScopeName)
		}
		s.attachAliasScopeMetadataWithNames(store, domainName, dstScopeName, noAliasScopes)
	}
	nextIndex := llvmBuildAdd(s.builder, indexValue, one, cStringFree("zip_map.index.next"))
	bodyEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, loopCondBB)
	nextIndexValues := []llvmIRValueRef{nextIndex}
	nextIndexBlocks := []llvmIRBlockRef{bodyEnd}
	llvmAddIncoming(indexValue, llvmValueSlicePtr(nextIndexValues), llvmBlockSlicePtr(nextIndexBlocks), 1)

	llvmPositionBuilderAtEnd(s.builder, loopEndBB)
	return nil, s.g.namedType("void"), true, nil
}
func zipMapViewInfo(t semantic.Type) (semantic.Type, semantic.Type, bool) {
	switch tt := t.(type) {
	case *semantic.ViewType:
		if tt == nil || tt.SurfaceName == "packedtags" {
			return nil, nil, false
		}
		return tt, tt.Elem, true
	default:
		return nil, nil, false
	}
}
func (s *functionState) emitDenseViewDataPointer(viewValue llvmIRValueRef, elemType semantic.Type, name string) (llvmIRValueRef, error) {
	if s == nil || s.g == nil {
		return nil, fmt.Errorf("missing function state for dense view data extraction")
	}
	if elemType == nil {
		return nil, fmt.Errorf("missing dense view element type")
	}
	return llvmBuildExtractValue(s.builder, viewValue, 0, cStringFree(name+".data")), nil
}
func (s *functionState) emitDenseViewIndexedAddress(dataPtr llvmIRValueRef, elemType semantic.Type, indexValue llvmIRValueRef, name string) (llvmIRValueRef, error) {
	if s == nil || s.g == nil {
		return nil, fmt.Errorf("missing function state for dense view indexing")
	}
	elemLLVMType, err := s.g.lowerType(elemType)
	if err != nil {
		return nil, err
	}
	indices := []llvmIRValueRef{indexValue}
	return llvmBuildGEP2(s.builder, elemLLVMType, dataPtr, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree(name+".ptr")), nil
}
func (s *functionState) emitArenaViewSliceValue(viewValue llvmIRValueRef, viewType *semantic.ViewType, startValue llvmIRValueRef, endValue llvmIRValueRef, name string) (llvmIRValueRef, error) {
	return s.emitDenseViewSliceValue(viewValue, viewType, startValue, endValue, name)
}
func (s *functionState) emitDenseViewSliceValue(viewValue llvmIRValueRef, viewType semantic.Type, startValue llvmIRValueRef, endValue llvmIRValueRef, name string) (llvmIRValueRef, error) {
	if viewType == nil {
		return nil, fmt.Errorf("missing view type for slice helper lowering")
	}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	dataValue := llvmBuildExtractValue(s.builder, viewValue, 0, cStringFree(name+".data"))
	lenValue := llvmBuildExtractValue(s.builder, viewValue, 1, cStringFree(name+".len"))
	var elemType semantic.Type = s.g.namedType("u8")
	if vt, ok := viewType.(*semantic.ViewType); ok {
		elemType = vt.Elem
	}
	elemSize, err := s.sizeOfType(elemType)
	if err != nil {
		return nil, err
	}
	elemSizeValue := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(elemSize), 0)
	lo := s.emitUnsignedMin(startValue, lenValue, usizeLLVMType, name+".lo")
	endClamped := s.emitUnsignedMin(endValue, lenValue, usizeLLVMType, name+".end.clamped")
	endAfterStart := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntUGE), endClamped, lo, cStringFree(name+".end.after.start"))
	hi := llvmBuildSelect(s.builder, endAfterStart, endClamped, lo, cStringFree(name+".hi"))
	sliceLen := llvmBuildSub(s.builder, hi, lo, cStringFree(name+".len.out"))
	offset := llvmBuildMul(s.builder, lo, elemSizeValue, cStringFree(name+".offset"))
	i8Type := llvmInt8TypeInContext(s.g.context)
	indices := []llvmIRValueRef{offset}
	sliceData := llvmBuildGEP2(s.builder, i8Type, dataValue, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree(name+".data.out"))
	out := llvmGetUndef(viewLLVMType)
	out = llvmBuildInsertValue(s.builder, out, sliceData, 0, cStringFree(name+".view.data"))
	out = llvmBuildInsertValue(s.builder, out, sliceLen, 1, cStringFree(name+".view.len"))
	return out, nil
}
func (s *functionState) emitChunksExactValidation(chunkSizeValue llvmIRValueRef, totalValue llvmIRValueRef, prefix string) error {
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	zero := llvmConstInt(usizeLLVMType, 0, 0)
	nonZeroBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(prefix+".nonzero"))
	okBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(prefix+".ok"))
	failBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(prefix+".fail"))
	isNonZero := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntNE), chunkSizeValue, zero, cStringFree(prefix+".chunk.nonzero"))
	llvmBuildCondBr(s.builder, isNonZero, nonZeroBB, failBB)

	llvmPositionBuilderAtEnd(s.builder, failBB)
	if err := s.emitTrapUnreachable(prefix + ".fail"); err != nil {
		return err
	}

	llvmPositionBuilderAtEnd(s.builder, nonZeroBB)
	remainder := llvmBuildURem(s.builder, totalValue, chunkSizeValue, cStringFree(prefix+".remainder"))
	isExact := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), remainder, zero, cStringFree(prefix+".exact"))
	llvmBuildCondBr(s.builder, isExact, okBB, failBB)

	llvmPositionBuilderAtEnd(s.builder, okBB)
	return nil
}
func (s *functionState) emitPackedStoreConstructorValue(expr *ast.CallExpr, storeType *semantic.PackedEnumStoreType) (llvmIRValueRef, semantic.Type, error) {
	if expr == nil || len(expr.Args) != 1 {
		return nil, nil, fmt.Errorf("packed store constructor expects exactly one arena argument")
	}
	value, err := s.emitPackedStoreValue(expr.Args[0], storeType)
	if err != nil {
		return nil, nil, err
	}
	return value, storeType, nil
}

func (s *functionState) emitPackedStoreValue(arenaExpr ast.Expr, storeType *semantic.PackedEnumStoreType) (llvmIRValueRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	arenaPtr, _, err := s.emitAddressOrTemp(arenaExpr)
	if err != nil {
		return nil, err
	}
	return s.emitPackedStoreValueFromArenaPtr(arenaPtr, storeType)
}

// emitPackedStoreValueFromArenaPtr builds a fresh PackedStoreState backed by the given Arena pointer
// (the `{arena, row_bytes, state}` triple). Used both by an explicit `Expr.Store(arena)` and by the
// implicit region-backed store created for `new[auto] Expr.V(...)` (docs/74), which threads the
// active inferred region's arena directly as a value rather than re-emitting an arena expression.
func (s *functionState) emitPackedStoreValueFromArenaPtr(arenaPtr llvmIRValueRef, storeType *semantic.PackedEnumStoreType) (llvmIRValueRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	if arenaPtr == nil {
		return nil, fmt.Errorf("missing arena for region-backed packed store")
	}
	if storeType.Enum == nil {
		return nil, fmt.Errorf("packed enum store %s is missing enum metadata", storeType.Name)
	}
	rowType, err := s.g.ensurePackedEnumStorageType(storeType.Enum)
	if err != nil {
		return nil, err
	}
	rowSizeBytes, err := s.g.abiSizeOfLLVMType(rowType)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	rowSizeValue := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(rowSizeBytes), 0)
	storeLLVMType, err := s.g.lowerPackedEnumStoreType(storeType)
	if err != nil {
		return nil, err
	}
	sideWords, err := s.g.packedEnumCommonSideTableWordCount(storeType.Enum)
	if err != nil {
		return nil, err
	}
	arenaType := s.g.namedType("Arena")
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.namedType("void"), State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	stateHelperName := "ctx_packed_store_state_new"
	stateHelperParams := []semantic.Type{arenaRefType, usizeType}
	stateArgs := []llvmIRValueRef{arenaPtr, rowSizeValue}
	if s.g.packedLoweringForStore(storeType) == packedEnumABIAoS {
		// docs/76 Slice 2: AoS store creation — same (arena, record_bytes) signature, lean record-array
		// backing instead of columns. record_bytes is the node-record (rowType) size already computed.
		if sideWords > 0 {
			// Cold commons (Stage 2): the AoS store grows a parallel dense side array — lockstep
			// chunks indexed by the same handle as the records.
			stateHelperName = "ctx_aos_store_new_with_side_words"
			stateHelperParams = []semantic.Type{arenaRefType, usizeType, usizeType}
			stateArgs = append(stateArgs, llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(sideWords), 0))
		} else {
			stateHelperName = "ctx_aos_store_new"
		}
	} else if s.g.packedLoweringForStore(storeType) == packedEnumABIVariantSparse {
		if sideWords > 0 {
			stateHelperName = "ctx_packed_store_state_new_variant_sparse_with_side_words"
			stateHelperParams = []semantic.Type{arenaRefType, usizeType, usizeType}
			stateArgs = append(stateArgs, llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(sideWords), 0))
		} else {
			stateHelperName = "ctx_packed_store_state_new_variant_sparse"
		}
	} else if storeType.Enum != nil && storeType.Enum.HasPackedPrefixOverride && storeType.Enum.PackedPrefixOverride == "common-only" && s.g.packedLoweringForStore(storeType) == packedEnumABIIndexSOA {
		prefixWords, err := s.g.packedEnumCommonPrefixWordCount(storeType.Enum)
		if err != nil {
			return nil, err
		}
		if sideWords > 0 {
			stateHelperName = "ctx_packed_store_state_new_with_prefix_and_side_words"
			stateHelperParams = []semantic.Type{arenaRefType, usizeType, usizeType, usizeType}
			stateArgs = append(stateArgs,
				llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(prefixWords), 0),
				llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(sideWords), 0),
			)
		} else {
			stateHelperName = "ctx_packed_store_state_new_with_prefix_words"
			stateHelperParams = []semantic.Type{arenaRefType, usizeType, usizeType}
			stateArgs = append(stateArgs, llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(prefixWords), 0))
		}
	} else if sideWords > 0 {
		stateHelperName = "ctx_packed_store_state_new_with_side_words"
		stateHelperParams = []semantic.Type{arenaRefType, usizeType, usizeType}
		stateArgs = append(stateArgs, llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(sideWords), 0))
	}
	stateHelperType := &semantic.FuncType{Name: stateHelperName, Params: stateHelperParams, Return: voidRefType}
	stateCallee, err := s.g.ensureFunctionDeclared(stateHelperName, stateHelperType)
	if err != nil {
		return nil, err
	}
	stateLLVMFnType, err := s.g.lowerFunctionType(stateHelperType)
	if err != nil {
		return nil, err
	}
	stateValue := s.buildCall(stateLLVMFnType, stateCallee, stateArgs, "packed.store.state")
	storeValue := llvmGetUndef(storeLLVMType)
	storeValue = llvmBuildInsertValue(s.builder, storeValue, arenaPtr, 0, cStringFree("packed.store.arena"))
	storeValue = llvmBuildInsertValue(s.builder, storeValue, rowSizeValue, 1, cStringFree("packed.store.row_bytes"))
	storeValue = llvmBuildInsertValue(s.builder, storeValue, stateValue, 2, cStringFree("packed.store.state"))
	return storeValue, nil
}
func (s *functionState) emitPackedStoreArenaValue(storeValue llvmIRValueRef, storeType *semantic.PackedEnumStoreType) (llvmIRValueRef, error) {
	return s.emitPackedStoreArenaValueNamed(storeValue, storeType, "packed.store.arena.value")
}
func (s *functionState) emitPackedStoreFieldValueNamed(storeValue llvmIRValueRef, storeType *semantic.PackedEnumStoreType, index llvmIRUnsigned, name string) (llvmIRValueRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	if block := llvmGetInsertBlock(s.builder); block != nil && storeValue != nil {
		key := packedStoreExtractCacheKey{block: block, store: storeValue, index: index}
		if cached, ok := s.lookupPackedStoreFieldValue(key); ok && cached != nil {
			return cached, nil
		}
		value := llvmBuildExtractValue(s.builder, storeValue, index, cStringFree(name))
		s.cachePackedStoreFieldValue(key, value)
		return value, nil
	}
	return llvmBuildExtractValue(s.builder, storeValue, index, cStringFree(name)), nil
}
func (s *functionState) emitPackedStoreArenaValueNamed(storeValue llvmIRValueRef, storeType *semantic.PackedEnumStoreType, name string) (llvmIRValueRef, error) {
	return s.emitPackedStoreFieldValueNamed(storeValue, storeType, 0, name)
}
func (s *functionState) emitPackedStoreRowBytesValue(storeValue llvmIRValueRef, storeType *semantic.PackedEnumStoreType) (llvmIRValueRef, error) {
	return s.emitPackedStoreRowBytesValueNamed(storeValue, storeType, "packed.store.row_bytes.value")
}
func (s *functionState) emitPackedStoreRowBytesValueNamed(storeValue llvmIRValueRef, storeType *semantic.PackedEnumStoreType, name string) (llvmIRValueRef, error) {
	return s.emitPackedStoreFieldValueNamed(storeValue, storeType, 1, name)
}
func (s *functionState) emitPackedStoreStateValueNamed(storeValue llvmIRValueRef, storeType *semantic.PackedEnumStoreType, name string) (llvmIRValueRef, error) {
	return s.emitPackedStoreFieldValueNamed(storeValue, storeType, 2, name)
}
