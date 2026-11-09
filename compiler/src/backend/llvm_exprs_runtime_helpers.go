//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) nodeTableFillTypeArgs(expr *ast.CallExpr) (*semantic.EnumType, semantic.Type, error) {
	if expr == nil || callSpecializedIdentName(expr) != "node_table_fill" {
		return nil, nil, fmt.Errorf("node_table_fill expects explicit specialization")
	}
	_, specialize, ok := callSpecializedIdent(expr.Func)
	if !ok || specialize == nil || len(specialize.TypeArgs) != 2 {
		return nil, nil, fmt.Errorf("node_table_fill expects exactly 2 type arguments")
	}
	enumArg, err := s.resolveTypeExpr(specialize.TypeArgs[0])
	if err != nil {
		return nil, nil, err
	}
	enumType, ok := semantic.StripAggregateStateType(enumArg).(*semantic.EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("node_table_fill expects a packed enum type argument")
	}
	elemType, err := s.resolveTypeExpr(specialize.TypeArgs[1])
	if err != nil {
		return nil, nil, err
	}
	return enumType, elemType, nil
}
func (s *functionState) emitNodeKeyIndexValue(expr ast.Expr) (C.LLVMValueRef, *semantic.EnumType, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	keyType := s.exprType(expr)
	enumType, ok := semantic.NodeKeyEnumType(keyType)
	if !ok || enumType == nil {
		return nil, nil, false, nil
	}
	value, actualType, err := s.emitExpr(expr, keyType)
	if err != nil {
		return nil, nil, true, err
	}
	if refType, ok := actualType.(*semantic.RefType); ok && refType != nil && refType.State == semantic.RefStateNonNull {
		loaded, loadErr := s.loadValue(value, refType.Elem, "nodekey.load")
		if loadErr != nil {
			return nil, nil, true, loadErr
		}
		value = loaded
	}
	return C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("nodekey.index")), enumType, true, nil
}
func (s *functionState) emitDenseKeyHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if callIdentName(expr) != "dense_key" {
		return nil, nil, false, nil
	}
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, "dense_key") {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("dense_key expects 2 arguments, got %d", len(expr.Args))
	}
	resultType := s.exprType(expr)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	_, storeType, err := s.emitPackedStoreValueFromExpr(expr.Args[1])
	if err != nil {
		return nil, nil, true, err
	}
	if storeType == nil || storeType.Enum == nil {
		return nil, nil, true, fmt.Errorf("dense_key requires frozen packed-store metadata")
	}
	var handleValue C.LLVMValueRef
	actualNodeType := s.exprType(expr.Args[0])
	sourceEnum, ok := denseKeySourceEnumType(actualNodeType)
	if !ok || sourceEnum == nil {
		return nil, nil, true, fmt.Errorf("dense_key expects a packed enum value or packedview")
	}
	if viewType, ok := actualNodeType.(*semantic.PackedVariantViewType); ok && viewType != nil {
		viewValue, _, err := s.emitExpr(expr.Args[0], actualNodeType)
		if err != nil {
			return nil, nil, true, err
		}
		handleValue = C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("nodekey.view.handle"))
	} else {
		var actualType semantic.Type
		handleValue, actualType, err = s.emitExpr(expr.Args[0], actualNodeType)
		if err != nil {
			return nil, nil, true, err
		}
		if refType, ok := actualType.(*semantic.RefType); ok && refType != nil && refType.State == semantic.RefStateNonNull {
			handleValue, err = s.loadValue(handleValue, refType.Elem, "nodekey.handle")
			if err != nil {
				return nil, nil, true, err
			}
		}
	}
	var indexValue C.LLVMValueRef
	switch s.g.packedModeForEnum(sourceEnum) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse, packedEnumABIAoS:
		indexValue, err = s.coerceValue(handleValue, sourceEnum, s.g.namedType("u32"))
		if err != nil {
			return nil, nil, true, err
		}
	default:
		return nil, nil, true, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedModeForEnum(sourceEnum))
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, indexValue, 0, cStringFree("nodekey.index.insert"))
	return resultValue, resultType, true, nil
}
func (s *functionState) emitNodeTableFillHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if callSpecializedIdentName(expr) != "node_table_fill" {
		return nil, nil, false, nil
	}
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, "node_table_fill") {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 3 {
		return nil, nil, true, fmt.Errorf("node_table_fill expects 3 arguments, got %d", len(expr.Args))
	}
	resultType := s.exprType(expr)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	_, elemType, err := s.nodeTableFillTypeArgs(expr)
	if err != nil {
		return nil, nil, true, err
	}
	storeValue, storeType, err := s.emitPackedStoreValueFromExpr(expr.Args[1])
	if err != nil {
		return nil, nil, true, err
	}
	if storeType == nil || storeType.Enum == nil {
		return nil, nil, true, fmt.Errorf("node_table_fill requires frozen packed-store metadata")
	}
	countValue, err := s.emitPackedStoreCountValue(storeValue, storeType, "node.table.count")
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroCount := C.LLVMConstInt(usizeLLVMType, 0, 0)
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("node.table.alloc"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("node.table.merge"))
	isZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), countValue, zeroCount, cStringFree("node.table.count.zero"))
	C.LLVMBuildCondBr(s.builder, isZero, mergeBB, allocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	arenaPtr, _, err := s.emitAddressOrTemp(expr.Args[0])
	if err != nil {
		return nil, nil, true, err
	}
	elemSize, err := s.sizeOfType(elemType)
	if err != nil {
		return nil, nil, true, err
	}
	byteCount, err := s.emitCheckedElemByteCount(countValue, elemSize, "node.table")
	if err != nil {
		return nil, nil, true, err
	}
	arenaType := s.g.namedType("Arena")
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidType := s.g.namedType("void")
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaPtr, byteCount}, "node.table.alloc.ptr")
	viewType := &semantic.ViewType{Elem: elemType, SurfaceName: "view"}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, nil, true, err
	}
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, allocPtr, 0, cStringFree("node.table.view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, countValue, 1, cStringFree("node.table.view.len"))
	initValue, actualInitType, err := s.emitExpr(expr.Args[2], elemType)
	if err != nil {
		return nil, nil, true, err
	}
	initValue, err = s.coerceValue(initValue, actualInitType, elemType)
	if err != nil {
		return nil, nil, true, err
	}
	fillType := s.g.cachedRuntimeHelperType("arena_da_fill", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_da_fill", Params: []semantic.Type{viewType, elemType}, Return: s.g.namedType("void")}
	})
	fillCallee, err := s.g.ensureFunctionDeclared("arena_da_fill", fillType)
	if err != nil {
		return nil, nil, true, err
	}
	fillLLVMType, err := s.g.lowerFunctionType(fillType)
	if err != nil {
		return nil, nil, true, err
	}
	_ = s.buildCall(fillLLVMType, fillCallee, []C.LLVMValueRef{viewValue, initValue}, "")
	materialized := C.LLVMGetUndef(resultLLVMType)
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewValue, 0, cStringFree("node.table.values.insert"))
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	zeroResult := C.LLVMConstNull(resultLLVMType)
	phi := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("node.table.result"))
	values := []C.LLVMValueRef{zeroResult, materialized}
	blocks := []C.LLVMBasicBlockRef{entryBlock, allocEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}
