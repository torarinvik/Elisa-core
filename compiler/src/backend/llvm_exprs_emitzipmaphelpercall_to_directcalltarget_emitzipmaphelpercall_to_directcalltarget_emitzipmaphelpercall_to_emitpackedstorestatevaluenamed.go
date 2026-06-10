//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int elisacoreLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef elisa_coreAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef elisa_coreAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned elisa_coreMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void elisa_coreSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = elisa_coreAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, elisa_coreMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef elisa_coreCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = elisa_coreAliasMDString(ctx, domainName);
	return elisa_coreAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef elisa_coreCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = elisa_coreAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return elisa_coreAliasMDNode(ctx, operands, 2);
}

static void elisa_coreAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = elisa_coreCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = elisa_coreCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	elisa_coreSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		elisa_coreSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitZipMapHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	dstSrc1Disjoint := s.g.result.ExprsAreDisjoint(expr.Args[0], expr.Args[1])
	dstSrc2Disjoint := s.g.result.ExprsAreDisjoint(expr.Args[0], expr.Args[2])
	src1Src2Disjoint := s.g.result.ExprsAreDisjoint(expr.Args[1], expr.Args[2])
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
	totalValue := C.LLVMBuildExtractValue(s.builder, dstValue, 1, cStringFree("zip_map.total"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	loopCondBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("zip_map.cond"))
	loopBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("zip_map.body"))
	loopEndBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("zip_map.end"))
	C.LLVMBuildBr(s.builder, loopCondBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopCondBB)
	indexValue := C.LLVMBuildPhi(s.builder, usizeLLVMType, cStringFree("zip_map.index"))
	initIndexValues := []C.LLVMValueRef{zero}
	initIndexBlocks := []C.LLVMBasicBlockRef{entryBlock}
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr(initIndexValues), llvmBlockSlicePtr(initIndexBlocks), 1)
	hasMore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, totalValue, cStringFree("zip_map.has_more"))
	C.LLVMBuildCondBr(s.builder, hasMore, loopBodyBB, loopEndBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopBodyBB)
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
	resultValue, err := s.emitFunctionValueCall(callbackValue, callbackType, []C.LLVMValueRef{src1Elem, src2Elem}, "zip_map.call")
	if err != nil {
		return nil, nil, true, err
	}
	coerced, err := s.coerceValue(resultValue, callbackType.Return, dstElemType)
	if err != nil {
		return nil, nil, true, err
	}
	store := C.LLVMBuildStore(s.builder, coerced, dstElemPtr)
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
	nextIndex := C.LLVMBuildAdd(s.builder, indexValue, one, cStringFree("zip_map.index.next"))
	bodyEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, loopCondBB)
	nextIndexValues := []C.LLVMValueRef{nextIndex}
	nextIndexBlocks := []C.LLVMBasicBlockRef{bodyEnd}
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr(nextIndexValues), llvmBlockSlicePtr(nextIndexBlocks), 1)

	C.LLVMPositionBuilderAtEnd(s.builder, loopEndBB)
	return nil, s.g.result.NamedTypes["void"], true, nil
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
func (s *functionState) emitDenseViewDataPointer(viewValue C.LLVMValueRef, elemType semantic.Type, name string) (C.LLVMValueRef, error) {
	if s == nil || s.g == nil {
		return nil, fmt.Errorf("missing function state for dense view data extraction")
	}
	if elemType == nil {
		return nil, fmt.Errorf("missing dense view element type")
	}
	return C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree(name+".data")), nil
}
func (s *functionState) emitDenseViewIndexedAddress(dataPtr C.LLVMValueRef, elemType semantic.Type, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if s == nil || s.g == nil {
		return nil, fmt.Errorf("missing function state for dense view indexing")
	}
	elemLLVMType, err := s.g.lowerType(elemType)
	if err != nil {
		return nil, err
	}
	indices := []C.LLVMValueRef{indexValue}
	return C.LLVMBuildGEP2(s.builder, elemLLVMType, dataPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(name+".ptr")), nil
}
func (s *functionState) emitArenaViewSliceValue(viewValue C.LLVMValueRef, viewType *semantic.ViewType, startValue C.LLVMValueRef, endValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	return s.emitDenseViewSliceValue(viewValue, viewType, startValue, endValue, name)
}
func (s *functionState) emitDenseViewSliceValue(viewValue C.LLVMValueRef, viewType semantic.Type, startValue C.LLVMValueRef, endValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if viewType == nil {
		return nil, fmt.Errorf("missing view type for slice helper lowering")
	}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	dataValue := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree(name+".data"))
	lenValue := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree(name+".len"))
	var elemType semantic.Type = s.g.result.NamedTypes["u8"]
	if vt, ok := viewType.(*semantic.ViewType); ok {
		elemType = vt.Elem
	}
	elemSize, err := s.sizeOfType(elemType)
	if err != nil {
		return nil, err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	lo := s.emitUnsignedMin(startValue, lenValue, usizeLLVMType, name+".lo")
	endClamped := s.emitUnsignedMin(endValue, lenValue, usizeLLVMType, name+".end.clamped")
	endAfterStart := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntUGE), endClamped, lo, cStringFree(name+".end.after.start"))
	hi := C.LLVMBuildSelect(s.builder, endAfterStart, endClamped, lo, cStringFree(name+".hi"))
	sliceLen := C.LLVMBuildSub(s.builder, hi, lo, cStringFree(name+".len.out"))
	offset := C.LLVMBuildMul(s.builder, lo, elemSizeValue, cStringFree(name+".offset"))
	i8Type := C.LLVMInt8TypeInContext(s.g.context)
	indices := []C.LLVMValueRef{offset}
	sliceData := C.LLVMBuildGEP2(s.builder, i8Type, dataValue, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(name+".data.out"))
	out := C.LLVMGetUndef(viewLLVMType)
	out = C.LLVMBuildInsertValue(s.builder, out, sliceData, 0, cStringFree(name+".view.data"))
	out = C.LLVMBuildInsertValue(s.builder, out, sliceLen, 1, cStringFree(name+".view.len"))
	return out, nil
}
func (s *functionState) emitChunksExactValidation(chunkSizeValue C.LLVMValueRef, totalValue C.LLVMValueRef, prefix string) error {
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	nonZeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(prefix+".nonzero"))
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(prefix+".ok"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(prefix+".fail"))
	isNonZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), chunkSizeValue, zero, cStringFree(prefix+".chunk.nonzero"))
	C.LLVMBuildCondBr(s.builder, isNonZero, nonZeroBB, failBB)

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if err := s.emitTrapUnreachable(prefix + ".fail"); err != nil {
		return err
	}

	C.LLVMPositionBuilderAtEnd(s.builder, nonZeroBB)
	remainder := C.LLVMBuildURem(s.builder, totalValue, chunkSizeValue, cStringFree(prefix+".remainder"))
	isExact := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), remainder, zero, cStringFree(prefix+".exact"))
	C.LLVMBuildCondBr(s.builder, isExact, okBB, failBB)

	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	return nil
}
func (s *functionState) emitPackedStoreConstructorValue(expr *ast.CallExpr, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || len(expr.Args) != 1 {
		return nil, nil, fmt.Errorf("packed store constructor expects exactly one arena argument")
	}
	value, err := s.emitPackedStoreValue(expr.Args[0], storeType)
	if err != nil {
		return nil, nil, err
	}
	return value, storeType, nil
}

