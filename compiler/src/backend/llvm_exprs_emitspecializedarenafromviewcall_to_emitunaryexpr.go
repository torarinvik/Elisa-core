//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
#include <llvm-c/Target.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitSpecializedArenaFromViewCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_from_view" {
		return nil, nil, false, nil
	}
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, ident.Name) {
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
	viewFacts, ok := s.g.semanticReadView().ExprOptimizationFacts(viewExpr)
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
	usizeType := s.g.namedType("usize")
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
	voidType := s.g.namedType("void")
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
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, ident.Name) {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	dstExpr := expr.Args[0]
	dstType := s.exprType(dstExpr)
	resultType := s.exprType(expr)
	fillExpr := expr.Args[1]
	_, funcType, _, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if funcType == nil || len(funcType.Params) != 2 {
		return nil, nil, true, fmt.Errorf("arena_da_fill target does not have the expected function type")
	}
	fillType := funcType.Params[1]
	fillByte, constByte := staticRepeatedByteFillValueForType(s, fillExpr, fillType)
	dynamicByte := !constByte && isSingleByteScalarFillType(s, fillType)
	if !isDynArrayViewCarrierType(dstType) || !s.g.semanticReadView().ExprSupportsDenseWrite(dstExpr) {
		return nil, nil, false, nil
	}
	exactFillCount := uint64(0)
	hasSmallExactFillCount := false
	if facts, ok := s.g.semanticReadView().ExprOptimizationFacts(dstExpr); ok {
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
		fillValue, err = s.coerceValue(typedFillValue, fillType, s.g.namedType("i32"))
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
	voidType := s.g.namedType("void")
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	// libc declares memset's byte value as C int, which is i32 on every supported
	// target. Elisa's `int` is i64, so using it here conflicts with the source-level
	// extern signature and can make a Stage0 bootstrap fail with two LLVM prototypes.
	memsetValueType := s.g.namedType("i32")
	memsetType := &semantic.FuncType{Name: "memset", Params: []semantic.Type{voidRefType, memsetValueType, s.g.namedType("usize")}, Return: voidRefType}
	memsetCallee, err := s.g.ensureFunctionDeclared("memset", memsetType)
	if err != nil {
		return nil, nil, true, err
	}
	fillValue, err = s.coerceValue(fillValue, s.g.namedType("i32"), memsetValueType)
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
