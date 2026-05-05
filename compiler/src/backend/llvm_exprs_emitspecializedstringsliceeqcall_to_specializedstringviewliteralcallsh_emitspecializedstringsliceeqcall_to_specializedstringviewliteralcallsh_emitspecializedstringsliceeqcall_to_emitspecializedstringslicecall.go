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
	"unsafe"
)

func (s *functionState) emitSpecializedStringSliceEqCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "ctx_string_slice_eq" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 4 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	leftType := s.exprType(leftExpr)
	if classifyRuntimeStringCompareKind(leftType) != runtimeStringCompareDStr {
		return nil, nil, false, nil
	}
	start, ok := s.staticIntLiteral(expr.Args[1])
	if !ok || start < 0 {
		return nil, nil, false, nil
	}
	end, ok := s.staticIntLiteral(expr.Args[2])
	if !ok || end < start {
		return nil, nil, false, nil
	}
	rightExpr := expr.Args[3]
	rightType := s.exprType(rightExpr)
	if classifyRuntimeStringCompareKind(rightType) != runtimeStringCompareDStr {
		return nil, nil, false, nil
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	leftData, leftSliceLen, err := s.emitConstantClampedStringSliceOperand(leftExpr, leftType, start, end, "strsliceeq.left")
	if err != nil {
		return nil, nil, true, err
	}
	rightData, rightLen, rightLenType, rightKind, err := s.emitRuntimeStringCompareOperand(rightExpr, rightType)
	if err != nil {
		return nil, nil, true, err
	}
	if rightKind != runtimeStringCompareDStr || rightLen == nil || rightLenType == nil {
		return nil, nil, false, nil
	}
	rightLenI64, err := s.coerceValue(rightLen, rightLenType, s.g.result.NamedTypes["i64"])
	if err != nil {
		return nil, nil, true, err
	}
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftSliceLen, rightLenI64, cStringFree("strsliceeq.len.eq"))
	sliceZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftSliceLen, C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), 0, 0), cStringFree("strsliceeq.len.zero"))
	dataEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftData, rightData, cStringFree("strsliceeq.data.eq"))
	usizeType := s.g.result.NamedTypes["usize"]
	lenValue, err := s.coerceValue(leftSliceLen, s.g.result.NamedTypes["i64"], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	zeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.zero"))
	nonZeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.nonzero"))
	sameBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.same"))
	memcmpBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.memcmp"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, zeroBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, zeroBB)
	zeroEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildCondBr(s.builder, sliceZero, mergeBB, nonZeroBB)

	C.LLVMPositionBuilderAtEnd(s.builder, nonZeroBB)
	C.LLVMBuildCondBr(s.builder, dataEqual, sameBB, memcmpBB)

	C.LLVMPositionBuilderAtEnd(s.builder, sameBB)
	sameEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, memcmpBB)
	disjoint := s.g.result.ExprsAreDisjoint(leftExpr, rightExpr)
	cmp, err := s.emitMemcmpEqualValue(leftData, rightData, lenValue, "strsliceeq.memcmp", disjoint)
	if err != nil {
		return nil, nil, true, err
	}
	memcmpEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("strsliceeq.result"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	trueValue := C.LLVMConstInt(boolType, 1, 0)
	values := []C.LLVMValueRef{falseValue, trueValue, trueValue, cmp}
	blocks := []C.LLVMBasicBlockRef{entryBlock, zeroEnd, sameEnd, memcmpEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return C.LLVMBuildZExt(s.builder, phi, intLLVMType, cStringFree("strsliceeq.int")), intType, true, nil
}
func (s *functionState) emitSpecializedStringSlicesEqCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "ctx_string_slices_eq" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 6 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	leftStartExpr := expr.Args[1]
	leftEndExpr := expr.Args[2]
	rightExpr := expr.Args[3]
	rightStartExpr := expr.Args[4]
	rightEndExpr := expr.Args[5]
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(rightExpr)
	if classifyRuntimeStringCompareKind(leftType) != runtimeStringCompareDStr || classifyRuntimeStringCompareKind(rightType) != runtimeStringCompareDStr {
		return nil, nil, false, nil
	}
	leftStart, ok := s.staticIntLiteral(leftStartExpr)
	if !ok || leftStart < 0 {
		return nil, nil, false, nil
	}
	leftEnd, ok := s.staticIntLiteral(leftEndExpr)
	if !ok || leftEnd < leftStart {
		return nil, nil, false, nil
	}
	rightStart, ok := s.staticIntLiteral(rightStartExpr)
	if !ok || rightStart < 0 {
		return nil, nil, false, nil
	}
	rightEnd, ok := s.staticIntLiteral(rightEndExpr)
	if !ok || rightEnd < rightStart {
		return nil, nil, false, nil
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	leftData, leftSliceLen, err := s.emitConstantClampedStringSliceOperand(leftExpr, leftType, leftStart, leftEnd, "strsliceseq.left")
	if err != nil {
		return nil, nil, true, err
	}
	rightData, rightSliceLen, err := s.emitConstantClampedStringSliceOperand(rightExpr, rightType, rightStart, rightEnd, "strsliceseq.right")
	if err != nil {
		return nil, nil, true, err
	}
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftSliceLen, rightSliceLen, cStringFree("strsliceseq.len.eq"))
	sliceZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftSliceLen, C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), 0, 0), cStringFree("strsliceseq.len.zero"))
	dataEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftData, rightData, cStringFree("strsliceseq.data.eq"))
	usizeType := s.g.result.NamedTypes["usize"]
	lenValue, err := s.coerceValue(leftSliceLen, s.g.result.NamedTypes["i64"], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	zeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.zero"))
	nonZeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.nonzero"))
	sameBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.same"))
	memcmpBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.memcmp"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, zeroBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, zeroBB)
	zeroEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildCondBr(s.builder, sliceZero, mergeBB, nonZeroBB)

	C.LLVMPositionBuilderAtEnd(s.builder, nonZeroBB)
	C.LLVMBuildCondBr(s.builder, dataEqual, sameBB, memcmpBB)

	C.LLVMPositionBuilderAtEnd(s.builder, sameBB)
	sameEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, memcmpBB)
	disjoint := s.g.result.ExprsAreDisjoint(leftExpr, rightExpr)
	cmp, err := s.emitMemcmpEqualValue(leftData, rightData, lenValue, "strsliceseq.memcmp", disjoint)
	if err != nil {
		return nil, nil, true, err
	}
	memcmpEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("strsliceseq.result"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	trueValue := C.LLVMConstInt(boolType, 1, 0)
	values := []C.LLVMValueRef{falseValue, trueValue, trueValue, cmp}
	blocks := []C.LLVMBasicBlockRef{entryBlock, zeroEnd, sameEnd, memcmpEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return C.LLVMBuildZExt(s.builder, phi, intLLVMType, cStringFree("strsliceseq.int")), intType, true, nil
}
func (s *functionState) emitSpecializedRuntimeStringCompareCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	switch ident.Name {
	case "ctx_streq", "ctx_string_view_eq", "string_view_eq", "ctx_string_views_eq", "string_views_eq":
	default:
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	rightExpr := expr.Args[1]
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(rightExpr)
	if ident.Name == "ctx_streq" {
		if literalText, ok := s.staticCStringLiteral(rightExpr); ok {
			cmp, err := s.emitDStrStaticLiteralEqual(leftExpr, leftType, rightExpr, literalText)
			if err != nil {
				return nil, nil, true, err
			}
			intType := s.g.result.NamedTypes["int"]
			intLLVMType, err := s.g.lowerType(intType)
			if err != nil {
				return nil, nil, true, err
			}
			return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("cstrlit.direct.int")), intType, true, nil
		}
		if literalText, ok := s.staticCStringLiteral(leftExpr); ok {
			cmp, err := s.emitDStrStaticLiteralEqual(rightExpr, rightType, leftExpr, literalText)
			if err != nil {
				return nil, nil, true, err
			}
			intType := s.g.result.NamedTypes["int"]
			intLLVMType, err := s.g.lowerType(intType)
			if err != nil {
				return nil, nil, true, err
			}
			return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("cstrlit.direct.int")), intType, true, nil
		}
	}
	cmp, ok, err := s.emitSameExtentRuntimeStringCompareExpr(lexer.TOKEN_EQEQ, leftExpr, leftType, rightExpr, rightType)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		cmp, ok, err = s.emitDisjointRuntimeStringCompareExpr(lexer.TOKEN_EQEQ, leftExpr, leftType, rightExpr, rightType)
		if err != nil {
			return nil, nil, true, err
		}
		if !ok {
			return nil, nil, false, nil
		}
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("streq.direct.int")), intType, true, nil
}
func isStringViewCarrierType(t semantic.Type) bool {
	return classifyRuntimeStringCompareKind(t) == runtimeStringCompareView
}
func (s *functionState) emitGlobalCStringLiteral(text string, name string) C.LLVMValueRef {
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	textC := cString(text)
	defer C.free(unsafe.Pointer(textC))
	return C.LLVMBuildGlobalStringPtr(s.builder, textC, nameC)
}
func (s *functionState) emitInternSmallStringCall(data C.LLVMValueRef, lenValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	u8Type := s.g.result.NamedTypes["u8"]
	usizeType := s.g.result.NamedTypes["usize"]
	srcType := &semantic.RefType{Elem: u8Type, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	retType := &semantic.RefType{Elem: u8Type, State: semantic.RefStateNonNull, Storage: semantic.RefStorageHeap, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "intern_small_string", Params: []semantic.Type{srcType, usizeType}, Return: retType}
	callee, err := s.g.ensureFunctionDeclared("intern_small_string", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{data, lenValue}, name), nil
}
func (s *functionState) emitDirectStringViewCopyLarge(viewData C.LLVMValueRef, viewLen C.LLVMValueRef) (C.LLVMValueRef, error) {
	i64Type := s.g.result.NamedTypes["i64"]
	usizeType := s.g.result.NamedTypes["usize"]
	voidType := s.g.result.NamedTypes["void"]
	u8Type := s.g.result.NamedTypes["u8"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	nullableU8RefType := &semantic.RefType{Elem: u8Type, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	heapVoidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageHeap, ExplicitStorage: true}
	allocType := &semantic.FuncType{Name: "alloc_perm", Params: []semantic.Type{i64Type}, Return: heapVoidRefType}
	allocCallee, err := s.g.ensureFunctionDeclared("alloc_perm", allocType)
	if err != nil {
		return nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, err
	}
	oneValue := C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), 1, 0)
	allocSize := C.LLVMBuildAdd(s.builder, viewLen, oneValue, cStringFree("svcopy.alloc.size"))
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{allocSize}, "svcopy.alloc")

	lenUsize, err := s.coerceValue(viewLen, i64Type, usizeType)
	if err != nil {
		return nil, err
	}
	memcpyType := &semantic.FuncType{Name: "memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	memcpyCallee, err := s.g.ensureFunctionDeclared("memcpy", memcpyType)
	if err != nil {
		return nil, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, err
	}
	_ = s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{allocPtr, viewData, lenUsize}, "svcopy.memcpy")

	i8LLVMType, err := s.g.lowerBuiltin("u8")
	if err != nil {
		return nil, err
	}
	bytePtr := C.LLVMBuildGEP2(s.builder, i8LLVMType, allocPtr, llvmValueSlicePtr([]C.LLVMValueRef{lenUsize}), 1, cStringFree("svcopy.term.ptr"))
	zeroByte := C.LLVMConstInt(i8LLVMType, 0, 0)
	C.LLVMBuildStore(s.builder, zeroByte, bytePtr)

	registerType := &semantic.FuncType{Name: "register_perm_string_len", Params: []semantic.Type{nullableU8RefType, usizeType}, Return: voidType}
	registerCallee, err := s.g.ensureFunctionDeclared("register_perm_string_len", registerType)
	if err != nil {
		return nil, err
	}
	registerLLVMType, err := s.g.lowerFunctionType(registerType)
	if err != nil {
		return nil, err
	}
	_ = s.buildCall(registerLLVMType, registerCallee, []C.LLVMValueRef{allocPtr, lenUsize}, "")
	return allocPtr, nil
}
func (s *functionState) emitSpecializedStringSliceCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "ctx_string_slice" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 3 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	resultType := s.exprType(expr)
	inputExpr := expr.Args[0]
	inputType := s.exprType(inputExpr)
	if _, ok := inputType.(*semantic.DStrType); !ok {
		return nil, nil, false, nil
	}
	if s.g.result.ExprsHaveSameExtent(expr, inputExpr) {
		value, _, err := s.emitExpr(inputExpr, inputType)
		return value, resultType, true, err
	}
	sliceFacts, ok := s.g.result.ExprOptimizationFacts(expr)
	if !ok || !sliceFacts.HasExactExtent() {
		return nil, nil, false, nil
	}
	exactLen, ok := constOptimizationExtentSize(sliceFacts.Extent)
	if !ok {
		return nil, nil, false, nil
	}
	begin, ok := parseOptimizationExtentConstInt(sliceFacts.Extent.Begin)
	if !ok || begin < 0 {
		return nil, nil, false, nil
	}
	inputValue, _, err := s.emitExpr(inputExpr, inputType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	beginValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(begin), 0)
	sliceData := inputValue
	if begin != 0 {
		i8LLVMType, err := s.g.lowerBuiltin("u8")
		if err != nil {
			return nil, nil, true, err
		}
		indices := []C.LLVMValueRef{beginValue}
		sliceData = C.LLVMBuildGEP2(s.builder, i8LLVMType, inputValue, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("strslice.data"))
	}
	lenValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(exactLen), 0)
	if exactLen == 0 {
		emptyPtr := s.emitGlobalCStringLiteral("", "strslice.empty")
		value, err := s.emitInternSmallStringCall(emptyPtr, lenValue, "strslice.zero.small")
		return value, resultType, true, err
	}
	if exactLen <= 8 {
		value, err := s.emitInternSmallStringCall(sliceData, lenValue, "strslice.small")
		return value, resultType, true, err
	}
	largeLen := C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), C.ulonglong(exactLen), 0)
	value, err := s.emitDirectStringViewCopyLarge(sliceData, largeLen)
	return value, resultType, true, err
}