func (s *functionState) emitPackedStoreValue(arenaExpr ast.Expr, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, error) {
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
func (s *functionState) emitPackedStoreValueFromArenaPtr(arenaPtr C.LLVMValueRef, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, error) {
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
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	rowSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(rowSizeBytes), 0)
	storeLLVMType, err := s.g.lowerPackedEnumStoreType(storeType)
	if err != nil {
		return nil, err
	}
	sideWords, err := s.g.packedEnumCommonSideTableWordCount(storeType.Enum)
	if err != nil {
		return nil, err
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	stateHelperName := "ctx_packed_store_state_new"
	stateHelperParams := []semantic.Type{arenaRefType, usizeType}
	stateArgs := []C.LLVMValueRef{arenaPtr, rowSizeValue}
	if s.g.packedLoweringForStore(storeType) == packedEnumABIAoS {
		// docs/76 Slice 2: AoS store creation — same (arena, record_bytes) signature, lean record-array
		// backing instead of columns. record_bytes is the node-record (rowType) size already computed.
		stateHelperName = "ctx_aos_store_new"
	} else if s.g.packedLoweringForStore(storeType) == packedEnumABIVariantSparse {
		if sideWords > 0 {
			stateHelperName = "ctx_packed_store_state_new_variant_sparse_with_side_words"
			stateHelperParams = []semantic.Type{arenaRefType, usizeType, usizeType}
			stateArgs = append(stateArgs, C.LLVMConstInt(usizeLLVMType, C.ulonglong(sideWords), 0))
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
				C.LLVMConstInt(usizeLLVMType, C.ulonglong(prefixWords), 0),
				C.LLVMConstInt(usizeLLVMType, C.ulonglong(sideWords), 0),
			)
		} else {
			stateHelperName = "ctx_packed_store_state_new_with_prefix_words"
			stateHelperParams = []semantic.Type{arenaRefType, usizeType, usizeType}
			stateArgs = append(stateArgs, C.LLVMConstInt(usizeLLVMType, C.ulonglong(prefixWords), 0))
		}
	} else if sideWords > 0 {
		stateHelperName = "ctx_packed_store_state_new_with_side_words"
		stateHelperParams = []semantic.Type{arenaRefType, usizeType, usizeType}
		stateArgs = append(stateArgs, C.LLVMConstInt(usizeLLVMType, C.ulonglong(sideWords), 0))
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
	storeValue := C.LLVMGetUndef(storeLLVMType)
	storeValue = C.LLVMBuildInsertValue(s.builder, storeValue, arenaPtr, 0, cStringFree("packed.store.arena"))
	storeValue = C.LLVMBuildInsertValue(s.builder, storeValue, rowSizeValue, 1, cStringFree("packed.store.row_bytes"))
	storeValue = C.LLVMBuildInsertValue(s.builder, storeValue, stateValue, 2, cStringFree("packed.store.state"))
	return storeValue, nil
}
func (s *functionState) emitPackedStoreArenaValue(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, error) {
	return s.emitPackedStoreArenaValueNamed(storeValue, storeType, "packed.store.arena.value")
}
func (s *functionState) emitPackedStoreFieldValueNamed(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, index C.unsigned, name string) (C.LLVMValueRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	if block := C.LLVMGetInsertBlock(s.builder); block != nil && storeValue != nil {
		key := packedStoreExtractCacheKey{block: block, store: storeValue, index: index}
		if cached, ok := s.lookupPackedStoreFieldValue(key); ok && cached != nil {
			return cached, nil
		}
		value := C.LLVMBuildExtractValue(s.builder, storeValue, index, cStringFree(name))
		s.cachePackedStoreFieldValue(key, value)
		return value, nil
	}
	return C.LLVMBuildExtractValue(s.builder, storeValue, index, cStringFree(name)), nil
}
func (s *functionState) emitPackedStoreArenaValueNamed(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, name string) (C.LLVMValueRef, error) {
	return s.emitPackedStoreFieldValueNamed(storeValue, storeType, 0, name)
}
func (s *functionState) emitPackedStoreRowBytesValue(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, error) {
	return s.emitPackedStoreRowBytesValueNamed(storeValue, storeType, "packed.store.row_bytes.value")
}
func (s *functionState) emitPackedStoreRowBytesValueNamed(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, name string) (C.LLVMValueRef, error) {
	return s.emitPackedStoreFieldValueNamed(storeValue, storeType, 1, name)
}
func (s *functionState) emitPackedStoreStateValueNamed(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, name string) (C.LLVMValueRef, error) {
	return s.emitPackedStoreFieldValueNamed(storeValue, storeType, 2, name)
}
