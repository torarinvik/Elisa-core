//go:build cgo

package backend

import "elisacore/src/semantic"

func (ops *packedStoreOps) loadSideWordAtOrigin(indexValue llvmValueRef, wordOffset llvmValueRef, origin packedReadOriginKey, name string) (llvmValueRef, error) {
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, err
	}
	u32Type := ops.s.g.namedType("u32")
	usizeType := ops.s.g.namedType("usize")
	uintptrType := ops.s.g.namedType("uintptr")
	coercedIndex, err := ops.s.coerceValue(indexValue, ops.storeType.Enum, u32Type)
	if err != nil {
		return nil, err
	}
	// AoS stores read cold commons from their lockstep side chunks; column stores from side
	// columns. Same (state, index, word_offset) -> word contract.
	helperName := "ctx_packed_store_read_side_word"
	if packedModeIsAoS(ops.s.g.packedLoweringForStore(ops.storeType)) {
		helperName = "ctx_aos_store_read_side_word"
	}
	helperType := ops.cachedRuntimeHelperType(helperName, func() *semantic.FuncType {
		return &semantic.FuncType{Name: helperName, Params: []semantic.Type{ops.voidRefType(), u32Type, usizeType}, Return: uintptrType}
	})
	callee, err := ops.s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	if ops.canCacheDenseHandleReads(ops.storeType.Enum) {
		if ops.s.packedDenseSideWordReads == nil {
			ops.s.packedDenseSideWordReads = map[packedDenseSideWordReadCacheKey]llvmValueRef{}
		}
		originKey, cacheIndex := ops.denseReadCacheIdentity(origin, coercedIndex)
		key := packedDenseSideWordReadCacheKey{block: ops.cacheKeyBlock(), storeType: ops.storeType, state: ops.cacheKeyStoreSSA(originKey, stateValue), origin: originKey, index: cacheIndex, offset: wordOffset}
		if cached, ok := ops.s.packedDenseSideWordReads[key]; ok && cached != nil {
			return cached, nil
		}
		wordValue := ops.s.buildCall(llvmFnType, callee, []llvmValueRef{stateValue, coercedIndex, wordOffset}, name)
		ops.s.packedDenseSideWordReads[key] = wordValue
		return wordValue, nil
	}
	return ops.s.buildCall(llvmFnType, callee, []llvmValueRef{stateValue, coercedIndex, wordOffset}, name), nil
}
func (ops *packedStoreOps) loadSideWord(indexValue llvmValueRef, wordOffset llvmValueRef, name string) (llvmValueRef, error) {
	return ops.loadSideWordAtOrigin(indexValue, wordOffset, packedReadOriginKey{}, name)
}
func (ops *packedStoreOps) storeTagsView(startValue llvmValueRef, endValue llvmValueRef, resultType *semantic.ViewType, name string) (llvmValueRef, semantic.Type, error) {
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   "ctx_packed_store_tags_view",
		Params: []semantic.Type{ops.voidRefType(), ops.s.g.namedType("usize"), ops.s.g.namedType("usize")},
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
	value := ops.s.buildCall(llvmFnType, callee, []llvmValueRef{stateValue, startValue, endValue}, name)
	return value, resultType, nil
}
