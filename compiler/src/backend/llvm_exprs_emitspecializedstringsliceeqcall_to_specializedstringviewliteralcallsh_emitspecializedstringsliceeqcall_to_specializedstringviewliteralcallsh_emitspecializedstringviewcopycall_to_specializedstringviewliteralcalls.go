//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int llcontextLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef llctxAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef llctxAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned llctxMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void llctxSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = llctxAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, llctxMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef llctxCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = llctxAliasMDString(ctx, domainName);
	return llctxAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef llctxCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = llctxAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return llctxAliasMDNode(ctx, operands, 2);
}

static void llctxAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = llctxCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = llctxCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	llctxSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = llctxCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = llctxCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		llctxSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"llcontext/src/ast"
	"llcontext/src/semantic"
)

func (s *functionState) emitSpecializedStringViewCopyCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || (ident.Name != "string_view_copy" && ident.Name != "ctx_string_from_view") {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	viewExpr := expr.Args[0]
	viewType := s.exprType(viewExpr)
	resultType := s.exprType(expr)
	if !isStringViewCarrierType(viewType) {
		return nil, nil, false, nil
	}
	viewFacts, ok := s.g.result.ExprOptimizationFacts(viewExpr)
	if !ok || !viewFacts.HasExactExtent() {
		return nil, nil, false, nil
	}
	viewValue, _, err := s.emitExpr(viewExpr, viewType)
	if err != nil {
		return nil, nil, true, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("svcopy.data"))
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("svcopy.len"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	if exactLen, ok := constOptimizationExtentSize(viewFacts.Extent); ok {
		lenValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(exactLen), 0)
		if exactLen <= 8 {
			dataValue := viewData
			if exactLen == 0 {
				dataValue = s.emitGlobalCStringLiteral("", "svcopy.empty")
			}
			value, err := s.emitInternSmallStringCall(dataValue, lenValue, "svcopy.small")
			return value, resultType, true, err
		}
		largeLen := C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), C.ulonglong(exactLen), 0)
		value, err := s.emitDirectStringViewCopyLarge(viewData, largeLen)
		return value, resultType, true, err
	}

	i64LLVMType := C.LLVMInt64TypeInContext(s.g.context)
	zeroLen := C.LLVMConstInt(i64LLVMType, 0, 0)
	eightLen := C.LLVMConstInt(i64LLVMType, 8, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntSLE), viewLen, zeroLen, cStringFree("svcopy.len.zero"))
	positiveBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.positive"))
	zeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.zero"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.merge"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildCondBr(s.builder, zeroCond, zeroBB, positiveBB)

	C.LLVMPositionBuilderAtEnd(s.builder, zeroBB)
	emptyPtr := s.emitGlobalCStringLiteral("", "svcopy.empty")
	zeroSmall, err := s.emitInternSmallStringCall(emptyPtr, C.LLVMConstInt(usizeLLVMType, 0, 0), "svcopy.zero.small")
	if err != nil {
		return nil, nil, true, err
	}
	zeroEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, positiveBB)
	smallCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntSLE), viewLen, eightLen, cStringFree("svcopy.len.small"))
	smallBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.small"))
	largeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.large"))
	C.LLVMBuildCondBr(s.builder, smallCond, smallBB, largeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, smallBB)
	viewLenUsize, err := s.coerceValue(viewLen, s.g.result.NamedTypes["i64"], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	smallValue, err := s.emitInternSmallStringCall(viewData, viewLenUsize, "svcopy.small")
	if err != nil {
		return nil, nil, true, err
	}
	smallEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, largeBB)
	largeValue, err := s.emitDirectStringViewCopyLarge(viewData, viewLen)
	if err != nil {
		return nil, nil, true, err
	}
	largeEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmResultType, cStringFree("svcopy.result"))
	values := []C.LLVMValueRef{zeroSmall, smallValue, largeValue}
	blocks := []C.LLVMBasicBlockRef{zeroEnd, smallEnd, largeEnd}
	_ = entryBlock
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}
func (s *functionState) emitSpecializedStringViewLiteralCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, false, nil
	}
	viewArgIndex, literalArgIndex, returnsInt, ok := s.specializedStringViewLiteralCallShape(ident.Name)
	if !ok {
		return nil, nil, false, nil
	}
	literalText, ok := s.staticCStringLiteral(expr.Args[literalArgIndex])
	if !ok {
		return nil, nil, false, nil
	}
	viewType := s.exprType(expr.Args[viewArgIndex])
	if classifyRuntimeStringCompareKind(viewType) != runtimeStringCompareView {
		return nil, nil, false, nil
	}
	cmp, err := s.emitStringViewStaticLiteralEqual(expr.Args[viewArgIndex], viewType, expr.Args[literalArgIndex], literalText)
	if err != nil {
		return nil, nil, true, err
	}
	if !returnsInt {
		return cmp, s.exprType(expr), true, nil
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("svlit.eq.int")), intType, true, nil
}
func (s *functionState) specializedStringViewLiteralCallShape(funcName string) (int, int, bool, bool) {
	if funcName == "string_view_eq" || funcName == "ctx_string_view_eq" {
		return 0, 1, true, true
	}
	sym, ok := s.g.result.GlobalScope.Lookup(funcName)
	if !ok {
		return 0, 0, false, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || len(decl.Params) != 2 || len(decl.Body) != 1 {
		return 0, 0, false, false
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		return 0, 0, false, false
	}
	callExpr, ok := s.boolStringViewLiteralWrapperCall(ret.Value, decl.Params[0].Name, decl.Params[1].Name)
	if !ok || callExpr == nil {
		return 0, 0, false, false
	}
	callee, ok := callExpr.Func.(*ast.Ident)
	if !ok {
		return 0, 0, false, false
	}
	if callee.Name != "string_view_eq" && callee.Name != "ctx_string_view_eq" {
		return 0, 0, false, false
	}
	return 0, 1, false, true
}
