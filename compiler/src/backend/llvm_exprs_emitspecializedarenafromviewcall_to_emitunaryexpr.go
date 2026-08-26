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
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
	"unsafe"
)

func (s *functionState) emitSpecializedArenaFromViewCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_from_view" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	arenaExpr := expr.Args[0]
	viewExpr := expr.Args[1]
	arenaType := s.exprType(arenaExpr)
	viewType := s.exprType(viewExpr)
	resultType := s.exprType(expr)
	if !isDynArrayViewCarrierType(viewType) || !isDynArrayCarrierType(resultType) {
		return nil, nil, false, nil
	}
	viewFacts, ok := s.g.result.ExprOptimizationFacts(viewExpr)
	if !ok || !viewFacts.HasExactExtent() {
		return nil, nil, false, nil
	}
	exactMaterializeCount := uint64(0)
	hasSmallExactMaterializeCount := false
	if elemType, ok := runtimeIndexedElemType(viewType); ok {
		if elemSize, err := s.sizeOfType(elemType); err == nil && elemSize != 0 {
			if count, ok := constOptimizationExtentSize(viewFacts.Extent); ok && count <= smallExactArenaCopyUnrollLimit {
				exactMaterializeCount = count
				hasSmallExactMaterializeCount = true
			}
		}
	}
	arenaValue, _, err := s.emitExpr(arenaExpr, arenaType)
	if err != nil {
		return nil, nil, true, err
	}
	viewValue, _, err := s.emitExpr(viewExpr, viewType)
	if err != nil {
		return nil, nil, true, err
	}
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroResult, err := s.zeroValue(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("view.materialize.src.data"))
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("view.materialize.src.len"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	materializeElemType, ok := runtimeIndexedElemType(viewType)
	if !ok {
		return nil, nil, true, fmt.Errorf("arena_da_from_view specialization expected a view element type, got %s", viewType)
	}
	elemSize, err := s.sizeOfType(materializeElemType)
	if err != nil {
		return nil, nil, true, err
	}
	byteCount, err := s.emitCheckedElemByteCount(viewLen, elemSize, "view.materialize")
	if err != nil {
		return nil, nil, true, err
	}
	zeroBytes := C.LLVMConstInt(usizeLLVMType, 0, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), byteCount, zeroBytes, cStringFree("view.materialize.bytes.zero"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.materialize.alloc"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.materialize.merge"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, allocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	if _, ok := resultType.(*semantic.DArrayType); !ok {
		return nil, nil, true, fmt.Errorf("arena_da_from_view specialization expected darray result type, got %T", resultType)
	}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaValue, byteCount}, "view.materialize.alloc")
	if hasSmallExactMaterializeCount {
		if exactMaterializeCount != 0 {
			elemType, ok := runtimeIndexedElemType(viewType)
			if !ok {
				return nil, nil, true, fmt.Errorf("arena_da_from_view specialization expected view element type")
			}
			elemLLVMType, err := s.g.lowerType(elemType)
			if err != nil {
				return nil, nil, true, err
			}
			indexLLVMType, err := s.g.lowerBuiltin("usize")
			if err != nil {
				return nil, nil, true, err
			}
			domainName := fmt.Sprintf("elisa_core.view.materialize.%p.domain", expr)
			dstScopeName := domainName + ".dst"
			srcScopeName := domainName + ".src"
			for i := uint64(0); i < exactMaterializeCount; i++ {
				indexValue := C.LLVMConstInt(indexLLVMType, C.ulonglong(i), 0)
				indices := []C.LLVMValueRef{indexValue}
				srcPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, viewData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("view.materialize.src.elem.ptr"))
				elemValue := C.LLVMBuildLoad2(s.builder, elemLLVMType, srcPtr, cStringFree("view.materialize.elem"))
				s.attachAliasScopeMetadataWithNames(elemValue, domainName, srcScopeName, []string{dstScopeName})
				dstPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, allocPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("view.materialize.dst.elem.ptr"))
				store := C.LLVMBuildStore(s.builder, elemValue, dstPtr)
				s.attachAliasScopeMetadataWithNames(store, domainName, dstScopeName, []string{srcScopeName})
			}
		}
	} else {

		memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
		})
		memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
		if err != nil {
			return nil, nil, true, err
		}
		memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
		if err != nil {
			return nil, nil, true, err
		}
		memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{allocPtr, viewData, byteCount}, "view.materialize.memcpy")
		s.addCallSiteEnumAttribute(memcpyCall, C.uint(1), "noalias")
		s.addCallSiteEnumAttribute(memcpyCall, C.uint(2), "noalias")
	}

	materialized := C.LLVMGetUndef(llvmResultType)
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, allocPtr, 0, cStringFree("view.materialize.items"))
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewLen, 1, cStringFree("view.materialize.count"))
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewLen, 2, cStringFree("view.materialize.capacity"))
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, llvmResultType, cStringFree("view.materialize.result"))
	values := []C.LLVMValueRef{zeroResult, materialized}
	blocks := []C.LLVMBasicBlockRef{entryBlock, allocEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}
