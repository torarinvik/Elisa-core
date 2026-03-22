//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

type packedStoreOps struct {
	s          *functionState
	storeValue C.LLVMValueRef
	storeType  *semantic.PackedEnumStoreType
}

func (s *functionState) packedStoreOpsFromExpr(expr ast.Expr) (*packedStoreOps, bool, error) {
	if expr == nil {
		return nil, false, nil
	}
	if _, ok := packedStoreOperandType(s.exprType(expr)); !ok {
		return nil, false, nil
	}
	storeValue, storeType, err := s.emitPackedStoreValueFromExpr(expr)
	if err != nil {
		return nil, false, err
	}
	if storeType == nil || storeType.Enum == nil {
		return nil, false, nil
	}
	return &packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}, true, nil
}

func (ops *packedStoreOps) voidRefType() *semantic.RefType {
	return &semantic.RefType{
		Elem:            ops.s.g.result.NamedTypes["void"],
		State:           semantic.RefStateNonNull,
		Storage:         semantic.RefStorageAny,
		ExplicitStorage: true,
	}
}

func (ops *packedStoreOps) stateValue(name string) (C.LLVMValueRef, error) {
	return ops.s.emitPackedStoreStateValueNamed(ops.storeValue, ops.storeType, name)
}

func (ops *packedStoreOps) storeCount(name string) (C.LLVMValueRef, error) {
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, err
	}
	usizeType := ops.s.g.result.NamedTypes["usize"]
	helperType := &semantic.FuncType{Name: "ctx_packed_store_count", Params: []semantic.Type{ops.voidRefType()}, Return: usizeType}
	callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_count", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue}, name), nil
}

func (ops *packedStoreOps) storeValueAt(indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, nil, err
	}
	usizeType := ops.s.g.result.NamedTypes["usize"]
	switch ops.s.g.packedEnumABI {
	case packedEnumABIRowHandle:
		helperType := &semantic.FuncType{Name: "ctx_packed_store_row_ptr_at", Params: []semantic.Type{ops.voidRefType(), usizeType}, Return: ops.voidRefType()}
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_row_ptr_at", helperType)
		if err != nil {
			return nil, nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, nil, err
		}
		value := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, indexValue}, name)
		coerced, err := ops.s.coerceValue(value, ops.voidRefType(), ops.storeType.Enum)
		if err != nil {
			return nil, nil, err
		}
		return coerced, ops.storeType.Enum, nil
	case packedEnumABIWordHandle:
		uintptrType := ops.s.g.result.NamedTypes["uintptr"]
		helperType := &semantic.FuncType{Name: "ctx_packed_store_word_handle_at", Params: []semantic.Type{ops.voidRefType(), usizeType}, Return: uintptrType}
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_word_handle_at", helperType)
		if err != nil {
			return nil, nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, nil, err
		}
		value := ops.s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stateValue, indexValue}, name)
		coerced, err := ops.s.coerceValue(value, uintptrType, ops.storeType.Enum)
		if err != nil {
			return nil, nil, err
		}
		return coerced, ops.storeType.Enum, nil
	default:
		return nil, nil, fmt.Errorf("unsupported packed enum ABI mode %d", ops.s.g.packedEnumABI)
	}
}

func (ops *packedStoreOps) storeSlice(startValue C.LLVMValueRef, endValue C.LLVMValueRef, resultType semantic.Type, name string) (C.LLVMValueRef, semantic.Type, error) {
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   "ctx_packed_store_view",
		Params: []semantic.Type{ops.voidRefType(), ops.s.g.result.NamedTypes["usize"], ops.s.g.result.NamedTypes["usize"]},
		Return: resultType,
	}
	callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_view", helperType)
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