func (s *functionState) emitResolvedCall(callee C.LLVMValueRef, funcType *semantic.FuncType, direct bool, args []C.LLVMValueRef, regionArenaArgs []C.LLVMValueRef) (C.LLVMValueRef, semantic.Type, error) {
	// ABI lowering may rewrite args. Preserve the source argument values only
	// for native extern calls whose postconditions will actually be checked.
	if !s.externEnsureChecksEnabled(funcType) {
		return s.emitResolvedCallInner(callee, funcType, direct, args, regionArenaArgs)
	}
	elisaArgs := append([]C.LLVMValueRef(nil), args...)
	value, valueType, err := s.emitResolvedCallInner(callee, funcType, direct, args, regionArenaArgs)
	if err != nil {
		return nil, nil, err
	}
	if err := s.emitExternEnsureChecks(funcType, elisaArgs, value, valueType); err != nil {
		return nil, nil, err
	}
	return value, valueType, nil
}

func (s *functionState) emitResolvedCallInner(callee C.LLVMValueRef, funcType *semantic.FuncType, direct bool, args []C.LLVMValueRef, regionArenaArgs []C.LLVMValueRef) (C.LLVMValueRef, semantic.Type, error) {
	if funcType == nil {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, err
	}
	layout, err := s.g.computeFuncAbiLayout(funcType)
	if err != nil {
		return nil, nil, err
	}
	// Optional arguments to an extern cross as the bare nullable pointer the C callee
	// declares; see externOptionalABI. Done before convertByvalArgs so the optional is
	// already a pointer by the time aggregate classification runs.
	args = s.convertExternResourceArgs(funcType, args)
	args = s.convertExternOptionalArgs(funcType, args)
	// Convert any memory-class aggregate arguments into byval pointers
	// (memcpy-filled stack temporaries) so they match the lowered pointer
	// parameter type. byvalTypes maps explicit-arg index -> aggregate type.
	args, byvalTypes, err := s.convertByvalArgs(funcType, args)
	if err != nil {
		return nil, nil, err
	}
	// A `view[T]` crossing to C splits into (ptr, len) in place; byval indices move with
	// it. Done last so every earlier per-argument conversion still sees one arg per param.
	args, byvalTypes = s.convertExternViewArgs(funcType, args, byvalTypes)
	// Region-parameterized containers: append the hidden Arena& args (one per
	// region param) after the explicit/implicit args, matching the param order
	// in lowerFunctionType. Empty for non-region-param functions.
	args = append(args, regionArenaArgs...)
	base := layout.paramBase()
	// applyByvalCallAttrs tags the byval pointer arguments on a built call. On
	// arm64 the memory-class arg convention is a plain indirect pointer, so the
	// attribute is suppressed there (the pointer-to-copy is still passed).
	applyByvalCallAttrs := func(call C.LLVMValueRef) {
		if !s.g.aggregateArgUsesByval(funcTypeIsCABI(funcType)) {
			return
		}
		for i, ty := range byvalTypes {
			s.addCallByvalAttr(call, base+i, ty)
		}
	}

	if retUnion, ok := nonVoidErrorUnion(funcType.Return); ok {
		resultSlot, err := s.emitStackTempZeroed(retUnion.Value, "call.result")
		if err != nil {
			return nil, nil, err
		}
		callArgs := make([]C.LLVMValueRef, 0, len(args)+1)
		callArgs = append(callArgs, resultSlot)
		callArgs = append(callArgs, args...)
		var call C.LLVMValueRef
		if direct {
			call = s.buildTypedCall(llvmFnType, callee, callArgs, "calltmp", funcType)
		} else {
			call, err = s.emitFunctionValueCall(callee, funcType, callArgs, "calltmp")
			if err != nil {
				return nil, nil, err
			}
		}
		applyByvalCallAttrs(call)
		unionValue, err := s.buildErrorUnionValueFromPointer(retUnion, call, resultSlot)
		if err != nil {
			return nil, nil, err
		}
		return unionValue, funcType.Return, nil
	}

	if layout.sret {
		// Large aggregate return: allocate a result slot, prepend it as the
		// hidden sret out-pointer, call (returns void), then load the result.
		resultSlot, err := s.createTempStorage("call.sret", funcType.Return)
		if err != nil {
			return nil, nil, err
		}
		callArgs := make([]C.LLVMValueRef, 0, len(args)+1)
		callArgs = append(callArgs, resultSlot)
		callArgs = append(callArgs, args...)
		var call C.LLVMValueRef
		if direct {
			call = s.buildTypedCall(llvmFnType, callee, callArgs, "", funcType)
		} else {
			call, err = s.emitFunctionValueCall(callee, funcType, callArgs, "")
			if err != nil {
				return nil, nil, err
			}
		}
		s.addCallSretAttr(call, layout.sretType)
		applyByvalCallAttrs(call)
		result, err := s.loadValue(resultSlot, funcType.Return, "call.sret.val")
		if err != nil {
			return nil, nil, err
		}
		return result, funcType.Return, nil
	}

	callName := ""
	if !isVoidType(funcType.Return) {
		callName = "calltmp"
	}
	var call C.LLVMValueRef
	if direct {
		call = s.buildTypedCall(llvmFnType, callee, args, callName, funcType)
	} else {
		call, err = s.emitFunctionValueCall(callee, funcType, args, "calltmp")
		if err != nil {
			return nil, nil, err
		}
	}
	applyByvalCallAttrs(call)
	// An extern returning `T?` returned the bare payload pointer; rebuild the optional
	// Elisa expects from a null test. See externOptionalABI.
	if _, ok := externNicheOptionalPayload(funcType, funcType.Return); ok {
		optionalValue, err := s.emitExternOptionalFromNiche(call, funcType.Return)
		if err != nil {
			return nil, nil, err
		}
		return optionalValue, funcType.Return, nil
	}
	if externReturnsResource(funcType) {
		wrapped, err := s.emitResourceFromHandle(call, funcType.Return)
		if err != nil {
			return nil, nil, err
		}
		return wrapped, funcType.Return, nil
	}
	return call, funcType.Return, nil
}
func (s *functionState) emitSafeFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Object == nil {
		return nil, nil, fmt.Errorf("optional field access requires a receiver")
	}
	resultType := s.exprType(expr)
	optionalType, ok := resultType.(*semantic.OptionalType)
	if !ok || optionalType == nil || optionalType.Value == nil {
		return nil, nil, fmt.Errorf("optional field access requires an optional result type")
	}
	presentValue, receiverValue, receiverType, err := s.emitSafeChainReceiverValue(expr.Object)
	if err != nil {
		return nil, nil, err
	}
	presentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.field.present"))
	noneBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.field.none"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.field.merge"))
	C.LLVMBuildCondBr(s.builder, presentValue, presentBB, noneBB)

	var (
		someValue  C.LLVMValueRef
		noneValue  C.LLVMValueRef
		presentEnd C.LLVMBasicBlockRef
		noneEnd    C.LLVMBasicBlockRef
	)

	C.LLVMPositionBuilderAtEnd(s.builder, presentBB)
	payloadValue, payloadType, err := s.emitFieldValueFromObjectValue(receiverValue, receiverType, expr.Field, "safe.field")
	if err != nil {
		return nil, nil, err
	}
	payloadValue, err = s.coerceValue(payloadValue, payloadType, optionalType.Value)
	if err != nil {
		return nil, nil, err
	}
	someValue, err = s.buildOptionalSome(optionalType, payloadValue)
	if err != nil {
		return nil, nil, err
	}
	presentEnd = C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, noneBB)
	noneValue, err = s.buildOptionalNone(optionalType)
	if err != nil {
		return nil, nil, err
	}
	noneEnd = C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("valuephi"))
	values := []C.LLVMValueRef{someValue, noneValue}
	blocks := []C.LLVMBasicBlockRef{presentEnd, noneEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, nil
}
