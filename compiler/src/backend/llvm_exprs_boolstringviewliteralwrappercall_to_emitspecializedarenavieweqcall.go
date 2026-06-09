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
)

func (s *functionState) boolStringViewLiteralWrapperCall(expr ast.Expr, viewParam string, literalParam string) (*ast.CallExpr, bool) {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return nil, false
	}
	if binary.Op != lexer.TOKEN_EQEQ && binary.Op != lexer.TOKEN_BANGEQ {
		return nil, false
	}
	callExpr, intLit, ok := unwrapCallComparedToZero(binary.Left, binary.Right)
	if !ok {
		callExpr, intLit, ok = unwrapCallComparedToZero(binary.Right, binary.Left)
	}
	if !ok || intLit == nil || intLit.Value != "0" {
		return nil, false
	}
	if binary.Op != lexer.TOKEN_BANGEQ {
		return nil, false
	}
	if !matchesStringViewLiteralWrapperArgs(callExpr, viewParam, literalParam) {
		return nil, false
	}
	return callExpr, true
}
func unwrapCallComparedToZero(left ast.Expr, right ast.Expr) (*ast.CallExpr, *ast.IntLit, bool) {
	callExpr, ok := left.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	intLit, ok := right.(*ast.IntLit)
	if !ok {
		return nil, nil, false
	}
	return callExpr, intLit, true
}
func matchesStringViewLiteralWrapperArgs(callExpr *ast.CallExpr, viewParam string, literalParam string) bool {
	if callExpr == nil || len(callExpr.Args) != 2 {
		return false
	}
	viewIdent, ok := callExpr.Args[0].(*ast.Ident)
	if !ok || viewIdent.Name != viewParam {
		return false
	}
	return exprIsParamOrCastOfParam(callExpr.Args[1], literalParam)
}
func exprIsParamOrCastOfParam(expr ast.Expr, paramName string) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name == paramName
	case *ast.ParenExpr:
		return exprIsParamOrCastOfParam(n.Inner, paramName)
	case *ast.CastExpr:
		return exprIsParamOrCastOfParam(n.Operand, paramName)
	default:
		return false
	}
}
func stripMemcpyOperandExpr(expr ast.Expr) ast.Expr {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.CanExpr:
			expr = n.Expr
		default:
			return expr
		}
	}
	return nil
}
func isMemcpyViewCarrierType(t semantic.Type) bool {
	switch tt := t.(type) {
	case *semantic.DArrayViewType, *semantic.SViewType:
		return true
	case *semantic.StructType:
		return tt != nil && (tt.Name == "DynArrayView" || tt.Name == "StringView")
	default:
		return false
	}
}
func isDynArrayViewCarrierType(t semantic.Type) bool {
	switch tt := t.(type) {
	case *semantic.DArrayViewType:
		return true
	case *semantic.StructType:
		return tt != nil && tt.Name == "DynArrayView"
	default:
		return false
	}
}
func isDynArrayCarrierType(t semantic.Type) bool {
	switch t.(type) {
	case *semantic.DArrayType:
		return true
	default:
		return false
	}
}
func (s *functionState) memcpyDisjointCarrierExpr(expr ast.Expr) ast.Expr {
	stripped := stripMemcpyOperandExpr(expr)
	fieldExpr, ok := stripped.(*ast.FieldExpr)
	if !ok || fieldExpr.Field != "data" {
		return nil
	}
	if !isMemcpyViewCarrierType(s.exprType(fieldExpr.Object)) {
		return nil
	}
	return fieldExpr.Object
}
func (s *functionState) memcpyOperandsAreDisjoint(destExpr ast.Expr, srcExpr ast.Expr) bool {
	if s == nil || s.g == nil || s.g.result == nil {
		return false
	}
	if s.g.result.ExprsAreDisjoint(destExpr, srcExpr) {
		return true
	}
	destCarrier := s.memcpyDisjointCarrierExpr(destExpr)
	srcCarrier := s.memcpyDisjointCarrierExpr(srcExpr)
	if destCarrier == nil || srcCarrier == nil {
		return false
	}
	return s.g.result.ExprsAreDisjoint(destCarrier, srcCarrier)
}
func (s *functionState) emitSpecializedMemcpyCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if ident.Name != "memcpy" && ident.Name != "arena_memcpy" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 3 {
		return nil, nil, false, nil
	}
	callee, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if funcType == nil {
		return nil, nil, true, fmt.Errorf("copy helper target does not have a function type")
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, true, err
	}
	args := make([]C.LLVMValueRef, 0, len(expr.Args))
	for i, arg := range expr.Args {
		var expected semantic.Type
		if i < len(funcType.Params) {
			expected = funcType.Params[i]
		}
		value, _, err := s.emitExpr(arg, expected)
		if err != nil {
			return nil, nil, true, err
		}
		args = append(args, value)
	}
	callName := "calltmp"
	if isVoidType(funcType.Return) {
		callName = ""
	}
	call := s.buildCall(llvmFnType, callee, args, callName)
	if s.memcpyOperandsAreDisjoint(expr.Args[0], expr.Args[1]) {
		s.addCallSiteEnumAttribute(call, C.uint(1), "noalias")
		s.addCallSiteEnumAttribute(call, C.uint(2), "noalias")
	}
	return call, funcType.Return, true, nil
}
func (s *functionState) emitSpecializedArenaViewCopyCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_copy_exact" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	dstExpr := expr.Args[0]
	srcExpr := expr.Args[1]
	dstType := s.exprType(dstExpr)
	srcType := s.exprType(srcExpr)
	if !isDynArrayViewCarrierType(dstType) || !isDynArrayViewCarrierType(srcType) {
		return nil, nil, false, nil
	}
	callee, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if funcType == nil {
		return nil, nil, true, fmt.Errorf("arena_da_copy_exact target does not have a function type")
	}
	exactCopyCount := uint64(0)
	hasSmallExactCopyCount := false
	if dstFacts, ok := s.g.result.ExprOptimizationFacts(dstExpr); ok {
		if dstCount, ok := constOptimizationExtentSize(dstFacts.Extent); ok && dstCount <= smallExactArenaCopyUnrollLimit {
			if srcFacts, ok := s.g.result.ExprOptimizationFacts(srcExpr); ok {
				if srcCount, ok := constOptimizationExtentSize(srcFacts.Extent); ok && srcCount == dstCount {
					exactCopyCount = dstCount
					hasSmallExactCopyCount = true
				}
			}
		}
	}
	disjoint := s.g.result.ExprsAreDisjoint(dstExpr, srcExpr)
	if !hasSmallExactCopyCount && !disjoint {
		return nil, nil, false, nil
	}
	dstValue, _, err := s.emitExpr(dstExpr, dstType)
	if err != nil {
		return nil, nil, true, err
	}
	srcValue, _, err := s.emitExpr(srcExpr, srcType)
	if err != nil {
		return nil, nil, true, err
	}
	if hasSmallExactCopyCount {
		if exactCopyCount == 0 {
			return nil, funcType.Return, true, nil
		}
		var elemType semantic.Type
		switch viewType := funcType.Params[0].(type) {
		case *semantic.DArrayViewType:
			elemType = viewType.Elem
		default:
			return nil, nil, true, fmt.Errorf("arena_da_copy_exact specialization expected dview parameter, got %T", funcType.Params[0])
		}
		elemLLVMType, err := s.g.lowerType(elemType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeType, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, nil, true, err
		}
		dstData := C.LLVMBuildExtractValue(s.builder, dstValue, 0, cStringFree("dview.copy.dst.data"))
		srcData := C.LLVMBuildExtractValue(s.builder, srcValue, 0, cStringFree("dview.copy.src.data"))
		domainName := ""
		dstScopeName := ""
		srcScopeName := ""
		if disjoint {
			domainName = fmt.Sprintf("elisa_core.dview.copy.%p.domain", expr)
			dstScopeName = domainName + ".dst"
			srcScopeName = domainName + ".src"
		}
		for i := uint64(0); i < exactCopyCount; i++ {
			indexValue := C.LLVMConstInt(usizeType, C.ulonglong(i), 0)
			indices := []C.LLVMValueRef{indexValue}
			srcPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, srcData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.copy.src.elem.ptr"))
			elemValue := C.LLVMBuildLoad2(s.builder, elemLLVMType, srcPtr, cStringFree("dview.copy.elem"))
			if disjoint {
				s.attachAliasScopeMetadataWithNames(elemValue, domainName, srcScopeName, []string{dstScopeName})
			}
			dstPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, dstData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.copy.dst.elem.ptr"))
			store := C.LLVMBuildStore(s.builder, elemValue, dstPtr)
			if disjoint {
				s.attachAliasScopeMetadataWithNames(store, domainName, dstScopeName, []string{srcScopeName})
			}
		}
		return nil, funcType.Return, true, nil
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, true, err
	}
	dstData := C.LLVMBuildExtractValue(s.builder, dstValue, 0, cStringFree("dview.copy.dst.data"))
	dstLen := C.LLVMBuildExtractValue(s.builder, dstValue, 1, cStringFree("dview.copy.dst.len"))
	dstElemSize := C.LLVMBuildExtractValue(s.builder, dstValue, 2, cStringFree("dview.copy.dst.elem_size"))
	srcData := C.LLVMBuildExtractValue(s.builder, srcValue, 0, cStringFree("dview.copy.src.data"))
	srcLen := C.LLVMBuildExtractValue(s.builder, srcValue, 1, cStringFree("dview.copy.src.len"))
	srcElemSize := C.LLVMBuildExtractValue(s.builder, srcValue, 2, cStringFree("dview.copy.src.elem_size"))
	dstBytes := C.LLVMBuildMul(s.builder, dstLen, dstElemSize, cStringFree("dview.copy.dst.bytes"))
	srcBytes := C.LLVMBuildMul(s.builder, srcLen, srcElemSize, cStringFree("dview.copy.src.bytes"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, true, err
	}
	zeroBytes := C.LLVMConstInt(usizeType, 0, 0)
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, s.g.result.NamedTypes["usize"]}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	buildMemcpyNoAlias := func(byteCount C.LLVMValueRef) {
		memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{dstData, srcData, byteCount}, "dview.copy.memcpy")
		s.addCallSiteEnumAttribute(memcpyCall, C.uint(1), "noalias")
		s.addCallSiteEnumAttribute(memcpyCall, C.uint(2), "noalias")
	}

	if s.g.result.ExprsHaveEqualExtentSize(dstExpr, srcExpr) {
		zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), dstBytes, zeroBytes, cStringFree("dview.copy.bytes.zero"))
		copyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.exact.fast"))
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.exact.merge"))
		C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, copyBB)

		C.LLVMPositionBuilderAtEnd(s.builder, copyBB)
		buildMemcpyNoAlias(dstBytes)
		C.LLVMBuildBr(s.builder, mergeBB)

		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		return nil, funcType.Return, true, nil
	}

	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), dstBytes, srcBytes, cStringFree("dview.copy.bytes.eq"))
	copyCheckBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.fast.check"))
	fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.fallback"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, copyCheckBB, fallbackBB)

	C.LLVMPositionBuilderAtEnd(s.builder, copyCheckBB)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), dstBytes, zeroBytes, cStringFree("dview.copy.bytes.zero"))
	copyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.fast"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, copyBB)

	C.LLVMPositionBuilderAtEnd(s.builder, copyBB)
	buildMemcpyNoAlias(dstBytes)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackCall := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{dstValue, srcValue}, "")
	_ = fallbackCall
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	return nil, funcType.Return, true, nil
}
func (s *functionState) emitSpecializedArenaViewEqCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_eq_exact" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	rightExpr := expr.Args[1]
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(rightExpr)
	resultType := s.exprType(expr)
	if resultType == nil {
		resultType = s.g.result.NamedTypes["bool"]
	}
	if !isDynArrayViewCarrierType(leftType) || !isDynArrayViewCarrierType(rightType) {
		return nil, nil, false, nil
	}
	if !s.g.result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		return nil, nil, false, nil
	}
	disjoint := s.g.result.ExprsAreDisjoint(leftExpr, rightExpr)
	exactEqByteCount := uint64(0)
	hasSmallExactEqByteCount := false
	if elemType, ok := runtimeIndexedElemType(leftType); ok {
		if elemSize, err := s.sizeOfType(elemType); err == nil && elemSize != 0 {
			if leftFacts, ok := s.g.result.ExprOptimizationFacts(leftExpr); ok {
				if leftCount, ok := constOptimizationExtentSize(leftFacts.Extent); ok {
					if rightFacts, ok := s.g.result.ExprOptimizationFacts(rightExpr); ok {
						if rightCount, ok := constOptimizationExtentSize(rightFacts.Extent); ok && rightCount == leftCount {
							totalBytes := leftCount * elemSize
							if totalBytes <= smallExactArenaEqUnrollByteLimit {
								exactEqByteCount = totalBytes
								hasSmallExactEqByteCount = true
							}
						}
					}
				}
			}
		}
	}

	leftValue, _, err := s.emitExpr(leftExpr, leftType)
	if err != nil {
		return nil, nil, true, err
	}
	rightValue, _, err := s.emitExpr(rightExpr, rightType)
	if err != nil {
		return nil, nil, true, err
	}
	leftData := C.LLVMBuildExtractValue(s.builder, leftValue, 0, cStringFree("dview.eq.left.data"))
	leftLen := C.LLVMBuildExtractValue(s.builder, leftValue, 1, cStringFree("dview.eq.left.len"))
	leftElemSize := C.LLVMBuildExtractValue(s.builder, leftValue, 2, cStringFree("dview.eq.left.elem_size"))
	rightData := C.LLVMBuildExtractValue(s.builder, rightValue, 0, cStringFree("dview.eq.right.data"))
	if hasSmallExactEqByteCount {
		if exactEqByteCount == 0 {
			return C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0), resultType, true, nil
		}
		byteType := C.LLVMInt8TypeInContext(s.g.context)
		usizeType, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, nil, true, err
		}
		cmpResult := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
		domainName := ""
		leftScopeName := ""
		rightScopeName := ""
		if disjoint {
			domainName = fmt.Sprintf("elisa_core.dview.eq.%p.domain", expr)
			leftScopeName = domainName + ".left"
			rightScopeName = domainName + ".right"
		}
		for i := uint64(0); i < exactEqByteCount; i++ {
			indexValue := C.LLVMConstInt(usizeType, C.ulonglong(i), 0)
			indices := []C.LLVMValueRef{indexValue}
			leftBytePtr := C.LLVMBuildGEP2(s.builder, byteType, leftData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.eq.left.byte.ptr"))
			leftByte := C.LLVMBuildLoad2(s.builder, byteType, leftBytePtr, cStringFree("dview.eq.left.byte"))
			if disjoint {
				s.attachAliasScopeMetadataWithNames(leftByte, domainName, leftScopeName, []string{rightScopeName})
			}
			rightBytePtr := C.LLVMBuildGEP2(s.builder, byteType, rightData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.eq.right.byte.ptr"))
			rightByte := C.LLVMBuildLoad2(s.builder, byteType, rightBytePtr, cStringFree("dview.eq.right.byte"))
			if disjoint {
				s.attachAliasScopeMetadataWithNames(rightByte, domainName, rightScopeName, []string{leftScopeName})
			}
			bytesEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftByte, rightByte, cStringFree("dview.eq.byte.eq"))
			cmpResult = C.LLVMBuildAnd(s.builder, cmpResult, bytesEqual, cStringFree("dview.eq.byte.and"))
		}
		return cmpResult, resultType, true, nil
	}
	_ = C.LLVMBuildExtractValue(s.builder, rightValue, 1, cStringFree("dview.eq.right.len"))
	_ = C.LLVMBuildExtractValue(s.builder, rightValue, 2, cStringFree("dview.eq.right.elem_size"))
	byteCount := C.LLVMBuildMul(s.builder, leftLen, leftElemSize, cStringFree("dview.eq.bytes"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, true, err
	}
	zeroBytes := C.LLVMConstInt(usizeType, 0, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), byteCount, zeroBytes, cStringFree("dview.eq.bytes.zero"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	memcmpBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.eq.memcmp"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.eq.merge"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, memcmpBB)

	C.LLVMPositionBuilderAtEnd(s.builder, memcmpBB)
	cmp, err := s.emitMemcmpEqualValue(leftData, rightData, byteCount, "dview.eq.memcmp", disjoint)
	if err != nil {
		return nil, nil, true, err
	}
	memcmpEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("dview.eq.result"))
	trueValue := C.LLVMConstInt(boolType, 1, 0)
	values := []C.LLVMValueRef{trueValue, cmp}
	blocks := []C.LLVMBasicBlockRef{entryBlock, memcmpEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}
