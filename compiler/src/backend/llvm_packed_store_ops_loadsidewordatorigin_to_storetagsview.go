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

import "llcontext/src/semantic"

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