func (s *functionState) emitSpecializedArenaViewFillCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_fill" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	dstExpr := expr.Args[0]
	dstType := s.exprType(dstExpr)
	resultType := s.exprType(expr)
	fillExpr := expr.Args[1]
	_, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if funcType == nil || len(funcType.Params) != 2 {
		return nil, nil, true, fmt.Errorf("arena_da_fill target does not have the expected function type")
	}
	fillType := funcType.Params[1]
	fillByte, constByte := staticRepeatedByteFillValueForType(s, fillExpr, fillType)
	dynamicByte := !constByte && isSingleByteScalarFillType(s, fillType)
	if !isDynArrayViewCarrierType(dstType) || !s.g.result.ExprSupportsDenseWrite(dstExpr) {
		return nil, nil, false, nil
	}
	exactFillCount := uint64(0)
	hasSmallExactFillCount := false
	if facts, ok := s.g.result.ExprOptimizationFacts(dstExpr); ok {
		if count, ok := constOptimizationExtentSize(facts.Extent); ok && count <= smallExactArenaFillUnrollLimit {
			exactFillCount = count
			hasSmallExactFillCount = true
		}
	}
	if !hasSmallExactFillCount && !constByte && !dynamicByte {
		return nil, nil, false, nil
	}
	dstValue, _, err := s.emitExpr(dstExpr, dstType)
	if err != nil {
		return nil, nil, true, err
	}
	fillRawValue, actualFillType, err := s.emitExpr(fillExpr, fillType)
	if err != nil {
		return nil, nil, true, err
	}
	typedFillValue, err := s.coerceValue(fillRawValue, actualFillType, fillType)
	if err != nil {
		return nil, nil, true, err
	}
	dstData := C.LLVMBuildExtractValue(s.builder, dstValue, 0, cStringFree("view.fill.dst.data"))
	if hasSmallExactFillCount {
		if exactFillCount == 0 {
			return nil, resultType, true, nil
		}
		elemLLVMType, err := s.g.lowerType(fillType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeType, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, nil, true, err
		}
		for i := uint64(0); i < exactFillCount; i++ {
			indexValue := C.LLVMConstInt(usizeType, C.ulonglong(i), 0)
			indices := []C.LLVMValueRef{indexValue}
			elemPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, dstData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("view.fill.elem.ptr"))
			C.LLVMBuildStore(s.builder, typedFillValue, elemPtr)
		}
		return nil, resultType, true, nil
	}
	var fillValue C.LLVMValueRef
	if constByte {
		fillValue = C.LLVMConstInt(C.LLVMInt32TypeInContext(s.g.context), C.ulonglong(fillByte), 0)
	} else {
		fillValue, err = s.coerceValue(typedFillValue, fillType, s.g.result.NamedTypes["i32"])
		if err != nil {
			return nil, nil, true, err
		}
	}
	dstLen := C.LLVMBuildExtractValue(s.builder, dstValue, 1, cStringFree("view.fill.dst.len"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, true, err
	}
	fillElemSize, err := s.sizeOfType(fillType)
	if err != nil {
		return nil, nil, true, err
	}
	dstBytes := C.LLVMBuildMul(s.builder, dstLen, C.LLVMConstInt(usizeType, C.ulonglong(fillElemSize), 0), cStringFree("view.fill.dst.bytes"))
	zeroBytes := C.LLVMConstInt(usizeType, 0, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), dstBytes, zeroBytes, cStringFree("view.fill.bytes.zero"))
	fillBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.fill.fast"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.fill.merge"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, fillBB)

	C.LLVMPositionBuilderAtEnd(s.builder, fillBB)
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memsetValueType := s.g.result.NamedTypes["int"]
	memsetType := &semantic.FuncType{Name: "memset", Params: []semantic.Type{voidRefType, memsetValueType, s.g.result.NamedTypes["usize"]}, Return: voidRefType}
	memsetCallee, err := s.g.ensureFunctionDeclared("memset", memsetType)
	if err != nil {
		return nil, nil, true, err
	}
	fillValue, err = s.coerceValue(fillValue, s.g.result.NamedTypes["i32"], memsetValueType)
	if err != nil {
		return nil, nil, true, err
	}
	memsetLLVMType, err := s.g.lowerFunctionType(memsetType)
	if err != nil {
		return nil, nil, true, err
	}
	_ = s.buildCall(memsetLLVMType, memsetCallee, []C.LLVMValueRef{dstData, fillValue, dstBytes}, "view.fill.memset")
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	return nil, resultType, true, nil
}
func (s *functionState) emitStringViewStaticLiteralEqual(viewExpr ast.Expr, viewType semantic.Type, literalExpr ast.Expr, literalText string) (C.LLVMValueRef, error) {
	// Through a REFERENCE (`found: mutable sview&`), emitExpr yields the POINTER rather
	// than the StringView aggregate, and the ExtractValue below then ran on a pointer and
	// took LLVM down with a SIGSEGV inside cgo -- a compiler crash, not a diagnostic.
	// emitStringCompareOperandValue already knows how to load a view through a ref; the
	// non-ref path is unchanged.
	viewValue, err := s.emitStringCompareOperandValue(viewExpr, viewType)
	if err != nil {
		return nil, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("svlit.data"))
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("svlit.len"))
	literalLen := len([]byte(literalText))
	lenLLVMType, err := s.g.lowerBuiltin("i64")
	if err != nil {
		return nil, err
	}
	lenValue := C.LLVMConstInt(lenLLVMType, C.ulonglong(literalLen), 0)
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), viewLen, lenValue, cStringFree("svlit.len.eq"))
	if literalLen == 0 {
		return lenEqual, nil
	}

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	compareBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svlit.compare"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svlit.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, compareBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, compareBB)
	var compareValue C.LLVMValueRef
	if literalLen <= 8 {
		compareValue, err = s.emitStringViewLiteralBytesEqual(viewData, literalText)
	} else {
		literalValue, _, emitErr := s.emitExpr(literalExpr, nil)
		if emitErr != nil {
			return nil, emitErr
		}
		compareValue, err = s.emitMemcmpEqual(viewData, literalValue, literalLen)
	}
	if err != nil {
		return nil, err
	}
	compareEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("svlit.eq"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	values := []C.LLVMValueRef{falseValue, compareValue}
	blocks := []C.LLVMBasicBlockRef{entryBlock, compareEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, nil
}
func (s *functionState) emitDStrStaticLiteralEqual(textExpr ast.Expr, textType semantic.Type, literalExpr ast.Expr, literalText string) (C.LLVMValueRef, error) {
	if classifyRuntimeStringCompareKind(textType) != runtimeStringCompareDStr {
		return nil, fmt.Errorf("cstr literal specialization requires cstr operand")
	}
	lenType := s.g.result.NamedTypes["i64"]
	var (
		textData C.LLVMValueRef
		textLen  C.LLVMValueRef
		err      error
	)
	if baseExpr, baseType, start, end, ok := s.constantDStrSliceCall(textExpr); ok {
		textData, textLen, err = s.emitConstantClampedStringSliceOperand(baseExpr, baseType, start, end, "cstrlit.slice")
		if err != nil {
			return nil, err
		}
	} else {
		textValue, _, err := s.emitExpr(textExpr, textType)
		if err != nil {
			return nil, err
		}
		textData = textValue
		textLen, err = s.emitRuntimeStringLengthValue(textValue, textType, lenType, "cstrlit.len")
		if err != nil {
			return nil, err
		}
	}
	literalLen := len([]byte(literalText))
	lenLLVMType, err := s.g.lowerBuiltin("i64")
	if err != nil {
		return nil, err
	}
	lenValue := C.LLVMConstInt(lenLLVMType, C.ulonglong(literalLen), 0)
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), textLen, lenValue, cStringFree("cstrlit.len.eq"))
	if literalLen == 0 {
		return lenEqual, nil
	}

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	compareBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cstrlit.compare"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cstrlit.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, compareBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, compareBB)
	var compareValue C.LLVMValueRef
	if literalLen <= 8 {
		compareValue, err = s.emitStringViewLiteralBytesEqual(textData, literalText)
	} else {
		literalValue, _, emitErr := s.emitExpr(literalExpr, nil)
		if emitErr != nil {
			return nil, emitErr
		}
		compareValue, err = s.emitMemcmpEqual(textData, literalValue, literalLen)
	}
	if err != nil {
		return nil, err
	}
	compareEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("cstrlit.eq"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	values := []C.LLVMValueRef{falseValue, compareValue}
	blocks := []C.LLVMBasicBlockRef{entryBlock, compareEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, nil
}
func (s *functionState) emitStringViewLiteralBytesEqual(viewData C.LLVMValueRef, literalText string) (C.LLVMValueRef, error) {
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	byteType := C.LLVMInt8TypeInContext(s.g.context)
	indexType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	result := C.LLVMConstInt(boolType, 1, 0)
	for i, b := range []byte(literalText) {
		indexValue := C.LLVMConstInt(indexType, C.ulonglong(i), 0)
		indices := []C.LLVMValueRef{indexValue}
		bytePtr := C.LLVMBuildGEP2(s.builder, byteType, viewData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("svlit.byte.ptr"))
		byteValue := C.LLVMBuildLoad2(s.builder, byteType, bytePtr, cStringFree("svlit.byte"))
		literalValue := C.LLVMConstInt(byteType, C.ulonglong(b), 0)
		byteEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), byteValue, literalValue, cStringFree("svlit.byte.eq"))
		result = C.LLVMBuildAnd(s.builder, result, byteEqual, cStringFree("svlit.bytes.and"))
	}
	return result, nil
}
func (s *functionState) emitMemcmpEqual(left C.LLVMValueRef, right C.LLVMValueRef, length int) (C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	lengthValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(length), 0)
	return s.emitMemcmpEqualValue(left, right, lengthValue, "svlit.memcmp", false)
}
func (s *functionState) emitMemcmpEqualValue(left C.LLVMValueRef, right C.LLVMValueRef, lengthValue C.LLVMValueRef, callName string, noAliasArgs bool) (C.LLVMValueRef, error) {
	voidType := s.g.result.NamedTypes["void"]
	usizeType := s.g.result.NamedTypes["usize"]
	intType := s.g.result.NamedTypes["int"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{
		Name:   "memcmp",
		Params: []semantic.Type{voidRefType, voidRefType, usizeType},
		Return: intType,
	}
	callee, err := s.g.ensureFunctionDeclared("memcmp", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{left, right, lengthValue}, callName)
	if noAliasArgs {
		s.addCallSiteEnumAttribute(call, C.uint(1), "noalias")
		s.addCallSiteEnumAttribute(call, C.uint(2), "noalias")
	}
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, err
	}
	zero := C.LLVMConstInt(intLLVMType, 0, 0)
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), call, zero, cStringFree(callName+".eq")), nil
}
func (s *functionState) staticCStringLiteral(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.StringLit:
		return n.Value, true
	case *ast.ParenExpr:
		return s.staticCStringLiteral(n.Inner)
	case *ast.CastExpr:
		return s.staticCStringLiteral(n.Operand)
	case *ast.Ident:
		value, ok := s.g.constValue(n.Name)
		if !ok || value.Kind != semantic.ConstString {
			return "", false
		}
		return value.String, true
	default:
		return "", false
	}
}
func (s *functionState) emitLogicalExpr(expr *ast.BinaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	left, _, err := s.emitExpr(expr.Left, s.g.result.NamedTypes["bool"])
	if err != nil {
		return nil, nil, err
	}
	parentBlock := C.LLVMGetInsertBlock(s.builder)
	rhsName := cString("logic.rhs")
	defer C.free(unsafe.Pointer(rhsName))
	rhsBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, rhsName)
	mergeName := cString("logic.end")
	defer C.free(unsafe.Pointer(mergeName))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, mergeName)

	if expr.Op == lexer.TOKEN_AND {
		C.LLVMBuildCondBr(s.builder, left, rhsBB, mergeBB)
	} else {
		C.LLVMBuildCondBr(s.builder, left, mergeBB, rhsBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, rhsBB)
	right, _, err := s.emitExpr(expr.Right, s.g.result.NamedTypes["bool"])
	if err != nil {
		return nil, nil, err
	}
	rhsEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(rhsEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, C.LLVMInt1TypeInContext(s.g.context), cStringFree("logicphi"))
	fallback := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0)
	if expr.Op == lexer.TOKEN_OR {
		fallback = C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
	}
	values := []C.LLVMValueRef{fallback, right}
	blocks := []C.LLVMBasicBlockRef{parentBlock, rhsEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, s.g.result.NamedTypes["bool"], nil
}
func (s *functionState) emitUnaryExpr(expr *ast.UnaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Operand == nil {
		pos := lexer.Pos{}
		op := lexer.TokenKind(0)
		if expr != nil {
			pos = expr.Pos()
			op = expr.Op
		}
		return nil, nil, fmt.Errorf("cannot emit unary expression %s with nil operand at %s", lexer.TokenName(op), pos)
	}
	// Overloaded unary operator (`-x` on a user type): emit the analyzer's desugared `T.__neg__(x)`.
	if expr.LoweredCall != nil {
		return s.emitExpr(expr.LoweredCall, s.exprType(expr))
	}
	operandType := s.exprType(expr.Operand)
	value, _, err := s.emitExpr(expr.Operand, operandType)
	if err != nil {
		return nil, nil, err
	}
	resultType := s.exprType(expr)
	switch expr.Op {
	case lexer.TOKEN_NOT:
		return C.LLVMBuildNot(s.builder, value, cStringFree("nottmp")), resultType, nil
	case lexer.TOKEN_MINUS:
		if isFloatType(operandType) {
			return C.LLVMBuildFNeg(s.builder, value, cStringFree("negtmp")), resultType, nil
		}
		return C.LLVMBuildNeg(s.builder, value, cStringFree("negtmp")), resultType, nil
	case lexer.TOKEN_TILDE:
		return C.LLVMBuildNot(s.builder, value, cStringFree("invt")), resultType, nil
	case lexer.TOKEN_BANG:
		return value, resultType, nil
	default:
		return nil, nil, fmt.Errorf("unsupported unary operator %s", lexer.TokenName(expr.Op))
	}
}
